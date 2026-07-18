package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/bograh/cargo/internal/github"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func githubError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, github.ErrNotConfigured):
		Error(w, http.StatusConflict, "github_not_configured", "the instance admin has not configured a GitHub App")
	case errors.Is(err, github.ErrNotConnected):
		Error(w, http.StatusConflict, "github_not_connected", "this organization has no GitHub installation")
	default:
		Error(w, http.StatusBadGateway, "github_error", "GitHub request failed")
	}
}

// --- instance admin: app credentials (FR-7.1, PhasedPlans 3.4) ---

func (s *Server) handleGetGithubApp(w http.ResponseWriter, r *http.Request) {
	configured, slug, appID, err := s.gh.AppStatus(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "settings read failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": configured,
		"app_slug":   slug,
		"app_id":     appID,
	})
}

func (s *Server) handlePutGithubApp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AppID         int64  `json:"app_id"`
		AppSlug       string `json:"app_slug"`
		PrivateKey    string `json:"private_key"`
		WebhookSecret string `json:"webhook_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.AppID == 0 || body.AppSlug == "" || body.PrivateKey == "" || body.WebhookSecret == "" {
		Error(w, http.StatusBadRequest, "validation_failed",
			"app_id, app_slug, private_key, and webhook_secret are required")
		return
	}
	err := s.gh.SaveApp(r.Context(), github.AppConfig{
		AppID: body.AppID, AppSlug: body.AppSlug,
		PrivateKey: body.PrivateKey, WebhookSecret: body.WebhookSecret,
	})
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "settings save failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// --- org-scoped connection + repo browsing ---

// orgRole loads the caller's role via the org service (404 for non-members).
func (s *Server) orgRole(w http.ResponseWriter, r *http.Request) (pgtype.UUID, string, bool) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return id, "", false
	}
	_, role, err := s.orgs.Get(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return id, "", false
	}
	return id, role, true
}

func (s *Server) handleOrgGithubStatus(w http.ResponseWriter, r *http.Request) {
	orgID, role, ok := s.orgRole(w, r)
	if !ok {
		return
	}
	configured, _, _, err := s.gh.AppStatus(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "settings read failed")
		return
	}
	connected, account, err := s.gh.OrgStatus(r.Context(), orgID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "installation read failed")
		return
	}
	out := map[string]any{
		"configured":    configured,
		"connected":     connected,
		"account_login": account,
	}
	if configured && orgs.RoleAtLeast(role, "admin") {
		idStr, _ := orgID.Value()
		if state, ok := idStr.(string); ok {
			if u, err := s.gh.InstallURL(r.Context(), state); err == nil {
				out["install_url"] = u
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGithubSetup is GitHub's post-install redirect target.
func (s *Server) handleGithubSetup(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	instIDStr := r.URL.Query().Get("installation_id")
	instID, err := strconv.ParseInt(instIDStr, 10, 64)
	var orgID pgtype.UUID
	if err != nil || state == "" || orgID.Scan(state) != nil {
		Error(w, http.StatusBadRequest, "validation_failed", "state and installation_id are required")
		return
	}
	// Only an admin+ of the org named in state may bind the installation.
	_, role, err := s.orgs.Get(r.Context(), orgID, userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	if !orgs.RoleAtLeast(role, "admin") {
		Error(w, http.StatusForbidden, "forbidden", "org admin required")
		return
	}
	if err := s.gh.ConnectOrg(r.Context(), orgID, instID); err != nil {
		Error(w, http.StatusInternalServerError, "internal", "saving installation failed")
		return
	}
	http.Redirect(w, r, "/orgs/"+state+"/settings", http.StatusFound)
}

func (s *Server) handleGithubRepos(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.orgRole(w, r)
	if !ok {
		return
	}
	repos, err := s.gh.Repos(r.Context(), orgID)
	if err != nil {
		githubError(w, err)
		return
	}
	if repos == nil {
		repos = []github.Repo{}
	}
	writeJSON(w, http.StatusOK, repos)
}

func (s *Server) handleGithubBranches(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.orgRole(w, r)
	if !ok {
		return
	}
	fullName := chi.URLParam(r, "owner") + "/" + chi.URLParam(r, "repo")
	branches, err := s.gh.Branches(r.Context(), orgID, fullName)
	if err != nil {
		githubError(w, err)
		return
	}
	if branches == nil {
		branches = []string{}
	}
	writeJSON(w, http.StatusOK, branches)
}
