package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/config"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startServer(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("cargo"),
		tcpostgres.WithUsername("cargo"),
		tcpostgres.WithPassword("cargo"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	if err := db.Migrate(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	box, err := crypto.New(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatalf("crypto: %v", err)
	}
	cfg := config.Config{Env: "development"}
	return NewServer(cfg, pool, box).Handler()
}

// client carries cookies between requests like a browser.
type client struct {
	h       http.Handler
	cookies []*http.Cookie
}

func (c *client) do(t *testing.T, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		replaced := false
		for i, old := range c.cookies {
			if old.Name == ck.Name {
				c.cookies[i] = ck
				replaced = true
			}
		}
		if !replaced {
			c.cookies = append(c.cookies, ck)
		}
	}
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec, decoded
}

func TestFullAuthOrgInviteFlow(t *testing.T) {
	h := startServer(t)

	// First user registers → instance admin.
	alice := &client{h: h}
	rec, body := alice.do(t, http.MethodPost, "/api/v1/auth/register",
		`{"email":"alice@x.co","password":"password-123"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	if body["is_instance_admin"] != true {
		t.Fatal("first user should be instance admin")
	}

	// Alice creates an org and an invite.
	rec, body = alice.do(t, http.MethodPost, "/api/v1/orgs", `{"name":"Acme"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org: %d %s", rec.Code, rec.Body)
	}
	orgID, _ := body["id"].(string)
	if orgID == "" {
		t.Fatalf("org id missing: %v", body)
	}
	rec, body = alice.do(t, http.MethodPost, "/api/v1/orgs/"+orgID+"/invites", `{"role":"member"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create invite: %d %s", rec.Code, rec.Body)
	}
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatal("invite token missing")
	}

	// Second user registers (not admin), sees no orgs, accepts the invite.
	bob := &client{h: h}
	rec, body = bob.do(t, http.MethodPost, "/api/v1/auth/register",
		`{"email":"bob@x.co","password":"password-123"}`)
	if rec.Code != http.StatusCreated || body["is_instance_admin"] != false {
		t.Fatalf("bob register: %d %v", rec.Code, body)
	}
	if rec, _ := bob.do(t, http.MethodGet, "/api/v1/orgs/"+orgID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob pre-invite org get: %d", rec.Code)
	}
	if rec, _ = bob.do(t, http.MethodPost, "/api/v1/invites/accept", `{"token":"`+token+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("accept invite: %d %s", rec.Code, rec.Body)
	}
	rec, body = bob.do(t, http.MethodGet, "/api/v1/orgs/"+orgID, "")
	if rec.Code != http.StatusOK || body["role"] != "member" {
		t.Fatalf("bob org get: %d %v", rec.Code, body)
	}

	// Bob (not instance admin) cannot use admin endpoints; Alice can.
	if rec, _ := bob.do(t, http.MethodGet, "/api/v1/admin/users", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("bob admin: %d", rec.Code)
	}
	if rec, _ := alice.do(t, http.MethodGet, "/api/v1/admin/users", ""); rec.Code != http.StatusOK {
		t.Fatalf("alice admin: %d", rec.Code)
	}

	// Refresh rotates and old refresh is rejected afterwards.
	var oldRefresh string
	for _, ck := range alice.cookies {
		if ck.Name == "cargo_refresh" {
			oldRefresh = ck.Value
		}
	}
	if rec, _ := alice.do(t, http.MethodPost, "/api/v1/auth/refresh", ""); rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d", rec.Code)
	}
	stale := &client{h: h, cookies: []*http.Cookie{{Name: "cargo_refresh", Value: oldRefresh}}}
	if rec, _ := stale.do(t, http.MethodPost, "/api/v1/auth/refresh", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale refresh: %d", rec.Code)
	}
}

func TestReadyz(t *testing.T) {
	h := startServer(t)
	orig := dockerPing
	t.Cleanup(func() { dockerPing = orig })

	// Real pool up + docker OK → 200 ready.
	dockerPing = func(context.Context) error { return nil }
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready: got %d, body %s", rec.Code, rec.Body)
	}

	// Docker unreachable → 503 naming docker.
	dockerPing = func(context.Context) error { return context.DeadlineExceeded }
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("docker-down: got %d", rec.Code)
	}
	var body map[string]map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["error"]["message"] == "" || !strings.Contains(body["error"]["message"], "docker") {
		t.Fatalf("expected docker in message, got %v", body)
	}
}
