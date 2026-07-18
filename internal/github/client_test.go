package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

func testServer(t *testing.T) (*Client, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/installations/42/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ey") {
			t.Errorf("expected app JWT bearer, got %q", auth)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "ghs_installtoken"})
	})
	mux.HandleFunc("GET /installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ghs_installtoken" {
			t.Errorf("repos auth = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"repositories": []map[string]any{
				{"full_name": "acme/api", "clone_url": "https://github.com/acme/api.git", "default_branch": "main", "private": true},
			},
		})
	})
	mux.HandleFunc("GET /repos/acme/api/branches", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "main"}, {"name": "dev"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewClient(AppConfig{AppID: 7, AppSlug: "cargo-app", PrivateKey: testKeyPEM(t), WebhookSecret: "s"})
	c.APIBase = srv.URL
	return c, srv
}

func TestInstallationTokenAndRepos(t *testing.T) {
	c, _ := testServer(t)
	ctx := context.Background()

	tok, err := c.InstallationToken(ctx, 42)
	if err != nil || tok != "ghs_installtoken" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	repos, err := c.ListRepos(ctx, 42)
	if err != nil || len(repos) != 1 || repos[0].FullName != "acme/api" {
		t.Fatalf("repos = %+v, %v", repos, err)
	}
	branches, err := c.ListBranches(ctx, 42, "acme/api")
	if err != nil || len(branches) != 2 || branches[0] != "main" {
		t.Fatalf("branches = %v, %v", branches, err)
	}
}

func TestInstallURL(t *testing.T) {
	c := NewClient(AppConfig{AppSlug: "cargo-app"})
	got := c.InstallURL("org-123")
	want := "https://github.com/apps/cargo-app/installations/new?state=org-123"
	if got != want {
		t.Fatalf("install url = %q", got)
	}
}
