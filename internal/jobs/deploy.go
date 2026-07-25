// Package jobs runs Cargo's background work on a River (Postgres-backed)
// queue: deployments and housekeeping.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/builder"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/deployments"
	"github.com/bograh/cargo/internal/obs"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type DeployArgs struct {
	DeploymentID string `json:"deployment_id"`
}

func (DeployArgs) Kind() string { return "deploy" }

// deployJobTimeout bounds a single deploy job's context. It must comfortably
// exceed a cold build (base-image pulls + dependency install) plus the
// reconciler's post-apply health gate (default 2 min). River's 1-minute
// default is far too short and cancels healthy deploys mid-flight.
const deployJobTimeout = 30 * time.Minute

type DeployWorker struct {
	river.WorkerDefaults[DeployArgs]
	P *Pipeline
}

func (w *DeployWorker) Work(ctx context.Context, job *river.Job[DeployArgs]) error {
	return w.P.Run(ctx, job.Args.DeploymentID)
}

func (w *DeployWorker) Timeout(*river.Job[DeployArgs]) time.Duration {
	return deployJobTimeout
}

// Pipeline executes one deployment end to end: clone → build → reconcile →
// apply → live/failed. All collaborators are injected so tests can fake them.
type Pipeline struct {
	Pool        *pgxpool.Pool
	Apps        *apps.Service
	Deployments *deployments.Service
	Provider    reconciler.DeployProvider
	NewBuilder  func(name string) builder.Builder
	Clone       func(ctx context.Context, url, branch, dest string, log io.Writer) (string, error)
	// CloneAuth may rewrite a repo URL to embed credentials (e.g. a GitHub
	// installation token). May be nil. The rewritten URL is never logged.
	CloneAuth        func(ctx context.Context, orgID pgtype.UUID, repoURL string) (string, error)
	DataDir          string
	AppsDomainSuffix func(ctx context.Context) string
	// DBEnv returns the injected env vars for an app's managed database
	// attachments. May be nil, in which case no injection happens.
	DBEnv func(ctx context.Context, appID pgtype.UUID) (map[string]string, error)
	// Instance-wide default resource caps, used when an app sets no override.
	DefaultMemLimit  string
	DefaultCPULimit  string
	DefaultPidsLimit int
}

func uuidOf(s string) (pgtype.UUID, error) {
	var id pgtype.UUID
	err := id.Scan(s)
	return id, err
}

func uuidStr(id pgtype.UUID) string {
	v, _ := id.Value()
	s, _ := v.(string)
	return s
}

func (p *Pipeline) Run(ctx context.Context, deploymentID string) error {
	depID, err := uuidOf(deploymentID)
	if err != nil {
		return fmt.Errorf("bad deployment id %q: %w", deploymentID, err)
	}
	dep, err := p.Deployments.GetRaw(ctx, depID)
	if err != nil {
		return err
	}
	switch dep.Status {
	case "live", "failed", "cancelled":
		return nil // terminal — nothing to do (e.g. duplicate retry)
	}

	// Serialize deploys per app (FR-4.4).
	conn, err := p.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	appIDStr := uuidStr(dep.AppID)
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext($1))", appIDStr); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock(hashtext($1))", appIDStr)
	}()

	logw, err := p.Deployments.LogWriter(deploymentID)
	if err != nil {
		return err
	}
	defer func() { _ = logw.Close() }()

	if err := p.run(ctx, dep, deploymentID, logw); err != nil {
		// Persist the failure even when ctx was cancelled (e.g. job timeout);
		// otherwise the status stays "deploying" and River's retry re-enters
		// run(), hitting an illegal deploying→building transition.
		_, _ = fmt.Fprintf(logw, "==> failed: %v\n", err)
		_ = p.Deployments.Finish(context.WithoutCancel(ctx), depID, "failed", err.Error())
		obs.RecordDeploy("failed", time.Since(dep.CreatedAt.Time))
		return err
	}
	_, _ = fmt.Fprintln(logw, "==> live")
	if err := p.Deployments.Finish(ctx, depID, "live", ""); err != nil {
		return err
	}
	obs.RecordDeploy("live", time.Since(dep.CreatedAt.Time))
	// This deployment now serves the app; demote the app's previously-live
	// deployment(s) to "superseded" so exactly one row reads as live. Best-effort
	// (like prune below): the container is already up, and stale "live" bookkeeping
	// must never fail an otherwise-successful deploy. Their images are retained,
	// so they remain valid rollback targets.
	if err := p.Deployments.Supersede(context.WithoutCancel(ctx), dep.AppID, depID); err != nil {
		slog.Warn("supersede prior live deployment failed", "app", appIDStr, "err", err)
	}
	// Reclaim disk immediately: keep only the newest few deployments/images
	// for this app rather than waiting for the daily prune. Best-effort — a
	// prune failure never fails the (already successful) deploy.
	if err := pruneApp(context.WithoutCancel(ctx), sqlc.New(p.Pool), p.Deployments, dep.AppID); err != nil {
		slog.Warn("post-deploy prune failed", "app", appIDStr, "err", err)
	}
	return nil
}

