package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/bograh/cargo/internal/api"
	"github.com/bograh/cargo/internal/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	r := api.NewRouter()
	slog.Info("cargo listening", "addr", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, r); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
