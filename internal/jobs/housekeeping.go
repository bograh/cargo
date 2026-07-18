package jobs

import (
	"context"
	"log/slog"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type HousekeepingArgs struct{}

func (HousekeepingArgs) Kind() string { return "housekeeping" }

type HousekeepingWorker struct {
	river.WorkerDefaults[HousekeepingArgs]
	Pool *pgxpool.Pool
}

func (w *HousekeepingWorker) Work(ctx context.Context, _ *river.Job[HousekeepingArgs]) error {
	sessions, invites, err := RunHousekeeping(ctx, w.Pool)
	if err != nil {
		return err
	}
	if sessions > 0 || invites > 0 {
		slog.Info("housekeeping purge", "sessions", sessions, "invites", invites)
	}
	return nil
}

// RunHousekeeping deletes expired/stale sessions and dead invites.
func RunHousekeeping(ctx context.Context, pool *pgxpool.Pool) (sessions, invites int64, err error) {
	q := sqlc.New(pool)
	sessions, err = q.PurgeSessions(ctx)
	if err != nil {
		return 0, 0, err
	}
	invites, err = q.PurgeInvites(ctx)
	return sessions, invites, err
}
