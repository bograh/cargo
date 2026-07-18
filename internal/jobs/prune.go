package jobs

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/deployments"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// keepDeployments is the per-app retention window for logs and images.
const keepDeployments = 5

type PruneArgs struct{}

func (PruneArgs) Kind() string { return "prune" }

type PruneWorker struct {
	river.WorkerDefaults[PruneArgs]
	Pool *pgxpool.Pool
	Deps *deployments.Service
}

func (w *PruneWorker) Work(ctx context.Context, _ *river.Job[PruneArgs]) error {
	return RunPrune(ctx, w.Pool, w.Deps)
}

// RunPrune keeps the newest keepDeployments per app and removes older
// deployments' rows, log files, and images (best effort on images).
func RunPrune(ctx context.Context, pool *pgxpool.Pool, deps *deployments.Service) error {
	q := sqlc.New(pool)
	rows, err := pool.Query(ctx, `SELECT DISTINCT app_id FROM deployments`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var appIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		appIDs = append(appIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, appID := range appIDs {
		id, err := uuidOf(appID)
		if err != nil {
			return err
		}
		old, err := q.ListPrunableDeployments(ctx, sqlc.ListPrunableDeploymentsParams{
			AppID: id, Offset: keepDeployments,
		})
		if err != nil {
			return fmt.Errorf("list prunable for %s: %w", appID, err)
		}
		for _, dep := range old {
			_ = os.Remove(deps.LogPath(uuidStr(dep.ID)))
			if dep.ImageTag != "" {
				_ = exec.CommandContext(ctx, "docker", "rmi", "-f", dep.ImageTag).Run()
			}
			if err := q.DeleteDeployment(ctx, dep.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
