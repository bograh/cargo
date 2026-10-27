package orgs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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

// An admin manages members and admins, but owners are above them: an admin
// cannot promote anyone (themselves included) to owner, touch an owner's role,
// remove an owner, or invite an owner.
func TestAdminCannotEscalateOrTouchOwners(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	admin := register(t, authSvc, "admin@x.co")
	member := register(t, authSvc, "member@x.co")

	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddMember(ctx, org.ID, admin, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMember(ctx, org.ID, member, "member"); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.UpdateRole(ctx, org.ID, admin, admin, "owner"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin self-promote err = %v, want ErrForbidden", err)
	}
	if _, err := svc.UpdateRole(ctx, org.ID, admin, member, "owner"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin grant owner err = %v, want ErrForbidden", err)
	}
	if _, err := svc.UpdateRole(ctx, org.ID, admin, owner, "viewer"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin demote owner err = %v, want ErrForbidden", err)
	}
	if err := svc.RemoveMember(ctx, org.ID, admin, owner); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin remove owner err = %v, want ErrForbidden", err)
	}
	if _, _, err := svc.CreateInvite(ctx, org.ID, admin, "owner", "", time.Hour); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin invite owner err = %v, want ErrForbidden", err)
	}

	// What admins may still do.
	if _, err := svc.UpdateRole(ctx, org.ID, admin, member, "admin"); err != nil {
		t.Fatalf("admin promote member to admin: %v", err)
	}
	if _, _, err := svc.CreateInvite(ctx, org.ID, admin, "admin", "", time.Hour); err != nil {
		t.Fatalf("admin invite admin: %v", err)
	}
	// And owners may promote to owner.
	if _, err := svc.UpdateRole(ctx, org.ID, owner, admin, "owner"); err != nil {
		t.Fatalf("owner promote: %v", err)
	}
}

// Two owners demoting each other at once must not both succeed and leave the
// org ownerless.
func TestConcurrentOwnerDemotionKeepsAnOwner(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	a := register(t, authSvc, "a@x.co")
	b := register(t, authSvc, "b@x.co")
	org, err := svc.Create(ctx, "Acme", a)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMember(ctx, org.ID, b, "owner"); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		var wg sync.WaitGroup
		var errA, errB error
		wg.Go(func() { _, errA = svc.UpdateRole(ctx, org.ID, a, b, "admin") })
		wg.Go(func() { _, errB = svc.UpdateRole(ctx, org.ID, b, a, "admin") })
		wg.Wait()
		// Exactly one wins; the loser is refused (it is no longer an owner, or
		// would be removing the last one) — never a deadlock or a 500.
		for _, err := range []error{errA, errB} {
			if err != nil && !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrLastOwner) {
				t.Fatalf("unexpected error from concurrent demotion: %v", err)
			}
		}
		members, err := svc.ListMembers(ctx, org.ID, a)
		if err != nil {
			t.Fatal(err)
		}
		owners := 0
		for _, m := range members {
			if m.Role == "owner" {
				owners++
			}
		}
		if owners == 0 {
			t.Fatal("concurrent demotions left the org with no owner")
		}
		// Reset to two owners for the next round, using whoever is still owner.
		var still pgtype.UUID
		for _, m := range members {
			if m.Role == "owner" {
				still = m.UserID
			}
		}
		for _, u := range []pgtype.UUID{a, b} {
			if _, err := svc.UpdateRole(ctx, org.ID, still, u, "owner"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
