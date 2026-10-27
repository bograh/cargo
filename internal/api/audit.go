package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgtype"
)

// auditMiddleware records every successful state-changing request against the
// authenticated actor. It is uniform (all mutations, no per-handler wiring) at
// the cost of coarse granularity: action = HTTP method, target = path. Reads
// (GET/HEAD) and non-2xx responses are not recorded. Best-effort.
func (s *Server) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.audit == nil || !isMutating(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		targetType, targetID := auditTarget(r.URL.Path)
		// Resolve the owning org before the handler runs: a DELETE removes the
		// very row the lookup needs.
		resolvedOrg := s.resourceOrg(r, targetType, targetID)
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if ww.Status() < 200 || ww.Status() >= 300 {
			return
		}
		actor := userFrom(r.Context())
		detail := map[string]any{"path": r.URL.Path, "status": ww.Status()}
		orgID := orgFromRequest(r)
		if !orgID.Valid {
			orgID = resolvedOrg
		}
		// Deleting an org: audit_log.org_id references organizations, so the
		// gone org can't be the row's org. Record it instance-level with the
		// ID in detail rather than losing the entry to a foreign-key error.
		if r.Method == http.MethodDelete && targetType == "orgs" && isResourceRoot(r.URL.Path) {
			detail["org_id"] = targetID
			orgID = pgtype.UUID{}
		}
		s.audit.Record(r.Context(), actor.ID, orgID, r.Method, targetType, targetID, detail)
	})
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// auditTarget derives a coarse (type, id) from the API path, e.g.
// /api/v1/apps/<id>/deploy → ("apps", "<id>").
func auditTarget(path string) (string, string) {
	p := strings.TrimPrefix(path, "/api/v1/")
	parts := strings.Split(p, "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", ""
	}
	id := ""
	if len(parts) > 1 {
		id = parts[1]
	}
	return parts[0], id
}

// resourceOrg finds the org behind /apps/{id}, /deployments/{id} and
// /databases/{id} paths, which carry no {orgID} route param.
func (s *Server) resourceOrg(r *http.Request, targetType, targetID string) pgtype.UUID {
	switch targetType {
	case "apps", "deployments", "databases":
	default:
		return pgtype.UUID{}
	}
	var id pgtype.UUID
	if id.Scan(targetID) != nil {
		return pgtype.UUID{}
	}
	return s.audit.ResolveOrg(r.Context(), targetType, id)
}

// isResourceRoot reports whether path addresses a resource itself
// (/api/v1/<type>/<id>) rather than a sub-resource of it.
func isResourceRoot(path string) bool {
	p := strings.Trim(strings.TrimPrefix(path, "/api/v1/"), "/")
	return len(strings.Split(p, "/")) == 2
}

// orgFromRequest extracts the {orgID} route param when present, so org-scoped
// mutations are attributable to their org in the org-admin view.
func orgFromRequest(r *http.Request) pgtype.UUID {
	var id pgtype.UUID
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if v := rc.URLParam("orgID"); v != "" {
			_ = id.Scan(v)
		}
	}
	return id
}

func auditLimit(r *http.Request) int32 {
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			return int32(n)
		}
	}
	return 100
}

// handleAdminAudit lists instance-wide audit entries (instance admin only).
func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	if s.audit == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	rows, err := s.audit.ListAll(r.Context(), auditLimit(r))
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "could not list audit log")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, e := range rows {
		out = append(out, map[string]any{
			"id": e.ID, "actor_email": e.ActorEmail, "org_id": e.OrgID,
			"action": e.Action, "target_type": e.TargetType, "target_id": e.TargetID,
			"detail": e.Detail, "created_at": e.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleOrgAudit lists audit entries for one org (org admin/owner only).
func (s *Server) handleOrgAudit(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	_, role, err := s.orgs.Get(r.Context(), orgID, userFrom(r.Context()).ID)
	if err != nil {
		Error(w, http.StatusNotFound, "not_found", "organization not found")
		return
	}
	if role != "admin" && role != "owner" {
		Error(w, http.StatusForbidden, "forbidden", "org admin required")
		return
	}
	if s.audit == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	rows, err := s.audit.ListByOrg(r.Context(), orgID, auditLimit(r))
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "could not list audit log")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, e := range rows {
		out = append(out, map[string]any{
			"id": e.ID, "actor_email": e.ActorEmail,
			"action": e.Action, "target_type": e.TargetType, "target_id": e.TargetID,
			"detail": e.Detail, "created_at": e.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
