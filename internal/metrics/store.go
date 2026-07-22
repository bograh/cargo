package metrics

import (
	"context"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is a thin sqlc-backed implementation of api.MetricStore.
type Store struct{ q *sqlc.Queries }

// NewStore builds a Store over the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{q: sqlc.New(pool)} }

// ListSince returns an app's metric samples recorded since the given time.
func (s *Store) ListSince(ctx context.Context, appID pgtype.UUID, since time.Time) ([]sqlc.AppMetric, error) {
	return s.q.ListAppMetricsSince(ctx, sqlc.ListAppMetricsSinceParams{
		AppID: appID, CreatedAt: pgtype.Timestamptz{Time: since, Valid: true},
	})
}

// Latest returns the most recently recorded sample for an app.
func (s *Store) Latest(ctx context.Context, appID pgtype.UUID) (sqlc.AppMetric, error) {
	return s.q.LatestAppMetric(ctx, appID)
}
