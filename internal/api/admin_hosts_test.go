package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/hostmgr"
	"github.com/bograh/cargo/internal/hosts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// stubHostsAdmin records calls and returns canned results.
type stubHostsAdmin struct {
	created   hosts.CreateInput
	verifyRes hostmgr.VerifyResult
	list      []sqlc.Host
}

func (s *stubHostsAdmin) Create(_ context.Context, in hosts.CreateInput) (sqlc.Host, error) {
	s.created = in
	id := sqlc.Host{ID: pgUUID("00000000-0000-0000-0000-000000000001"), Name: in.Name,
		Address: in.Address, Port: int32(in.Port), Status: "pending",
		AppsDomainSuffix: in.DomainSuffix, LetsencryptEmail: in.LetsEncryptEmail,
		PrivateKeyEnc: []byte{1},
	}
	return id, nil
}
func (s *stubHostsAdmin) List(_ context.Context) ([]sqlc.Host, error) { return s.list, nil }
func (s *stubHostsAdmin) Delete(_ context.Context, _ string) error {
	return hosts.ErrHostInUse
}
func (s *stubHostsAdmin) ReplaceKey(_ context.Context, _ string, _ string) error {
	return nil
}
func (s *stubHostsAdmin) Verify(_ context.Context, h sqlc.Host) (hostmgr.VerifyResult, error) {
	return s.verifyRes, nil
}

type stubHostReader struct{}

func (stubHostReader) GetByID(_ context.Context, _ string) (sqlc.Host, error) {
	return sqlc.Host{}, hosts.ErrNotFound
}

func pgUUID(s string) (out pgtype.UUID) {
	u, err := uuid.Parse(s)
	if err != nil {
		panic(err)
	}
	out.Bytes = u
	out.Valid = true
	return out
}

func hostsServer(admin *stubHostsAdmin, isInstanceAdmin bool) *Server {
	return &Server{
		auth:       stubAuth{user: sqlc.User{Email: "a@b.co", IsInstanceAdmin: isInstanceAdmin}},
		admin:      stubAdmin{},
		orgs:       stubOrgs{role: "owner"},
		hostsAdmin: admin,
		hostSvc:    stubHostReader{},
	}
}

func TestCreateHostVerifies(t *testing.T) {
	admin := &stubHostsAdmin{verifyRes: hostmgr.VerifyResult{
		Status: "online", EngineVersion: "27.3.1", CPUCount: 8, MemTotalMB: 32000,
	}}
	body := `{"name":"w1","address":"deploy@10.0.0.5","port":22,
	          "key_pem":"-----BEGIN OPENSSH PRIVATE KEY-----x","apps_domain_suffix":"w1.example.com",
	          "letsencrypt_email":"ops@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/hosts", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
	rec := httptest.NewRecorder()
	NewRouter(hostsServer(admin, true)).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"status":"online"`) ||
		!strings.Contains(rec.Body.String(), `"engine_version":"27.3.1"`) {
		t.Fatalf("verify result missing from body: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "OPENSSH") || strings.Contains(rec.Body.String(), "key_pem") {
		t.Fatal("private key material leaked in response")
	}
}

func TestHostListNeverReturnsPrivateKey(t *testing.T) {
	admin := &stubHostsAdmin{list: []sqlc.Host{{
		ID: pgUUID("00000000-0000-0000-0000-000000000002"), Name: "w2",
		Address: "deploy@10.0.0.6", Port: 22, Status: "online",
		PrivateKeyEnc: []byte("SEALED-BYTES"),
	}}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/hosts", nil)
	req.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
	NewRouter(hostsServer(admin, true)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "SEALED-BYTES") {
		t.Fatal("sealed key bytes leaked in listing")
	}
	if !strings.Contains(rec.Body.String(), `"has_key":true`) {
		t.Fatalf("has_key flag missing: %s", rec.Body)
	}
}

func TestNonAdminCannotManageHosts(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/admin/hosts"},
		{http.MethodGet, "/api/v1/admin/hosts"},
		{http.MethodDelete, "/api/v1/admin/hosts/00000000-0000-0000-0000-000000000001"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		req.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
		rec := httptest.NewRecorder()
		NewRouter(hostsServer(&stubHostsAdmin{}, false)).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
}

func TestDeleteHostWithAppsConflicts(t *testing.T) {
	admin := &stubHostsAdmin{}
	req := httptest.NewRequest(http.MethodDelete,
		"/api/v1/admin/hosts/00000000-0000-0000-0000-000000000009", nil)
	req.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
	rec := httptest.NewRecorder()
	NewRouter(hostsServer(admin, true)).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}
