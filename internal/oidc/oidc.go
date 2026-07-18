// Package oidc implements generic OIDC login: admin-configured issuer
// discovery, the authorization-code flow helpers, and resolving a verified
// id_token to a local user (by identity, verified email link, or SSO
// provisioning).
package oidc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/settings"
)

var (
	ErrNotConfigured = errors.New("oidc not configured")
	ErrValidation    = errors.New("oidc validation failed")
	ErrExchange      = errors.New("oidc exchange failed")
)

const discoveryTimeout = 5 * time.Second

type Service struct {
	q          *sqlc.Queries
	pool       *pgxpool.Pool
	settings   *settings.Service
	httpClient *http.Client
}

func NewService(pool *pgxpool.Pool, settingsSvc *settings.Service) *Service {
	return &Service{
		q:          sqlc.New(pool),
		pool:       pool,
		settings:   settingsSvc,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *Service) Configured(ctx context.Context) (bool, error) {
	cfg, err := s.settings.OIDC(ctx)
	return cfg != nil, err
}

func (s *Service) PublicConfig(ctx context.Context) (issuerURL, clientID string, configured bool, err error) {
	cfg, err := s.settings.OIDC(ctx)
	if err != nil || cfg == nil {
		return "", "", false, err
	}
	return cfg.IssuerURL, cfg.ClientID, true, nil
}

// SetConfig validates the config against the issuer's discovery document
// before persisting it encrypted.
func (s *Service) SetConfig(ctx context.Context, cfg settings.OIDCConfig) error {
	dctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	if _, err := s.provider(dctx, cfg.IssuerURL); err != nil {
		return fmt.Errorf("%w: issuer discovery failed: %v", ErrValidation, err)
	}
	return s.settings.SetOIDC(ctx, cfg)
}

func (s *Service) ClearConfig(ctx context.Context) error {
	return s.settings.ClearOIDC(ctx)
}

// StartURL builds the authorization-code redirect URL carrying state and nonce.
func (s *Service) StartURL(ctx context.Context, redirectURL, state, nonce string) (string, error) {
	cfg, err := s.settings.OIDC(ctx)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", ErrNotConfigured
	}
	provider, err := s.provider(ctx, cfg.IssuerURL)
	if err != nil {
		return "", fmt.Errorf("%w: issuer discovery failed: %v", ErrValidation, err)
	}
	oc := oauth2Config(provider, cfg, redirectURL)
	return oc.AuthCodeURL(state, gooidc.Nonce(nonce)), nil
}

// ResolveCallback exchanges the code, verifies the id_token (signature,
// audience, nonce, verified email) and resolves it to a local user.
func (s *Service) ResolveCallback(ctx context.Context, redirectURL, code, wantNonce string) (sqlc.User, error) {
	cfg, err := s.settings.OIDC(ctx)
	if err != nil {
		return sqlc.User{}, err
	}
	if cfg == nil {
		return sqlc.User{}, ErrNotConfigured
	}
	provider, err := s.provider(ctx, cfg.IssuerURL)
	if err != nil {
		return sqlc.User{}, fmt.Errorf("%w: issuer discovery failed: %v", ErrValidation, err)
	}
	oc := oauth2Config(provider, cfg, redirectURL)
	tok, err := oc.Exchange(s.clientContext(ctx), code)
	if err != nil {
		return sqlc.User{}, fmt.Errorf("%w: code exchange failed", ErrExchange)
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return sqlc.User{}, fmt.Errorf("%w: token response missing id_token", ErrExchange)
	}
	idToken, err := provider.Verifier(&gooidc.Config{ClientID: cfg.ClientID}).Verify(s.clientContext(ctx), rawIDToken)
	if err != nil {
		return sqlc.User{}, fmt.Errorf("%w: id_token verification failed", ErrValidation)
	}
	if idToken.Nonce != wantNonce {
		return sqlc.User{}, fmt.Errorf("%w: nonce mismatch", ErrValidation)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return sqlc.User{}, fmt.Errorf("%w: id_token claims: %v", ErrValidation, err)
	}
	if claims.Email == "" || !claims.EmailVerified {
		return sqlc.User{}, fmt.Errorf("%w: a verified email claim is required", ErrValidation)
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	return s.resolveUser(ctx, idToken.Issuer, idToken.Subject, email)
}

// resolveUser maps (issuer, subject) to a user: existing identity wins, then a
// verified-email link to an existing account, then SSO provisioning of a new
// passwordless user.
func (s *Service) resolveUser(ctx context.Context, issuer, subject, email string) (sqlc.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sqlc.User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	ident, err := q.GetIdentityByIssuerSubject(ctx, sqlc.GetIdentityByIssuerSubjectParams{Issuer: issuer, Subject: subject})
	if err == nil {
		u, err := q.GetUserByID(ctx, ident.UserID)
		if err != nil {
			return sqlc.User{}, err
		}
		return u, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, err
	}

	u, err := q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		u, err = q.CreateUser(ctx, sqlc.CreateUserParams{Email: email, PasswordHash: pgtype.Text{}})
	}
	if err != nil {
		return sqlc.User{}, err
	}
	if _, err := q.CreateIdentity(ctx, sqlc.CreateIdentityParams{UserID: u.ID, Issuer: issuer, Subject: subject, Email: email}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Concurrent callback created the identity first; use its user.
			_ = tx.Rollback(ctx)
			ident, err := s.q.GetIdentityByIssuerSubject(ctx, sqlc.GetIdentityByIssuerSubjectParams{Issuer: issuer, Subject: subject})
			if err != nil {
				return sqlc.User{}, err
			}
			return s.q.GetUserByID(ctx, ident.UserID)
		}
		return sqlc.User{}, err
	}
	return u, tx.Commit(ctx)
}

// clientContext routes go-oidc and oauth2 HTTP traffic through s.httpClient.
func (s *Service) clientContext(ctx context.Context) context.Context {
	return gooidc.ClientContext(ctx, s.httpClient)
}

func (s *Service) provider(ctx context.Context, issuer string) (*gooidc.Provider, error) {
	return gooidc.NewProvider(s.clientContext(ctx), issuer)
}

func oauth2Config(p *gooidc.Provider, cfg *settings.OIDCConfig, redirectURL string) oauth2.Config {
	return oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  redirectURL,
		Scopes:       []string{gooidc.ScopeOpenID, "email"},
	}
}
