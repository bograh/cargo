package oidc

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db"
	"github.com/bograh/cargo/internal/settings"
)

func startService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("cargo"), tcpostgres.WithUsername("cargo"), tcpostgres.WithPassword("cargo"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	dbURL, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	box, err := crypto.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(pool, settings.NewService(pool, box)), pool
}

func configure(t *testing.T, svc *Service, idp *mockIDP) {
	t.Helper()
	err := svc.SetConfig(context.Background(), settings.OIDCConfig{
		IssuerURL: idp.issuer(), ClientID: idp.ClientID, ClientSecret: "secret",
	})
	if err != nil {
		t.Fatalf("set config: %v", err)
	}
}

const redirectURL = "http://cargo.localhost/api/v1/auth/oidc/callback"

func TestUnconfigured(t *testing.T) {
	svc, _ := startService(t)
	ctx := context.Background()

	if ok, err := svc.Configured(ctx); err != nil || ok {
		t.Fatalf("configured = %v, %v", ok, err)
	}
	if _, err := svc.StartURL(ctx, redirectURL, "st", "no"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("start err = %v", err)
	}
	if _, err := svc.ResolveCallback(ctx, redirectURL, "code", "no"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("callback err = %v", err)
	}
}

func TestSetConfigUndiscoverableIssuer(t *testing.T) {
	svc, _ := startService(t)
	err := svc.SetConfig(context.Background(), settings.OIDCConfig{
		IssuerURL: "http://127.0.0.1:1/realms/nope", ClientID: "id", ClientSecret: "secret",
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v", err)
	}
	if ok, _ := svc.Configured(context.Background()); ok {
		t.Fatal("config persisted despite failed discovery")
	}
}

func TestStartURL(t *testing.T) {
	svc, _ := startService(t)
	idp := newMockIDP(t)
	configure(t, svc, idp)

	raw, err := svc.StartURL(context.Background(), redirectURL, "the-state", "the-nonce")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, idp.issuer()+"/authorize") {
		t.Fatalf("url = %q", raw)
	}
	q := u.Query()
	if q.Get("state") != "the-state" || q.Get("nonce") != "the-nonce" || q.Get("redirect_uri") != redirectURL {
		t.Fatalf("query = %v", q)
	}
	if q.Get("scope") != "openid email" {
		t.Fatalf("scope = %q", q.Get("scope"))
	}

	if ok, err := svc.Configured(context.Background()); err != nil || !ok {
		t.Fatalf("configured = %v, %v", ok, err)
	}
	iss, cid, ok, err := svc.PublicConfig(context.Background())
	if err != nil || !ok || iss != idp.issuer() || cid != idp.ClientID {
		t.Fatalf("public config = %q, %q, %v, %v", iss, cid, ok, err)
	}
}

func TestResolveCallbackProvisionAndIdentity(t *testing.T) {
	svc, _ := startService(t)
	idp := newMockIDP(t)
	configure(t, svc, idp)
	ctx := context.Background()

	idp.Sub, idp.Email, idp.Nonce = "sub-1", "First@Example.com", "n1"
	first, err := svc.ResolveCallback(ctx, redirectURL, "code", "n1")
	if err != nil {
		t.Fatalf("first callback: %v", err)
	}
	if first.Email != "first@example.com" || !first.IsInstanceAdmin || first.PasswordHash.Valid {
		t.Fatalf("first user = %+v", first)
	}

	idp.Sub, idp.Email, idp.Nonce = "sub-2", "second@example.com", "n2"
	second, err := svc.ResolveCallback(ctx, redirectURL, "code", "n2")
	if err != nil {
		t.Fatalf("second callback: %v", err)
	}
	if second.IsInstanceAdmin {
		t.Fatal("second SSO user should not be instance admin")
	}

	// Same identity resolves to the same user, even if the email changed.
	idp.Sub, idp.Email, idp.Nonce = "sub-1", "renamed@example.com", "n3"
	again, err := svc.ResolveCallback(ctx, redirectURL, "code", "n3")
	if err != nil {
		t.Fatalf("repeat callback: %v", err)
	}
	if again.ID != first.ID {
		t.Fatal("identity did not resolve to the original user")
	}
}

func TestResolveCallbackLinksVerifiedEmail(t *testing.T) {
	svc, pool := startService(t)
	idp := newMockIDP(t)
	configure(t, svc, idp)
	ctx := context.Background()

	authSvc := auth.NewService(pool)
	existing, _, err := authSvc.Register(ctx, "linked@example.com", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}

	idp.Sub, idp.Email, idp.Nonce = "sub-link", "linked@example.com", "n1"
	u, err := svc.ResolveCallback(ctx, redirectURL, "code", "n1")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if u.ID != existing.ID {
		t.Fatal("SSO login did not link to the existing account")
	}
	if _, _, err := authSvc.Login(ctx, "linked@example.com", "correct-horse-battery"); err != nil {
		t.Fatalf("password login broken after link: %v", err)
	}
}

func TestResolveCallbackRejections(t *testing.T) {
	svc, _ := startService(t)
	idp := newMockIDP(t)
	configure(t, svc, idp)
	ctx := context.Background()

	idp.Sub, idp.Email, idp.Nonce = "sub-x", "x@example.com", "good-nonce"
	idp.EmailVerified = false
	if _, err := svc.ResolveCallback(ctx, redirectURL, "code", "good-nonce"); !errors.Is(err, ErrValidation) {
		t.Fatalf("unverified email err = %v", err)
	}

	idp.EmailVerified = true
	if _, err := svc.ResolveCallback(ctx, redirectURL, "code", "other-nonce"); !errors.Is(err, ErrValidation) {
		t.Fatalf("wrong nonce err = %v", err)
	}

	idp.Email = ""
	if _, err := svc.ResolveCallback(ctx, redirectURL, "code", "good-nonce"); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing email err = %v", err)
	}
}
