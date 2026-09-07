package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound   = errors.New("application not found")
	ErrForbidden  = errors.New("insufficient role")
	ErrValidation = errors.New("validation failed")
)

var roleRank = map[string]int{"viewer": 0, "member": 1, "admin": 2, "owner": 3}

type RegistryCreds struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type CreateInput struct {
	Name            string
	SourceType      string
	Builder         string
	GitRepoURL      string
	GitBranch       string
	ImageRef        string
	ExposedPort     int32
	HealthcheckPath string
	AutoDeploy      bool
	BuildContext    string
	DockerfilePath  string
	BuildArgs       map[string]string
	RegistryCreds   *RegistryCreds
	// Resource caps; empty string / 0 means "use the instance default" (NULL).
	MemLimit  string
	CPULimit  string
	PidsLimit int32
	// NotifyOnSuccess opts this app into notifications on a successful deploy.
	NotifyOnSuccess bool
	// DeployStrategy overrides the instance default; "" means "use the default".
	DeployStrategy string
	// Compose source: the file's path in the repo and the service that
	// receives traffic.
	ComposePath    string
	ComposeService string
}

type UpdateInput struct {
	Name            *string
	Builder         *string
	GitBranch       *string
	ImageRef        *string
	HealthcheckPath *string
	BuildContext    *string
	DockerfilePath  *string
	ExposedPort     *int32
	AutoDeploy      *bool
	BuildArgs       map[string]string
	RegistryCreds   *RegistryCreds
	// Resource caps; nil leaves the value unchanged. A non-nil empty string /
	// zero clears the override back to the instance default (NULL).
	MemLimit        *string
	CPULimit        *string
	PidsLimit       *int32
	NotifyOnSuccess *bool
	// DeployStrategy; nil leaves it unchanged, a non-nil empty string clears
	// the override back to the instance default (NULL).
	DeployStrategy *string
	ComposePath    *string
	ComposeService *string
}

// AttachmentCleaner tears down the engine-level credentials for every managed
// database attached to an app. It is an optional collaborator (nil in tests
// and until wired) satisfied by *databases.Service, injected via a setter to
// avoid an apps -> databases import dependency.
type AttachmentCleaner interface {
	DetachAllForApp(ctx context.Context, appID pgtype.UUID) error
}

type Service struct {
	q *sqlc.Queries
	// pool is kept alongside q so a multi-statement write can run in one
	// transaction (SetEnvVars).
	pool    *pgxpool.Pool
	box     *crypto.Box
	cleaner AttachmentCleaner
	// maxLimits is the instance ceiling on per-app resource caps. The zero
	// value means no ceiling, which is what a single-operator install wants.
	maxLimits MaxLimits
	// allowPrivateGitHosts lets a repo URL name a host inside the deployment.
	// Off by default (CARGO_ALLOW_PRIVATE_GIT_HOSTS).
	allowPrivateGitHosts bool
}

func NewService(pool *pgxpool.Pool, box *crypto.Box) *Service {
	return &Service{q: sqlc.New(pool), pool: pool, box: box}
}

// SetAttachmentCleaner wires the managed-database cleanup collaborator used by
// Delete to drop engine credentials before an app row is removed.
func (s *Service) SetAttachmentCleaner(c AttachmentCleaner) { s.cleaner = c }

// SetMaxLimits installs the instance ceiling on per-app resource caps.
func (s *Service) SetMaxLimits(m MaxLimits) { s.maxLimits = m }

// SetAllowPrivateGitHosts opens repository URLs to hosts inside the
// deployment, for an install whose git server shares the control plane's
// private network.
func (s *Service) SetAllowPrivateGitHosts(v bool) { s.allowPrivateGitHosts = v }

// roleIn returns the actor's role in org or ErrNotFound (scoping, FR-2.4).
func (s *Service) roleIn(ctx context.Context, orgID, actor pgtype.UUID) (string, error) {
	m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: actor})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return m.Role, err
}

// appFor loads an app and verifies the actor holds at least minRole in its org.
func (s *Service) appFor(ctx context.Context, appID, actor pgtype.UUID, minRole string) (sqlc.Application, error) {
	app, err := s.q.GetApplication(ctx, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Application{}, ErrNotFound
	}
	if err != nil {
		return sqlc.Application{}, err
	}
	role, err := s.roleIn(ctx, app.OrgID, actor)
	if err != nil {
		return sqlc.Application{}, err
	}
	if roleRank[role] < roleRank[minRole] {
		return sqlc.Application{}, ErrForbidden
	}
	return app, nil
}

