package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/bograh/cargo/internal/settings"
	"github.com/jackc/pgx/v5"
)

const version = "1.5.0"

func (s *Server) getInstanceInfo(w http.ResponseWriter, r *http.Request) {
	suffix := s.settingOrDefault(r, "apps_domain_suffix", "apps.localhost")
	// The sign-up form is hidden unless the instance is open, so the mode has
	// to be readable before anyone has an account. It reveals policy, not data.
	registration := s.settingOrDefault(r, "registration_mode", settings.DefaultRegistration)
	if !settings.ValidRegistrationMode(registration) {
		registration = settings.DefaultRegistration
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"version":            version,
		"apps_domain_suffix": suffix,
		"registration_mode":  registration,
	})
}

// settingOrDefault reads a JSON-string setting, falling back on missing rows.
func (s *Server) settingOrDefault(r *http.Request, key, def string) string {
	row, err := s.settings.GetInstanceSetting(r.Context(), key)
	if errors.Is(err, pgx.ErrNoRows) {
		return def
	}
	if err != nil {
		slog.Warn("instance setting read failed", "key", key, "err", err)
		return def
	}
	var v string
	if err := json.Unmarshal(row.Value, &v); err != nil {
		slog.Warn("instance setting decode failed", "key", key, "err", err)
		return def
	}
	return v
}
