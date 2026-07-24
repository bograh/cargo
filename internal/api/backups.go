package api

import (
	"net/http"

	"github.com/bograh/cargo/internal/jobs"
)

// handleRunBackup enqueues an on-demand control-plane backup (instance admin).
func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request) {
	if s.enqueue == nil {
		Error(w, http.StatusServiceUnavailable, "unavailable", "backups are not available")
		return
	}
	if err := s.enqueue.EnqueuePlatformBackup(r.Context()); err != nil {
		Error(w, http.StatusInternalServerError, "internal", "could not enqueue backup")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued"})
}

// handleListBackups lists on-disk control-plane backup sets (instance admin).
func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	list, err := jobs.ListBackups(s.cfg.DataDir)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "could not list backups")
		return
	}
	writeJSON(w, http.StatusOK, list)
}
