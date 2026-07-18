package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

type stubAdmin struct{}

func (stubAdmin) ListUsers(_ context.Context) ([]sqlc.User, error) {
	return []sqlc.User{{Email: "a@b.co", PasswordHash: pgtype.Text{String: "SECRET-HASH", Valid: true}}}, nil
}
func (stubAdmin) ListAllOrganizations(_ context.Context) ([]sqlc.Organization, error) {
	return []sqlc.Organization{{Name: "Acme", Slug: "acme"}}, nil
}

func adminRequest(t *testing.T, isAdmin bool, path string) *httptest.ResponseRecorder {
	t.Helper()
	s := &Server{
		auth:  stubAuth{user: sqlc.User{Email: "a@b.co", IsInstanceAdmin: isAdmin}},
		admin: stubAdmin{},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
	NewRouter(s).ServeHTTP(rec, req)
	return rec
}

func TestAdminEndpointsForbiddenForNonAdmin(t *testing.T) {
	for _, path := range []string{"/api/v1/admin/users", "/api/v1/admin/orgs"} {
		if rec := adminRequest(t, false, path); rec.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
	}
}

func TestAdminListUsersOmitsPasswordHash(t *testing.T) {
	rec := adminRequest(t, true, "/api/v1/admin/users")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "SECRET-HASH") {
		t.Fatal("password hash leaked in admin users listing")
	}
	if !strings.Contains(rec.Body.String(), "a@b.co") {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestAdminListOrgs(t *testing.T) {
	rec := adminRequest(t, true, "/api/v1/admin/orgs")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "acme") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}
