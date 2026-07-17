package api

import "github.com/go-chi/chi/v5"

func NewRouter() *chi.Mux {
	r := chi.NewMux()
	r.Get("/healthz", HealthHandler)
	return r
}
