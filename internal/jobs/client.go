package jobs

import (
	"context"
	"time"

	"github.com/bograh/cargo/internal/databases"
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
// cap: 2 workers, FR-4.4) and a daily retention prune.
func NewClient(pool *pgxpool.Pool, p *Pipeline, dbSvc *databases.Service) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &DeployWorker{P: p})
	river.AddWorker(workers, &PruneWorker{Pool: pool, Deps: p.Deployments})
	river.AddWorker(workers, &DomainCheckWorker{Pool: pool})
	river.AddWorker(workers, &HousekeepingWorker{Pool: pool})
	river.AddWorker(workers, &DBProvisionWorker{Databases: dbSvc})
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			"deploy":           {MaxWorkers: 2},
			river.QueueDefault: {MaxWorkers: 2},
		},
		Workers: workers,
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(24*time.Hour),
				func() (river.JobArgs, *river.InsertOpts) { return PruneArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: false},
			),
			river.NewPeriodicJob(
				river.PeriodicInterval(10*time.Minute),
				func() (river.JobArgs, *river.InsertOpts) { return DomainCheckArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true},
			),
			river.NewPeriodicJob(
				river.PeriodicInterval(24*time.Hour),
				func() (river.JobArgs, *river.InsertOpts) { return HousekeepingArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true},
			),
		},
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

// EnqueueDBProvision inserts a provision_database job for the given instance.
func (e *Enqueuer) EnqueueDBProvision(ctx context.Context, instanceID string) error {
	_, err := e.Client.Insert(ctx, DBProvisionArgs{InstanceID: instanceID}, nil)
	return err
}
