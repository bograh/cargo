package api

import (
	"github.com/bograh/cargo/internal/webui"
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

		r.Route("/auth", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(authRateLimiter())
				r.Post("/register", s.handleRegister)
				r.Post("/login", s.handleLogin)
				r.Post("/refresh", s.handleRefresh)
			})
			r.Post("/logout", s.handleLogout)
			r.With(s.requireAuth).Get("/me", s.handleMe)
		})
	})
	r.Mount("/", webui.Handler())
	return r
}
