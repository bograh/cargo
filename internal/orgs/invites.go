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

// CreateInvite issues an invite. email is optional: a non-empty value ties the
// invite to a recipient (used to email the link and prefill the accept page); an
// empty value creates a shareable link invite.
func (s *Service) CreateInvite(ctx context.Context, orgID, actor pgtype.UUID, role, email string, ttl time.Duration) (string, sqlc.Invite, error) {
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
	var emailCol pgtype.Text
	if email != "" {
		emailCol = pgtype.Text{String: email, Valid: true}
	}
	inv, err := s.q.CreateInvite(ctx, sqlc.CreateInviteParams{
		OrgID:     orgID,
		TokenHash: hash,
		Role:      role,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true},
		CreatedBy: actor,
		Email:     emailCol,
	})
	return token, inv, err
}

// InvitePreview is the public, unauthenticated view of an invite.
type InvitePreview struct {
	OrgName string
	Role    string
	Email   string
}

// PreviewInvite resolves a raw token to its org/role/email without requiring
// authentication, so an invited user can see what they're joining before they
// sign in. Returns ErrInviteInvalid for unknown/revoked/expired tokens.
func (s *Service) PreviewInvite(ctx context.Context, token string) (InvitePreview, error) {
	sum := sha256.Sum256([]byte(token))
	inv, err := s.q.GetInviteByTokenHash(ctx, sum[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return InvitePreview{}, ErrInviteInvalid
	}
	if err != nil {
		return InvitePreview{}, err
	}
	if inv.RevokedAt.Valid || time.Now().After(inv.ExpiresAt.Time) {
		return InvitePreview{}, ErrInviteInvalid
	}
	org, err := s.q.GetOrganizationByID(ctx, inv.OrgID)
	if err != nil {
		return InvitePreview{}, err
	}
	return InvitePreview{OrgName: org.Name, Role: inv.Role, Email: inv.Email.String}, nil
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
