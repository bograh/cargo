package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type HousekeepingArgs struct{}

func (HousekeepingArgs) Kind() string { return "housekeeping" }

type HousekeepingWorker struct {
	river.WorkerDefaults[HousekeepingArgs]
	Pool *pgxpool.Pool
	// AuditRetentionDays bounds audit-log history (0 → default 180).
	AuditRetentionDays int
}

func (w *HousekeepingWorker) Work(ctx context.Context, _ *river.Job[HousekeepingArgs]) error {
	sessions, invites, err := RunHousekeeping(ctx, w.Pool, w.AuditRetentionDays)
	if err != nil {
		return err
	}
	if sessions > 0 || invites > 0 {
		slog.Info("housekeeping purge", "sessions", sessions, "invites", invites)
	}
	return nil
}

// RunHousekeeping deletes expired/stale sessions, dead invites, metrics older
// than 48h, and audit entries older than the retention window.
func RunHousekeeping(ctx context.Context, pool *pgxpool.Pool, auditRetentionDays int) (sessions, invites int64, err error) {
	q := sqlc.New(pool)
	sessions, err = q.PurgeSessions(ctx)
	if err != nil {
		return 0, 0, err
	}
	invites, err = q.PurgeInvites(ctx)
	if err != nil {
		return sessions, invites, err
	}
	if _, err = q.PurgeAppMetrics(ctx); err != nil {
		return sessions, invites, err
	}
	if auditRetentionDays <= 0 {
		auditRetentionDays = 180
	}
	cutoff := pgtype.Timestamptz{Time: time.Now().AddDate(0, 0, -auditRetentionDays), Valid: true}
	if _, err = q.PurgeAuditLog(ctx, cutoff); err != nil {
		return sessions, invites, err
	}
	return sessions, invites, nil
}
