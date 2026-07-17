package api

import (
	"context"

	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/config"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SettingsStore is satisfied by *dbsqlc.Queries.
type SettingsStore interface {
	GetInstanceSetting(ctx context.Context, key string) (sqlc.InstanceSetting, error)
}

// AuthService is satisfied by *auth.Service.
type AuthService interface {
	Register(ctx context.Context, email, password string) (sqlc.User, auth.Tokens, error)
	Login(ctx context.Context, email, password string) (sqlc.User, auth.Tokens, error)
	Refresh(ctx context.Context, refreshToken string) (auth.Tokens, error)
	Logout(ctx context.Context, accessToken string) error
	UserForAccessToken(ctx context.Context, accessToken string) (sqlc.User, error)
}

type Server struct {
	cfg      config.Config
	pool     *pgxpool.Pool
	settings SettingsStore
	auth     AuthService
}

// NewServer builds a Server. pool may be nil in tests that stub dependencies.
func NewServer(cfg config.Config, pool *pgxpool.Pool) *Server {
	s := &Server{cfg: cfg, pool: pool}
	if pool != nil {
		s.settings = sqlc.New(pool)
		s.auth = auth.NewService(pool)
	}
	return s
}

func (s *Server) Handler() *chi.Mux { return NewRouter(s) }
