package orgs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrInviteInvalid = errors.New("invite is invalid, revoked, or expired")

func newInviteToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("token: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(tok))
	return tok, sum[:], nil
}

func (s *Service) CreateInvite(ctx context.Context, orgID, actor pgtype.UUID, role string, ttl time.Duration) (string, sqlc.Invite, error) {
	if !ValidRole(role) {
		return "", sqlc.Invite{}, ErrBadRole
	}
	m, err := s.membership(ctx, orgID, actor)
	if err != nil {
		return "", sqlc.Invite{}, err
	}
	if !RoleAtLeast(m.Role, "admin") {
		return "", sqlc.Invite{}, ErrForbidden
	}
	token, hash, err := newInviteToken()
	if err != nil {
		return "", sqlc.Invite{}, err
	}
	inv, err := s.q.CreateInvite(ctx, sqlc.CreateInviteParams{
		OrgID:     orgID,
		TokenHash: hash,
		Role:      role,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true},
		CreatedBy: actor,
	})
	return token, inv, err
}

func (s *Service) ListInvites(ctx context.Context, orgID, actor pgtype.UUID) ([]sqlc.Invite, error) {
	m, err := s.membership(ctx, orgID, actor)
	if err != nil {
		return nil, err
	}
	if !RoleAtLeast(m.Role, "admin") {
		return nil, ErrForbidden
	}
	rows, err := s.q.ListInvitesForOrg(ctx, orgID)
	if rows == nil {
		rows = []sqlc.Invite{}
	}
	return rows, err
}

func (s *Service) RevokeInvite(ctx context.Context, orgID, actor, inviteID pgtype.UUID) error {
	m, err := s.membership(ctx, orgID, actor)
	if err != nil {
		return err
	}
	if !RoleAtLeast(m.Role, "admin") {
		return ErrForbidden
	}
	return s.q.RevokeInvite(ctx, sqlc.RevokeInviteParams{ID: inviteID, OrgID: orgID})
}

func (s *Service) AcceptInvite(ctx context.Context, token string, userID pgtype.UUID) (sqlc.Organization, error) {
	sum := sha256.Sum256([]byte(token))
	inv, err := s.q.GetInviteByTokenHash(ctx, sum[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Organization{}, ErrInviteInvalid
	}
	if err != nil {
		return sqlc.Organization{}, err
	}
	if inv.RevokedAt.Valid || time.Now().After(inv.ExpiresAt.Time) {
		return sqlc.Organization{}, ErrInviteInvalid
	}
	if err := s.AddMember(ctx, inv.OrgID, userID, inv.Role); err != nil {
		return sqlc.Organization{}, err
	}
	return s.q.GetOrganizationByID(ctx, inv.OrgID)
}
