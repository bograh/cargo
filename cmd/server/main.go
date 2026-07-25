package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bograh/cargo/internal/api"
	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/builder"
	"github.com/bograh/cargo/internal/config"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/databases"
	"github.com/bograh/cargo/internal/db"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/deployments"
	"github.com/bograh/cargo/internal/events"
	"github.com/bograh/cargo/internal/github"
	"github.com/bograh/cargo/internal/jobs"
	"github.com/bograh/cargo/internal/metrics"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		slog.Error("migrations failed", "err", err)
		os.Exit(1)
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database unavailable", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := jobs.Migrate(ctx, pool); err != nil {
		slog.Error("river migrations failed", "err", err)
		os.Exit(1)
	}

	box, err := crypto.New(cfg.MasterKey)
	if err != nil {
		slog.Error("invalid master key", "err", err)
		os.Exit(1)
	}

	hub := events.NewHub()
	appSvc := apps.NewService(pool, box)
	depSvc := deployments.NewService(pool, hub, cfg.DataDir)
	provider := reconciler.NewDocker(cfg.DataDir)
	dbSvc := databases.NewService(pool, box, provider, cfg.DataDir)
	ghSvc := github.NewService(pool, box)
	pipeline := &jobs.Pipeline{
		Pool:             pool,
		Apps:             appSvc,
		Deployments:      depSvc,
		Provider:         provider,
		NewBuilder:       builder.ForName,
		Clone:            builder.CloneAtBranch,
		CloneAuth:        ghSvc.CloneAuth,
		DataDir:          cfg.DataDir,
		AppsDomainSuffix: appsDomainSuffix(pool),
		DBEnv:            dbSvc.EnvFor,
		DefaultMemLimit:  cfg.DefaultMemLimit,
		DefaultCPULimit:  cfg.DefaultCPULimit,
		DefaultPidsLimit: cfg.DefaultPidsLimit,
	}
	traefikURL := getenvDefault("CARGO_TRAEFIK_METRICS_URL", "http://traefik:8082/metrics")
	collector := metrics.NewCollector(pool, hub, provider, traefikURL)
	backuper := &jobs.PlatformBackuper{
		DataDir:     cfg.DataDir,
		DatabaseURL: cfg.DatabaseURL,
		MasterKey:   cfg.MasterKey,
		Keep:        cfg.PlatformBackupKeep,
		Project:     cfg.ComposeProject,
		DBService:   cfg.PlatformDBService,
		// Alerter wired in m11 Task 8 (notifications); nil = no alerts for now.
	}
	diskChecker := &jobs.DiskChecker{
		Pool:       pool,
		DataDir:    cfg.DataDir,
		MinFreePct: cfg.DiskMinFreePct,
		// Alerter wired in m11 Task 8.
	}
	client, err := jobs.NewClient(pool, pipeline, dbSvc, collector, backuper, diskChecker)
	if err != nil {
		slog.Error("job client init failed", "err", err)
		os.Exit(1)
	}
	if err := client.Start(ctx); err != nil {
		slog.Error("job client start failed", "err", err)
		os.Exit(1)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.Stop(stopCtx); err != nil {
			slog.Warn("job client stop", "err", err)
		}
	}()

	// Fail deployments left mid-flight by a prior crash/restart so they don't
	// hang in a non-terminal state forever (recent ones resume via River).
	if n, err := depSvc.ReapOrphaned(ctx, 15*time.Minute); err != nil {
		slog.Warn("orphan reaper failed", "err", err)
	} else if n > 0 {
		slog.Info("reaped orphaned deployments", "count", n)
	}

	srv := api.NewServer(cfg, pool, box)
	enqueuer := &jobs.Enqueuer{Client: client}
	srv.WireDeployments(depSvc, enqueuer, hub, provider)
	srv.WireMetrics(metrics.NewStore(pool))
	srv.WireDatabases(dbSvc)

	httpServer := &http.Server{Addr: cfg.HTTPAddr, Handler: srv.Handler()}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutCtx)
	}()
	slog.Info("cargo listening", "addr", cfg.HTTPAddr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}

// appsDomainSuffix reads the configurable suffix from instance settings,
// defaulting to apps.localhost.
func appsDomainSuffix(pool *pgxpool.Pool) func(context.Context) string {
	q := sqlc.New(pool)
	return func(ctx context.Context) string {
		row, err := q.GetInstanceSetting(ctx, "apps_domain_suffix")
		if errors.Is(err, pgx.ErrNoRows) {
			return "apps.localhost"
		}
		if err != nil {
			slog.Warn("apps_domain_suffix read failed", "err", err)
			return "apps.localhost"
		}
		var v string
		if err := json.Unmarshal(row.Value, &v); err != nil || v == "" {
			return "apps.localhost"
		}
		return v
	}
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