func (p *Pipeline) run(ctx context.Context, dep sqlc.Deployment, deploymentID string, logw io.Writer) error {
	app, err := p.Apps.GetRaw(ctx, dep.AppID)
	if err != nil {
		return fmt.Errorf("load app: %w", err)
	}

	imageTag := dep.ImageTag
	if dep.Trigger == "rollback" {
		_, _ = fmt.Fprintf(logw, "==> rollback to image %s (no build)\n", imageTag)
	} else {
		if err := p.Deployments.SetStatus(ctx, dep.ID, "building"); err != nil {
			return err
		}
		switch app.SourceType {
		case "git":
			imageTag, err = p.buildFromGit(ctx, app, dep, deploymentID, logw)
			if err != nil {
				return err
			}
		case "image":
			imageTag = app.ImageRef
			if err := p.registryLogin(ctx, app, logw); err != nil {
				return err
			}
			if err := p.Deployments.SetBuildInfo(ctx, dep.ID, "", imageTag); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown source type %q", app.SourceType)
		}
	}

	if err := p.Deployments.SetStatus(ctx, dep.ID, "deploying"); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(logw, "==> deploying")
	env, err := p.Apps.DecryptedEnv(ctx, app.ID)
	if err != nil {
		return err
	}
	var networks []string
	if p.DBEnv != nil {
		dbEnv, err := p.DBEnv(ctx, app.ID)
		if err != nil {
			return fmt.Errorf("load database env: %w", err)
		}
		if len(dbEnv) > 0 {
			for k, v := range dbEnv {
				if _, exists := env[k]; exists {
					return fmt.Errorf("env var %q collides with a managed database variable", k)
				}
				env[k] = v
			}
			networks = []string{"cargo-proxy", "cargo-data"}
		}
	}
	domains := []string{app.Slug + "." + p.AppsDomainSuffix(ctx)}
	custom, err := p.Apps.CustomDomains(ctx, app.ID)
	if err != nil {
		return err
	}
	domains = append(domains, custom...)
	memLimit := p.DefaultMemLimit
	if app.MemLimit.Valid {
		memLimit = app.MemLimit.String
	}
	cpuLimit := p.DefaultCPULimit
	if app.CpuLimit.Valid {
		cpuLimit = app.CpuLimit.String
	}
	pidsLimit := p.DefaultPidsLimit
	if app.PidsLimit.Valid {
		pidsLimit = int(app.PidsLimit.Int32)
	}
	spec := reconciler.Spec{
		AppID:           uuidStr(app.ID),
		Slug:            app.Slug,
		Image:           imageTag,
		Port:            app.ExposedPort,
		HealthcheckPath: app.HealthcheckPath,
		Env:             env,
		Domains:         domains,
		Networks:        networks,
		MemoryLimit:     memLimit,
		CPULimit:        cpuLimit,
		PidsLimit:       pidsLimit,
	}
	if err := p.Provider.Apply(ctx, spec, logw); err != nil {
		return err
	}
	// A successful deploy means the app should be up: clear any prior "stopped"
	// intent so a stopped app that gets redeployed comes back running.
	if err := p.Apps.SetDesiredStateRaw(ctx, app.ID, "running"); err != nil {
		return err
	}
	return nil
}

func (p *Pipeline) buildFromGit(ctx context.Context, app sqlc.Application, dep sqlc.Deployment, deploymentID string, logw io.Writer) (string, error) {
	workDir := filepath.Join(p.DataDir, "builds", deploymentID)
	defer func() { _ = os.RemoveAll(workDir) }()
	_, _ = fmt.Fprintf(logw, "==> cloning %s (%s)\n", app.GitRepoUrl, app.GitBranch)
	cloneURL := app.GitRepoUrl
	if p.CloneAuth != nil {
		authed, err := p.CloneAuth(ctx, app.OrgID, app.GitRepoUrl)
		if err != nil {
			return "", fmt.Errorf("clone auth: %w", err)
		}
		cloneURL = authed
	}
	sha, err := p.Clone(ctx, cloneURL, app.GitBranch, workDir, logw)
	if err != nil {
		return "", err
	}
	short := strings.ReplaceAll(deploymentID, "-", "")
	if len(short) > 8 {
		short = short[:8]
	}
	imageTag := fmt.Sprintf("app-%s:%s", app.Slug, short)
	if err := p.Deployments.SetBuildInfo(ctx, dep.ID, sha, imageTag); err != nil {
		return "", err
	}
	name := app.Builder
	if name == "" || name == "auto" {
		name = builder.Detect(filepath.Join(workDir, app.BuildContext), app.DockerfilePath)
	}
	_, _ = fmt.Fprintf(logw, "==> building with %s → %s\n", name, imageTag)
	var buildArgs map[string]string
	if len(app.BuildArgs) > 0 {
		if err := json.Unmarshal(app.BuildArgs, &buildArgs); err != nil {
			return "", fmt.Errorf("build args: %w", err)
		}
	}
	b := p.NewBuilder(name)
	err = b.Build(ctx, builder.Input{
		WorkDir: workDir, ImageTag: imageTag, ContextPath: app.BuildContext,
		DockerfilePath: app.DockerfilePath, BuildArgs: buildArgs, Log: logw,
	})
	return imageTag, err
}

// registryLogin authenticates the local docker daemon for private image pulls.
func (p *Pipeline) registryLogin(ctx context.Context, app sqlc.Application, logw io.Writer) error {
	creds, err := p.Apps.DecryptedRegistryCreds(ctx, app)
	if err != nil || creds == nil {
		return err
	}
	_, _ = fmt.Fprintf(logw, "==> docker login %s\n", creds.Server)
	cmd := exec.CommandContext(ctx, "docker", "login", creds.Server, "-u", creds.Username, "--password-stdin")
	cmd.Stdin = strings.NewReader(creds.Password)
	cmd.Stdout, cmd.Stderr = logw, logw
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker login: %w", err)
	}
	return nil
}
