package deployments

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/events"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/jackc/pgx/v5/pgtype"
)

type fixture struct {
	svc           *Service
	hub           *events.Hub
	app           sqlc.Application
	owner, viewer pgtype.UUID
	outsider      pgtype.UUID
}

func setup(t *testing.T) *fixture {
	t.Helper()
	pool := startPool(t)
	hub := events.NewHub()
	box, err := crypto.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	authSvc := auth.NewService(pool)
	orgSvc := orgs.NewService(pool)
	appSvc := apps.NewService(pool, box)
	reg := func(email string) pgtype.UUID {
		u, _, err := authSvc.Register(ctx, email, "password-123")
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	owner, viewer, outsider := reg("o@x.co"), reg("v@x.co"), reg("out@x.co")
	org, err := orgSvc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := orgSvc.AddMember(ctx, org.ID, viewer, "viewer"); err != nil {
		t.Fatal(err)
	}
	app, err := appSvc.Create(ctx, org.ID, owner, apps.CreateInput{
		Name: "web", SourceType: "image", ImageRef: "nginx:alpine", ExposedPort: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{svc: NewService(pool, hub, t.TempDir()), hub: hub,
		app: app, owner: owner, viewer: viewer, outsider: outsider}
}

func TestCreateDeployment(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	dep, err := f.svc.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if dep.Status != "queued" || dep.Trigger != "manual" {
		t.Fatalf("dep = %+v", dep)
	}
	if _, err := f.svc.Create(ctx, f.app.ID, f.viewer, "manual"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer create err = %v", err)
	}
	if _, err := f.svc.Get(ctx, dep.ID, f.outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider get err = %v", err)
	}
}

func TestStatusMachine(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	dep, err := f.svc.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []string{"building", "deploying"} {
		if err := f.svc.SetStatus(ctx, dep.ID, next); err != nil {
			t.Fatalf("→%s: %v", next, err)
		}
	}
	if err := f.svc.Finish(ctx, dep.ID, "live", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := f.svc.SetStatus(ctx, dep.ID, "building"); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("live→building err = %v", err)
	}
	got, err := f.svc.Get(ctx, dep.ID, f.viewer)
	if err != nil || got.Status != "live" || !got.FinishedAt.Valid {
		t.Fatalf("final = %+v, %v", got, err)
	}
}

func TestRollback(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	dep, err := f.svc.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	// Target without image tag / not live → rejected.
	if _, err := f.svc.Rollback(ctx, f.app.ID, f.owner, dep.ID); !errors.Is(err, ErrBadRollbackTarget) {
		t.Fatalf("queued target err = %v", err)
	}
	if err := f.svc.SetBuildInfo(ctx, dep.ID, "abc123", "app-web:d1"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetStatus(ctx, dep.ID, "deploying"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Finish(ctx, dep.ID, "live", ""); err != nil {
		t.Fatal(err)
	}
	rb, err := f.svc.Rollback(ctx, f.app.ID, f.owner, dep.ID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rb.Trigger != "rollback" || rb.ImageTag != "app-web:d1" {
		t.Fatalf("rb = %+v", rb)
	}
}

// liveDeploy drives a fresh deployment all the way to live and returns it.
func liveDeploy(t *testing.T, f *fixture, imageTag string) sqlc.Deployment {
	t.Helper()
	ctx := context.Background()
	dep, err := f.svc.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetBuildInfo(ctx, dep.ID, "sha", imageTag); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetStatus(ctx, dep.ID, "deploying"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Finish(ctx, dep.ID, "live", ""); err != nil {
		t.Fatal(err)
	}
	return dep
}

func TestSupersedeDemotesPriorLive(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	first := liveDeploy(t, f, "app-web:d1")
	second := liveDeploy(t, f, "app-web:d2")

	// The second deploy takes over: demote everything else that was live.
	if err := f.svc.Supersede(ctx, f.app.ID, second.ID); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	got1, _ := f.svc.Get(ctx, first.ID, f.owner)
	got2, _ := f.svc.Get(ctx, second.ID, f.owner)
	if got1.Status != "superseded" {
		t.Fatalf("first status = %s, want superseded", got1.Status)
	}
	if got2.Status != "live" {
		t.Fatalf("second status = %s, want live", got2.Status)
	}

	// A superseded deployment must still be rollback-eligible (image retained).
	rb, err := f.svc.Rollback(ctx, f.app.ID, f.owner, first.ID)
	if err != nil {
		t.Fatalf("rollback to superseded: %v", err)
	}
	if rb.ImageTag != "app-web:d1" {
		t.Fatalf("rollback image = %s", rb.ImageTag)
	}
}

func TestLogWriterFileAndHub(t *testing.T) {
	f := setup(t)
	ch, cancel := f.hub.Subscribe("deploy:dep-42")
	defer cancel()
	w, err := f.svc.LogWriter("dep-42")
	if err != nil {
		t.Fatalf("LogWriter: %v", err)
	}
	if _, err := w.Write([]byte("hello log\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.svc.LogPath("dep-42"))
	if err != nil || string(data) != "hello log\n" {
		t.Fatalf("file = %q, %v", data, err)
	}
	select {
	case msg := <-ch:
		if string(msg) != "hello log\n" {
			t.Fatalf("hub msg = %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("hub message not received")
	}
}
