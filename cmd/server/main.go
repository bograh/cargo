package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/bograh/cargo/internal/api"
	"github.com/bograh/cargo/internal/config"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}
	ctx := context.Background()
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

	box, err := crypto.New(cfg.MasterKey)
	if err != nil {
		slog.Error("invalid master key", "err", err)
		os.Exit(1)
	}

	srv := api.NewServer(cfg, pool, box)
	slog.Info("cargo listening", "addr", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, srv.Handler()); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
