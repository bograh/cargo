package orgs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bograh/cargo/internal/auth"
)

func TestInviteLifecycle(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	joiner := register(t, authSvc, "joiner@x.co")

	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	token, inv, err := svc.CreateInvite(ctx, org.ID, owner, "member", "", 24*time.Hour)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if token == "" || inv.Role != "member" {
		t.Fatalf("token=%q inv=%v", token, inv)
	}

	got, err := svc.AcceptInvite(ctx, token, joiner)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if got.ID != org.ID {
		t.Fatal("accepted into wrong org")
	}
	_, role, err := svc.Get(ctx, org.ID, joiner)
	if err != nil || role != "member" {
		t.Fatalf("joiner role = %q, %v", role, err)
	}
	// Accepting again is a no-op, not an error.
	if _, err := svc.AcceptInvite(ctx, token, joiner); err != nil {
		t.Fatalf("re-accept: %v", err)
	}
}

func TestInviteRevokedAndExpired(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	joiner := register(t, authSvc, "joiner@x.co")
	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	token, inv, err := svc.CreateInvite(ctx, org.ID, owner, "member", "", 24*time.Hour)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := svc.RevokeInvite(ctx, org.ID, owner, inv.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.AcceptInvite(ctx, token, joiner); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("revoked accept err = %v", err)
	}

	expTok, _, err := svc.CreateInvite(ctx, org.ID, owner, "member", "", -time.Hour)
	if err != nil {
		t.Fatalf("create expired: %v", err)
	}
	if _, err := svc.AcceptInvite(ctx, expTok, joiner); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("expired accept err = %v", err)
	}
	if _, err := svc.AcceptInvite(ctx, "bogus-token", joiner); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("bogus accept err = %v", err)
	}
}

func TestInviteRequiresAdmin(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	viewer := register(t, authSvc, "viewer@x.co")
	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := svc.AddMember(ctx, org.ID, viewer, "viewer"); err != nil {
		t.Fatalf("add viewer: %v", err)
	}
	if _, _, err := svc.CreateInvite(ctx, org.ID, viewer, "member", "", time.Hour); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer invite err = %v", err)
	}
}
