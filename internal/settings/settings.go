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
	keySuffix       = "apps_domain_suffix"
	keySMTP         = "smtp"
	keyRegistration = "registration_mode"
	// DefaultSuffix is used when the admin has not set one.
	DefaultSuffix = "apps.localhost"
)

// Registration modes decide who may create an account on the instance.
//
// A Cargo instance hands whoever holds an account the ability to run
// containers on the host, so an unset mode has to mean "not everyone". The
// default is RegistrationInvite rather than RegistrationOpen for that reason —
// including on upgrade, where no row exists yet.
const (
	// RegistrationOpen lets anyone who can reach the instance sign up.
	RegistrationOpen = "open"
	// RegistrationInvite requires a valid organisation invite token.
	RegistrationInvite = "invite"
	// RegistrationClosed refuses every registration.
	RegistrationClosed = "closed"
	// DefaultRegistration applies when no mode has been set.
	DefaultRegistration = RegistrationInvite
)

// ValidRegistrationMode reports whether mode is one Cargo understands.
func ValidRegistrationMode(mode string) bool {
	switch mode {
	case RegistrationOpen, RegistrationInvite, RegistrationClosed:
		return true
	}
	return false
}

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

// Registration returns the instance's registration mode, defaulting closed
// enough to be safe when the setting is missing or unreadable: an instance
// that cannot tell you its policy is not one to open the door on.
func (s *Service) Registration(ctx context.Context) (string, error) {
	row, err := s.q.GetInstanceSetting(ctx, keyRegistration)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultRegistration, nil
	}
	if err != nil {
		return DefaultRegistration, err
	}
	var v string
	if err := json.Unmarshal(row.Value, &v); err != nil || !ValidRegistrationMode(v) {
		return DefaultRegistration, nil
	}
	return v, nil
}

// SetRegistration stores the registration mode.
func (s *Service) SetRegistration(ctx context.Context, mode string) error {
	if !ValidRegistrationMode(mode) {
		return fmt.Errorf("%w: registration must be open, invite, or closed", ErrValidation)
	}
	value, err := json.Marshal(mode)
	if err != nil {
		return err
	}
	_, err = s.q.UpsertInstanceSetting(ctx, sqlc.UpsertInstanceSettingParams{Key: keyRegistration, Value: value})
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
