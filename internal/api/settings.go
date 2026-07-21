package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/bograh/cargo/internal/mailer"
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

// handleTestSMTP sends a test email to the requesting admin and returns the
// concrete SMTP error on failure, so delivery problems are diagnosable from
// the UI without digging through server logs.
func (s *Server) handleTestSMTP(w http.ResponseWriter, r *http.Request) {
	cfg := s.smtpConfig(r.Context())
	if cfg == nil {
		Error(w, http.StatusBadRequest, "smtp_not_configured", "save SMTP settings before sending a test")
		return
	}
	to := userFrom(r.Context()).Email
	err := mailer.Send(*cfg, []string{to},
		"Cargo SMTP test",
		"<p>This is a test email from Cargo. If you can read this, SMTP delivery is working.</p>",
		"This is a test email from Cargo. If you can read this, SMTP delivery is working.\n")
	if err != nil {
		slog.Warn("smtp test failed", "err", err)
		Error(w, http.StatusBadGateway, "smtp_test_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent", "to": to})
}
