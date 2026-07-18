package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotConfigured = errors.New("github app is not configured")
var ErrNotConnected = errors.New("organization has no github installation")

// Service is the API-facing façade: it loads the encrypted app config per
// call and owns the org↔installation mapping.
type Service struct {
	q   *sqlc.Queries
	box *crypto.Box
	// NewClientFn allows tests to intercept client construction.
	NewClientFn func(cfg AppConfig) *Client
}

func NewService(pool *pgxpool.Pool, box *crypto.Box) *Service {
	return &Service{q: sqlc.New(pool), box: box, NewClientFn: NewClient}
}

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

func (s *Service) SaveApp(ctx context.Context, cfg AppConfig) error {
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

func (s *Service) InstallURL(ctx context.Context, state string) (string, error) {
	c, err := s.client(ctx)
	if err != nil {
		return "", err
	}
	return c.InstallURL(state), nil
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
// callback). The account login is fetched best-effort.
func (s *Service) ConnectOrg(ctx context.Context, orgID pgtype.UUID, installationID int64) error {
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