func validateCreate(in CreateInput, max MaxLimits, allowPrivateGit bool) error {
	if in.Name == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	if err := validatePort(in.ExposedPort); err != nil {
		return err
	}
	switch in.SourceType {
	case "git":
		if in.GitRepoURL == "" || in.GitBranch == "" {
			return fmt.Errorf("%w: git source requires git_repo_url and git_branch", ErrValidation)
		}
		if err := validateGitURL(in.GitRepoURL, allowPrivateGit); err != nil {
			return err
		}
	case "image":
		if in.ImageRef == "" {
			return fmt.Errorf("%w: image source requires image_ref", ErrValidation)
		}
	case "compose":
		if in.GitRepoURL == "" || in.GitBranch == "" {
			return fmt.Errorf("%w: compose source requires git_repo_url and git_branch", ErrValidation)
		}
		if err := validateGitURL(in.GitRepoURL, allowPrivateGit); err != nil {
			return err
		}
		if in.ComposeService == "" {
			return fmt.Errorf("%w: compose source requires compose_service (the service that serves HTTP)", ErrValidation)
		}
	default:
		return fmt.Errorf("%w: source_type must be git, image, or compose", ErrValidation)
	}
	switch in.Builder {
	case "", "auto", "dockerfile", "nixpacks":
	default:
		return fmt.Errorf("%w: builder must be auto, dockerfile, or nixpacks", ErrValidation)
	}
	if err := validateDeployStrategy(in.DeployStrategy); err != nil {
		return err
	}
	for _, f := range []struct{ field, path string }{
		{"dockerfile_path", in.DockerfilePath},
		{"build_context", in.BuildContext},
		{"compose_path", in.ComposePath},
	} {
		if err := validateRepoPath(f.field, f.path); err != nil {
			return err
		}
	}
	return validateLimits(in.MemLimit, in.CPULimit, in.PidsLimit, max)
}

func (s *Service) sealCreds(c *RegistryCreds) ([]byte, error) {
	if c == nil {
		return nil, nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return s.box.Seal(raw)
}

func (s *Service) Create(ctx context.Context, orgID, actor pgtype.UUID, in CreateInput) (sqlc.Application, error) {
	role, err := s.roleIn(ctx, orgID, actor)
	if err != nil {
		return sqlc.Application{}, err
	}
	if roleRank[role] < roleRank["member"] {
		return sqlc.Application{}, ErrForbidden
	}
	if err := validateCreate(in, s.maxLimits, s.allowPrivateGitHosts); err != nil {
		return sqlc.Application{}, err
	}
	if in.Builder == "" {
		in.Builder = "auto"
	}
	if in.HealthcheckPath == "" {
		in.HealthcheckPath = "/"
	}
	if in.BuildContext == "" {
		in.BuildContext = "."
	}
	if in.DockerfilePath == "" {
		in.DockerfilePath = "Dockerfile"
	}
	if in.SourceType == "compose" && in.ComposePath == "" {
		in.ComposePath = "docker-compose.yml"
	}
	if in.BuildArgs == nil {
		in.BuildArgs = map[string]string{}
	}
	argsJSON, err := json.Marshal(in.BuildArgs)
	if err != nil {
		return sqlc.Application{}, err
	}
	creds, err := s.sealCreds(in.RegistryCreds)
	if err != nil {
		return sqlc.Application{}, err
	}
	params := sqlc.CreateApplicationParams{
		OrgID: orgID, Name: in.Name, Slug: slugify(in.Name),
		SourceType: in.SourceType, Builder: in.Builder,
		GitRepoUrl: in.GitRepoURL, GitBranch: in.GitBranch, ImageRef: in.ImageRef,
		RegistryCredsEnc: creds, ExposedPort: in.ExposedPort,
		HealthcheckPath: in.HealthcheckPath, AutoDeploy: in.AutoDeploy,
		BuildContext: in.BuildContext, DockerfilePath: in.DockerfilePath, BuildArgs: argsJSON,
		MemLimit: textOrNull(in.MemLimit), CpuLimit: textOrNull(in.CPULimit),
		PidsLimit: int4OrNull(in.PidsLimit), NotifyOnSuccess: in.NotifyOnSuccess,
		DeployStrategy: textOrNull(in.DeployStrategy),
		ComposePath:    in.ComposePath, ComposeService: in.ComposeService,
	}
	app, err := s.q.CreateApplication(ctx, params)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		params.Slug = fmt.Sprintf("%s-%s", params.Slug, randomSuffix())
		app, err = s.q.CreateApplication(ctx, params)
	}
	return app, err
}

