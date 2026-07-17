package api

import (
	"context"
	"time"

	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/config"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

// OrgService is satisfied by *orgs.Service.
type OrgService interface {
	Create(ctx context.Context, name string, creator pgtype.UUID) (sqlc.Organization, error)
	ListForUser(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListOrganizationsForUserRow, error)
	Get(ctx context.Context, orgID, userID pgtype.UUID) (sqlc.Organization, string, error)
	Delete(ctx context.Context, orgID, userID pgtype.UUID) error
	AddMember(ctx context.Context, orgID, userID pgtype.UUID, role string) error
	ListMembers(ctx context.Context, orgID, userID pgtype.UUID) ([]sqlc.ListMembersRow, error)
	UpdateRole(ctx context.Context, orgID, actor, target pgtype.UUID, role string) (sqlc.Membership, error)
	RemoveMember(ctx context.Context, orgID, actor, target pgtype.UUID) error
	CreateInvite(ctx context.Context, orgID, actor pgtype.UUID, role string, ttl time.Duration) (string, sqlc.Invite, error)
	ListInvites(ctx context.Context, orgID, actor pgtype.UUID) ([]sqlc.Invite, error)
	RevokeInvite(ctx context.Context, orgID, actor, inviteID pgtype.UUID) error
	AcceptInvite(ctx context.Context, token string, userID pgtype.UUID) (sqlc.Organization, error)
}

// AdminStore is satisfied by *sqlc.Queries.
type AdminStore interface {
	ListUsers(ctx context.Context) ([]sqlc.User, error)
	ListAllOrganizations(ctx context.Context) ([]sqlc.Organization, error)
}

type Server struct {
	cfg      config.Config
	pool     *pgxpool.Pool
	settings SettingsStore
	auth     AuthService
	orgs     OrgService
	admin    AdminStore
}

// NewServer builds a Server. pool may be nil in tests that stub dependencies.
func NewServer(cfg config.Config, pool *pgxpool.Pool) *Server {
	s := &Server{cfg: cfg, pool: pool}
	if pool != nil {
		s.settings = sqlc.New(pool)
		s.auth = auth.NewService(pool)
		s.orgs = orgs.NewService(pool)
		s.admin = sqlc.New(pool)
	}
	return s
}

func (s *Server) Handler() *chi.Mux { return NewRouter(s) }
