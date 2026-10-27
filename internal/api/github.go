package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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

// githubOwnership is implemented by services that can prove the installing
// GitHub user controls an installation (OAuth user-to-server exchange). It is
// optional so test fakes without it exercise the unverified fallback.
type githubOwnership interface {
	OwnershipVerification(ctx context.Context) (clientID string, verified bool, err error)
	VerifyInstallation(ctx context.Context, code string, installationID int64) (bool, error)
	SaveOAuthClient(ctx context.Context, clientID, clientSecret string) error
}

// --- instance admin: app credentials (FR-7.1, PhasedPlans 3.4) ---

func (s *Server) handleGetGithubApp(w http.ResponseWriter, r *http.Request) {
	configured, slug, appID, err := s.gh.AppStatus(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "settings read failed")
		return
	}
	out := map[string]any{
		"configured":         configured,
		"app_slug":           slug,
		"app_id":             appID,
		"client_id":          "",
		"ownership_verified": false,
	}
	if own, ok := s.gh.(githubOwnership); ok && configured {
		clientID, verified, err := own.OwnershipVerification(r.Context())
		if err != nil {
			Error(w, http.StatusInternalServerError, "internal", "settings read failed")
			return
		}
		out["client_id"] = clientID
		out["ownership_verified"] = verified
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePutGithubApp saves app credentials. A body with only client_id /
// client_secret updates the OAuth client on the existing app, so apps created
// before ownership verification can turn it on without re-entering the key.
// The client secret is write-only: blank keeps the stored one.
func (s *Server) handlePutGithubApp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AppID         int64  `json:"app_id"`
		AppSlug       string `json:"app_slug"`
		PrivateKey    string `json:"private_key"`
		WebhookSecret string `json:"webhook_secret"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		Error(w, http.StatusBadRequest, "validation_failed", "invalid JSON body")
		return
	}
	oauthOnly := body.AppID == 0 && body.AppSlug == "" && body.PrivateKey == "" && body.WebhookSecret == ""
	if oauthOnly {
		own, ok := s.gh.(githubOwnership)
		if !ok {
			Error(w, http.StatusBadRequest, "validation_failed", "OAuth client credentials are not supported")
			return
		}
		if err := own.SaveOAuthClient(r.Context(), body.ClientID, body.ClientSecret); err != nil {
			githubSaveError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
		return
	}
	if body.AppID == 0 || body.AppSlug == "" || body.PrivateKey == "" || body.WebhookSecret == "" {
		Error(w, http.StatusBadRequest, "validation_failed",
			"app_id, app_slug, private_key, and webhook_secret are required")
		return
	}
	err := s.gh.SaveApp(r.Context(), github.AppConfig{
		AppID: body.AppID, AppSlug: body.AppSlug,
		PrivateKey: body.PrivateKey, WebhookSecret: body.WebhookSecret,
		ClientID: body.ClientID, ClientSecret: body.ClientSecret,
	})
	if err != nil {
		githubSaveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func githubSaveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, github.ErrNotConfigured):
		Error(w, http.StatusConflict, "github_not_configured", "configure the GitHub App before adding OAuth client credentials")
	case errors.Is(err, github.ErrClientSecretRequired):
		Error(w, http.StatusBadRequest, "validation_failed", err.Error())
	default:
		Error(w, http.StatusInternalServerError, "internal", "settings save failed")
	}
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
// caller can name — decides which org the installation binds to. When the
// app has OAuth client credentials, the redirect's `code` must also prove the
// installing GitHub user can administer `installation_id`; otherwise anyone
// who learned an installation ID could bind it to their own org.
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
	if !s.verifyGithubInstallation(w, r, instID) {
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

// verifyGithubInstallation enforces installation ownership when the app can
// verify it, writing the error response itself. An app without OAuth client
// credentials falls back to nonce-only binding, logged so the gap is visible.
func (s *Server) verifyGithubInstallation(w http.ResponseWriter, r *http.Request, instID int64) bool {
	own, ok := s.gh.(githubOwnership)
	if !ok {
		return true
	}
	verified, err := own.VerifyInstallation(r.Context(), r.URL.Query().Get("code"), instID)
	switch {
	case errors.Is(err, github.ErrAuthorizationRequired):
		Error(w, http.StatusForbidden, "github_authorization_required",
			"GitHub did not authorize this installation; start again from organization settings")
		return false
	case errors.Is(err, github.ErrInstallationNotAccessible):
		Error(w, http.StatusForbidden, "installation_not_accessible",
			"your GitHub account cannot administer this installation")
		return false
	case err != nil:
		slog.Warn("github installation verification failed", "installation_id", instID, "err", err)
		Error(w, http.StatusBadGateway, "github_error", "could not verify the installation with GitHub")
		return false
	}
	if !verified {
		slog.Warn("github installation bound without ownership verification; add the app's OAuth client ID and secret in admin settings",
			"installation_id", instID)
	}
	return true
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
