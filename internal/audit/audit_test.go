package audit

import (
	"context"
	"testing"
	"time"

	"github.com/bograh/cargo/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("cargo"),
		tcpostgres.WithUsername("cargo"),
		tcpostgres.WithPassword("cargo"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}
	if err := db.Migrate(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestRecordListPurge(t *testing.T) {
	pool := startPool(t)
	s := NewService(pool)
	ctx := context.Background()

	// Record with NULL actor/org (allowed) — the write must never error out.
	s.Record(ctx, pgtype.UUID{}, pgtype.UUID{}, "POST", "apps", "app-1",
		map[string]any{"path": "/api/v1/apps/app-1/deploy", "status": 200})

	rows, err := s.ListAll(ctx, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].Action != "POST" || rows[0].TargetID != "app-1" {
		t.Fatalf("unexpected rows: %+v", rows)
	}

	// Nothing older than the future cutoff-of-now-minus... use a past cutoff: no purge.
	past := pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	if n, err := s.Purge(ctx, past); err != nil || n != 0 {
		t.Fatalf("purge(past) = %d, %v; want 0", n, err)
	}
	// Future cutoff → the entry (created now) is older than it → purged.
	future := pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
	if n, err := s.Purge(ctx, future); err != nil || n != 1 {
		t.Fatalf("purge(future) = %d, %v; want 1", n, err)
	}
}
