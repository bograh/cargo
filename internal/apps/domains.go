package apps

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

var hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

var ErrDomainTaken = errors.New("domain already attached")

// AddDomain attaches a custom domain (admin+, FR-5.2). Hostnames under the
// instance apps-suffix are rejected — they collide with auto subdomains.
func (s *Service) AddDomain(ctx context.Context, appID, actor pgtype.UUID, hostname, appsSuffix string) (sqlc.Domain, error) {
	app, err := s.appFor(ctx, appID, actor, "admin")
	if err != nil {
		return sqlc.Domain{}, err
	}
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if len(hostname) > 253 || !hostnameRe.MatchString(hostname) {
		return sqlc.Domain{}, fmt.Errorf("%w: hostname is not a valid domain name", ErrValidation)
	}
	if appsSuffix != "" && strings.HasSuffix(hostname, "."+strings.ToLower(appsSuffix)) {
		return sqlc.Domain{}, fmt.Errorf("%w: domains under %s are reserved for auto subdomains", ErrValidation, appsSuffix)
	}
	d, err := s.q.CreateDomain(ctx, sqlc.CreateDomainParams{AppID: app.ID, Hostname: hostname})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return sqlc.Domain{}, ErrDomainTaken
	}
	return d, err
}

func (s *Service) ListDomains(ctx context.Context, appID, actor pgtype.UUID) ([]sqlc.Domain, error) {
	app, err := s.appFor(ctx, appID, actor, "viewer")
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListDomainsForApp(ctx, app.ID)
	if rows == nil {
		rows = []sqlc.Domain{}
	}
	return rows, err
}

func (s *Service) RemoveDomain(ctx context.Context, appID, actor, domainID pgtype.UUID) error {
	app, err := s.appFor(ctx, appID, actor, "admin")
	if err != nil {
		return err
	}
	return s.q.DeleteDomain(ctx, sqlc.DeleteDomainParams{ID: domainID, AppID: app.ID})
}

// CustomDomains is pipeline-facing: hostnames only, no actor check.
func (s *Service) CustomDomains(ctx context.Context, appID pgtype.UUID) ([]string, error) {
	rows, err := s.q.ListDomainsForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, d := range rows {
		out = append(out, d.Hostname)
	}
	return out, nil
}
