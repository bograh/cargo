package settings

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

const keyOIDC = "oidc"

type OIDCConfig struct {
	IssuerURL    string `json:"issuer_url"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// OIDC returns nil when unset.
func (s *Service) OIDC(ctx context.Context) (*OIDCConfig, error) {
	row, err := s.q.GetInstanceSetting(ctx, keyOIDC)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Enc string `json:"enc"`
	}
	if err := json.Unmarshal(row.Value, &wrapper); err != nil {
		return nil, err
	}
	if wrapper.Enc == "" {
		return nil, nil
	}
	sealed, err := base64.StdEncoding.DecodeString(wrapper.Enc)
	if err != nil {
		return nil, err
	}
	raw, err := s.box.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("oidc settings decrypt: %w", err)
	}
	var cfg OIDCConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (s *Service) SetOIDC(ctx context.Context, cfg OIDCConfig) error {
	if cfg.IssuerURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return fmt.Errorf("%w: issuer url, client id, and client secret are required", ErrValidation)
	}
	if err := validateIssuerURL(cfg.IssuerURL); err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	sealed, err := s.box.Seal(raw)
	if err != nil {
		return err
	}
	value, err := json.Marshal(map[string]string{"enc": base64.StdEncoding.EncodeToString(sealed)})
	if err != nil {
		return err
	}
	_, err = s.q.UpsertInstanceSetting(ctx, sqlc.UpsertInstanceSettingParams{Key: keyOIDC, Value: value})
	return err
}

func (s *Service) ClearOIDC(ctx context.Context) error {
	value, err := json.Marshal(map[string]string{"enc": ""})
	if err != nil {
		return err
	}
	_, err = s.q.UpsertInstanceSetting(ctx, sqlc.UpsertInstanceSettingParams{Key: keyOIDC, Value: value})
	return err
}

// validateIssuerURL requires an https URL, except http is allowed for
// loopback hosts so a dev Keycloak works.
func validateIssuerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: issuer url must be an https URL: %v", ErrValidation, err)
	}
	if u.Scheme == "https" && u.Host != "" {
		return nil
	}
	if u.Scheme == "http" {
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return nil
		}
	}
	return fmt.Errorf("%w: issuer url must be an https URL (http allowed for loopback hosts only)", ErrValidation)
}
