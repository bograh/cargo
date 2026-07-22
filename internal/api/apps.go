package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func appJSON(a sqlc.Application) map[string]any {
	return map[string]any{
		"id":                       a.ID,
		"org_id":                   a.OrgID,
		"name":                     a.Name,
		"slug":                     a.Slug,
		"source_type":              a.SourceType,
		"builder":                  a.Builder,
		"git_repo_url":             a.GitRepoUrl,
		"git_branch":               a.GitBranch,
		"image_ref":                a.ImageRef,
		"exposed_port":             a.ExposedPort,
		"healthcheck_path":         a.HealthcheckPath,
		"auto_deploy":              a.AutoDeploy,
		"build_context":            a.BuildContext,
		"dockerfile_path":          a.DockerfilePath,
		"build_args":               json.RawMessage(a.BuildArgs),
		"has_registry_credentials": len(a.RegistryCredsEnc) > 0,
		"desired_state":            a.DesiredState,
		"created_at":               a.CreatedAt,
		"updated_at":               a.UpdatedAt,
	}
}

func appError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, apps.ErrNotFound):
		Error(w, http.StatusNotFound, "not_found", "application not found")
	case errors.Is(err, apps.ErrForbidden):
		Error(w, http.StatusForbidden, "forbidden", "insufficient role")
	case errors.Is(err, apps.ErrValidation):
		Error(w, http.StatusBadRequest, "validation_failed", err.Error())
	default:
		Error(w, http.StatusInternalServerError, "internal", "operation failed")
	}
}

func appIDParam(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if err := id.Scan(chi.URLParam(r, "appID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "application not found")
		return id, false
	}
	return id, true
}

type appBody struct {
	Name            *string             `json:"name"`
	SourceType      string              `json:"source_type"`
	Builder         *string             `json:"builder"`
	GitRepoURL      string              `json:"git_repo_url"`
	GitBranch       *string             `json:"git_branch"`
	ImageRef        *string             `json:"image_ref"`
	ExposedPort     *int32              `json:"exposed_port"`
	HealthcheckPath *string             `json:"healthcheck_path"`
	AutoDeploy      *bool               `json:"auto_deploy"`
	BuildContext    *string             `json:"build_context"`
	DockerfilePath  *string             `json:"dockerfile_path"`
	BuildArgs       map[string]string   `json:"build_args"`
	RegistryCreds   *apps.RegistryCreds `json:"registry_credentials"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	var body appBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := apps.CreateInput{
		Name:            str(body.Name),
		SourceType:      body.SourceType,
		Builder:         str(body.Builder),
		GitRepoURL:      body.GitRepoURL,
		GitBranch:       str(body.GitBranch),
		ImageRef:        str(body.ImageRef),
		HealthcheckPath: str(body.HealthcheckPath),
		BuildContext:    str(body.BuildContext),
		DockerfilePath:  str(body.DockerfilePath),
		BuildArgs:       body.BuildArgs,
		RegistryCreds:   body.RegistryCreds,
		AutoDeploy:      true,
	}
	if body.ExposedPort != nil {
		in.ExposedPort = *body.ExposedPort
	}
	if body.AutoDeploy != nil {
		in.AutoDeploy = *body.AutoDeploy
	}
	app, err := s.apps.Create(r.Context(), orgID, userFrom(r.Context()).ID, in)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, appJSON(app))
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	list, err := s.apps.List(r.Context(), orgID, userFrom(r.Context()).ID)
	if err != nil {
		appError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		out = append(out, appJSON(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	app, err := s.apps.Get(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, appJSON(app))
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	var body appBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := apps.UpdateInput{
		Name: body.Name, Builder: body.Builder, GitBranch: body.GitBranch,
		ImageRef: body.ImageRef, HealthcheckPath: body.HealthcheckPath,
		BuildContext: body.BuildContext, DockerfilePath: body.DockerfilePath,
		ExposedPort: body.ExposedPort, AutoDeploy: body.AutoDeploy,
		BuildArgs: body.BuildArgs, RegistryCreds: body.RegistryCreds,
	}
	app, err := s.apps.Update(r.Context(), id, userFrom(r.Context()).ID, in)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, appJSON(app))
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	// Snapshot identity before the row disappears — teardown needs it.
	app, err := s.apps.Get(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		appError(w, err)
		return
	}
	if err := s.apps.Delete(r.Context(), id, userFrom(r.Context()).ID); err != nil {
		appError(w, err)
		return
	}
	if s.provider != nil {
		appID, _ := app.ID.Value()
		idStr, _ := appID.(string)
		if err := s.provider.Teardown(r.Context(), idStr, app.Slug, io.Discard); err != nil {
			slog.Warn("app teardown failed", "app", app.Slug, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func uuidString(id pgtype.UUID) string {
	v, _ := id.Value()
	s, _ := v.(string)
	return s
}

func (s *Server) handleStopApp(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	s.setAppRunState(w, r, id, "stopped")
}

func (s *Server) handleStartApp(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	s.setAppRunState(w, r, id, "running")
}

// setAppRunState records the desired run state (role-gated), performs the
// matching provider action, and rolls the recorded intent back if that action
// fails so the stored state matches reality.
func (s *Server) setAppRunState(w http.ResponseWriter, r *http.Request, id pgtype.UUID, state string) {
	ctx := r.Context()
	app, err := s.apps.SetDesiredState(ctx, id, userFrom(ctx).ID, state)
	if err != nil {
		appError(w, err)
		return
	}
	if s.provider == nil {
		writeJSON(w, http.StatusOK, appJSON(app))
		return
	}
	var perr error
	if state == "stopped" {
		perr = s.provider.Stop(ctx, uuidString(id), io.Discard)
	} else {
		perr = s.provider.Start(ctx, uuidString(id), io.Discard)
	}
	if perr != nil {
		prev := "running"
		if state == "running" {
			prev = "stopped"
		}
		_ = s.apps.SetDesiredStateRaw(ctx, id, prev)
		slog.Warn("app run-state change failed", "app", app.Slug, "state", state, "err", perr)
		Error(w, http.StatusInternalServerError, "internal", perr.Error())
		return
	}
	writeJSON(w, http.StatusOK, appJSON(app))
}

func (s *Server) handleListEnvKeys(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	keys, err := s.apps.ListEnvKeys(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		appError(w, err)
		return
	}
	if keys == nil {
		keys = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Server) handleSetEnvVars(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	var body struct {
		Vars map[string]string `json:"vars"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Vars) == 0 {
		Error(w, http.StatusBadRequest, "validation_failed", "vars object is required")
		return
	}
	if err := s.apps.SetEnvVars(r.Context(), id, userFrom(r.Context()).ID, body.Vars); err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleDeleteEnvVar(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	key := chi.URLParam(r, "key")
	if err := s.apps.DeleteEnvVar(r.Context(), id, userFrom(r.Context()).ID, key); err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
