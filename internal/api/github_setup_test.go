package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/github"
)

func setupRequest(query string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/github/setup?"+query, nil)
	return r.WithContext(context.WithValue(r.Context(), userKey, sqlc.User{}))
}

// The org an installation binds to comes only from a nonce this user minted;
// an org UUID (or any unknown value) in `state` is refused.
func TestGithubSetupRequiresMintedState(t *testing.T) {
	gh := &stubGitHub{configured: true}
	_ = gh.setupOrg.Scan(testUUID)
	s := &Server{gh: gh, orgs: stubOrgs{role: "admin"}}

	rec := httptest.NewRecorder()
	s.handleGithubSetup(rec, setupRequest("state="+testUUID+"&installation_id=42"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("org-UUID state: %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.handleGithubSetup(rec, setupRequest("state=good-nonce&installation_id=42"))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/orgs/"+testUUID+"/settings" {
		t.Fatalf("valid nonce: %d %q", rec.Code, rec.Header().Get("Location"))
	}

	gh.connectErr = github.ErrInstallationTaken
	rec = httptest.NewRecorder()
	s.handleGithubSetup(rec, setupRequest("state=good-nonce&installation_id=42"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("installation bound elsewhere: %d, want 409", rec.Code)
	}

	gh.connectErr = nil
	s.orgs = stubOrgs{role: "member"}
	rec = httptest.NewRecorder()
	s.handleGithubSetup(rec, setupRequest("state=good-nonce&installation_id=42"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d, want 403", rec.Code)
	}
}
