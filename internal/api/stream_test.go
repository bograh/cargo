package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// Each app-log stream holds an open `docker compose logs -f` process for as
// long as the browser stays on the page. Nothing bounded them.
func TestStreamLimiterCapsPerUser(t *testing.T) {
	var l streamLimiter
	releases := make([]func(), 0, maxStreamsPerUser)
	for i := range maxStreamsPerUser {
		release, ok := l.acquire("alice")
		if !ok {
			t.Fatalf("stream %d rejected inside the per-user cap", i)
		}
		releases = append(releases, release)
	}
	if _, ok := l.acquire("alice"); ok {
		t.Fatal("alice allowed past the per-user cap")
	}
	// Another user is unaffected while there is instance headroom.
	if _, ok := l.acquire("bob"); !ok {
		t.Fatal("bob rejected by alice's usage")
	}
	// Closing one frees exactly one slot.
	releases[0]()
	if _, ok := l.acquire("alice"); !ok {
		t.Fatal("released slot was not reusable")
	}
}

func TestStreamLimiterCapsInstanceWide(t *testing.T) {
	var l streamLimiter
	users := maxStreamsTotal/maxStreamsPerUser + 1
	granted := 0
	for u := range users {
		for range maxStreamsPerUser {
			if _, ok := l.acquire(string(rune('a' + u))); ok {
				granted++
			}
		}
	}
	if granted != maxStreamsTotal {
		t.Fatalf("granted %d streams, want the instance cap of %d", granted, maxStreamsTotal)
	}
}

// A double release would hand out slots that were never taken.
func TestStreamLimiterReleaseIsIdempotent(t *testing.T) {
	var l streamLimiter
	release, _ := l.acquire("alice")
	release()
	release()
	if l.total != 0 {
		t.Fatalf("total = %d after a double release, want 0", l.total)
	}
	if n := l.perUser["alice"]; n != 0 {
		t.Fatalf("alice holds %d streams after a double release, want 0", n)
	}
}

func streamServer(t *testing.T) *Server {
	t.Helper()
	var uid pgtype.UUID
	if err := uid.Scan("11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatal(err)
	}
	return &Server{auth: stubAuth{user: sqlc.User{ID: uid, Email: "a@b.co"}}}
}

func streamRequest(s *Server) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/apps/x/logs", nil)
	r.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
	u, _ := s.auth.UserForAccessToken(context.Background(), "acc")
	return r.WithContext(context.WithValue(r.Context(), userKey, u))
}

func TestBeginStreamRefusesPastTheCap(t *testing.T) {
	s := streamServer(t)
	always := func(context.Context) error { return nil }
	for range maxStreamsPerUser {
		_, _, cleanup, ok := s.beginStream(httptest.NewRecorder(), streamRequest(s), always)
		if !ok {
			t.Fatal("stream rejected inside the cap")
		}
		defer cleanup()
	}
	rec := httptest.NewRecorder()
	if _, _, _, ok := s.beginStream(rec, streamRequest(s), always); ok {
		t.Fatal("stream allowed past the cap")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
}

// Authorization is otherwise decided once, at connect time: a stream opened
// before a revocation keeps delivering to someone who lost access.
func TestBeginStreamEndsWhenAccessLapses(t *testing.T) {
	orig := streamRecheckEvery
	streamRecheckEvery = 5 * time.Millisecond
	t.Cleanup(func() { streamRecheckEvery = orig })

	s := streamServer(t)
	revoked := make(chan struct{})
	ctx, _, cleanup, ok := s.beginStream(httptest.NewRecorder(), streamRequest(s),
		func(context.Context) error {
			select {
			case <-revoked:
				return errors.New("session revoked")
			default:
				return nil
			}
		})
	if !ok {
		t.Fatal("stream refused")
	}
	defer cleanup()

	select {
	case <-ctx.Done():
		t.Fatal("stream ended while access was still valid")
	case <-time.After(20 * time.Millisecond):
	}

	close(revoked)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("stream outlived the access that opened it")
	}
}

// The slot has to come back when the stream ends, or a user who opens and
// closes the same page repeatedly locks themselves out.
func TestBeginStreamCleanupFreesTheSlot(t *testing.T) {
	s := streamServer(t)
	always := func(context.Context) error { return nil }
	for range maxStreamsPerUser * 2 {
		_, _, cleanup, ok := s.beginStream(httptest.NewRecorder(), streamRequest(s), always)
		if !ok {
			t.Fatal("slot was not returned after the previous stream closed")
		}
		cleanup()
	}
}
