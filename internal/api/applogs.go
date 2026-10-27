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
	ctx, flusher, cleanup, ok := s.beginStream(w, r, func(ctx context.Context) error {
		u, err := s.revalidate(ctx, r)
		if err != nil {
			return err
		}
		_, err = s.apps.Get(ctx, id, u.ID)
		return err
	})
	if !ok {
		return
	}
	defer cleanup()

	appIDVal, _ := app.ID.Value()
	appIDStr, _ := appIDVal.(string)

	// Route log streaming through the app's worker host when it has one
	// (Phase 12a). The target-bound provider reaches the right daemon.
	if app.HostID.Valid {
		bound, err := s.providerFor(ctx, app)
		if err != nil {
			Error(w, http.StatusBadGateway, "host_unavailable", err.Error())
			return
		}
		if bs, isStreamer := bound.(appLogStreamer); isStreamer {
			streamer = bs
		}
	}
	// The reader is bound to the stream context, so a lapsed session kills the
	// underlying `docker compose logs -f` process rather than leaving it
	// running for a viewer who is no longer allowed to read it. That holds for
	// a worker host too: the bound streamer runs the same command over SSH.
	rc, err := streamer.AppLogs(ctx, appIDStr, 200)
	if err != nil {
		Error(w, http.StatusConflict, "app_not_running", "no running container for this app yet")
		return
	}
	defer func() { _ = rc.Close() }()

	startSSE(w)
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
		flusher.Flush()
	}
}
