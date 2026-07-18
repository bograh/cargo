package jobs

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/builder"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/databases"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/deployments"
	"github.com/bograh/cargo/internal/events"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeProvider struct {
	mu    sync.Mutex
	specs []reconciler.Spec
	err   error
}

func (f *fakeProvider) Apply(_ context.Context, spec reconciler.Spec, _ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs = append(f.specs, spec)
	return f.err
}

func (f *fakeProvider) Teardown(_ context.Context, _, _ string, _ io.Writer) error { return nil }

type fakeBuilder struct{ called bool }

func (b *fakeBuilder) Build(_ context.Context, _ builder.Input) error {
	b.called = true
	return nil
}

type fixture struct {
	pipeline *Pipeline
	provider *fakeProvider
	builder  *fakeBuilder
	deps     *deployments.Service
	appSvc   *apps.Service
	app      sqlc.Application
	owner    pgtype.UUID
}

func setup(t *testing.T, sourceType string) *fixture {
	t.Helper()
	pool := startPool(t)
	box, err := crypto.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u, _, err := auth.NewService(pool).Register(ctx, "o@x.co", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	org, err := orgs.NewService(pool).Create(ctx, "Acme", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	appSvc := apps.NewService(pool, box)
	in := apps.CreateInput{Name: "web", SourceType: sourceType, ExposedPort: 80}
	if sourceType == "image" {
		in.ImageRef = "nginx:alpine"
	} else {
		in.GitRepoURL = "file:///tmp/fake-repo"
		in.GitBranch = "main"
	}
	app, err := appSvc.Create(ctx, org.ID, u.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := appSvc.SetEnvVars(ctx, app.ID, u.ID, map[string]string{"KEY": "val"}); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	depSvc := deployments.NewService(pool, events.NewHub(), dataDir)
	provider := &fakeProvider{}
	fb := &fakeBuilder{}
	p := &Pipeline{
		Pool: pool, Apps: appSvc, Deployments: depSvc, Provider: provider,
		NewBuilder: func(string) builder.Builder { return fb },
		Clone: func(_ context.Context, _, _, dest string, _ io.Writer) (string, error) {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return "", err
			}
			return strings.Repeat("a", 40), nil
		},
		DataDir:          dataDir,
		AppsDomainSuffix: func(context.Context) string { return "apps.localhost" },
	}
	return &fixture{pipeline: p, provider: provider, builder: fb,
		deps: depSvc, appSvc: appSvc, app: app, owner: u.ID}
}

func uuidString(t *testing.T, id pgtype.UUID) string {
	t.Helper()
	v, err := id.Value()
	if err != nil {
		t.Fatal(err)
	}
	return v.(string)
}

func TestPipelineImageDeploySuccess(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pipeline.Run(ctx, uuidString(t, dep.ID)); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := f.deps.GetRaw(ctx, dep.ID)
	if err != nil || got.Status != "live" {
		t.Fatalf("status = %s, %v", got.Status, err)
	}
	if len(f.provider.specs) != 1 {
		t.Fatalf("provider calls = %d", len(f.provider.specs))
	}
	spec := f.provider.specs[0]
	if spec.Image != "nginx:alpine" || spec.Env["KEY"] != "val" ||
		spec.Domains[0] != f.app.Slug+".apps.localhost" {
		t.Fatalf("spec = %+v", spec)
	}
	data, err := os.ReadFile(f.deps.LogPath(uuidString(t, dep.ID)))
	if err != nil || !strings.Contains(string(data), "==> deploying") {
		t.Fatalf("log = %q, %v", data, err)
	}
}

func TestPipelineFailureMarksFailed(t *testing.T) {
	f := setup(t, "image")
	f.provider.err = io.ErrUnexpectedEOF
	ctx := context.Background()
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pipeline.Run(ctx, uuidString(t, dep.ID)); err == nil {
		t.Fatal("expected error")
	}
	got, _ := f.deps.GetRaw(ctx, dep.ID)
	if got.Status != "failed" || got.Error == "" {
		t.Fatalf("dep = %+v", got)
	}
	if _, err := os.Stat(f.deps.LogPath(uuidString(t, dep.ID))); err != nil {
		t.Fatalf("log not retained: %v", err)
	}
}

func TestPipelineGitBuild(t *testing.T) {
	f := setup(t, "git")
	ctx := context.Background()
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pipeline.Run(ctx, uuidString(t, dep.ID)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !f.builder.called {
		t.Fatal("builder not called for git source")
	}
	got, _ := f.deps.GetRaw(ctx, dep.ID)
	if got.CommitSha != strings.Repeat("a", 40) || !strings.HasPrefix(got.ImageTag, "app-web:") {
		t.Fatalf("build info = %+v", got)
	}
}

func TestPipelineRollbackSkipsBuild(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	// Make a live deployment to roll back to.
	first, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.deps.SetBuildInfo(ctx, first.ID, "", "app-web:old"); err != nil {
		t.Fatal(err)
	}
	if err := f.deps.SetStatus(ctx, first.ID, "deploying"); err != nil {
		t.Fatal(err)
	}
	if err := f.deps.Finish(ctx, first.ID, "live", ""); err != nil {
		t.Fatal(err)
	}
	rb, err := f.deps.Rollback(ctx, f.app.ID, f.owner, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pipeline.Run(ctx, uuidString(t, rb.ID)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if f.builder.called {
		t.Fatal("builder called during rollback")
	}
	spec := f.provider.specs[len(f.provider.specs)-1]
	if spec.Image != "app-web:old" {
		t.Fatalf("rollback image = %s", spec.Image)
	}
}

func TestRiverMigrateAndClient(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	if err := Migrate(ctx, f.pipeline.Pool); err != nil {
		t.Fatalf("river migrate: %v", err)
	}
	box, err := crypto.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	dbSvc := databases.NewService(f.pipeline.Pool, box, &fakeDBProvider{}, t.TempDir())
	client, err := NewClient(f.pipeline.Pool, f.pipeline, dbSvc)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	e := &Enqueuer{Client: client}
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.EnqueueDeploy(ctx, uuidString(t, dep.ID)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
}

func TestPipelineUsesCloneAuth(t *testing.T) {
	f := setup(t, "git")
	ctx := context.Background()
	var clonedURL string
	f.pipeline.CloneAuth = func(_ context.Context, _ pgtype.UUID, repoURL string) (string, error) {
		return "https://x-access-token:SECRET-TOKEN@github.com/acme/api.git", nil
	}
	baseClone := f.pipeline.Clone
	f.pipeline.Clone = func(ctx context.Context, url, branch, dest string, log io.Writer) (string, error) {
		clonedURL = url
		return baseClone(ctx, url, branch, dest, log)
	}
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pipeline.Run(ctx, uuidString(t, dep.ID)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(clonedURL, "x-access-token:SECRET-TOKEN") {
		t.Fatalf("clone url = %q", clonedURL)
	}
	data, err := os.ReadFile(f.deps.LogPath(uuidString(t, dep.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SECRET-TOKEN") {
		t.Fatal("installation token leaked into deployment log")
	}
}

func TestPipelineInjectsDBEnv(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	f.pipeline.DBEnv = func(context.Context, pgtype.UUID) (map[string]string, error) {
		return map[string]string{"DATABASE_URL": "postgres://x"}, nil
	}
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pipeline.Run(ctx, uuidString(t, dep.ID)); err != nil {
		t.Fatalf("run: %v", err)
	}
	spec := f.provider.specs[0]
	if spec.Env["DATABASE_URL"] != "postgres://x" {
		t.Fatalf("env = %+v", spec.Env)
	}
	if len(spec.Networks) != 2 || spec.Networks[0] != "cargo-proxy" || spec.Networks[1] != "cargo-data" {
		t.Fatalf("networks = %v", spec.Networks)
	}
}

func TestPipelineWithoutDBEnvLeavesNetworksDefault(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pipeline.Run(ctx, uuidString(t, dep.ID)); err != nil {
		t.Fatalf("run: %v", err)
	}
	spec := f.provider.specs[0]
	if len(spec.Networks) != 0 {
		t.Fatalf("networks = %v", spec.Networks)
	}
}

func TestPipelineEmptyDBEnvLeavesNetworksDefault(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	f.pipeline.DBEnv = func(context.Context, pgtype.UUID) (map[string]string, error) {
		return map[string]string{}, nil
	}
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pipeline.Run(ctx, uuidString(t, dep.ID)); err != nil {
		t.Fatalf("run: %v", err)
	}
	spec := f.provider.specs[0]
	if len(spec.Networks) != 0 {
		t.Fatalf("networks = %v", spec.Networks)
	}
}

func TestPipelineDBEnvCollisionFailsDeploy(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	// "KEY" is already set as a user env var in setup().
	f.pipeline.DBEnv = func(context.Context, pgtype.UUID) (map[string]string, error) {
		return map[string]string{"KEY": "postgres://x"}, nil
	}
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pipeline.Run(ctx, uuidString(t, dep.ID)); err == nil {
		t.Fatal("expected error on env var collision")
	} else if !strings.Contains(err.Error(), "KEY") {
		t.Fatalf("error = %v, want mention of colliding key", err)
	}
	got, _ := f.deps.GetRaw(ctx, dep.ID)
	if got.Status != "failed" {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestPipelineIncludesCustomDomains(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	if _, err := f.appSvc.AddDomain(ctx, f.app.ID, f.owner, "api.example.com", ""); err != nil {
		t.Fatal(err)
	}
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pipeline.Run(ctx, uuidString(t, dep.ID)); err != nil {
		t.Fatalf("run: %v", err)
	}
	spec := f.provider.specs[0]
	if len(spec.Domains) != 2 || spec.Domains[1] != "api.example.com" {
		t.Fatalf("domains = %v", spec.Domains)
	}
}
