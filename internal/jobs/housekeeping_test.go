package jobs

import (
	"context"
	"testing"

	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestRunHousekeepingPurgesStaleRows(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	pool := f.pipeline.Pool

	// A live session (from setup's register) must survive; add stale rows.
	if _, _, err := auth.NewService(pool).Register(ctx, "second@x.co", "password-123"); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `
		UPDATE sessions SET refresh_expires_at = now() - interval '1 day'
		WHERE user_id = (SELECT id FROM users WHERE email = 'second@x.co')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO invites (org_id, token_hash, role, expires_at, created_by, revoked_at)
		SELECT o.id, 'stale-tok', 'member', now() - interval '30 days', u.id, now() - interval '30 days'
		FROM organizations o, users u WHERE u.email = 'o@x.co' LIMIT 1`)
	if err != nil {
		t.Fatal(err)
	}

	sessions, invites, err := RunHousekeeping(ctx, pool, 180)
	if err != nil {
		t.Fatalf("housekeeping: %v", err)
	}
	if sessions != 1 || invites != 1 {
		t.Fatalf("purged sessions=%d invites=%d, want 1 and 1", sessions, invites)
	}

	var liveSessions int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&liveSessions); err != nil {
		t.Fatal(err)
	}
	if liveSessions == 0 {
		t.Fatal("valid sessions were purged too")
	}
}

func TestPurgeAppMetrics(t *testing.T) {
	pool := startPool(t)
	ctx := context.Background()
	q := sqlc.New(pool)
	// Seed an org+app to satisfy the FK.
	var appID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		WITH o AS (INSERT INTO organizations (name, slug) VALUES ('m', 'm-metrics') RETURNING id)
		INSERT INTO applications (org_id, name, slug, source_type, image_ref, exposed_port)
		SELECT id, 'a', 'a-metrics', 'image', 'nginx', 80 FROM o RETURNING id`).Scan(&appID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// One fresh row, one 49h-old row.
	if err := q.CreateAppMetric(ctx, sqlc.CreateAppMetricParams{AppID: appID}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_metrics (app_id, created_at) VALUES ($1, now() - interval '49 hours')`, appID); err != nil {
		t.Fatal(err)
	}
	n, err := q.PurgeAppMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}
}
