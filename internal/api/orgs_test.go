package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/jackc/pgx/v5/pgtype"
)

type stubOrgs struct {
	org     sqlc.Organization
	role    string
	preview orgs.InvitePreview
	err     error
}

func (s stubOrgs) Create(_ context.Context, _ string, _ pgtype.UUID) (sqlc.Organization, error) {
	return s.org, s.err
}
func (s stubOrgs) ListForUser(_ context.Context, _ pgtype.UUID) ([]sqlc.ListOrganizationsForUserRow, error) {
	return []sqlc.ListOrganizationsForUserRow{}, s.err
}
func (s stubOrgs) Get(_ context.Context, _, _ pgtype.UUID) (sqlc.Organization, string, error) {
	return s.org, s.role, s.err
}
func (s stubOrgs) Delete(_ context.Context, _, _ pgtype.UUID) error { return s.err }
func (s stubOrgs) AddMember(_ context.Context, _, _ pgtype.UUID, _ string) error {
	return s.err
}
func (s stubOrgs) ListMembers(_ context.Context, _, _ pgtype.UUID) ([]sqlc.ListMembersRow, error) {
	return []sqlc.ListMembersRow{}, s.err
}
func (s stubOrgs) UpdateRole(_ context.Context, _, _, _ pgtype.UUID, _ string) (sqlc.Membership, error) {
	return sqlc.Membership{}, s.err
}
func (s stubOrgs) RemoveMember(_ context.Context, _, _, _ pgtype.UUID) error { return s.err }
func (s stubOrgs) CreateInvite(_ context.Context, _, _ pgtype.UUID, _, _ string, _ time.Duration) (string, sqlc.Invite, error) {
	return "tok", sqlc.Invite{}, s.err
}
func (s stubOrgs) ListInvites(_ context.Context, _, _ pgtype.UUID) ([]sqlc.Invite, error) {
	return []sqlc.Invite{}, s.err
}
func (s stubOrgs) RevokeInvite(_ context.Context, _, _, _ pgtype.UUID) error { return s.err }
func (s stubOrgs) AcceptInvite(_ context.Context, _ string, _ pgtype.UUID) (sqlc.Organization, error) {
	return s.org, s.err
}
func (s stubOrgs) PreviewInvite(_ context.Context, _ string) (orgs.InvitePreview, error) {
	if s.preview.OrgName != "" {
		return s.preview, s.err
	}
	return orgs.InvitePreview{OrgName: s.org.Name, Role: "member"}, s.err
}

func authedServer(o OrgService) *Server {
	return &Server{
		auth: stubAuth{user: sqlc.User{Email: "a@b.co"}},
		orgs: o,
	}
}

func doAuthed(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
	NewRouter(s).ServeHTTP(rec, req)
	return rec
}

func TestCreateOrg(t *testing.T) {
	s := authedServer(stubOrgs{org: sqlc.Organization{Name: "Acme", Slug: "acme"}})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/orgs", `{"name":"Acme"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["Slug"] != "acme" && body["slug"] != "acme" {
		t.Fatalf("body = %v", body)
	}
}

func TestCreateOrgRequiresName(t *testing.T) {
	s := authedServer(stubOrgs{})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/orgs", `{"name":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestOrgsRequireAuth(t *testing.T) {
	s := &Server{auth: stubAuth{err: orgs.ErrForbidden}, orgs: stubOrgs{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs", nil)
	NewRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestGetOrgNotFoundForNonMember(t *testing.T) {
	s := authedServer(stubOrgs{err: orgs.ErrNotFound})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/orgs/5f4c1c9e-0000-0000-0000-000000000000", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDeleteOrgForbidden(t *testing.T) {
	s := authedServer(stubOrgs{err: orgs.ErrForbidden})
	rec := doAuthed(t, s, http.MethodDelete, "/api/v1/orgs/5f4c1c9e-0000-0000-0000-000000000000", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}
