package settings

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// SeedSuffix stores suffix as the apps-domain suffix only when no admin has
// set one, so the installer's CARGO_APPS_SUFFIX answer takes effect on first
// boot without ever overriding a later change made in the UI. An empty suffix
// is a no-op. It reports whether the setting was written.
func (s *Service) SeedSuffix(ctx context.Context, suffix string) (bool, error) {
	if suffix == "" {
		return false, nil
	}
	_, err := s.q.GetInstanceSetting(ctx, keySuffix)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if err := s.SetSuffix(ctx, suffix); err != nil {
		return false, err
	}
	return true, nil
}
