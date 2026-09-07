package db

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func poolConfig(t *testing.T, dsn string) *pgxpool.Config {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse %q: %v", dsn, err)
	}
	applyPoolDefaults(cfg, dsn)
	return cfg
}

// pgx sizes a pool at max(4, NumCPU), which on a two-vCPU host is four
// connections shared between every request handler, River, and the metrics
// collector.
func TestPoolDefaultsAreApplied(t *testing.T) {
	cfg := poolConfig(t, "postgres://cargo@db:5432/cargo")
	if cfg.MaxConns != defaultMaxConns {
		t.Errorf("MaxConns = %d, want %d", cfg.MaxConns, defaultMaxConns)
	}
	if cfg.MinConns != defaultMinConns {
		t.Errorf("MinConns = %d, want %d", cfg.MinConns, defaultMinConns)
	}
	if cfg.MaxConnLifetime != defaultMaxConnLifetime {
		t.Errorf("MaxConnLifetime = %s, want %s", cfg.MaxConnLifetime, defaultMaxConnLifetime)
	}
	if cfg.HealthCheckPeriod != defaultHealthCheckPeriod {
		t.Errorf("HealthCheckPeriod = %s, want %s", cfg.HealthCheckPeriod, defaultHealthCheckPeriod)
	}
}

// An operator who tuned the DSN keeps their values — the defaults only fill in
// what was left unset.
func TestPoolDefaultsYieldToTheDSN(t *testing.T) {
	cfg := poolConfig(t, "postgres://cargo@db:5432/cargo?pool_max_conns=50&pool_max_conn_lifetime=15m")
	if cfg.MaxConns != 50 {
		t.Errorf("MaxConns = %d, want the DSN's 50", cfg.MaxConns)
	}
	if cfg.MaxConnLifetime != 15*time.Minute {
		t.Errorf("MaxConnLifetime = %s, want the DSN's 15m", cfg.MaxConnLifetime)
	}
	// Untouched settings still get a default.
	if cfg.HealthCheckPeriod != defaultHealthCheckPeriod {
		t.Errorf("HealthCheckPeriod = %s, want %s", cfg.HealthCheckPeriod, defaultHealthCheckPeriod)
	}
}

// pgxpool rejects a minimum above the maximum at connect time. A half-tuned
// DSN should degrade, not refuse to start.
func TestPoolClampsMinAboveMax(t *testing.T) {
	cfg := poolConfig(t, "postgres://cargo@db:5432/cargo?pool_max_conns=3")
	if cfg.MinConns > cfg.MaxConns {
		t.Fatalf("MinConns %d exceeds MaxConns %d", cfg.MinConns, cfg.MaxConns)
	}
}
