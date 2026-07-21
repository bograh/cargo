package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ManifestCallbackPath is where GitHub redirects after the admin creates the
// app from a manifest; the temporary code arrives as a query parameter.
const ManifestCallbackPath = "/api/v1/admin/settings/github-app/manifest/callback"

// WebhookPath is the push-event webhook endpoint baked into the manifest.
const WebhookPath = "/api/v1/webhooks/github"

// BuildManifest renders the GitHub App manifest JSON for this instance. origin
// is the public base URL (e.g. https://cargo.example.com). GitHub uses it to
// pre-fill a new-app form; the admin can still rename the app before creating.
func BuildManifest(origin string) (string, error) {
	origin = strings.TrimRight(origin, "/")
	m := map[string]any{
		"name": "Cargo (" + hostLabel(origin) + ")",
		"url":  origin,
		"hook_attributes": map[string]any{
			"url":    origin + WebhookPath,
			"active": true,
		},
		"redirect_url": origin + ManifestCallbackPath,
		"public":       false,
		// Least privilege: read repository contents (to clone) + metadata, and
		// subscribe to push events for auto-deploy.
		"default_permissions": map[string]string{
			"contents": "read",
			"metadata": "read",
		},
		"default_events": []string{"push"},
	}
	b, err := json.Marshal(m)
	return string(b), err
}

// hostLabel extracts the host from an origin for use in the app's display name.
func hostLabel(origin string) string {
	if u, err := url.Parse(origin); err == nil && u.Host != "" {
		return u.Host
	}
	return "self-hosted"
}

// ExchangeManifestCode converts a manifest temporary code into the app's
// credentials via GitHub's one-time conversions endpoint. The code is
// single-use and expires within about an hour.
func ExchangeManifestCode(ctx context.Context, code string) (AppConfig, error) {
	endpoint := "https://api.github.com/app-manifests/" + url.PathEscape(code) + "/conversions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return AppConfig{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return AppConfig{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return AppConfig{}, fmt.Errorf("github manifest conversion failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		PEM           string `json:"pem"`
		WebhookSecret string `json:"webhook_secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return AppConfig{}, err
	}
	if out.ID == 0 || out.PEM == "" {
		return AppConfig{}, fmt.Errorf("github manifest conversion returned an incomplete app")
	}
	return AppConfig{
		AppID:         out.ID,
		AppSlug:       out.Slug,
		PrivateKey:    out.PEM,
		WebhookSecret: out.WebhookSecret,
	}, nil
}

// CreateFromManifest exchanges a manifest code and persists the resulting app
// config. Returns the app slug.
func (s *Service) CreateFromManifest(ctx context.Context, code string) (string, error) {
	cfg, err := ExchangeManifestCode(ctx, code)
	if err != nil {
		return "", err
	}
	if err := s.SaveApp(ctx, cfg); err != nil {
		return "", err
	}
	return cfg.AppSlug, nil
}
