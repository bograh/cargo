package orgs

import (
	"context"
	"errors"
	"fmt"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound  = errors.New("organization not found")
	ErrForbidden = errors.New("insufficient role")
	ErrLastOwner = errors.New("organization must keep at least one owner")
	ErrBadRole   = errors.New("invalid role")
)

var roleRank = map[string]int{"viewer": 0, "member": 1, "admin": 2, "owner": 3}

// RoleAtLeast reports whether role meets the minimum required role.
func RoleAtLeast(role, min string) bool {
	r, ok := roleRank[role]
	m, ok2 := roleRank[min]
	return ok && ok2 && r >= m
}

func ValidRole(role string) bool { _, ok := roleRank[role]; return ok }

type Service struct {
	q *sqlc.Queries
	// pool is kept alongside q for the one operation that needs a transaction:
	// redeeming an invite has to mark it used and grant membership together.
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{q: sqlc.New(pool), pool: pool}
}

// membership returns the caller's membership or ErrNotFound (org is invisible
// to non-members — FR-2.4).
func (s *Service) membership(ctx context.Context, orgID, userID pgtype.UUID) (sqlc.Membership, error) {
	m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Membership{}, ErrNotFound
	}
	return m, err
}

func (s *Service) Create(ctx context.Context, name string, creator pgtype.UUID) (sqlc.Organization, error) {
	slug := slugify(name)
	org, err := s.q.CreateOrganization(ctx, sqlc.CreateOrganizationParams{Name: name, Slug: slug})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		org, err = s.q.CreateOrganization(ctx, sqlc.CreateOrganizationParams{
			Name: name, Slug: fmt.Sprintf("%s-%s", slug, randomSuffix()),
		})
	}
	if err != nil {
		return sqlc.Organization{}, err
	}
	_, err = s.q.CreateMembership(ctx, sqlc.CreateMembershipParams{
		OrgID: org.ID, UserID: creator, Role: "owner",
	})
	if err != nil {
		return sqlc.Organization{}, err
	}
	return org, nil
}

func (s *Service) ListForUser(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListOrganizationsForUserRow, error) {
	rows, err := s.q.ListOrganizationsForUser(ctx, userID)
	if rows == nil {
		rows = []sqlc.ListOrganizationsForUserRow{}
	}
	return rows, err
}

func (s *Service) Get(ctx context.Context, orgID, userID pgtype.UUID) (sqlc.Organization, string, error) {
	m, err := s.membership(ctx, orgID, userID)
	if err != nil {
		return sqlc.Organization{}, "", err
	}
	org, err := s.q.GetOrganizationByID(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Organization{}, "", ErrNotFound
	}
	return org, m.Role, err
}

func (s *Service) Delete(ctx context.Context, orgID, userID pgtype.UUID) error {
	m, err := s.membership(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if m.Role != "owner" {
		return ErrForbidden
	}
	return s.q.DeleteOrganization(ctx, orgID)
}

// AddMember inserts a membership directly (used by invite acceptance and tests).
func (s *Service) AddMember(ctx context.Context, orgID, userID pgtype.UUID, role string) error {
	if !ValidRole(role) {
		return ErrBadRole
	}
	_, err := s.q.CreateMembership(ctx, sqlc.CreateMembershipParams{OrgID: orgID, UserID: userID, Role: role})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil // already a member — accepting an invite twice is a no-op
	}
	return err
}

func (s *Service) ListMembers(ctx context.Context, orgID, userID pgtype.UUID) ([]sqlc.ListMembersRow, error) {
	if _, err := s.membership(ctx, orgID, userID); err != nil {
		return nil, err
	}
	rows, err := s.q.ListMembers(ctx, orgID)
	if rows == nil {
		rows = []sqlc.ListMembersRow{}
	}
	return rows, err
}

func (s *Service) UpdateRole(ctx context.Context, orgID, actor, target pgtype.UUID, role string) (sqlc.Membership, error) {
	if !ValidRole(role) {
		return sqlc.Membership{}, ErrBadRole
	}
	am, err := s.membership(ctx, orgID, actor)
	if err != nil {
		return sqlc.Membership{}, err
	}
	if !RoleAtLeast(am.Role, "admin") {
		return sqlc.Membership{}, ErrForbidden
	}
	tm, err := s.membership(ctx, orgID, target)
	if err != nil {
		return sqlc.Membership{}, err
	}
	if tm.Role == "owner" && role != "owner" {
		if err := s.ensureNotLastOwner(ctx, orgID); err != nil {
			return sqlc.Membership{}, err
		}
	}
	return s.q.UpdateMembershipRole(ctx, sqlc.UpdateMembershipRoleParams{
		OrgID: orgID, UserID: target, Role: role,
	})
}

func (s *Service) RemoveMember(ctx context.Context, orgID, actor, target pgtype.UUID) error {
	am, err := s.membership(ctx, orgID, actor)
	if err != nil {
		return err
	}
	// Admins can remove others; anyone can remove themselves (leave).
	if actor != target && !RoleAtLeast(am.Role, "admin") {
		return ErrForbidden
	}
	tm, err := s.membership(ctx, orgID, target)
	if err != nil {
		return err
	}
	if tm.Role == "owner" {
		if err := s.ensureNotLastOwner(ctx, orgID); err != nil {
			return err
		}
	}
	return s.q.DeleteMembership(ctx, sqlc.DeleteMembershipParams{OrgID: orgID, UserID: target})
}

func (s *Service) ensureNotLastOwner(ctx context.Context, orgID pgtype.UUID) error {
	n, err := s.q.CountOwners(ctx, orgID)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastOwner
	}
	return nil
}
