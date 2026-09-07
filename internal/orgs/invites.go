package orgs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrInviteInvalid = errors.New("invite is invalid, revoked, expired, or already used")

// ErrInviteWrongEmail is returned when an invite addressed to one person is
// presented by an account with a different address.
var ErrInviteWrongEmail = errors.New("this invite was sent to a different email address")

// usable reports whether an invite can still be accepted. A link invite (no
// email) is shareable by design and stays usable until it expires or is
// revoked; one addressed to a person is single-use.
func usable(inv sqlc.Invite) bool {
	return !inv.RevokedAt.Valid && !inv.AcceptedAt.Valid && time.Now().Before(inv.ExpiresAt.Time)
}

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
	if !usable(inv) {
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

// AcceptInvite joins the invited user to the organisation.
//
// An invite addressed to someone is honoured only for that address, and only
// once. Both halves were missing: a forwarded or leaked link granted membership
// to whoever opened it, repeatedly, until it expired. A link invite carries no
// address and stays shareable — that is what it is for.
//
// Claiming and joining happen in one transaction, and the claim is the
// conditional UPDATE itself, so two people redeeming the same addressed invite
// cannot both win.
func (s *Service) AcceptInvite(ctx context.Context, token string, userID pgtype.UUID) (sqlc.Organization, error) {
	sum := sha256.Sum256([]byte(token))
	inv, err := s.q.GetInviteByTokenHash(ctx, sum[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Organization{}, ErrInviteInvalid
	}
	if err != nil {
		return sqlc.Organization{}, err
	}
	if !usable(inv) {
		return sqlc.Organization{}, ErrInviteInvalid
	}
	if inv.Email.Valid {
		u, err := s.q.GetUserByID(ctx, userID)
		if err != nil {
			return sqlc.Organization{}, err
		}
		if !sameEmail(u.Email, inv.Email.String) {
			return sqlc.Organization{}, ErrInviteWrongEmail
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sqlc.Organization{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	if inv.Email.Valid {
		// WHERE accepted_at IS NULL makes this the claim: no rows means someone
		// else redeemed it between the read above and here.
		claimed, err := q.MarkInviteAccepted(ctx, inv.ID)
		if err != nil {
			return sqlc.Organization{}, err
		}
		if claimed == 0 {
			return sqlc.Organization{}, ErrInviteInvalid
		}
	}
	// Already a member — accepting twice is a no-op, as it always was. The
	// insert runs inside a savepoint because in Postgres a failed statement
	// aborts the entire transaction: without one, swallowing the unique
	// violation left every following command failing with 25P02, so the
	// "no-op" path failed the accept anyway.
	sp, err := tx.Begin(ctx)
	if err != nil {
		return sqlc.Organization{}, err
	}
	if _, cerr := s.q.WithTx(sp).CreateMembership(ctx, sqlc.CreateMembershipParams{
		OrgID: inv.OrgID, UserID: userID, Role: inv.Role,
	}); cerr != nil {
		_ = sp.Rollback(ctx)
		var pgErr *pgconn.PgError
		if !errors.As(cerr, &pgErr) || pgErr.Code != "23505" {
			return sqlc.Organization{}, cerr
		}
	} else if err := sp.Commit(ctx); err != nil {
		return sqlc.Organization{}, err
	}
	org, err := q.GetOrganizationByID(ctx, inv.OrgID)
	if err != nil {
		return sqlc.Organization{}, err
	}
	return org, tx.Commit(ctx)
}

// sameEmail compares two addresses the way registration stores them: trimmed
// and lowercased.
func sameEmail(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
