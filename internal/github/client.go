package github

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Client struct {
	Config   AppConfig
	APIBase  string // default https://api.github.com
	HTTPBase string // default https://github.com
	HTTP     *http.Client
}

func NewClient(cfg AppConfig) *Client {
	return &Client{
		Config:   cfg,
		APIBase:  "https://api.github.com",
		HTTPBase: "https://github.com",
		HTTP:     &http.Client{Timeout: 15 * time.Second},
	}
}

type Repo struct {
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// InstallURL is where an org admin installs the App; state carries the org ID.
func (c *Client) InstallURL(state string) string {
	return fmt.Sprintf("%s/apps/%s/installations/new?state=%s", c.HTTPBase, c.Config.AppSlug, url.QueryEscape(state))
}

func (c *Client) parseKey() (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(c.Config.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("github: private key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github: parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("github: private key is not RSA")
	}
	return key, nil
}

// appJWT signs a short-lived RS256 token identifying the GitHub App itself.
func (c *Client) appJWT() (string, error) {
	key, err := c.parseKey()
	if err != nil {
		return "", err
	}
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": fmt.Sprintf("%d", c.Config.AppID),
	})
	return tok.SignedString(key)
}

func (c *Client) doJSON(ctx context.Context, method, urlStr, bearer string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, urlStr, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("github: %s %s → %d: %s", method, urlStr, res.StatusCode, body)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// InstallationToken exchanges the App JWT for a short-lived installation token.
func (c *Client) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	jwtStr, err := c.appJWT()
	if err != nil {
		return "", err
	}
	var out struct {
		Token string `json:"token"`
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.APIBase, installationID)
	if err := c.doJSON(ctx, http.MethodPost, url, jwtStr, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}

// ListRepos returns all repositories the installation can access.
func (c *Client) ListRepos(ctx context.Context, installationID int64) ([]Repo, error) {
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	var all []Repo
	for page := 1; ; page++ {
		var out struct {
			Repositories []Repo `json:"repositories"`
		}
		url := fmt.Sprintf("%s/installation/repositories?per_page=100&page=%d", c.APIBase, page)
		if err := c.doJSON(ctx, http.MethodGet, url, token, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Repositories...)
		if len(out.Repositories) < 100 {
			return all, nil
		}
	}
}

// ListBranches returns branch names for owner/repo.
func (c *Client) ListBranches(ctx context.Context, installationID int64, fullName string) ([]string, error) {
	token, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	var out []struct {
		Name string `json:"name"`
	}
	url := fmt.Sprintf("%s/repos/%s/branches?per_page=100", c.APIBase, fullName)
	if err := c.doJSON(ctx, http.MethodGet, url, token, &out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out))
	for _, b := range out {
		names = append(names, b.Name)
	}
	return names, nil
}

// InstallationAccount fetches the installation's account login (best effort).
func (c *Client) InstallationAccount(ctx context.Context, installationID int64) (string, error) {
	jwtStr, err := c.appJWT()
	if err != nil {
		return "", err
	}
	var out struct {
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	url := fmt.Sprintf("%s/app/installations/%d", c.APIBase, installationID)
	if err := c.doJSON(ctx, http.MethodGet, url, jwtStr, &out); err != nil {
		return "", err
	}
	return out.Account.Login, nil
}
