package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/bograh/cargo/internal/databases"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func instanceJSON(inst sqlc.DatabaseInstance) map[string]any {
	return map[string]any{
		"id":         inst.ID,
		"org_id":     inst.OrgID,
		"name":       inst.Name,
		"engine":     inst.Engine,
		"version":    inst.Version,
		"redis_mode": inst.RedisMode,
		"host_port":  inst.HostPort,
		"status":     inst.Status,
		"created_at": inst.CreatedAt,
	}
}

func attachmentJSON(att sqlc.DatabaseAttachment) map[string]any {
	m := map[string]any{
		"id":         att.ID,
		"app_id":     att.AppID,
		"created_at": att.CreatedAt,
	}
	if att.DbName.Valid {
		m["db_name"] = att.DbName.String
	}
	if att.DbIndex.Valid {
		m["db_index"] = att.DbIndex.Int32
	}
	return m
}

func databasesError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, databases.ErrValidation):
		Error(w, http.StatusBadRequest, "validation_failed", err.Error())
	case errors.Is(err, databases.ErrNotFound):
		Error(w, http.StatusNotFound, "not_found", "database not found")
	case errors.Is(err, databases.ErrForbidden):
		Error(w, http.StatusForbidden, "forbidden", "insufficient role")
	case errors.Is(err, databases.ErrConflict):
		Error(w, http.StatusConflict, "conflict", err.Error())
	default:
		Error(w, http.StatusInternalServerError, "internal", "operation failed")
	}
}

// dbIDParam parses the {dbID} URL param; writes 404 and returns false on bad UUIDs.
func dbIDParam(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if err := id.Scan(chi.URLParam(r, "dbID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "database not found")
		return id, false
	}
	return id, true
}

func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	var body struct {
		Name       string `json:"name"`
		Engine     string `json:"engine"`
		Version    string `json:"version"`
		RedisMode  string `json:"redis_mode"`
		ExposePort bool   `json:"expose_port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	inst, err := s.databases.Create(r.Context(), orgID, userFrom(r.Context()).ID, databases.CreateInput{
		Name:       body.Name,
		Engine:     body.Engine,
		Version:    body.Version,
		RedisMode:  body.RedisMode,
		ExposePort: body.ExposePort,
	})
	if err != nil {
		databasesError(w, err)
		return
	}
	idVal, _ := inst.ID.Value()
	idStr, _ := idVal.(string)
	if err := s.enqueue.EnqueueDBProvision(r.Context(), idStr); err != nil {
		_ = s.databases.MarkError(r.Context(), inst.ID)
		Error(w, http.StatusInternalServerError, "internal", "failed to enqueue provisioning")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": inst.ID, "status": inst.Status})
}

func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	list, err := s.databases.List(r.Context(), orgID, userFrom(r.Context()).ID)
	if err != nil {
		databasesError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, row := range list {
		m := instanceJSON(row.Instance)
		m["attachment_count"] = row.AttachmentCount
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := dbIDParam(w, r)
	if !ok {
		return
	}
	detail, err := s.databases.Get(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		databasesError(w, err)
		return
	}
	m := instanceJSON(detail.Instance)
	m["size_bytes"] = detail.SizeBytes
	atts := make([]map[string]any, 0, len(detail.Attachments))
	for _, a := range detail.Attachments {
		atts = append(atts, attachmentJSON(a))
	}
	m["attachments"] = atts
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := dbIDParam(w, r)
	if !ok {
		return
	}
	if err := s.databases.Delete(r.Context(), id, userFrom(r.Context()).ID); err != nil {
		databasesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleDatabaseLogs replays the persisted provision log file. Managed
// databases have no live event bus (unlike deployments), so this is a plain
// text dump gated by the same role check as the detail endpoint.
func (s *Server) handleDatabaseLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := dbIDParam(w, r)
	if !ok {
		return
	}
	idStr := chi.URLParam(r, "dbID")
	if _, err := s.databases.Get(r.Context(), id, userFrom(r.Context()).ID); err != nil {
		databasesError(w, err)
		return
	}
	f, err := os.Open(s.databases.LogPath(idStr))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"log": ""})
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

func (s *Server) handleAttachDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := dbIDParam(w, r)
	if !ok {
		return
	}
	var body struct {
		AppID string `json:"app_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AppID == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "app_id is required")
		return
	}
	var appID pgtype.UUID
	if err := appID.Scan(body.AppID); err != nil {
		Error(w, http.StatusBadRequest, "validation_failed", "app_id must be a UUID")
		return
	}
	url, att, err := s.databases.Attach(r.Context(), id, appID, userFrom(r.Context()).ID)
	if err != nil {
		databasesError(w, err)
		return
	}
	envKey := "DATABASE_URL"
	if !att.DbName.Valid {
		envKey = "REDIS_URL"
	}
	writeJSON(w, http.StatusCreated, map[string]string{"url": url, "env_key": envKey})
}

func (s *Server) handleDetachDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := dbIDParam(w, r)
	if !ok {
		return
	}
	var appID pgtype.UUID
	if err := appID.Scan(chi.URLParam(r, "appID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "application not found")
		return
	}
	if err := s.databases.Detach(r.Context(), id, appID, userFrom(r.Context()).ID); err != nil {
		databasesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "detached"})
}

func (s *Server) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := dbIDParam(w, r)
	if !ok {
		return
	}
	name, err := s.databases.Snapshot(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		databasesError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": name})
}

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	id, ok := dbIDParam(w, r)
	if !ok {
		return
	}
	list, err := s.databases.ListSnapshots(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		databasesError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, sn := range list {
		out = append(out, map[string]any{"name": sn.Name, "size": sn.Size, "created_at": sn.Created})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := dbIDParam(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	path, err := s.databases.SnapshotPath(r.Context(), id, userFrom(r.Context()).ID, name)
	if err != nil {
		databasesError(w, err)
		return
	}
	http.ServeFile(w, r, path)
}

func (s *Server) handleDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := dbIDParam(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if err := s.databases.DeleteSnapshot(r.Context(), id, userFrom(r.Context()).ID, name); err != nil {
		databasesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
