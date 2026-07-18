package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func domainJSON(d sqlc.Domain) map[string]any {
	return map[string]any{
		"id":              d.ID,
		"app_id":          d.AppID,
		"hostname":        d.Hostname,
		"status":          d.Status,
		"last_checked_at": d.LastCheckedAt,
		"created_at":      d.CreatedAt,
	}
}

func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	list, err := s.apps.ListDomains(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		appError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, d := range list {
		out = append(out, domainJSON(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	var body struct {
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Hostname == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "hostname is required")
		return
	}
	suffix := s.settingOrDefault(r, "apps_domain_suffix", "apps.localhost")
	d, err := s.apps.AddDomain(r.Context(), id, userFrom(r.Context()).ID, body.Hostname, suffix)
	if errors.Is(err, apps.ErrDomainTaken) {
		Error(w, http.StatusConflict, "domain_taken", "domain is already attached to an app")
		return
	}
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, domainJSON(d))
}

func (s *Server) handleRemoveDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	var domainID pgtype.UUID
	if err := domainID.Scan(chi.URLParam(r, "domainID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "domain not found")
		return
	}
	if err := s.apps.RemoveDomain(r.Context(), id, userFrom(r.Context()).ID, domainID); err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}
