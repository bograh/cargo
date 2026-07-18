package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/oidc"
	"github.com/bograh/cargo/internal/settings"
)

type stubOIDC struct {
	configured bool
	issuer     string
	clientID   string
	user       sqlc.User
	startErr   error
	resolveErr error

	gotSet   *settings.OIDCConfig
	cleared  bool
	gotNonce string
}

func (s *stubOIDC) Configured(context.Context) (bool, error) { return s.configured, nil }
func (s *stubOIDC) PublicConfig(context.Context) (string, string, bool, error) {
	return s.issuer, s.clientID, s.configured, nil
}
func (s *stubOIDC) SetConfig(_ context.Context, cfg settings.OIDCConfig) error {
	s.gotSet = &cfg
	return s.startErr
}
func (s *stubOIDC) ClearConfig(context.Context) error { s.cleared = true; return nil }
func (s *stubOIDC) StartURL(_ context.Context, redirectURL, state, nonce string) (string, error) {
	if s.startErr != nil {
		return "", s.startErr
	}
	return "https://idp.example.com/authorize?state=" + state + "&nonce=" + nonce + "&redirect_uri=" + redirectURL, nil
}
func (s *stubOIDC) ResolveCallback(_ context.Context, _, _, wantNonce string) (sqlc.User, error) {
	s.gotNonce = wantNonce
	return s.user, s.resolveErr
}

func testBox(t *testing.T) *crypto.Box {
	t.Helper()
	box, err := crypto.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func oidcServer(t *testing.T, stub *stubOIDC) *Server {
	t.Helper()
	return &Server{auth: stubAuth{user: stub.user, tokens: testTokens()}, box: testBox(t), oidc: stub}
}

func TestAuthProviders(t *testing.T) {
	for _, configured := range []bool{true, false} {
		s := oidcServer(t, &stubOIDC{configured: configured})
		rec := httptest.NewRecorder()
		NewRouter(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/providers", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var body map[string]bool
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body["password"] || body["oidc"] != configured {
			t.Fatalf("configured=%v body = %v", configured, body)
		}
	}
}

func TestOIDCStart(t *testing.T) {
	stub := &stubOIDC{configured: true}
	s := oidcServer(t, stub)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/start", nil)
	NewRouter(s).ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://idp.example.com/authorize?state=") {
		t.Fatalf("location = %q", loc)
	}
	var state *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "cargo_oidc_state" {
			state = c
		}
	}
	if state == nil {
		t.Fatal("no state cookie set")
	}
	if !state.HttpOnly || state.Path != "/api/v1/auth/oidc" || state.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags = %+v", state)
	}
	if state.Secure {
		t.Fatal("Secure should be off outside production")
	}
}

func TestOIDCStartUnconfigured(t *testing.T) {
	s := oidcServer(t, &stubOIDC{startErr: oidc.ErrNotConfigured})
	rec := httptest.NewRecorder()
	NewRouter(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/start", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
}

func startThenCallback(t *testing.T, s *Server, tamper func(*http.Cookie)) *httptest.ResponseRecorder {
	t.Helper()
	r := NewRouter(s)
	startRec := httptest.NewRecorder()
	r.ServeHTTP(startRec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/start", nil))
	if startRec.Code != http.StatusFound {
		t.Fatalf("start status = %d", startRec.Code)
	}
	var cookie *http.Cookie
	for _, c := range startRec.Result().Cookies() {
		if c.Name == "cargo_oidc_state" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no state cookie")
	}
	loc, err := startRec.Result().Location()
	if err != nil {
		t.Fatal(err)
	}
	state := loc.Query().Get("state")
	if tamper != nil {
		tamper(cookie)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?code=the-code&state="+state, nil)
	req.AddCookie(cookie)
	r.ServeHTTP(rec, req)
	return rec
}

func TestOIDCCallbackHappyPath(t *testing.T) {
	stub := &stubOIDC{configured: true, user: sqlc.User{Email: "sso@example.com"}}
	s := oidcServer(t, stub)
	rec := startThenCallback(t, s, nil)

	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
	var names []string
	for _, c := range rec.Result().Cookies() {
		names = append(names, c.Name)
		if c.Name == "cargo_oidc_state" && c.MaxAge != -1 {
			t.Fatal("state cookie not cleared")
		}
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "cargo_access") || !strings.Contains(joined, "cargo_refresh") {
		t.Fatalf("cookies = %v", names)
	}
	if stub.gotNonce == "" {
		t.Fatal("nonce not passed to ResolveCallback")
	}
}

func TestOIDCCallbackMissingOrTamperedCookie(t *testing.T) {
	stub := &stubOIDC{configured: true}

	rec := httptest.NewRecorder()
	NewRouter(oidcServer(t, stub)).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?code=c&state=s", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login?error=oidc" {
		t.Fatalf("missing cookie: status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}

	rec = startThenCallback(t, oidcServer(t, stub), func(c *http.Cookie) { c.Value = "tampered" + c.Value })
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login?error=oidc" {
		t.Fatalf("tampered cookie: status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestOIDCCallbackResolveFailure(t *testing.T) {
	stub := &stubOIDC{configured: true, resolveErr: oidc.ErrValidation}
	rec := startThenCallback(t, oidcServer(t, stub), nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login?error=oidc" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
}

func adminOIDCServer(t *testing.T, stub *stubOIDC) *Server {
	t.Helper()
	s := oidcServer(t, stub)
	s.auth = stubAuth{user: sqlc.User{IsInstanceAdmin: true}, tokens: testTokens()}
	return s
}

func adminReq(method, path, body string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
	return req
}

func TestAdminOIDCGetOmitsSecret(t *testing.T) {
	stub := &stubOIDC{configured: true, issuer: "https://idp.example.com", clientID: "cargo"}
	rec := httptest.NewRecorder()
	NewRouter(adminOIDCServer(t, stub)).ServeHTTP(rec, adminReq(http.MethodGet, "/api/v1/admin/settings/oidc", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("secret leaked: %s", rec.Body)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["configured"] != true || body["issuer_url"] != "https://idp.example.com" || body["client_id"] != "cargo" {
		t.Fatalf("body = %v", body)
	}
}

func TestAdminOIDCPutDelete(t *testing.T) {
	stub := &stubOIDC{}
	s := adminOIDCServer(t, stub)
	r := NewRouter(s)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, adminReq(http.MethodPut, "/api/v1/admin/settings/oidc",
		`{"issuer_url":"https://idp.example.com","client_id":"cargo","client_secret":"shh"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, body = %s", rec.Code, rec.Body)
	}
	if stub.gotSet == nil || stub.gotSet.ClientSecret != "shh" || stub.gotSet.IssuerURL != "https://idp.example.com" {
		t.Fatalf("SetConfig got %+v", stub.gotSet)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, adminReq(http.MethodDelete, "/api/v1/admin/settings/oidc", ""))
	if rec.Code != http.StatusOK || !stub.cleared {
		t.Fatalf("delete status = %d, cleared = %v", rec.Code, stub.cleared)
	}
}

func TestAdminOIDCPutValidationError(t *testing.T) {
	stub := &stubOIDC{startErr: oidc.ErrValidation}
	rec := httptest.NewRecorder()
	NewRouter(adminOIDCServer(t, stub)).ServeHTTP(rec, adminReq(http.MethodPut, "/api/v1/admin/settings/oidc",
		`{"issuer_url":"https://bad.example.com","client_id":"x","client_secret":"y"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}
