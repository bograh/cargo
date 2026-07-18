// Package settings manages instance-level configuration stored in
// instance_settings: the apps-domain suffix (plain) and SMTP credentials
// (encrypted at rest).
package settings

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	keySuffix = "apps_domain_suffix"
	keySMTP   = "smtp"
	// DefaultSuffix is used when the admin has not set one.
	DefaultSuffix = "apps.localhost"
)

var ErrValidation = errors.New("validation failed")

var suffixRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
}

type Service struct {
	q   *sqlc.Queries
	box *crypto.Box
}

func NewService(pool *pgxpool.Pool, box *crypto.Box) *Service {
	return &Service{q: sqlc.New(pool), box: box}
}

func (s *Service) Suffix(ctx context.Context) (string, error) {
	row, err := s.q.GetInstanceSetting(ctx, keySuffix)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultSuffix, nil
	}
	if err != nil {
		return DefaultSuffix, err
	}
	var v string
	if err := json.Unmarshal(row.Value, &v); err != nil || v == "" {
		return DefaultSuffix, nil
	}
	return v, nil
}

func (s *Service) SetSuffix(ctx context.Context, suffix string) error {
	if len(suffix) > 253 || !suffixRe.MatchString(suffix) {
		return fmt.Errorf("%w: suffix must be a bare domain name like apps.example.com", ErrValidation)
	}
	value, err := json.Marshal(suffix)
	if err != nil {
		return err
	}
	_, err = s.q.UpsertInstanceSetting(ctx, sqlc.UpsertInstanceSettingParams{Key: keySuffix, Value: value})
	return err
}

// SMTP returns nil when unset.
func (s *Service) SMTP(ctx context.Context) (*SMTPConfig, error) {
	row, err := s.q.GetInstanceSetting(ctx, keySMTP)
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
		return nil, fmt.Errorf("smtp settings decrypt: %w", err)
	}
	var cfg SMTPConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (s *Service) SetSMTP(ctx context.Context, cfg SMTPConfig) error {
	if cfg.Host == "" || cfg.Port < 1 || cfg.Port > 65535 || cfg.From == "" {
		return fmt.Errorf("%w: host, port (1-65535), and from are required", ErrValidation)
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
	_, err = s.q.UpsertInstanceSetting(ctx, sqlc.UpsertInstanceSettingParams{Key: keySMTP, Value: value})
	return err
}

func (s *Service) ClearSMTP(ctx context.Context) error {
	value, err := json.Marshal(map[string]string{"enc": ""})
	if err != nil {
		return err
	}
	_, err = s.q.UpsertInstanceSetting(ctx, sqlc.UpsertInstanceSettingParams{Key: keySMTP, Value: value})
	return err
}
