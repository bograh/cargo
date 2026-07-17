package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(s *Server) *chi.Mux {
	r := chi.NewMux()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	r.Get("/healthz", HealthHandler)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/instance/info", s.getInstanceInfo)
	})
	return r
}
