package api

import (
	"context"
	"time"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/config"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/databases"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/deployments"
	"github.com/bograh/cargo/internal/events"
	"github.com/bograh/cargo/internal/github"
	"github.com/bograh/cargo/internal/oidc"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/bograh/cargo/internal/settings"
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
	IssueSession(ctx context.Context, userID pgtype.UUID) (auth.Tokens, error)
}

// OIDCService is satisfied by *oidc.Service.
type OIDCService interface {
	Configured(ctx context.Context) (bool, error)
	PublicConfig(ctx context.Context) (issuerURL, clientID string, configured bool, err error)
	SetConfig(ctx context.Context, cfg settings.OIDCConfig) error
	ClearConfig(ctx context.Context) error
	StartURL(ctx context.Context, redirectURL, state, nonce string) (string, error)
	ResolveCallback(ctx context.Context, redirectURL, code, wantNonce string) (sqlc.User, error)
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
	CreateInvite(ctx context.Context, orgID, actor pgtype.UUID, role, email string, ttl time.Duration) (string, sqlc.Invite, error)
	ListInvites(ctx context.Context, orgID, actor pgtype.UUID) ([]sqlc.Invite, error)
	RevokeInvite(ctx context.Context, orgID, actor, inviteID pgtype.UUID) error
	AcceptInvite(ctx context.Context, token string, userID pgtype.UUID) (sqlc.Organization, error)
	PreviewInvite(ctx context.Context, token string) (orgs.InvitePreview, error)
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
	SetDesiredState(ctx context.Context, appID, actor pgtype.UUID, state string) (sqlc.Application, error)
	SetDesiredStateRaw(ctx context.Context, appID pgtype.UUID, state string) error
	SetEnvVars(ctx context.Context, appID, actor pgtype.UUID, vars map[string]string) error
	ListEnvKeys(ctx context.Context, appID, actor pgtype.UUID) ([]string, error)
	DeleteEnvVar(ctx context.Context, appID, actor pgtype.UUID, key string) error
	AddDomain(ctx context.Context, appID, actor pgtype.UUID, hostname, appsSuffix string) (sqlc.Domain, error)
	ListDomains(ctx context.Context, appID, actor pgtype.UUID) ([]sqlc.Domain, error)
	RemoveDomain(ctx context.Context, appID, actor, domainID pgtype.UUID) error
}

type Server struct {
	cfg              config.Config
	pool             *pgxpool.Pool
	settings         SettingsStore
	auth             AuthService
	orgs             OrgService
	admin            AdminStore
	apps             AppService
	deps             DeploymentService
	databases        DatabaseService
	enqueue          Enqueuer
	hub              *events.Hub
	logPath          func(id string) string
	provider         reconciler.DeployProvider
	gh               GitHubService
	webhookApps      WebhookApps
	instanceSettings InstanceSettings
	box              *crypto.Box
	oidc             OIDCService
	metrics          MetricStore
	notify           NotifyService
}

// NotifyService is satisfied by *notify.Service.
type NotifyService interface {
	SetWebhook(ctx context.Context, url string) error
	WebhookConfigured(ctx context.Context) bool
}

// WireNotify attaches the notification service used by the admin webhook API.
func (s *Server) WireNotify(n NotifyService) { s.notify = n }

// MetricStore is satisfied by *metrics.Store.
type MetricStore interface {
	ListSince(ctx context.Context, appID pgtype.UUID, since time.Time) ([]sqlc.AppMetric, error)
	Latest(ctx context.Context, appID pgtype.UUID) (sqlc.AppMetric, error)
}

// DatabaseService is satisfied by *databases.Service.
type DatabaseService interface {
	Create(ctx context.Context, orgID, actor pgtype.UUID, in databases.CreateInput) (sqlc.DatabaseInstance, error)
	List(ctx context.Context, orgID, actor pgtype.UUID) ([]databases.InstanceSummary, error)
	Get(ctx context.Context, id, actor pgtype.UUID) (databases.Detail, error)
	Delete(ctx context.Context, id, actor pgtype.UUID) error
	Attach(ctx context.Context, instanceID, appID, actor pgtype.UUID) (string, sqlc.DatabaseAttachment, error)
	Detach(ctx context.Context, instanceID, appID, actor pgtype.UUID) error
	Snapshot(ctx context.Context, id, actor pgtype.UUID) (string, error)
	ListSnapshots(ctx context.Context, id, actor pgtype.UUID) ([]databases.SnapshotInfo, error)
	SnapshotPath(ctx context.Context, id, actor pgtype.UUID, name string) (string, error)
	DeleteSnapshot(ctx context.Context, id, actor pgtype.UUID, name string) error
	LogPath(id string) string
	MarkError(ctx context.Context, id pgtype.UUID) error
}

