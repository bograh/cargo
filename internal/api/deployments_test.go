package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/deployments"
	"github.com/bograh/cargo/internal/events"
	"github.com/jackc/pgx/v5/pgtype"
)

type stubDeps struct {
	dep sqlc.Deployment
	err error
}

func (s stubDeps) Create(_ context.Context, _, _ pgtype.UUID, _ string) (sqlc.Deployment, error) {
	return s.dep, s.err
}
func (s stubDeps) Rollback(_ context.Context, _, _, _ pgtype.UUID) (sqlc.Deployment, error) {
	return s.dep, s.err
}
func (s stubDeps) List(_ context.Context, _, _ pgtype.UUID) ([]sqlc.Deployment, error) {
	return []sqlc.Deployment{}, s.err
}
func (s stubDeps) Get(_ context.Context, _, _ pgtype.UUID) (sqlc.Deployment, error) {
	return s.dep, s.err
}
func (s stubDeps) Finish(_ context.Context, _ pgtype.UUID, _, _ string) error { return nil }
func (s stubDeps) CreateSystem(_ context.Context, _ pgtype.UUID, _ string) (sqlc.Deployment, error) {
	return s.dep, s.err
}

type recordingEnqueuer struct {
	ids []string
	err error
}

func (e *recordingEnqueuer) EnqueueDeploy(_ context.Context, id string) error {
	e.ids = append(e.ids, id)
	return e.err
}

func depServer(d DeploymentService, e Enqueuer) *Server {
	return &Server{
		auth:    stubAuth{user: sqlc.User{Email: "a@b.co"}},
		deps:    d,
		enqueue: e,
		hub:     events.NewHub(),
		logPath: func(string) string { return filepath.Join(os.TempDir(), "does-not-exist.log") },
	}
}

func liveDeployment(t *testing.T) sqlc.Deployment {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(testUUID); err != nil {
		t.Fatal(err)
	}
	return sqlc.Deployment{ID: id, Status: "queued", Trigger: "manual"}
}

func TestDeployEnqueues(t *testing.T) {
	enq := &recordingEnqueuer{}
	s := depServer(stubDeps{dep: liveDeployment(t)}, enq)
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/apps/"+testUUID+"/deploy", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if len(enq.ids) != 1 || enq.ids[0] != testUUID {
		t.Fatalf("enqueued = %v", enq.ids)
	}
}

func TestRollbackBadTarget(t *testing.T) {
	s := depServer(stubDeps{err: deployments.ErrBadRollbackTarget}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/apps/"+testUUID+"/rollback",
		`{"deployment_id":"`+testUUID+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestGetDeploymentNotFoundForOutsider(t *testing.T) {
	s := depServer(stubDeps{err: deployments.ErrNotFound}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/deployments/"+testUUID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDeploymentLogsSSEReplay(t *testing.T) {
	dep := liveDeployment(t)
	dep.Status = "live" // terminal → handler exits after replay
	dir := t.TempDir()
	logFile := filepath.Join(dir, testUUID+".log")
	if err := os.WriteFile(logFile, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := depServer(stubDeps{dep: dep}, &recordingEnqueuer{})
	s.logPath = func(id string) string { return filepath.Join(dir, id+".log") }

	rec := doAuthed(t, s, http.MethodGet, "/api/v1/deployments/"+testUUID+"/logs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data: line one\n") || !strings.Contains(body, "data: line two\n") {
		t.Fatalf("body = %q", body)
	}
	if !strings.Contains(body, "[deployment live]") {
		t.Fatalf("missing terminal marker: %q", body)
	}
}
