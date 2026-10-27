package github

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestAppConfigRoundTripEncrypted(t *testing.T) {
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("cargo"), tcpostgres.WithUsername("cargo"), tcpostgres.WithPassword("cargo"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	q := sqlc.New(pool)
	box, err := crypto.New(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}

	// Unset → nil, no error.
	cfg, err := LoadAppConfig(ctx, q, box)
	if err != nil || cfg != nil {
		t.Fatalf("unset = %v, %v", cfg, err)
	}

	in := AppConfig{AppID: 99, AppSlug: "cargo", PrivateKey: "PEMKEY", WebhookSecret: "hush-hush"}
	if err := SaveAppConfig(ctx, q, box, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := LoadAppConfig(ctx, q, box)
	if err != nil || out == nil || *out != in {
		t.Fatalf("load = %+v, %v", out, err)
	}

	// Raw row must not contain the secret.
	row, err := q.GetInstanceSetting(ctx, "github_app")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(row.Value), "hush-hush") || strings.Contains(string(row.Value), "PEMKEY") {
		t.Fatal("secrets stored in plaintext")
	}

	// OAuth client credentials ride inside the same sealed blob.
	svc := &Service{q: q, box: box, NewClientFn: NewClient, oauth: fakeGitHub(t, []int64{42})}
	if _, verified, err := svc.OwnershipVerification(ctx); err != nil || verified {
		t.Fatalf("before client creds: verified=%v err=%v", verified, err)
	}
	if ok, err := svc.VerifyInstallation(ctx, "", 42); err != nil || ok {
		t.Fatalf("unverifiable app must fall back: ok=%v err=%v", ok, err)
	}
	if err := svc.SaveOAuthClient(ctx, "cid", ""); !errors.Is(err, ErrClientSecretRequired) {
		t.Fatalf("new client id without secret: %v", err)
	}
	if err := svc.SaveOAuthClient(ctx, "cid", "csecret"); err != nil {
		t.Fatal(err)
	}
	// Blank secret with the same client ID keeps the stored secret.
	if err := svc.SaveOAuthClient(ctx, "cid", ""); err != nil {
		t.Fatal(err)
	}
	clientID, verified, err := svc.OwnershipVerification(ctx)
	if err != nil || !verified || clientID != "cid" {
		t.Fatalf("after save: id=%q verified=%v err=%v", clientID, verified, err)
	}
	row, err = q.GetInstanceSetting(ctx, "github_app")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(row.Value), "csecret") {
		t.Fatal("client secret stored in plaintext")
	}
	// Re-saving the app credentials without OAuth fields keeps them.
	if err := svc.SaveApp(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, verified, _ := svc.OwnershipVerification(ctx); !verified {
		t.Fatal("re-saving app credentials dropped the OAuth client")
	}
	if ok, err := svc.VerifyInstallation(ctx, "good-code", 42); err != nil || !ok {
		t.Fatalf("owned installation: ok=%v err=%v", ok, err)
	}
	if _, err := svc.VerifyInstallation(ctx, "good-code", 7); !errors.Is(err, ErrInstallationNotAccessible) {
		t.Fatalf("foreign installation: %v", err)
	}
	if _, err := svc.VerifyInstallation(ctx, "", 42); !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("missing code: %v", err)
	}
}
