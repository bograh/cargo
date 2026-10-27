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
		if u, err := s.gh.InstallURL(r.Context(), orgID, userFrom(r.Context()).ID); err == nil {
			out["install_url"] = u
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGithubSetup is GitHub's post-install redirect target. `state` is the
// single-use nonce minted by InstallURL for this user; it — not anything the
// caller can name — decides which org the installation binds to.
func (s *Server) handleGithubSetup(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	instIDStr := r.URL.Query().Get("installation_id")
	instID, err := strconv.ParseInt(instIDStr, 10, 64)
	if err != nil || state == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "state and installation_id are required")
		return
	}
	ctx := r.Context()
	orgID, err := s.gh.ConsumeSetupState(ctx, state, userFrom(ctx).ID)
	if errors.Is(err, github.ErrInvalidSetupState) {
		Error(w, http.StatusBadRequest, "invalid_state", "this install link is invalid or has expired; start again from organization settings")
		return
	}
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "setup state lookup failed")
		return
	}
	// Re-check the role now: it may have changed since the link was minted.
	_, role, err := s.orgs.Get(ctx, orgID, userFrom(ctx).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	if !orgs.RoleAtLeast(role, "admin") {
		Error(w, http.StatusForbidden, "forbidden", "org admin required")
		return
	}
	if err := s.gh.ConnectOrg(ctx, orgID, instID); err != nil {
		if errors.Is(err, github.ErrInstallationTaken) {
			Error(w, http.StatusConflict, "installation_taken", "this GitHub installation is already connected to another organization")
			return
		}
		Error(w, http.StatusInternalServerError, "internal", "saving installation failed")
		return
	}
	http.Redirect(w, r, "/orgs/"+uuidString(orgID)+"/settings", http.StatusFound)
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
