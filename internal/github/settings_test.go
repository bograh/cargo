package github

import (
	"bytes"
	"context"
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
}
