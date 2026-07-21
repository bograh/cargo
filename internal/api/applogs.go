package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
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
