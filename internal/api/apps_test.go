package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

type stubApps struct {
	app sqlc.Application
	err error
}

func (s stubApps) Create(_ context.Context, _, _ pgtype.UUID, _ apps.CreateInput) (sqlc.Application, error) {
	return s.app, s.err
}
func (s stubApps) List(_ context.Context, _, _ pgtype.UUID) ([]sqlc.Application, error) {
	return []sqlc.Application{}, s.err
}
func (s stubApps) Get(_ context.Context, _, _ pgtype.UUID) (sqlc.Application, error) {
	return s.app, s.err
}
func (s stubApps) Update(_ context.Context, _, _ pgtype.UUID, _ apps.UpdateInput) (sqlc.Application, error) {
	return s.app, s.err
}
func (s stubApps) Delete(_ context.Context, _, _ pgtype.UUID) error { return s.err }
func (s stubApps) SetEnvVars(_ context.Context, _, _ pgtype.UUID, _ map[string]string) error {
	return s.err
}
func (s stubApps) ListEnvKeys(_ context.Context, _, _ pgtype.UUID) ([]string, error) {
	return []string{"DB_URL"}, s.err
}
func (s stubApps) DeleteEnvVar(_ context.Context, _, _ pgtype.UUID, _ string) error { return s.err }
func (s stubApps) AddDomain(_ context.Context, _, _ pgtype.UUID, hostname, _ string) (sqlc.Domain, error) {
	return sqlc.Domain{Hostname: hostname, Status: "pending"}, s.err
}
func (s stubApps) ListDomains(_ context.Context, _, _ pgtype.UUID) ([]sqlc.Domain, error) {
	return []sqlc.Domain{{Hostname: "api.example.com", Status: "active"}}, s.err
}
func (s stubApps) RemoveDomain(_ context.Context, _, _, _ pgtype.UUID) error { return s.err }

func appServer(a AppService) *Server {
	return &Server{
		auth:     stubAuth{user: sqlc.User{Email: "a@b.co"}},
		apps:     a,
		settings: stubSettings{values: map[string]string{}},
	}
}

func TestAddDomain(t *testing.T) {
	s := appServer(stubApps{})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/apps/"+testUUID+"/domains", `{"hostname":"api.example.com"}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "api.example.com") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}

func TestListDomains(t *testing.T) {
	s := appServer(stubApps{})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/apps/"+testUUID+"/domains", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"active"`) {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}

func TestAddDomainForbidden(t *testing.T) {
	s := appServer(stubApps{err: apps.ErrForbidden})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/apps/"+testUUID+"/domains", `{"hostname":"api.example.com"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

const testUUID = "5f4c1c9e-0000-0000-0000-000000000000"

func TestCreateApp(t *testing.T) {
	s := appServer(stubApps{app: sqlc.Application{Name: "api", Slug: "api", SourceType: "image"}})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/orgs/"+testUUID+"/apps",
		`{"name":"api","source_type":"image","image_ref":"nginx:alpine","exposed_port":80}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "registry_creds") {
		t.Fatal("registry creds leaked")
	}
}

func TestCreateAppValidationError(t *testing.T) {
	s := appServer(stubApps{err: apps.ErrValidation})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/orgs/"+testUUID+"/apps", `{"name":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestGetAppNotFound(t *testing.T) {
	s := appServer(stubApps{err: apps.ErrNotFound})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/apps/"+testUUID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestEnvKeysOnly(t *testing.T) {
	s := appServer(stubApps{})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/apps/"+testUUID+"/env", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "DB_URL") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}

func TestSetEnvForbidden(t *testing.T) {
	s := appServer(stubApps{err: apps.ErrForbidden})
	rec := doAuthed(t, s, http.MethodPut, "/api/v1/apps/"+testUUID+"/env", `{"vars":{"A":"b"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}
