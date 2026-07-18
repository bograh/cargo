package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestNullPasswordHashCannotPasswordLogin(t *testing.T) {
	pool := startPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	q := sqlc.New(pool)

	// SSO-provisioned account: NULL password_hash.
	u, err := q.CreateUser(ctx, sqlc.CreateUserParams{Email: "sso@example.com", PasswordHash: pgtype.Text{}})
	if err != nil {
		t.Fatalf("create user with NULL hash: %v", err)
	}
	if u.PasswordHash.Valid {
		t.Fatal("password hash should be NULL")
	}

	if _, _, err := svc.Login(ctx, "sso@example.com", "password-123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login err = %v, want ErrInvalidCredentials", err)
	}
}

func TestIdentityRoundTrip(t *testing.T) {
	pool := startPool(t)
	ctx := context.Background()
	q := sqlc.New(pool)

	u, err := q.CreateUser(ctx, sqlc.CreateUserParams{Email: "sso@example.com", PasswordHash: pgtype.Text{}})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	id, err := q.CreateIdentity(ctx, sqlc.CreateIdentityParams{
		UserID:  u.ID,
		Issuer:  "https://idp.example.com",
		Subject: "sub-123",
		Email:   "sso@example.com",
	})
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	got, err := q.GetIdentityByIssuerSubject(ctx, sqlc.GetIdentityByIssuerSubjectParams{
		Issuer:  "https://idp.example.com",
		Subject: "sub-123",
	})
	if err != nil {
		t.Fatalf("get identity: %v", err)
	}
	if got.ID != id.ID || got.UserID != u.ID || got.Email != "sso@example.com" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}
