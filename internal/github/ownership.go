package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SetupPath is the post-install redirect target. With "request user
// authorization during installation" enabled, GitHub sends the user here
// (as the OAuth callback URL) carrying both installation_id and an OAuth code.
const SetupPath = "/api/v1/github/setup"

var (
	// ErrAuthorizationRequired: ownership verification is configured but the
	// setup redirect carried no OAuth code.
	ErrAuthorizationRequired = errors.New("github user authorization is required to connect an installation")
	// ErrInstallationNotAccessible: the authorizing GitHub user cannot manage
	// the installation named in the redirect.
	ErrInstallationNotAccessible = errors.New("the authorizing GitHub user cannot access this installation")
	// ErrClientSecretRequired: a new client ID was saved without its secret.
	ErrClientSecretRequired = errors.New("a client secret is required when changing the client ID")
)

// maxInstallationPages caps /user/installations paging (100 per page).
const maxInstallationPages = 20

// OAuthEndpoints are GitHub's web (token exchange) and API bases; tests point
// them at an httptest server.
type OAuthEndpoints struct {
	WebBase string // default https://github.com
	APIBase string // default https://api.github.com
	HTTP    *http.Client
}

func defaultOAuthEndpoints() OAuthEndpoints {
	return OAuthEndpoints{
		WebBase: "https://github.com",
		APIBase: "https://api.github.com",
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// VerifyInstallationAccess proves the GitHub user who completed the install
// can manage installationID: it exchanges the OAuth code for a user-to-server
// token and looks for the installation in GET /user/installations. That list
// only contains installations the user has explicit permission to administer,
// so an installation ID learned elsewhere is refused.
func VerifyInstallationAccess(ctx context.Context, ep OAuthEndpoints, clientID, clientSecret, code string, installationID int64) error {
	if code == "" {
		return ErrAuthorizationRequired
	}
	token, err := exchangeOAuthCode(ctx, ep, clientID, clientSecret, code)
	if err != nil {
		return err
	}
	for page := 1; page <= maxInstallationPages; page++ {
		var out struct {
			Installations []struct {
				ID int64 `json:"id"`
			} `json:"installations"`
		}
		u := fmt.Sprintf("%s/user/installations?per_page=100&page=%d", strings.TrimRight(ep.APIBase, "/"), page)
		if err := getJSON(ctx, ep.HTTP, u, token, &out); err != nil {
			return err
		}
		for _, inst := range out.Installations {
			if inst.ID == installationID {
				return nil
			}
		}
		if len(out.Installations) < 100 {
			break
		}
	}
	return ErrInstallationNotAccessible
}

func exchangeOAuthCode(ctx context.Context, ep OAuthEndpoints, clientID, clientSecret, code string) (string, error) {
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(ep.WebBase, "/")+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := ep.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return "", fmt.Errorf("github oauth exchange failed (%d): %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	// GitHub reports a bad/expired code as 200 with an "error" field.
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != "" || out.AccessToken == "" {
		return "", fmt.Errorf("%w: oauth exchange returned %q", ErrAuthorizationRequired, out.Error)
	}
	return out.AccessToken, nil
}

func getJSON(ctx context.Context, hc *http.Client, urlStr, bearer string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("github: GET %s → %d: %s", urlStr, res.StatusCode, body)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// OwnershipVerification reports the app's OAuth client ID and whether setup
// callbacks can be verified (both client ID and secret are stored).
func (s *Service) OwnershipVerification(ctx context.Context) (clientID string, verified bool, err error) {
	cfg, err := LoadAppConfig(ctx, s.q, s.box)
	if err != nil || cfg == nil {
		return "", false, err
	}
	return cfg.ClientID, cfg.ClientID != "" && cfg.ClientSecret != "", nil
}

// VerifyInstallation checks the setup callback's OAuth code against
// installationID. It returns (false, nil) when the app has no OAuth client
// credentials — the caller decides how to treat an unverifiable setup.
func (s *Service) VerifyInstallation(ctx context.Context, code string, installationID int64) (bool, error) {
	cfg, err := LoadAppConfig(ctx, s.q, s.box)
	if err != nil {
		return false, err
	}
	if cfg == nil {
		return false, ErrNotConfigured
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return false, nil
	}
	if err := VerifyInstallationAccess(ctx, s.oauth, cfg.ClientID, cfg.ClientSecret, code, installationID); err != nil {
		return false, err
	}
	return true, nil
}

// SaveOAuthClient adds or replaces the OAuth client credentials on an
// already-configured app. An empty secret keeps the stored one, so the admin
// form never has to echo it back.
func (s *Service) SaveOAuthClient(ctx context.Context, clientID, clientSecret string) error {
	cfg, err := LoadAppConfig(ctx, s.q, s.box)
	if err != nil {
		return err
	}
	if cfg == nil {
		return ErrNotConfigured
	}
	switch {
	case clientID == "":
		// Clearing the client ID turns verification off entirely.
		cfg.ClientSecret = ""
	case clientSecret != "":
		cfg.ClientSecret = clientSecret
	case clientID != cfg.ClientID:
		// A stored secret belongs to the old client; never pair it with a new ID.
		return ErrClientSecretRequired
	}
	cfg.ClientID = clientID
	return SaveAppConfig(ctx, s.q, s.box, *cfg)
}
