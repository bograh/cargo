package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool defaults.
//
// pgx sizes a pool at max(4, NumCPU) connections by default, which on the
// two-vCPU host this product targets is four — shared between every HTTP
// handler, River's job queue and its notifier, and the 15-second metrics
// collector. A deploy holding pg_advisory_lock alongside a couple of open SSE
// streams can starve ordinary requests out of a pool that size.
//
// The lifetime and health-check settings matter for a different reason: a
// connection that died with the database (a restart, a failover) is otherwise
// discovered by a user's failed request rather than by the pool.
const (
	defaultMaxConns          = 20
	defaultMinConns          = 2
	defaultMaxConnLifetime   = time.Hour
	defaultMaxConnIdleTime   = 30 * time.Minute
	defaultHealthCheckPeriod = time.Minute
)

// applyPoolDefaults fills in pool settings the connection string did not set.
// ParseConfig already honours pool_max_conns and friends, so an operator who
// tuned the DSN keeps their values and everyone else gets something usable
// instead of pgx's CPU-count default.
func applyPoolDefaults(cfg *pgxpool.Config, dsn string) {
	unset := func(param string) bool { return !strings.Contains(dsn, param) }
	if unset("pool_max_conns") {
		cfg.MaxConns = defaultMaxConns
	}
	if unset("pool_min_conns") {
		cfg.MinConns = defaultMinConns
	}
	if unset("pool_max_conn_lifetime") {
		cfg.MaxConnLifetime = defaultMaxConnLifetime
	}
	if unset("pool_max_conn_idle_time") {
		cfg.MaxConnIdleTime = defaultMaxConnIdleTime
	}
	if unset("pool_health_check_period") {
		cfg.HealthCheckPeriod = defaultHealthCheckPeriod
	}
	// A min above max is rejected at connect time; clamp rather than fail on a
	// half-tuned DSN.
	if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}
}

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	applyPoolDefaults(cfg, url)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}