func (s *Service) List(ctx context.Context, orgID, actor pgtype.UUID) ([]sqlc.Application, error) {
	if _, err := s.roleIn(ctx, orgID, actor); err != nil {
		return nil, err
	}
	rows, err := s.q.ListApplicationsForOrg(ctx, orgID)
	if rows == nil {
		rows = []sqlc.Application{}
	}
	return rows, err
}

func (s *Service) Get(ctx context.Context, appID, actor pgtype.UUID) (sqlc.Application, error) {
	return s.appFor(ctx, appID, actor, "viewer")
}

func (s *Service) Update(ctx context.Context, appID, actor pgtype.UUID, in UpdateInput) (sqlc.Application, error) {
	app, err := s.appFor(ctx, appID, actor, "member")
	if err != nil {
		return sqlc.Application{}, err
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&app.Name, in.Name)
	set(&app.Builder, in.Builder)
	set(&app.GitBranch, in.GitBranch)
	set(&app.ImageRef, in.ImageRef)
	set(&app.HealthcheckPath, in.HealthcheckPath)
	set(&app.BuildContext, in.BuildContext)
	set(&app.DockerfilePath, in.DockerfilePath)
	set(&app.ComposePath, in.ComposePath)
	set(&app.ComposeService, in.ComposeService)
	for _, f := range []struct{ field, path string }{
		{"dockerfile_path", app.DockerfilePath},
		{"build_context", app.BuildContext},
		{"compose_path", app.ComposePath},
	} {
		if err := validateRepoPath(f.field, f.path); err != nil {
			return sqlc.Application{}, err
		}
	}
	if in.ExposedPort != nil {
		if err := validatePort(*in.ExposedPort); err != nil {
			return sqlc.Application{}, err
		}
		app.ExposedPort = *in.ExposedPort
	}
	if in.AutoDeploy != nil {
		app.AutoDeploy = *in.AutoDeploy
	}
	if in.BuildArgs != nil {
		raw, err := json.Marshal(in.BuildArgs)
		if err != nil {
			return sqlc.Application{}, err
		}
		app.BuildArgs = raw
	}
	if in.RegistryCreds != nil {
		creds, err := s.sealCreds(in.RegistryCreds)
		if err != nil {
			return sqlc.Application{}, err
		}
		app.RegistryCredsEnc = creds
	}
	if in.MemLimit != nil {
		app.MemLimit = textOrNull(*in.MemLimit)
	}
	if in.CPULimit != nil {
		app.CpuLimit = textOrNull(*in.CPULimit)
	}
	if in.PidsLimit != nil {
		app.PidsLimit = int4OrNull(*in.PidsLimit)
	}
	if in.NotifyOnSuccess != nil {
		app.NotifyOnSuccess = *in.NotifyOnSuccess
	}
	if in.DeployStrategy != nil {
		if err := validateDeployStrategy(*in.DeployStrategy); err != nil {
			return sqlc.Application{}, err
		}
		app.DeployStrategy = textOrNull(*in.DeployStrategy)
	}
	memLimit, cpuLimit := "", ""
	if app.MemLimit.Valid {
		memLimit = app.MemLimit.String
	}
	if app.CpuLimit.Valid {
		cpuLimit = app.CpuLimit.String
	}
	if err := validateLimits(memLimit, cpuLimit, app.PidsLimit.Int32, s.maxLimits); err != nil {
		return sqlc.Application{}, err
	}
	return s.q.UpdateApplication(ctx, sqlc.UpdateApplicationParams{
		ID: app.ID, Name: app.Name, Builder: app.Builder, GitBranch: app.GitBranch,
		ImageRef: app.ImageRef, ExposedPort: app.ExposedPort,
		HealthcheckPath: app.HealthcheckPath, AutoDeploy: app.AutoDeploy,
		BuildContext: app.BuildContext, DockerfilePath: app.DockerfilePath,
		BuildArgs: app.BuildArgs, RegistryCredsEnc: app.RegistryCredsEnc,
		MemLimit: app.MemLimit, CpuLimit: app.CpuLimit, PidsLimit: app.PidsLimit,
		NotifyOnSuccess: app.NotifyOnSuccess, DeployStrategy: app.DeployStrategy,
		ComposePath: app.ComposePath, ComposeService: app.ComposeService,
	})
}

