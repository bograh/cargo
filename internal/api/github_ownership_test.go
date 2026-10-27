package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/github"
)

// verifyingGitHub is a stubGitHub that can check installation ownership: the
// authorizing user administers only `owned`.
type verifyingGitHub struct {
	*stubGitHub
	clientID, clientSecret string
	owned                  int64
	savedClientID          string
	savedClientSecret      string
}

func (v *verifyingGitHub) OwnershipVerification(context.Context) (string, bool, error) {
	return v.clientID, v.clientID != "" && v.clientSecret != "", nil
}

func (v *verifyingGitHub) VerifyInstallation(_ context.Context, code string, instID int64) (bool, error) {
	if v.clientID == "" || v.clientSecret == "" {
		return false, nil
	}
	if code != "good-code" {
		return false, github.ErrAuthorizationRequired
	}
	if instID != v.owned {
		return false, github.ErrInstallationNotAccessible
	}
	return true, nil
}

func (v *verifyingGitHub) SaveOAuthClient(_ context.Context, id, secret string) error {
	v.savedClientID, v.savedClientSecret = id, secret
	return nil
}

func newVerifyingServer(clientSecret string) *Server {
	gh := &verifyingGitHub{stubGitHub: &stubGitHub{configured: true}, clientID: "cid", clientSecret: clientSecret, owned: 42}
	_ = gh.setupOrg.Scan(testUUID)
	return &Server{gh: gh, orgs: stubOrgs{role: "admin"}}
}

func TestGithubSetupVerifiesInstallationOwnership(t *testing.T) {
	s := newVerifyingServer("csecret")
	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"verified owner", "state=good-nonce&installation_id=42&code=good-code", http.StatusFound},
		{"foreign installation", "state=good-nonce&installation_id=99&code=good-code", http.StatusForbidden},
		{"missing code", "state=good-nonce&installation_id=42", http.StatusForbidden},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		s.handleGithubSetup(rec, setupRequest(tc.query))
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
	}
}

// Without OAuth client credentials the app can't verify ownership; setup
// keeps working on the nonce alone and the admin status reports it.
func TestGithubSetupFallsBackWhenUnverifiable(t *testing.T) {
	s := newVerifyingServer("")
	rec := httptest.NewRecorder()
	s.handleGithubSetup(rec, setupRequest("state=good-nonce&installation_id=99"))
	if rec.Code != http.StatusFound {
		t.Fatalf("fallback setup: %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	s.handleGetGithubApp(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/github-app", nil))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ownership_verified"] != false || out["client_id"] != "cid" {
		t.Fatalf("status = %v", out)
	}
	if _, leaked := out["client_secret"]; leaked {
		t.Fatal("client secret exposed")
	}
}

func TestPutGithubAppOAuthOnlyUpdate(t *testing.T) {
	s := newVerifyingServer("")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/github-app",
		strings.NewReader(`{"client_id":"cid2","client_secret":"s2"}`))
	s.handlePutGithubApp(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("oauth-only save: %d %s", rec.Code, rec.Body)
	}
	gh := s.gh.(*verifyingGitHub)
	if gh.savedClientID != "cid2" || gh.savedClientSecret != "s2" {
		t.Fatalf("saved %q/%q", gh.savedClientID, gh.savedClientSecret)
	}
	if gh.saved != nil {
		t.Fatal("oauth-only update must not overwrite app credentials")
	}
}
