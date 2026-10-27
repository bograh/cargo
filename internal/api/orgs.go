package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// isFKViolation reports a foreign-key violation (the RESTRICT on
// database_instances, if one is created between the check and the delete).
func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
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
	case errors.Is(err, errOrgHasDatabases), isFKViolation(err):
		Error(w, http.StatusConflict, "has_databases",
			"delete the organization's databases before deleting the organization")
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
	ctx := r.Context()
	actor := userFrom(ctx).ID
	// Deleting the row cascades the org's applications away, but nothing
	// stopped their containers: they kept serving their domains with no
	// record pointing at them. Check, collect and tear down here, with the
	// owner check first so a non-owner cannot stop anything.
	_, role, err := s.orgs.Get(ctx, id, actor)
	if err != nil {
		orgError(w, err)
		return
	}
	if role != "owner" {
		orgError(w, orgs.ErrForbidden)
		return
	}
	if s.pool != nil {
		var dbs int
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM database_instances WHERE org_id = $1`, id).Scan(&dbs); err != nil {
			orgError(w, err)
			return
		}
		if dbs > 0 {
			orgError(w, errOrgHasDatabases)
			return
		}
	}
	type teardown struct {
		app      sqlc.Application
		provider reconciler.DeployProvider
	}
	var teardowns []teardown
	if s.apps != nil && s.provider != nil {
		list, err := s.apps.List(ctx, id, actor)
		if err != nil {
			orgError(w, err)
			return
		}
		for _, a := range list {
			p, err := s.providerFor(ctx, a)
			if err != nil {
				Error(w, http.StatusBadGateway, "host_unavailable",
					"cannot reach the worker host of app "+a.Name+" to remove its containers: "+err.Error())
				return
			}
			teardowns = append(teardowns, teardown{a, p})
		}
	}
	if err := s.orgs.Delete(ctx, id, actor); err != nil {
		orgError(w, err)
		return
	}
	bg := context.WithoutCancel(ctx)
	for _, t := range teardowns {
		if err := t.provider.Teardown(bg, uuidString(t.app.ID), t.app.Slug, io.Discard); err != nil {
			slog.Warn("org delete: app teardown failed", "app", t.app.Slug, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// errOrgHasDatabases: database_instances.org_id is ON DELETE RESTRICT, so an
// org with managed databases cannot be deleted until they are.
var errOrgHasDatabases = errors.New("organization still has managed databases")

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
