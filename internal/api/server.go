package api

import (
	"context"

	"github.com/bograh/cargo/internal/config"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SettingsStore is satisfied by *dbsqlc.Queries.
type SettingsStore interface {
	GetInstanceSetting(ctx context.Context, key string) (sqlc.InstanceSetting, error)
}

type Server struct {
	cfg      config.Config
	pool     *pgxpool.Pool
	settings SettingsStore
}

// NewServer builds a Server. pool may be nil in tests that stub settings.
func NewServer(cfg config.Config, pool *pgxpool.Pool) *Server {
	s := &Server{cfg: cfg, pool: pool}
	if pool != nil {
		s.settings = sqlc.New(pool)
	}
	return s
}

func (s *Server) Handler() *chi.Mux { return NewRouter(s) }
