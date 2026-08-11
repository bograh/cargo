package api

import (
	"encoding/json"
	"net/http"
)

// handleGetNotifyWebhook reports whether an outbound webhook is configured. The
// URL itself is write-only and never returned.
func (s *Server) handleGetNotifyWebhook(w http.ResponseWriter, r *http.Request) {
	if s.notify == nil {
		Error(w, http.StatusServiceUnavailable, "unavailable", "notifications are not available")
		return
	}
	configured, needsReentry := s.notify.WebhookStatus(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": configured, "needs_reentry": needsReentry,
	})
}

// handlePutNotifyWebhook stores the outbound webhook URL (encrypted at rest).
func (s *Server) handlePutNotifyWebhook(w http.ResponseWriter, r *http.Request) {
	if s.notify == nil {
		Error(w, http.StatusServiceUnavailable, "unavailable", "notifications are not available")
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "a webhook url is required")
		return
	}
	if err := s.notify.SetWebhook(r.Context(), body.URL); err != nil {
		Error(w, http.StatusInternalServerError, "internal", "could not save webhook")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true})
}

// handleDeleteNotifyWebhook clears the outbound webhook.
func (s *Server) handleDeleteNotifyWebhook(w http.ResponseWriter, r *http.Request) {
	if s.notify == nil {
		Error(w, http.StatusServiceUnavailable, "unavailable", "notifications are not available")
		return
	}
	if err := s.notify.SetWebhook(r.Context(), ""); err != nil {
		Error(w, http.StatusInternalServerError, "internal", "could not clear webhook")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": false})
}
