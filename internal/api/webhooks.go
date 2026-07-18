package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// normalizeRepoURL reduces the various ways to write the same GitHub repo to
// one comparable form: host/owner/repo (lowercase, no scheme, no .git).
func normalizeRepoURL(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "git://")
	if after, ok := strings.CutPrefix(s, "git@"); ok {
		s = strings.Replace(after, ":", "/", 1)
	}
	s = strings.TrimSuffix(s, ".git")
	return strings.TrimSuffix(s, "/")
}

// handleGithubWebhook implements FR-4.1 push-to-deploy with HMAC validation
// (NFR-5). It never trusts the payload before the signature check.
func (s *Server) handleGithubWebhook(w http.ResponseWriter, r *http.Request) {
	secret, err := s.gh.WebhookSecret(r.Context())
	if err != nil {
		Error(w, http.StatusUnauthorized, "unauthenticated", "webhook not configured")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_body", "could not read body")
		return
	}
	sig := r.Header.Get("X-Hub-Signature-256")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if sig == "" || !hmac.Equal([]byte(sig), []byte(want)) {
		Error(w, http.StatusUnauthorized, "invalid_signature", "webhook signature mismatch")
		return
	}

	if r.Header.Get("X-GitHub-Event") != "push" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	var payload struct {
		Ref        string `json:"ref"`
		Repository struct {
			CloneURL string `json:"clone_url"`
			HTMLURL  string `json:"html_url"`
			SSHURL   string `json:"ssh_url"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "malformed payload")
		return
	}
	branch, ok := strings.CutPrefix(payload.Ref, "refs/heads/")
	if !ok {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	pushed := map[string]bool{
		normalizeRepoURL(payload.Repository.CloneURL): true,
		normalizeRepoURL(payload.Repository.HTMLURL):  true,
		normalizeRepoURL(payload.Repository.SSHURL):   true,
	}

	apps, err := s.webhookApps.ListGitAppsByBranch(r.Context(), branch)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "app lookup failed")
		return
	}
	deployed := 0
	for _, app := range apps {
		if !app.AutoDeploy || !pushed[normalizeRepoURL(app.GitRepoUrl)] {
			continue
		}
		dep, err := s.deps.CreateSystem(r.Context(), app.ID, "webhook")
		if err != nil {
			slog.Warn("webhook deployment create failed", "app", app.Slug, "err", err)
			continue
		}
		id, _ := dep.ID.Value()
		idStr, _ := id.(string)
		if err := s.enqueue.EnqueueDeploy(r.Context(), idStr); err != nil {
			_ = s.deps.Finish(r.Context(), dep.ID, "failed", "enqueue failed: "+err.Error())
			slog.Warn("webhook enqueue failed", "app", app.Slug, "err", err)
			continue
		}
		deployed++
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "deployments": deployed})
}
