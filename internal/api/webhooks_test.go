package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/github"
	"github.com/jackc/pgx/v5/pgtype"
)

type stubGitHub struct {
	configured bool
	secret     string
	connected  bool
	account    string
	repos      []github.Repo
	branches   []string
	saved      *github.AppConfig
	connectErr error
}

func (s *stubGitHub) AppStatus(context.Context) (bool, string, int64, error) {
	return s.configured, "cargo-app", 7, nil
}
func (s *stubGitHub) SaveApp(_ context.Context, cfg github.AppConfig) error {
	s.saved = &cfg
	return nil
}
func (s *stubGitHub) WebhookSecret(context.Context) (string, error) {
	if !s.configured {
		return "", github.ErrNotConfigured
	}
	return s.secret, nil
}
func (s *stubGitHub) InstallURL(_ context.Context, state string) (string, error) {
	return "https://github.com/apps/cargo-app/installations/new?state=" + state, nil
}
func (s *stubGitHub) OrgStatus(context.Context, pgtype.UUID) (bool, string, error) {
	return s.connected, s.account, nil
}
func (s *stubGitHub) ConnectOrg(context.Context, pgtype.UUID, int64) error { return s.connectErr }
func (s *stubGitHub) Repos(context.Context, pgtype.UUID) ([]github.Repo, error) {
	return s.repos, nil
}
func (s *stubGitHub) Branches(context.Context, pgtype.UUID, string) ([]string, error) {
	return s.branches, nil
}

type stubWebhookApps struct {
	apps []sqlc.Application
}

func (s stubWebhookApps) ListGitAppsByBranch(context.Context, string) ([]sqlc.Application, error) {
	return s.apps, nil
}

type recordingDeps struct {
	stubDeps
	created []string
}

func (r *recordingDeps) CreateSystem(_ context.Context, appID pgtype.UUID, trigger string) (sqlc.Deployment, error) {
	v, _ := appID.Value()
	idStr, _ := v.(string)
	r.created = append(r.created, trigger+":"+idStr)
	var depID pgtype.UUID
	_ = depID.Scan(testUUID)
	return sqlc.Deployment{ID: depID, AppID: appID, Trigger: trigger, Status: "queued"}, nil
}

func signedWebhook(t *testing.T, secret, event, body string) *http.Request {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-GitHub-Event", event)
	return req
}

func webhookServer(apps []sqlc.Application) (*Server, *recordingDeps, *recordingEnqueuer) {
	deps := &recordingDeps{}
	enq := &recordingEnqueuer{}
	s := &Server{
		gh:          &stubGitHub{configured: true, secret: "hush"},
		webhookApps: stubWebhookApps{apps: apps},
		deps:        deps,
		enqueue:     enq,
	}
	return s, deps, enq
}

func pushPayload(repo, branch string) string {
	return `{"ref":"refs/heads/` + branch + `","repository":{"clone_url":"https://github.com/acme/api.git","html_url":"https://github.com/acme/api","ssh_url":"git@github.com:acme/api.git"}}`
}

func gitApp(t *testing.T, autoDeploy bool) sqlc.Application {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(testUUID); err != nil {
		t.Fatal(err)
	}
	return sqlc.Application{
		ID: id, Slug: "api", SourceType: "git", AutoDeploy: autoDeploy,
		GitRepoUrl: "https://github.com/Acme/API.git", GitBranch: "main",
	}
}

func TestWebhookBadSignatureRejected(t *testing.T) {
	s, deps, _ := webhookServer([]sqlc.Application{gitApp(t, true)})
	body := pushPayload("acme/api", "main")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	req.Header.Set("X-GitHub-Event", "push")
	rec := httptest.NewRecorder()
	NewRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(deps.created) != 0 {
		t.Fatal("deployment created despite bad signature")
	}
}

func TestWebhookPushDeploysMatchingApp(t *testing.T) {
	s, deps, enq := webhookServer([]sqlc.Application{gitApp(t, true)})
	rec := httptest.NewRecorder()
	NewRouter(s).ServeHTTP(rec, signedWebhook(t, "hush", "push", pushPayload("acme/api", "main")))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if len(deps.created) != 1 || !strings.HasPrefix(deps.created[0], "webhook:") {
		t.Fatalf("created = %v", deps.created)
	}
	if len(enq.ids) != 1 {
		t.Fatalf("enqueued = %v", enq.ids)
	}
}

func TestWebhookRespectsAutoDeployToggle(t *testing.T) {
	s, deps, _ := webhookServer([]sqlc.Application{gitApp(t, false)})
	rec := httptest.NewRecorder()
	NewRouter(s).ServeHTTP(rec, signedWebhook(t, "hush", "push", pushPayload("acme/api", "main")))
	if rec.Code != http.StatusOK || len(deps.created) != 0 {
		t.Fatalf("status = %d created = %v", rec.Code, deps.created)
	}
}

func TestWebhookIgnoresNonPushEvents(t *testing.T) {
	s, deps, _ := webhookServer([]sqlc.Application{gitApp(t, true)})
	rec := httptest.NewRecorder()
	NewRouter(s).ServeHTTP(rec, signedWebhook(t, "hush", "ping", `{"zen":"hi"}`))
	if rec.Code != http.StatusOK || len(deps.created) != 0 {
		t.Fatalf("status = %d created = %v", rec.Code, deps.created)
	}
}

func TestNormalizeRepoURL(t *testing.T) {
	want := "github.com/acme/api"
	for _, in := range []string{
		"https://github.com/Acme/API.git",
		"http://github.com/acme/api",
		"git@github.com:acme/api.git",
		"https://github.com/acme/api/",
	} {
		if got := normalizeRepoURL(in); got != want {
			t.Fatalf("normalize(%q) = %q", in, got)
		}
	}
}
