package main

import (
	"net/http"
	"testing"
)

// A server with no read deadline holds a goroutine for any client willing to
// dribble header bytes, which costs the attacker nothing.
func TestNewHTTPServerSetsReadTimeouts(t *testing.T) {
	s := newHTTPServer(":8080", http.NewServeMux())
	if s.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout unset: a slow-header client can hold a connection forever")
	}
	if s.ReadTimeout <= 0 {
		t.Error("ReadTimeout unset: a slow-body client can hold a connection forever")
	}
	if s.IdleTimeout <= 0 {
		t.Error("IdleTimeout unset: keep-alive connections accumulate")
	}
}

// The log and metric endpoints stream Server-Sent Events for as long as the
// browser stays on the page. A write deadline would cut them off mid-stream.
func TestNewHTTPServerLeavesWriteTimeoutUnsetForSSE(t *testing.T) {
	if s := newHTTPServer(":8080", http.NewServeMux()); s.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want 0 so SSE streams are not cut off", s.WriteTimeout)
	}
}
