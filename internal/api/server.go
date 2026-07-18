package api

import (
	"context"
	"time"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/config"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/deployments"
	"github.com/bograh/cargo/internal/events"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/bograh/cargo/internal/reconciler"
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

// AppService is satisfied by *apps.Service.
type AppService interface {
	Create(ctx context.Context, orgID, actor pgtype.UUID, in apps.CreateInput) (sqlc.Application, error)
	List(ctx context.Context, orgID, actor pgtype.UUID) ([]sqlc.Application, error)
	Get(ctx context.Context, appID, actor pgtype.UUID) (sqlc.Application, error)
	Update(ctx context.Context, appID, actor pgtype.UUID, in apps.UpdateInput) (sqlc.Application, error)
	Delete(ctx context.Context, appID, actor pgtype.UUID) error
	SetEnvVars(ctx context.Context, appID, actor pgtype.UUID, vars map[string]string) error
	ListEnvKeys(ctx context.Context, appID, actor pgtype.UUID) ([]string, error)
	DeleteEnvVar(ctx context.Context, appID, actor pgtype.UUID, key string) error
}

type Server struct {
	cfg      config.Config
	pool     *pgxpool.Pool
	settings SettingsStore
	auth     AuthService
	orgs     OrgService
	admin    AdminStore
	apps     AppService
	deps     DeploymentService
	enqueue  Enqueuer
	hub      *events.Hub
	logPath  func(id string) string
	provider reconciler.DeployProvider
}

// DeploymentService is satisfied by *deployments.Service.
type DeploymentService interface {
	Create(ctx context.Context, appID, actor pgtype.UUID, trigger string) (sqlc.Deployment, error)
	Rollback(ctx context.Context, appID, actor, targetID pgtype.UUID) (sqlc.Deployment, error)
	List(ctx context.Context, appID, actor pgtype.UUID) ([]sqlc.Deployment, error)
	Get(ctx context.Context, deploymentID, actor pgtype.UUID) (sqlc.Deployment, error)
	Finish(ctx context.Context, id pgtype.UUID, status, errMsg string) error
}

// Enqueuer inserts deploy jobs; satisfied by *jobs.Enqueuer.
type Enqueuer interface {
	EnqueueDeploy(ctx context.Context, deploymentID string) error
}

// WireDeployments attaches the deployment service, job enqueuer, SSE hub,
// and deploy provider (used to tear down containers on app deletion).
func (s *Server) WireDeployments(d *deployments.Service, e Enqueuer, hub *events.Hub, p reconciler.DeployProvider) {
	s.deps = d
	s.enqueue = e
	s.hub = hub
	s.logPath = d.LogPath
	s.provider = p
}

// NewServer builds a Server. pool/box may be nil in tests that stub dependencies.
func NewServer(cfg config.Config, pool *pgxpool.Pool, box *crypto.Box) *Server {
	s := &Server{cfg: cfg, pool: pool}
	if pool != nil {
		s.settings = sqlc.New(pool)
		s.auth = auth.NewService(pool)
		s.orgs = orgs.NewService(pool)
		s.admin = sqlc.New(pool)
		if box != nil {
			s.apps = apps.NewService(pool, box)
		}
	}
	return s
}

func (s *Server) Handler() *chi.Mux { return NewRouter(s) }
