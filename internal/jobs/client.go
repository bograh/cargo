package jobs

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// Migrate applies River's own schema migrations (runs at startup, FR-7.3 style).
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return err
	}
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	return err
}

// NewClient builds the River client with the deploy queue (build concurrency
// cap: 2 workers, FR-4.4) and housekeeping.
func NewClient(pool *pgxpool.Pool, p *Pipeline) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &DeployWorker{P: p})
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			"deploy": {MaxWorkers: 2},
		},
		Workers: workers,
	})
}

// Enqueuer is the API-facing seam for inserting deploy jobs.
type Enqueuer struct {
	Client *river.Client[pgx.Tx]
}

func (e *Enqueuer) EnqueueDeploy(ctx context.Context, deploymentID string) error {
	_, err := e.Client.Insert(ctx, DeployArgs{DeploymentID: deploymentID},
		&river.InsertOpts{Queue: "deploy"})
	return err
}
