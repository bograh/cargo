package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/bograh/cargo/internal/reconciler"
)

// appLogStreamer is the runtime-log capability the deploy provider exposes.
// *reconciler.Docker satisfies it; the handler type-asserts so the interface
// used by the deploy pipeline stays unchanged.
type appLogStreamer interface {
	AppLogs(ctx context.Context, appID string, tail int) (io.ReadCloser, error)
}

// handleAppLogs streams a running app's container logs (application + request
// output) over SSE. Any org member (viewer+) of the app may read them.
func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	app, err := s.apps.Get(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		appError(w, err)
		return
	}
	streamer, ok := s.provider.(appLogStreamer)
	if !ok {
		Error(w, http.StatusInternalServerError, "internal", "log streaming unsupported")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		Error(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	appIDVal, _ := app.ID.Value()
	appIDStr, _ := appIDVal.(string)

	// Route log streaming through the app's worker host when it has one
	// (Phase 12a). The target-bound provider reaches the right daemon.
	if resolver, ok := s.hostsAdmin.(interface {
		Resolve(ctx context.Context, appID string) (*reconciler.Target, string, error)
	}); ok && app.HostID.Valid {
		target, _, rerr := resolver.Resolve(r.Context(), appIDStr)
		if rerr == nil && target != nil {
			if fb, ok := streamer.(interface {
				ForTarget(reconciler.Target) reconciler.DeployProvider
			}); ok {
				if bound, isStreamer := fb.ForTarget(*target).(appLogStreamer); isStreamer {
					streamer = bound
				}
			}
		}
	}
	rc, err := streamer.AppLogs(r.Context(), appIDStr, 200)
	if err != nil {
		Error(w, http.StatusConflict, "app_not_running", "no running container for this app yet")
		return
	}
	defer func() { _ = rc.Close() }()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-r.Context().Done():
			return
		default:
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
		flusher.Flush()
	}
}
