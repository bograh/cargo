package orgs

import (
	"context"
	"errors"
	"testing"

	"github.com/bograh/cargo/internal/auth"
	"github.com/jackc/pgx/v5/pgtype"
)

func register(t *testing.T, svc *auth.Service, email string) pgtype.UUID {
	t.Helper()
	u, _, err := svc.Register(context.Background(), email, "password-123")
	if err != nil {
		t.Fatalf("register %s: %v", email, err)
	}
	return u.ID
}

func TestCreateOrgMakesCreatorOwner(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")

	org, err := svc.Create(ctx, "Acme Team", owner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if org.Slug != "acme-team" {
		t.Fatalf("slug = %q", org.Slug)
	}
	got, role, err := svc.Get(ctx, org.ID, owner)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != org.ID || role != "owner" {
		t.Fatalf("get = %v role %q", got, role)
	}
}

func TestOrgInvisibleToNonMembers(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	outsider := register(t, authSvc, "outsider@x.co")

	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := svc.Get(ctx, org.ID, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider get err = %v, want ErrNotFound", err)
	}
	if _, err := svc.ListMembers(ctx, org.ID, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider members err = %v", err)
	}
	list, err := svc.ListForUser(ctx, outsider)
	if err != nil || len(list) != 0 {
		t.Fatalf("outsider list = %v, %v", list, err)
	}
}

func TestSlugCollisionGetsSuffix(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")

	if _, err := svc.Create(ctx, "Acme", owner); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	org2, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if org2.Slug == "acme" || len(org2.Slug) <= len("acme") {
		t.Fatalf("collision slug = %q", org2.Slug)
	}
}

func TestDeleteRequiresOwner(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	member := register(t, authSvc, "member@x.co")

	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddMember(ctx, org.ID, member, "admin"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := svc.Delete(ctx, org.ID, member); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin delete err = %v, want ErrForbidden", err)
	}
	if err := svc.Delete(ctx, org.ID, owner); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
}

func TestRoleChangesAndLastOwnerGuard(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	member := register(t, authSvc, "member@x.co")

	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddMember(ctx, org.ID, member, "viewer"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// member (viewer) cannot change roles
	if _, err := svc.UpdateRole(ctx, org.ID, member, member, "admin"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer update err = %v", err)
	}
	// owner promotes member
	m, err := svc.UpdateRole(ctx, org.ID, owner, member, "admin")
	if err != nil || m.Role != "admin" {
		t.Fatalf("promote = %v, %v", m, err)
	}
	// cannot demote the last owner
	if _, err := svc.UpdateRole(ctx, org.ID, owner, owner, "member"); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("demote last owner err = %v", err)
	}
	// cannot remove the last owner
	if err := svc.RemoveMember(ctx, org.ID, owner, owner); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("remove last owner err = %v", err)
	}
	// invalid role rejected
	if _, err := svc.UpdateRole(ctx, org.ID, owner, member, "superuser"); !errors.Is(err, ErrBadRole) {
		t.Fatalf("bad role err = %v", err)
	}
}
