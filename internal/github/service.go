package github

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotConfigured = errors.New("github app is not configured")
var ErrNotConnected = errors.New("organization has no github installation")
var ErrInstallationTaken = errors.New("github installation is already connected to another organization")
var ErrInvalidSetupState = errors.New("github setup state is invalid, expired, or belongs to another user")

// setupStateTTL bounds how long an install link stays redeemable.
const setupStateTTL = 15 * time.Minute

// Service is the API-facing façade: it loads the encrypted app config per
// call and owns the org↔installation mapping.
type Service struct {
	q   *sqlc.Queries
	box *crypto.Box
	// NewClientFn allows tests to intercept client construction.
	NewClientFn func(cfg AppConfig) *Client
	oauth       OAuthEndpoints
}

func NewService(pool *pgxpool.Pool, box *crypto.Box) *Service {
	return &Service{q: sqlc.New(pool), box: box, NewClientFn: NewClient, oauth: defaultOAuthEndpoints()}
}

// SetOAuthEndpoints redirects the OAuth exchange and /user/installations
// lookups (tests).
func (s *Service) SetOAuthEndpoints(ep OAuthEndpoints) { s.oauth = ep }

func (s *Service) client(ctx context.Context) (*Client, error) {
	cfg, err := LoadAppConfig(ctx, s.q, s.box)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, ErrNotConfigured
	}
	return s.NewClientFn(*cfg), nil
}

// SaveApp stores the app credentials. A config without OAuth client
// credentials keeps the stored ones when the app ID is unchanged, so
// re-entering the private key doesn't silently turn ownership checks off.
func (s *Service) SaveApp(ctx context.Context, cfg AppConfig) error {
	if cfg.ClientID == "" && cfg.ClientSecret == "" {
		if prev, err := LoadAppConfig(ctx, s.q, s.box); err == nil && prev != nil && prev.AppID == cfg.AppID {
			cfg.ClientID, cfg.ClientSecret = prev.ClientID, prev.ClientSecret
		}
	} else if cfg.ClientSecret == "" {
		prev, err := LoadAppConfig(ctx, s.q, s.box)
		if err != nil {
			return err
		}
		if prev == nil || prev.ClientID != cfg.ClientID || prev.ClientSecret == "" {
			return ErrClientSecretRequired
		}
		cfg.ClientSecret = prev.ClientSecret
	}
	return SaveAppConfig(ctx, s.q, s.box, cfg)
}

func (s *Service) AppStatus(ctx context.Context) (configured bool, slug string, appID int64, err error) {
	cfg, err := LoadAppConfig(ctx, s.q, s.box)
	if err != nil || cfg == nil {
		return false, "", 0, err
	}
	return true, cfg.AppSlug, cfg.AppID, nil
}

func (s *Service) WebhookSecret(ctx context.Context) (string, error) {
	cfg, err := LoadAppConfig(ctx, s.q, s.box)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", ErrNotConfigured
	}
	return cfg.WebhookSecret, nil
}

// InstallURL returns the App's install link for an org. Its `state` is a
// fresh random nonce bound to (org, user) and redeemable once, within
// setupStateTTL, only by the same user — so the post-install callback can't
// be forged or replayed against another org.
func (s *Service) InstallURL(ctx context.Context, orgID, userID pgtype.UUID) (string, error) {
	c, err := s.client(ctx)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(buf)
	// Opportunistic GC: status reads mint a nonce each time.
	_ = s.q.DeleteExpiredGithubSetupStates(ctx)
	if err := s.q.CreateGithubSetupState(ctx, sqlc.CreateGithubSetupStateParams{
		Nonce: nonce, OrgID: orgID, UserID: userID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(setupStateTTL), Valid: true},
	}); err != nil {
		return "", err
	}
	return c.InstallURL(nonce), nil
}

// ConsumeSetupState redeems an install-link nonce for the user who minted it
// and returns the org it was minted for. Single use.
func (s *Service) ConsumeSetupState(ctx context.Context, nonce string, userID pgtype.UUID) (pgtype.UUID, error) {
	orgID, err := s.q.ConsumeGithubSetupState(ctx, sqlc.ConsumeGithubSetupStateParams{Nonce: nonce, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, ErrInvalidSetupState
	}
	return orgID, err
}

func (s *Service) OrgStatus(ctx context.Context, orgID pgtype.UUID) (connected bool, accountLogin string, err error) {
	inst, err := s.q.GetGithubInstallation(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, inst.AccountLogin, nil
}

// ConnectOrg records the installation for an org (called from the setup
// callback). The account login is fetched best-effort. An installation can
// belong to only one org.
//
// The installation_id comes from GitHub's redirect query string. The setup
// nonce proves which Cargo user started the flow; ownership of the
// installation itself is proven by the caller via VerifyInstallation (OAuth
// user-to-server exchange + GET /user/installations) before this runs. Apps
// without OAuth client credentials can't be verified — the setup handler
// logs that and the admin settings flag it (ownership_verified: false).
func (s *Service) ConnectOrg(ctx context.Context, orgID pgtype.UUID, installationID int64) error {
	if existing, err := s.q.GetGithubInstallationByInstallationID(ctx, installationID); err == nil {
		if existing.OrgID != orgID {
			return ErrInstallationTaken
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	login := ""
	if c, err := s.client(ctx); err == nil {
		if l, err := c.InstallationAccount(ctx, installationID); err == nil {
			login = l
		} else {
			slog.Warn("github installation account lookup failed", "err", err)
		}
	}
	_, err := s.q.UpsertGithubInstallation(ctx, sqlc.UpsertGithubInstallationParams{
		OrgID: orgID, InstallationID: installationID, AccountLogin: login,
	})
	// The UNIQUE constraint closes the race between the check above and here.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrInstallationTaken
	}
	return err
}

func (s *Service) installationFor(ctx context.Context, orgID pgtype.UUID) (int64, error) {
	inst, err := s.q.GetGithubInstallation(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotConnected
	}
	if err != nil {
		return 0, err
	}
	return inst.InstallationID, nil
}

func (s *Service) Repos(ctx context.Context, orgID pgtype.UUID) ([]Repo, error) {
	id, err := s.installationFor(ctx, orgID)
	if err != nil {
		return nil, err
	}
	c, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	return c.ListRepos(ctx, id)
}

func (s *Service) Branches(ctx context.Context, orgID pgtype.UUID, fullName string) ([]string, error) {
	id, err := s.installationFor(ctx, orgID)
	if err != nil {
		return nil, err
	}
	c, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	return c.ListBranches(ctx, id, fullName)
}

// CloneAuth rewrites a github.com clone URL to embed a fresh installation
// token; non-GitHub URLs and unconnected orgs pass through unchanged.
func (s *Service) CloneAuth(ctx context.Context, orgID pgtype.UUID, repoURL string) (string, error) {
	u, err := url.Parse(repoURL)
	if err != nil || !strings.EqualFold(u.Host, "github.com") {
		return repoURL, nil
	}
	id, err := s.installationFor(ctx, orgID)
	if errors.Is(err, ErrNotConnected) {
		return repoURL, nil
	}
	if err != nil {
		return "", err
	}
	c, err := s.client(ctx)
	if errors.Is(err, ErrNotConfigured) {
		return repoURL, nil
	}
	if err != nil {
		return "", err
	}
	token, err := c.InstallationToken(ctx, id)
	if err != nil {
		return "", fmt.Errorf("github installation token: %w", err)
	}
	u.User = url.UserPassword("x-access-token", token)
	return u.String(), nil
}
