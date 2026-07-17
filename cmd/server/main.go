package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/bograh/cargo/internal/api"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	r := api.NewRouter()
	slog.Info("cargo listening", "addr", ":8080")
	if err := http.ListenAndServe(":8080", r); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
