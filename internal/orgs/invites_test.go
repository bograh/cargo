package orgs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
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
	// Accepting again is a no-op, not an error. This is a link invite (no
	// email), which stays usable by design; the second accept hits the
	// membership unique constraint, and the savepoint around it is what keeps
	// the surrounding transaction alive.
	got2, err := svc.AcceptInvite(ctx, token, joiner)
	if err != nil {
		t.Fatalf("re-accept: %v", err)
	}
	if got2.ID != org.ID {
		t.Fatal("re-accept returned the wrong org")
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

// A link invite carries no address and is shareable by design. One addressed
// to a person is not: before this, a forwarded link let anyone join, and let
// them do it repeatedly until it expired.
func TestUsableInvite(t *testing.T) {
	future := pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
	past := pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}

	for _, tc := range []struct {
		name string
		inv  sqlc.Invite
		want bool
	}{
		{"open", sqlc.Invite{ExpiresAt: future}, true},
		{"expired", sqlc.Invite{ExpiresAt: past}, false},
		{"revoked", sqlc.Invite{ExpiresAt: future, RevokedAt: now}, false},
		{"already accepted", sqlc.Invite{ExpiresAt: future, AcceptedAt: now}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := usable(tc.inv); got != tc.want {
				t.Fatalf("usable = %v, want %v", got, tc.want)
			}
		})
	}
}

// Registration lowercases and trims the address it stores, so the comparison
// that binds an invite to its recipient has to do the same or it would reject
// the very person it was sent to.
func TestSameEmail(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"bob@x.co", "bob@x.co", true},
		{"Bob@X.co", "bob@x.co", true},
		{" bob@x.co ", "bob@x.co", true},
		{"bob@x.co", "mallory@x.co", false},
		{"", "bob@x.co", false},
	} {
		if got := sameEmail(tc.a, tc.b); got != tc.want {
			t.Fatalf("sameEmail(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