func (s *Service) Delete(ctx context.Context, appID, actor pgtype.UUID) error {
	app, err := s.appFor(ctx, appID, actor, "admin")
	if err != nil {
		return err
	}
	// Drop managed-database engine credentials (postgres roles, redis ACL
	// users) BEFORE deleting the app. The attachment rows themselves cascade
	// with the app, but the engine-side credentials do not — leaving them
	// alive is a cross-tenant breach once a freed redis index is reassigned.
	// Hard-fail: if engine cleanup errors we do not delete the app, so the
	// operation can be safely retried rather than orphaning credentials.
	if s.cleaner != nil {
		if err := s.cleaner.DetachAllForApp(ctx, app.ID); err != nil {
			return err
		}
	}
	return s.q.DeleteApplication(ctx, app.ID)
}

// SetDesiredState records whether an app should be running or stopped. It
// gates on the "member" role (same as deploy) and returns the updated app.
// The caller is responsible for the matching provider action (Stop/Start).
func (s *Service) SetDesiredState(ctx context.Context, appID, actor pgtype.UUID, state string) (sqlc.Application, error) {
	if state != "running" && state != "stopped" {
		return sqlc.Application{}, fmt.Errorf("%w: desired_state must be running or stopped", ErrValidation)
	}
	app, err := s.appFor(ctx, appID, actor, "member")
	if err != nil {
		return sqlc.Application{}, err
	}
	return s.q.SetApplicationDesiredState(ctx, sqlc.SetApplicationDesiredStateParams{ID: app.ID, DesiredState: state})
}

// SetDesiredStateRaw sets desired_state without an actor check. Pipeline-facing
// (a successful deploy implies the app should be up) and used to roll back the
// recorded intent when a provider Stop/Start fails.
func (s *Service) SetDesiredStateRaw(ctx context.Context, appID pgtype.UUID, state string) error {
	_, err := s.q.SetApplicationDesiredState(ctx, sqlc.SetApplicationDesiredStateParams{ID: appID, DesiredState: state})
	return err
}

func (s *Service) SetEnvVars(ctx context.Context, appID, actor pgtype.UUID, vars map[string]string) error {
	app, err := s.appFor(ctx, appID, actor, "member")
	if err != nil {
		return err
	}
	// Validate the whole batch before writing any of it, so a rejected key
	// does not leave half the variables applied.
	keys := make([]string, 0, len(vars))
	for k := range vars {
		if err := validateEnvKey(k); err != nil {
			return err
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)
	for _, k := range keys {
		enc, err := s.box.Seal([]byte(vars[k]))
		if err != nil {
			return err
		}
		if err := q.UpsertEnvVar(ctx, sqlc.UpsertEnvVarParams{AppID: app.ID, Key: k, ValueEnc: enc}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) ListEnvKeys(ctx context.Context, appID, actor pgtype.UUID) ([]string, error) {
	app, err := s.appFor(ctx, appID, actor, "viewer")
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListEnvVars(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, r.Key)
	}
	return keys, nil
}

func (s *Service) DeleteEnvVar(ctx context.Context, appID, actor pgtype.UUID, key string) error {
	app, err := s.appFor(ctx, appID, actor, "member")
	if err != nil {
		return err
	}
	return s.q.DeleteEnvVar(ctx, sqlc.DeleteEnvVarParams{AppID: app.ID, Key: key})
}

// GetRaw is pipeline-facing: no actor check.
func (s *Service) GetRaw(ctx context.Context, appID pgtype.UUID) (sqlc.Application, error) {
	app, err := s.q.GetApplication(ctx, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Application{}, ErrNotFound
	}
	return app, err
}

// DecryptedEnv is pipeline-facing: no actor check.
func (s *Service) DecryptedEnv(ctx context.Context, appID pgtype.UUID) (map[string]string, error) {
	rows, err := s.q.ListEnvVars(ctx, appID)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string, len(rows))
	for _, r := range rows {
		pt, err := s.box.Open(r.ValueEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt %s: %w", r.Key, err)
		}
		env[r.Key] = string(pt)
	}
	return env, nil
}

func (s *Service) DecryptedRegistryCreds(ctx context.Context, app sqlc.Application) (*RegistryCreds, error) {
	if len(app.RegistryCredsEnc) == 0 {
		return nil, nil
	}
	pt, err := s.box.Open(app.RegistryCredsEnc)
	if err != nil {
		return nil, err
	}
	var c RegistryCreds
	if err := json.Unmarshal(pt, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
