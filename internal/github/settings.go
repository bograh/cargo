// Package github integrates Cargo with GitHub Apps: encrypted credential
// storage, installation tokens, repo/branch listing, and webhook helpers.
package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

const settingsKey = "github_app"

type AppConfig struct {
	AppID         int64  `json:"app_id"`
	AppSlug       string `json:"app_slug"`
	PrivateKey    string `json:"private_key"`
	WebhookSecret string `json:"webhook_secret"`
}

// SaveAppConfig encrypts the whole config and stores it as {"enc": base64}.
func SaveAppConfig(ctx context.Context, q *sqlc.Queries, box *crypto.Box, cfg AppConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	sealed, err := box.Seal(raw)
	if err != nil {
		return err
	}
	value, err := json.Marshal(map[string]string{"enc": base64.StdEncoding.EncodeToString(sealed)})
	if err != nil {
		return err
	}
	_, err = q.UpsertInstanceSetting(ctx, sqlc.UpsertInstanceSettingParams{Key: settingsKey, Value: value})
	return err
}

// LoadAppConfig returns nil when GitHub is not configured.
func LoadAppConfig(ctx context.Context, q *sqlc.Queries, box *crypto.Box) (*AppConfig, error) {
	row, err := q.GetInstanceSetting(ctx, settingsKey)
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
		return nil, fmt.Errorf("github settings: %w", err)
	}
	sealed, err := base64.StdEncoding.DecodeString(wrapper.Enc)
	if err != nil {
		return nil, fmt.Errorf("github settings: %w", err)
	}
	raw, err := box.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("github settings decrypt: %w", err)
	}
	var cfg AppConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
