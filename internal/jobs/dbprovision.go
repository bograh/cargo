package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/bograh/cargo/internal/databases"
	"github.com/riverqueue/river"
)

// DBProvisionArgs carries the target managed-database instance id.
type DBProvisionArgs struct {
	InstanceID string `json:"instance_id"`
}

func (DBProvisionArgs) Kind() string { return "provision_database" }

// dbProvisionTimeout bounds a provisioning job. It must cover a cold image
// pull (mysql/mongo images are hundreds of MB) plus first-boot init; River's
// 1-minute default cancels the pull mid-flight.
const dbProvisionTimeout = 30 * time.Minute

// DBProvisionWorker runs container-level provisioning for a managed database
// instance. Provisioning is not idempotent-safe, so the worker always
// returns nil: a provider failure is recorded as instance status "error" by
// databases.Service.Provision and surfaced via the UI, but must not trigger
// River's retry machinery.
type DBProvisionWorker struct {
	river.WorkerDefaults[DBProvisionArgs]
	Databases *databases.Service
}

func (w *DBProvisionWorker) Timeout(*river.Job[DBProvisionArgs]) time.Duration {
	return dbProvisionTimeout
}

func (w *DBProvisionWorker) Work(ctx context.Context, job *river.Job[DBProvisionArgs]) error {
	id, err := uuidOf(job.Args.InstanceID)
	if err != nil {
		return fmt.Errorf("bad instance id %q: %w", job.Args.InstanceID, err)
	}
	if err := w.Databases.Provision(ctx, id); err != nil {
		slog.Error("database provisioning failed", "instance_id", job.Args.InstanceID, "err", err)
	}
	return nil
}