// InstanceSettings is satisfied by *settings.Service.
type InstanceSettings interface {
	Suffix(ctx context.Context) (string, error)
	SetSuffix(ctx context.Context, suffix string) error
	SMTP(ctx context.Context) (*settings.SMTPConfig, error)
	SetSMTP(ctx context.Context, cfg settings.SMTPConfig) error
	ClearSMTP(ctx context.Context) error
}

// GitHubService is satisfied by *github.Service.
type GitHubService interface {
	AppStatus(ctx context.Context) (configured bool, slug string, appID int64, err error)
	SaveApp(ctx context.Context, cfg github.AppConfig) error
	CreateFromManifest(ctx context.Context, code string) (string, error)
	WebhookSecret(ctx context.Context) (string, error)
	InstallURL(ctx context.Context, state string) (string, error)
	OrgStatus(ctx context.Context, orgID pgtype.UUID) (connected bool, accountLogin string, err error)
	ConnectOrg(ctx context.Context, orgID pgtype.UUID, installationID int64) error
	Repos(ctx context.Context, orgID pgtype.UUID) ([]github.Repo, error)
	Branches(ctx context.Context, orgID pgtype.UUID, fullName string) ([]string, error)
}

// WebhookApps is satisfied by *sqlc.Queries.
type WebhookApps interface {
	ListGitAppsByBranch(ctx context.Context, gitBranch string) ([]sqlc.Application, error)
}

// DeploymentService is satisfied by *deployments.Service.
type DeploymentService interface {
	Create(ctx context.Context, appID, actor pgtype.UUID, trigger string) (sqlc.Deployment, error)
	CreateSystem(ctx context.Context, appID pgtype.UUID, trigger string) (sqlc.Deployment, error)
	Rollback(ctx context.Context, appID, actor, targetID pgtype.UUID) (sqlc.Deployment, error)
	List(ctx context.Context, appID, actor pgtype.UUID) ([]sqlc.Deployment, error)
	Get(ctx context.Context, deploymentID, actor pgtype.UUID) (sqlc.Deployment, error)
	Finish(ctx context.Context, id pgtype.UUID, status, errMsg string) error
}

// Enqueuer inserts deploy/db-provision/backup jobs; satisfied by *jobs.Enqueuer.
type Enqueuer interface {
	EnqueueDeploy(ctx context.Context, deploymentID string) error
	EnqueueDBProvision(ctx context.Context, instanceID string) error
	EnqueuePlatformBackup(ctx context.Context) error
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

// WireDatabases attaches the managed-databases service and wires its
// engine-level attachment cleanup into the app-deletion path, so deleting an
// app also drops the postgres roles / redis ACL users of its attachments
// (their rows cascade with the app, but the engine credentials do not).
func (s *Server) WireDatabases(d DatabaseService) {
	s.databases = d
	if a, ok := s.apps.(*apps.Service); ok {
		if c, ok := d.(apps.AttachmentCleaner); ok {
			a.SetAttachmentCleaner(c)
		}
	}
}

// WireMetrics attaches the metrics store used by the metrics endpoints.
func (s *Server) WireMetrics(m MetricStore) { s.metrics = m }

// NewServer builds a Server. pool/box may be nil in tests that stub dependencies.
func NewServer(cfg config.Config, pool *pgxpool.Pool, box *crypto.Box) *Server {
	s := &Server{cfg: cfg, pool: pool, box: box}
	if pool != nil {
		s.settings = sqlc.New(pool)
		s.auth = auth.NewService(pool)
		s.orgs = orgs.NewService(pool)
		s.admin = sqlc.New(pool)
		s.webhookApps = sqlc.New(pool)
		if box != nil {
			s.apps = apps.NewService(pool, box)
			s.gh = github.NewService(pool, box)
			s.instanceSettings = settings.NewService(pool, box)
			s.oidc = oidc.NewService(pool, settings.NewService(pool, box))
		}
	}
	return s
}

func (s *Server) Handler() *chi.Mux { return NewRouter(s) }
