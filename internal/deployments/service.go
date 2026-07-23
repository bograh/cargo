package deployments

import (
	"context"
	"errors"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/events"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound          = errors.New("deployment not found")
	ErrForbidden         = errors.New("insufficient role")
	ErrBadTransition     = errors.New("illegal status transition")
	ErrBadRollbackTarget = errors.New("rollback target must be a live or superseded deployment")
)

var roleRank = map[string]int{"viewer": 0, "member": 1, "admin": 2, "owner": 3}

// transitions encodes the FR-4.2 status machine.
var transitions = map[string]map[string]bool{
	"queued":    {"building": true, "deploying": true, "cancelled": true},
	"building":  {"deploying": true, "failed": true, "cancelled": true},
	"deploying": {"live": true, "failed": true},
}

type Service struct {
	q       *sqlc.Queries
	hub     *events.Hub
	dataDir string
}

func NewService(pool *pgxpool.Pool, hub *events.Hub, dataDir string) *Service {
	return &Service{q: sqlc.New(pool), hub: hub, dataDir: dataDir}
}

// appFor loads an app and checks the actor holds at least minRole in its org.
func (s *Service) appFor(ctx context.Context, appID, actor pgtype.UUID, minRole string) (sqlc.Application, error) {
	app, err := s.q.GetApplication(ctx, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Application{}, ErrNotFound
	}
	if err != nil {
		return sqlc.Application{}, err
	}
	m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: app.OrgID, UserID: actor})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Application{}, ErrNotFound
	}
	if err != nil {
		return sqlc.Application{}, err
	}
	if roleRank[m.Role] < roleRank[minRole] {
		return sqlc.Application{}, ErrForbidden
	}
	return app, nil
}

func (s *Service) Create(ctx context.Context, appID, actor pgtype.UUID, trigger string) (sqlc.Deployment, error) {
	if trigger != "manual" && trigger != "webhook" {
		return sqlc.Deployment{}, ErrBadTransition
	}
	app, err := s.appFor(ctx, appID, actor, "member")
	if err != nil {
		return sqlc.Deployment{}, err
	}
	return s.q.CreateDeployment(ctx, sqlc.CreateDeploymentParams{
		AppID: app.ID, Trigger: trigger, Actor: actor,
	})
}

// CreateSystem creates a deployment with no acting user (webhook trigger).
func (s *Service) CreateSystem(ctx context.Context, appID pgtype.UUID, trigger string) (sqlc.Deployment, error) {
	if trigger != "webhook" {
		return sqlc.Deployment{}, ErrBadTransition
	}
	return s.q.CreateDeployment(ctx, sqlc.CreateDeploymentParams{AppID: appID, Trigger: trigger})
}

// Rollback creates a new deployment reusing the target's image tag (FR-4.6).
func (s *Service) Rollback(ctx context.Context, appID, actor, targetID pgtype.UUID) (sqlc.Deployment, error) {
	app, err := s.appFor(ctx, appID, actor, "member")
	if err != nil {
		return sqlc.Deployment{}, err
	}
	target, err := s.q.GetDeployment(ctx, targetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Deployment{}, ErrBadRollbackTarget
	}
	if err != nil {
		return sqlc.Deployment{}, err
	}
	// A rollback target is any of the app's successfully-built deployments with
	// an image still on disk: the currently-serving "live" row or an older
	// "superseded" one demoted when a newer deploy took over.
	if target.AppID != app.ID || target.ImageTag == "" ||
		(target.Status != "live" && target.Status != "superseded") {
		return sqlc.Deployment{}, ErrBadRollbackTarget
	}
	return s.q.CreateDeployment(ctx, sqlc.CreateDeploymentParams{
		AppID: app.ID, Trigger: "rollback", Actor: actor,
		ImageTag: target.ImageTag, CommitSha: target.CommitSha,
	})
}

func (s *Service) List(ctx context.Context, appID, actor pgtype.UUID) ([]sqlc.Deployment, error) {
	app, err := s.appFor(ctx, appID, actor, "viewer")
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListDeploymentsForApp(ctx, app.ID)
	if rows == nil {
		rows = []sqlc.Deployment{}
	}
	return rows, err
}

func (s *Service) Get(ctx context.Context, deploymentID, actor pgtype.UUID) (sqlc.Deployment, error) {
	dep, err := s.q.GetDeployment(ctx, deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Deployment{}, ErrNotFound
	}
	if err != nil {
		return sqlc.Deployment{}, err
	}
	if _, err := s.appFor(ctx, dep.AppID, actor, "viewer"); err != nil {
		return sqlc.Deployment{}, err
	}
	return dep, nil
}

// --- pipeline-facing (no actor checks) ---

func (s *Service) GetRaw(ctx context.Context, id pgtype.UUID) (sqlc.Deployment, error) {
	dep, err := s.q.GetDeployment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Deployment{}, ErrNotFound
	}
	return dep, err
}

func (s *Service) SetStatus(ctx context.Context, id pgtype.UUID, status string) error {
	dep, err := s.GetRaw(ctx, id)
	if err != nil {
		return err
	}
	if !transitions[dep.Status][status] {
		return ErrBadTransition
	}
	return s.q.MarkDeploymentStatus(ctx, sqlc.MarkDeploymentStatusParams{ID: id, Status: status})
}

func (s *Service) Finish(ctx context.Context, id pgtype.UUID, status, errMsg string) error {
	if status != "live" && status != "failed" && status != "cancelled" {
		return ErrBadTransition
	}
	return s.q.FinishDeployment(ctx, sqlc.FinishDeploymentParams{ID: id, Status: status, Error: errMsg})
}

// Supersede demotes an app's prior live deployment(s) to "superseded", leaving
// exceptID (the deployment that just went live) as the only live row. Their
// images are retained so they stay valid rollback targets. Callers hold the
// per-app deploy lock, so this races with no concurrent deploy of the same app.
func (s *Service) Supersede(ctx context.Context, appID, exceptID pgtype.UUID) error {
	return s.q.SupersedePriorLiveDeployments(ctx, sqlc.SupersedePriorLiveDeploymentsParams{
		AppID: appID, ID: exceptID,
	})
}

func (s *Service) SetBuildInfo(ctx context.Context, id pgtype.UUID, commitSHA, imageTag string) error {
	return s.q.SetDeploymentBuildInfo(ctx, sqlc.SetDeploymentBuildInfoParams{
		ID: id, CommitSha: commitSHA, ImageTag: imageTag,
	})
}
