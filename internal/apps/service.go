package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
}

type Service struct {
	q   *sqlc.Queries
	box *crypto.Box
}

func NewService(pool *pgxpool.Pool, box *crypto.Box) *Service {
	return &Service{q: sqlc.New(pool), box: box}
}

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

func validateCreate(in CreateInput) error {
	if in.Name == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	if in.ExposedPort < 1 {
		return fmt.Errorf("%w: exposed_port must be 1-65535", ErrValidation)
	}
	switch in.SourceType {
	case "git":
		if in.GitRepoURL == "" || in.GitBranch == "" {
			return fmt.Errorf("%w: git source requires git_repo_url and git_branch", ErrValidation)
		}
	case "image":
		if in.ImageRef == "" {
			return fmt.Errorf("%w: image source requires image_ref", ErrValidation)
		}
	default:
		return fmt.Errorf("%w: source_type must be git or image", ErrValidation)
	}
	switch in.Builder {
	case "", "auto", "dockerfile", "nixpacks":
	default:
		return fmt.Errorf("%w: builder must be auto, dockerfile, or nixpacks", ErrValidation)
	}
	return nil
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
	if err := validateCreate(in); err != nil {
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
	if in.ExposedPort != nil {
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
	return s.q.UpdateApplication(ctx, sqlc.UpdateApplicationParams{
		ID: app.ID, Name: app.Name, Builder: app.Builder, GitBranch: app.GitBranch,
		ImageRef: app.ImageRef, ExposedPort: app.ExposedPort,
		HealthcheckPath: app.HealthcheckPath, AutoDeploy: app.AutoDeploy,
		BuildContext: app.BuildContext, DockerfilePath: app.DockerfilePath,
		BuildArgs: app.BuildArgs, RegistryCredsEnc: app.RegistryCredsEnc,
	})
}

func (s *Service) Delete(ctx context.Context, appID, actor pgtype.UUID) error {
	app, err := s.appFor(ctx, appID, actor, "admin")
	if err != nil {
		return err
	}
	return s.q.DeleteApplication(ctx, app.ID)
}

func (s *Service) SetEnvVars(ctx context.Context, appID, actor pgtype.UUID, vars map[string]string) error {
	app, err := s.appFor(ctx, appID, actor, "member")
	if err != nil {
		return err
	}
	for k, v := range vars {
		if k == "" {
			return fmt.Errorf("%w: env var key must not be empty", ErrValidation)
		}
		enc, err := s.box.Seal([]byte(v))
		if err != nil {
			return err
		}
		if err := s.q.UpsertEnvVar(ctx, sqlc.UpsertEnvVarParams{AppID: app.ID, Key: k, ValueEnc: enc}); err != nil {
			return err
		}
	}
	return nil
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
