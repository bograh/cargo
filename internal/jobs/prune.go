package jobs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/deployments"
	"github.com/jackc/pgx/v5/pgtype"
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
		if err := pruneApp(ctx, q, deps, id, nil); err != nil {
			return fmt.Errorf("prune %s: %w", appID, err)
		}
	}
	return nil
}

// imageRemover untags an image on a worker host. nil means the control
// plane only.
type imageRemover func(ctx context.Context, ref string) error

// pruneApp keeps the newest keepDeployments for one app and removes older
// finished deployments' rows, log files, and images. Live and in-flight
// deployments are never pruned (see ListPrunableDeployments). Image removal is
// best-effort.
//
// A tag is only removed once no retained deployment still references it. Two
// cases make that check necessary rather than relying on the keep window:
// a rollback creates a fresh deployment reusing an older deployment's image,
// and a blue/green hand-off has two colors running different images at once.
// Untagging an image out from under a running container leaves it unable to
// restart.
//
// Only tags Cargo built (app-<slug>:<id>) are removed. An image-source app's
// tag is a registry reference such as redis:7 that other apps and managed
// databases may share; `rmi -f` on it untagged their image too.
//
// remote, when set, also untags the image on the worker host the app runs on
// for deployments that ran there; the build copy on the control plane is
// always removed locally.
func pruneApp(ctx context.Context, q *sqlc.Queries, deps *deployments.Service, appID pgtype.UUID, remote imageRemover) error {
	old, err := q.ListPrunableDeployments(ctx, sqlc.ListPrunableDeploymentsParams{
		AppID: appID, Keep: keepDeployments,
	})
	if err != nil {
		return err
	}
	retainedTags, err := q.ListRetainedImageTags(ctx, sqlc.ListRetainedImageTagsParams{
		AppID: appID, Keep: keepDeployments,
	})
	if err != nil {
		return err
	}
	retained := make(map[string]bool, len(retainedTags))
	for _, tag := range retainedTags {
		retained[tag] = true
	}
	for _, dep := range old {
		_ = os.Remove(deps.LogPath(uuidStr(dep.ID)))
		if cargoBuiltTag(dep.ImageTag) && !retained[dep.ImageTag] {
			_ = exec.CommandContext(ctx, "docker", "rmi", "-f", dep.ImageTag).Run()
			if remote != nil && dep.HostID.Valid {
				_ = remote(ctx, dep.ImageTag)
			}
		}
		if err := q.DeleteDeployment(ctx, dep.ID); err != nil {
			return err
		}
	}
	return nil
}

// cargoBuiltTag reports whether ref is an image tag the build pipeline
// produced (see buildFromGit), as opposed to a user-supplied registry image.
func cargoBuiltTag(ref string) bool {
	return strings.HasPrefix(ref, "app-") && !strings.Contains(ref, "/")
}
