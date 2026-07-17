package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/bograh/cargo/internal/orgs"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const defaultInviteTTL = 7 * 24 * time.Hour

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	token, inv, err := s.orgs.CreateInvite(r.Context(), id, userFrom(r.Context()).ID, body.Role, defaultInviteTTL)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"invite": inv,
		"token":  token, // shown once; only the hash is stored
	})
}

func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	list, err := s.orgs.ListInvites(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	var inviteID pgtype.UUID
	if err := inviteID.Scan(chi.URLParam(r, "inviteID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "invite not found")
		return
	}
	if err := s.orgs.RevokeInvite(r.Context(), id, userFrom(r.Context()).ID, inviteID); err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "token is required")
		return
	}
	org, err := s.orgs.AcceptInvite(r.Context(), body.Token, userFrom(r.Context()).ID)
	if errors.Is(err, orgs.ErrInviteInvalid) {
		Error(w, http.StatusBadRequest, "invite_invalid", "invite is invalid, revoked, or expired")
		return
	}
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orgJSON(org))
}
