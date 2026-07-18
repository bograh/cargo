package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bograh/cargo/internal/settings"
)

func settingsError(w http.ResponseWriter, err error) {
	if errors.Is(err, settings.ErrValidation) {
		Error(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	Error(w, http.StatusInternalServerError, "internal", "settings operation failed")
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	suffix, err := s.instanceSettings.Suffix(r.Context())
	if err != nil {
		settingsError(w, err)
		return
	}
	smtpOut := map[string]any{"configured": false}
	if smtp, err := s.instanceSettings.SMTP(r.Context()); err == nil && smtp != nil {
		smtpOut = map[string]any{
			"configured": true,
			"host":       smtp.Host,
			"port":       smtp.Port,
			"username":   smtp.Username,
			"from":       smtp.From,
			// password intentionally omitted (write-only)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"apps_domain_suffix": suffix,
		"smtp":               smtpOut,
	})
}

func (s *Server) handlePutSuffix(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Suffix string `json:"suffix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Suffix == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "suffix is required")
		return
	}
	if err := s.instanceSettings.SetSuffix(r.Context(), body.Suffix); err != nil {
		settingsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handlePutSMTP(w http.ResponseWriter, r *http.Request) {
	var cfg settings.SMTPConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if err := s.instanceSettings.SetSMTP(r.Context(), cfg); err != nil {
		settingsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleDeleteSMTP(w http.ResponseWriter, r *http.Request) {
	if err := s.instanceSettings.ClearSMTP(r.Context()); err != nil {
		settingsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}
