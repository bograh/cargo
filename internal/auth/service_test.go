package auth

import (
	"context"
	"errors"
	"testing"
)

func TestRegisterFirstUserIsInstanceAdmin(t *testing.T) {
	svc := NewService(startPool(t))
	ctx := context.Background()

	u1, tok, err := svc.Register(ctx, "First@Example.com", "password-123")
	if err != nil {
		t.Fatalf("register 1: %v", err)
	}
	if !u1.IsInstanceAdmin {
		t.Fatal("first user should be instance admin")
	}
	if u1.Email != "first@example.com" {
		t.Fatalf("email not lowercased: %q", u1.Email)
	}
	if tok.Access == "" || tok.Refresh == "" {
		t.Fatal("tokens missing")
	}

	u2, _, err := svc.Register(ctx, "second@example.com", "password-123")
	if err != nil {
		t.Fatalf("register 2: %v", err)
	}
	if u2.IsInstanceAdmin {
		t.Fatal("second user must not be instance admin")
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	svc := NewService(startPool(t))
	ctx := context.Background()
	if _, _, err := svc.Register(ctx, "a@b.co", "password-123"); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, _, err := svc.Register(ctx, "a@b.co", "password-456")
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("err = %v, want ErrEmailTaken", err)
	}
}

func TestRegisterWeakPassword(t *testing.T) {
	svc := NewService(startPool(t))
	_, _, err := svc.Register(context.Background(), "a@b.co", "short")
	if !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("err = %v, want ErrWeakPassword", err)
	}
}

func TestLoginAndAccessToken(t *testing.T) {
	svc := NewService(startPool(t))
	ctx := context.Background()
	if _, _, err := svc.Register(ctx, "a@b.co", "password-123"); err != nil {
		t.Fatalf("register: %v", err)
	}

	u, tok, err := svc.Login(ctx, "A@B.CO", "password-123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	got, err := svc.UserForAccessToken(ctx, tok.Access)
	if err != nil {
		t.Fatalf("UserForAccessToken: %v", err)
	}
	if got.ID != u.ID {
		t.Fatal("access token resolved to wrong user")
	}

	if _, _, err := svc.Login(ctx, "a@b.co", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password err = %v", err)
	}
	if _, _, err := svc.Login(ctx, "nobody@b.co", "password-123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown email err = %v", err)
	}
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	svc := NewService(startPool(t))
	ctx := context.Background()
	_, tok1, err := svc.Register(ctx, "a@b.co", "password-123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	tok2, err := svc.Refresh(ctx, tok1.Refresh)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tok2.Refresh == tok1.Refresh || tok2.Access == tok1.Access {
		t.Fatal("tokens not rotated")
	}

	// Reusing the already-rotated refresh token must fail AND revoke the family.
	if _, err := svc.Refresh(ctx, tok1.Refresh); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("reuse err = %v, want ErrUnauthenticated", err)
	}
	if _, err := svc.Refresh(ctx, tok2.Refresh); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("family not revoked after reuse: %v", err)
	}
	if _, err := svc.UserForAccessToken(ctx, tok2.Access); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("access token usable after family revoke: %v", err)
	}
}

func TestLogoutRevokesFamily(t *testing.T) {
	svc := NewService(startPool(t))
	ctx := context.Background()
	_, tok, err := svc.Register(ctx, "a@b.co", "password-123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := svc.Logout(ctx, tok.Access); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.UserForAccessToken(ctx, tok.Access); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("access token usable after logout: %v", err)
	}
	if _, err := svc.Refresh(ctx, tok.Refresh); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("refresh usable after logout: %v", err)
	}
}
