package settings

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("cargo"), tcpostgres.WithUsername("cargo"), tcpostgres.WithPassword("cargo"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")))
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
	box, err := crypto.New(bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(pool, box), pool
}

func TestSuffix(t *testing.T) {
	svc, _ := startService(t)
	ctx := context.Background()

	got, err := svc.Suffix(ctx)
	if err != nil || got != DefaultSuffix {
		t.Fatalf("default = %q, %v", got, err)
	}
	for _, bad := range []string{"https://apps.example.com", "apps example.com", "single", "-x.example.com"} {
		if err := svc.SetSuffix(ctx, bad); !errors.Is(err, ErrValidation) {
			t.Fatalf("suffix %q err = %v", bad, err)
		}
	}
	if err := svc.SetSuffix(ctx, "apps.example.com"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err = svc.Suffix(ctx)
	if err != nil || got != "apps.example.com" {
		t.Fatalf("suffix = %q, %v", got, err)
	}
}

func TestSMTPRoundTripEncrypted(t *testing.T) {
	svc, pool := startService(t)
	ctx := context.Background()

	cfg, err := svc.SMTP(ctx)
	if err != nil || cfg != nil {
		t.Fatalf("unset = %v, %v", cfg, err)
	}
	in := SMTPConfig{Host: "smtp.example.com", Port: 587, Username: "mailer", Password: "mail-secret", From: "cargo@example.com"}
	if err := svc.SetSMTP(ctx, in); err != nil {
		t.Fatalf("set: %v", err)
	}
	out, err := svc.SMTP(ctx)
	if err != nil || out == nil || *out != in {
		t.Fatalf("get = %+v, %v", out, err)
	}
	row, err := sqlc.New(pool).GetInstanceSetting(ctx, "smtp")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(row.Value), "mail-secret") {
		t.Fatal("smtp password stored in plaintext")
	}
	if err := svc.ClearSMTP(ctx); err != nil {
		t.Fatal(err)
	}
	if out, err := svc.SMTP(ctx); err != nil || out != nil {
		t.Fatalf("after clear = %v, %v", out, err)
	}
	if err := svc.SetSMTP(ctx, SMTPConfig{Host: "", Port: 0}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid smtp err = %v", err)
	}
}
