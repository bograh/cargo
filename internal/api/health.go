package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"time"
)

func HealthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// dockerPing verifies the Docker daemon is reachable. It's a package var so
// tests can simulate the daemon being down.
var dockerPing = func(ctx context.Context) error {
	return exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Run()
}

// ReadyHandler is a readiness probe: 200 only when Postgres and Docker — the
// two hard dependencies of every real operation — are both reachable, else 503
// naming the failed dependency. Kept distinct from /healthz (liveness).
func (s *Server) ReadyHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if s == nil || s.pool == nil {
		readyFail(w, "database")
		return
	}
	if err := s.pool.Ping(ctx); err != nil {
		readyFail(w, "database")
		return
	}
	if err := dockerPing(ctx); err != nil {
		readyFail(w, "docker")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func readyFail(w http.ResponseWriter, dep string) {
	Error(w, http.StatusServiceUnavailable, "not_ready", dep+" is not reachable")
}
