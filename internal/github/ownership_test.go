package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// fakeGitHub serves the OAuth token exchange and a paged /user/installations
// listing for whichever installation IDs the authorizing user can administer.
func fakeGitHub(t *testing.T, owned []int64) OAuthEndpoints {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "csecret" || r.Form.Get("code") != "good-code" {
			_, _ = fmt.Fprint(w, `{"error":"bad_verification_code"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"access_token":"user-token","token_type":"bearer"}`)
	})
	mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer user-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		start := (page - 1) * 100
		type inst struct {
			ID int64 `json:"id"`
		}
		out := struct {
			Installations []inst `json:"installations"`
		}{Installations: []inst{}}
		for i := start; i < len(owned) && i < start+100; i++ {
			out.Installations = append(out.Installations, inst{ID: owned[i]})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return OAuthEndpoints{WebBase: srv.URL, APIBase: srv.URL, HTTP: srv.Client()}
}

func TestVerifyInstallationAccessAcceptsOwnedInstallation(t *testing.T) {
	ep := fakeGitHub(t, []int64{7, 42})
	if err := VerifyInstallationAccess(context.Background(), ep, "cid", "csecret", "good-code", 42); err != nil {
		t.Fatalf("owned installation refused: %v", err)
	}
}

// The installation list is paged; an installation past the first page must
// still be found.
func TestVerifyInstallationAccessPages(t *testing.T) {
	owned := make([]int64, 150)
	for i := range owned {
		owned[i] = int64(1000 + i)
	}
	ep := fakeGitHub(t, owned)
	if err := VerifyInstallationAccess(context.Background(), ep, "cid", "csecret", "good-code", 1140); err != nil {
		t.Fatalf("installation on page 2 refused: %v", err)
	}
}

func TestVerifyInstallationAccessRejectsForeignInstallation(t *testing.T) {
	ep := fakeGitHub(t, []int64{7})
	err := VerifyInstallationAccess(context.Background(), ep, "cid", "csecret", "good-code", 42)
	if !errors.Is(err, ErrInstallationNotAccessible) {
		t.Fatalf("foreign installation: got %v, want ErrInstallationNotAccessible", err)
	}
}

func TestVerifyInstallationAccessRequiresValidCode(t *testing.T) {
	ep := fakeGitHub(t, []int64{42})
	if err := VerifyInstallationAccess(context.Background(), ep, "cid", "csecret", "", 42); !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("missing code: got %v, want ErrAuthorizationRequired", err)
	}
	if err := VerifyInstallationAccess(context.Background(), ep, "cid", "csecret", "stale-code", 42); !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("rejected code: got %v, want ErrAuthorizationRequired", err)
	}
}

func TestBuildManifestRequestsOAuthOnInstall(t *testing.T) {
	raw, err := BuildManifest("https://cargo.example.com")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m["request_oauth_on_install"] != true {
		t.Fatalf("request_oauth_on_install = %v", m["request_oauth_on_install"])
	}
	cbs, _ := m["callback_urls"].([]any)
	if len(cbs) != 1 || cbs[0] != "https://cargo.example.com"+SetupPath {
		t.Fatalf("callback_urls = %v", m["callback_urls"])
	}
}
