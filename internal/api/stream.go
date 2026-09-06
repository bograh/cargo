package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
)

// Bounds on Server-Sent Events streams.
//
// A stream is not a cheap connection. Each app-log stream holds an open
// `docker compose logs -f` process for as long as the browser stays on the
// page, and every stream holds a goroutine and a hub subscription. Nothing
// bounded them, so one authenticated member with a loop could exhaust process
// slots and memory on the host.
//
// The per-user cap is generous for a person — a handful of tabs, each with a
// log and a metric stream — while the instance cap is what actually protects
// the host from a client that ignores the first one.
const (
	maxStreamsPerUser = 8
	maxStreamsTotal   = 64
)

// streamRecheckEvery bounds how long a stream outlives the access it was opened
// with. Access tokens last 15 minutes, so an established stream is dropped
// within a minute of its token lapsing; EventSource reconnects with the
// refreshed cookie, which is what makes that safe to do. A variable so tests
// need not wait a minute to observe the recheck.
var streamRecheckEvery = time.Minute

// streamLimiter counts open streams per user and instance-wide. The zero value
// is usable.
type streamLimiter struct {
	mu      sync.Mutex
	perUser map[string]int
	total   int
}

// acquire reserves a stream slot, returning the release to call when it ends.
func (l *streamLimiter) acquire(userID string) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.perUser == nil {
		l.perUser = map[string]int{}
	}
	if l.total >= maxStreamsTotal || l.perUser[userID] >= maxStreamsPerUser {
		return nil, false
	}
	l.total++
	l.perUser[userID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.total--
			if n := l.perUser[userID] - 1; n > 0 {
				l.perUser[userID] = n
			} else {
				delete(l.perUser, userID)
			}
		})
	}, true
}

// revalidate re-resolves the request's session. A revoked or expired session,
// or a deleted account, fails here.
func (s *Server) revalidate(ctx context.Context, r *http.Request) (sqlc.User, error) {
	c, err := r.Cookie("cargo_access")
	if err != nil {
		return sqlc.User{}, err
	}
	return s.auth.UserForAccessToken(ctx, c.Value)
}

// beginStream reserves a stream slot and returns a context cancelled when the
// client disconnects or when the viewer's access lapses.
//
// It deliberately does not write the response headers. A caller may still fail
// after this point — app-log streaming has no container to attach to — and once
// the SSE headers and a 200 are out, there is no way to report that. Call
// startSSE once the stream is certain.
//
// The recheck is the point. Authorization is otherwise decided once, at connect
// time, and a stream established before a revocation keeps delivering — to
// someone removed from the organization, or whose session was revoked as
// stolen. recheck re-runs both halves: that the session still resolves, and
// that the user it resolves to may still read this resource.
//
// The returned cleanup must be deferred by the caller.
func (s *Server) beginStream(
	w http.ResponseWriter, r *http.Request, recheck func(context.Context) error,
) (ctx context.Context, flusher http.Flusher, cleanup func(), ok bool) {
	flusher, ok = w.(http.Flusher)
	if !ok {
		Error(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return nil, nil, nil, false
	}
	release, ok := s.streams.acquire(uuidString(userFrom(r.Context()).ID))
	if !ok {
		Error(w, http.StatusTooManyRequests, "too_many_streams",
			"too many open log or metric streams — close one and try again")
		return nil, nil, nil, false
	}

	ctx, cancel := context.WithCancel(r.Context())
	go func() {
		ticker := time.NewTicker(streamRecheckEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if recheck(ctx) != nil {
					cancel()
					return
				}
			}
		}
	}()

	return ctx, flusher, func() { cancel(); release() }, true
}

// startSSE commits the response to Server-Sent Events. Nothing after this can
// report an error to the client through a status code.
func startSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
}
