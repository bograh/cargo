package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func orgJSON(o sqlc.Organization) map[string]any {
	return map[string]any{
		"id":         o.ID,
		"name":       o.Name,
		"slug":       o.Slug,
		"created_at": o.CreatedAt,
	}
}

// orgIDParam parses the {orgID} URL param; writes 404 and returns false on bad UUIDs.
func orgIDParam(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if err := id.Scan(chi.URLParam(r, "orgID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "organization not found")
		return id, false
	}
	return id, true
}

// orgError maps service errors onto the API envelope.
func orgError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orgs.ErrNotFound):
		Error(w, http.StatusNotFound, "not_found", "organization not found")
	case errors.Is(err, orgs.ErrForbidden):
		Error(w, http.StatusForbidden, "forbidden", "insufficient role")
	case errors.Is(err, orgs.ErrLastOwner):
		Error(w, http.StatusConflict, "last_owner", "organization must keep at least one owner")
	case errors.Is(err, orgs.ErrBadRole):
		Error(w, http.StatusBadRequest, "invalid_role", "role must be owner, admin, member, or viewer")
	default:
		Error(w, http.StatusInternalServerError, "internal", "operation failed")
	}
}

func (s *Server) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "name is required")
		return
	}
	org, err := s.orgs.Create(r.Context(), strings.TrimSpace(body.Name), userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, orgJSON(org))
}

func (s *Server) handleListOrgs(w http.ResponseWriter, r *http.Request) {
	list, err := s.orgs.ListForUser(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetOrg(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	org, role, err := s.orgs.Get(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"organization": orgJSON(org), "role": role})
}

func (s *Server) handleDeleteOrg(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	if err := s.orgs.Delete(r.Context(), id, userFrom(r.Context()).ID); err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	list, err := s.orgs.ListMembers(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleUpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	var target pgtype.UUID
	if err := target.Scan(chi.URLParam(r, "userID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "member not found")
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	m, err := s.orgs.UpdateRole(r.Context(), id, userFrom(r.Context()).ID, target, body.Role)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	var target pgtype.UUID
	if err := target.Scan(chi.URLParam(r, "userID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "member not found")
		return
	}
	if err := s.orgs.RemoveMember(r.Context(), id, userFrom(r.Context()).ID, target); err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}
