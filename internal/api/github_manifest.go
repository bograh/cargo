package api

import (
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"net/http"

	"github.com/bograh/cargo/internal/github"
)

// manifestFormTmpl auto-submits the app manifest to GitHub. GitHub reads the
// hidden "manifest" field, shows a confirmation page, then redirects back to
// the manifest callback with a temporary code.
var manifestFormTmpl = template.Must(template.New("manifest").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Creating GitHub App…</title></head>
<body style="background:#0b0c0e;color:#e8e9eb;font-family:system-ui,sans-serif;display:flex;min-height:100vh;align-items:center;justify-content:center">
<form id="f" method="post" action="{{.Action}}">
  <input type="hidden" name="manifest" value="{{.Manifest}}">
  <noscript><button type="submit">Continue to GitHub</button></noscript>
</form>
<p>Redirecting to GitHub…</p>
<script>document.getElementById('f').submit();</script>
</body></html>`))

// handleGithubManifestStart renders a page that posts a prefilled GitHub App
// manifest to GitHub, so the admin creates the app in one click without
// copying any credentials. A state cookie guards the return trip.
func (s *Server) handleGithubManifestStart(w http.ResponseWriter, r *http.Request) {
	manifest, err := github.BuildManifest(baseURL(r))
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "could not build manifest")
		return
	}
	state, err := randomState()
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "could not start setup")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "gh_manifest_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = manifestFormTmpl.Execute(w, map[string]string{
		"Action":   "https://github.com/settings/apps/new?state=" + template.URLQueryEscaper(state),
		"Manifest": manifest,
	})
}

// handleGithubManifestCallback is GitHub's redirect target after app creation.
// It exchanges the temporary code for credentials, stores them, and returns to
// the admin page.
func (s *Server) handleGithubManifestCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie("gh_manifest_state")
	// Clear the state cookie regardless of outcome.
	http.SetCookie(w, &http.Cookie{Name: "gh_manifest_state", Value: "", Path: "/", MaxAge: -1})
	if code == "" || state == "" || err != nil || cookie.Value != state {
		Error(w, http.StatusBadRequest, "validation_failed", "invalid or expired setup state")
		return
	}
	if _, err := s.gh.CreateFromManifest(r.Context(), code); err != nil {
		http.Redirect(w, r, "/admin?github=error", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin?github=connected", http.StatusFound)
}

func randomState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
