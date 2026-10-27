package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/deployments"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func deploymentJSON(d sqlc.Deployment) map[string]any {
	return map[string]any{
		"id":          d.ID,
		"app_id":      d.AppID,
		"trigger":     d.Trigger,
		"status":      d.Status,
		"commit_sha":  d.CommitSha,
		"image_tag":   d.ImageTag,
		"error":       d.Error,
		"created_at":  d.CreatedAt,
		"started_at":  d.StartedAt,
		"finished_at": d.FinishedAt,
	}
}

func deploymentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deployments.ErrNotFound):
		Error(w, http.StatusNotFound, "not_found", "deployment not found")
	case errors.Is(err, deployments.ErrForbidden):
		Error(w, http.StatusForbidden, "forbidden", "insufficient role")
	case errors.Is(err, deployments.ErrBadRollbackTarget):
		Error(w, http.StatusBadRequest, "bad_rollback_target", "rollback target must be a previous live deployment")
	case errors.Is(err, deployments.ErrBadTransition):
		Error(w, http.StatusConflict, "bad_transition", "illegal deployment state change")
	default:
		Error(w, http.StatusInternalServerError, "internal", "operation failed")
	}
}

func deploymentIDParam(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if err := id.Scan(chi.URLParam(r, "deploymentID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "deployment not found")
		return id, false
	}
	return id, true
}

// enqueueOrFail inserts the deploy job; on enqueue failure the deployment is
// marked failed so it never sits queued forever.
func (s *Server) enqueueOrFail(w http.ResponseWriter, r *http.Request, dep sqlc.Deployment) {
	id, _ := dep.ID.Value()
	idStr, _ := id.(string)
	if err := s.enqueue.EnqueueDeploy(r.Context(), idStr); err != nil {
		_ = s.deps.Finish(r.Context(), dep.ID, "failed", "enqueue failed: "+err.Error())
		Error(w, http.StatusInternalServerError, "internal", "failed to enqueue deployment")
		return
	}
	writeJSON(w, http.StatusAccepted, deploymentJSON(dep))
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	appID, ok := appIDParam(w, r)
	if !ok {
		return
	}
	dep, err := s.deps.Create(r.Context(), appID, userFrom(r.Context()).ID, "manual")
	if err != nil {
		deploymentError(w, err)
		return
	}
	s.enqueueOrFail(w, r, dep)
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	appID, ok := appIDParam(w, r)
	if !ok {
		return
	}
	var body struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DeploymentID == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "deployment_id is required")
		return
	}
	var target pgtype.UUID
	if err := target.Scan(body.DeploymentID); err != nil {
		Error(w, http.StatusBadRequest, "validation_failed", "deployment_id must be a UUID")
		return
	}
	dep, err := s.deps.Rollback(r.Context(), appID, userFrom(r.Context()).ID, target)
	if err != nil {
		deploymentError(w, err)
		return
	}
	s.enqueueOrFail(w, r, dep)
}

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	appID, ok := appIDParam(w, r)
	if !ok {
		return
	}
	list, err := s.deps.List(r.Context(), appID, userFrom(r.Context()).ID)
	if err != nil {
		deploymentError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, d := range list {
		out = append(out, deploymentJSON(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := deploymentIDParam(w, r)
	if !ok {
		return
	}
	dep, err := s.deps.Get(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		deploymentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deploymentJSON(dep))
}

func isTerminal(status string) bool {
	switch status {
	case "live", "failed", "cancelled", "superseded":
		return true
	}
	return false
}

// deployStatusPoll is the fallback for a terminal transition the stream isn't
// told about (the orphan reaper, a status write from another process).
const deployStatusPoll = 5 * time.Second

// handleDeploymentLogs streams a deployment's log over SSE (FR-4.3):
// replay the persisted file, then live events until the client disconnects.
func (s *Server) handleDeploymentLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := deploymentIDParam(w, r)
	if !ok {
		return
	}
	dep, err := s.deps.Get(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		deploymentError(w, err)
		return
	}
	ctx, flusher, cleanup, ok := s.beginStream(w, r, func(ctx context.Context) error {
		u, err := s.revalidate(ctx, r)
		if err != nil {
			return err
		}
		_, err = s.deps.Get(ctx, id, u.ID)
		return err
	})
	if !ok {
		return
	}
	defer cleanup()

	idStr := chi.URLParam(r, "deploymentID")

	// The log file is the single source of truth for lines; hub messages only
	// say "the file grew". Reading every line from the file at a tracked offset
	// means a line written while the replay runs is sent exactly once (it used
	// to arrive both from the replay and from the live channel).
	// Subscribe before the first read so no growth is missed.
	logCh, cancelLog := s.hub.Subscribe("deploy:" + idStr)
	defer cancelLog()
	statusCh, cancelStatus := s.hub.Subscribe(deployments.StatusTopic(idStr))
	defer cancelStatus()

	startSSE(w)

	send := func(line string) {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", line)
	}
	tail := &logTail{path: s.logPath(idStr)}
	defer tail.close()
	drain := func(final bool) {
		for _, line := range tail.read(final) {
			send(line)
		}
		flusher.Flush()
	}
	finish := func(status string) {
		drain(true)
		send("[deployment " + status + "]")
		flusher.Flush()
	}

	drain(false)
	if isTerminal(dep.Status) {
		finish(dep.Status)
		return
	}
	poll := time.NewTicker(deployStatusPoll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-logCh:
			drain(false)
		case st := <-statusCh:
			finish(string(st))
			return
		case <-poll.C:
			cur, err := s.deps.Get(ctx, id, userFrom(r.Context()).ID)
			if err == nil && isTerminal(cur.Status) {
				finish(cur.Status)
				return
			}
			drain(false)
		}
	}
}

// logTail incrementally reads complete lines from a deployment log that may
// not exist yet and may still be growing.
type logTail struct {
	path    string
	f       *os.File
	partial []byte
}

func (t *logTail) read(final bool) []string {
	if t.f == nil {
		f, err := os.Open(t.path)
		if err != nil {
			return nil
		}
		t.f = f
	}
	buf, _ := io.ReadAll(t.f)
	t.partial = append(t.partial, buf...)
	var out []string
	for {
		i := bytes.IndexByte(t.partial, '\n')
		if i < 0 {
			break
		}
		out = append(out, strings.TrimSuffix(string(t.partial[:i]), "\r"))
		t.partial = t.partial[i+1:]
	}
	if final && len(t.partial) > 0 {
		out = append(out, string(t.partial))
		t.partial = nil
	}
	return out
}

func (t *logTail) close() {
	if t.f != nil {
		_ = t.f.Close()
	}
}
