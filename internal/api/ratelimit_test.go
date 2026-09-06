package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func request(remoteAddr, forwarded string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.RemoteAddr = remoteAddr
	if forwarded != "" {
		r.Header.Set("X-Forwarded-For", forwarded)
	}
	return r
}

// Behind Traefik every request arrives from the proxy's container address.
// Keying on that alone gives the whole instance one bucket, so a single client
// hammering /login would lock every other user out.
func TestClientIPSeparatesClientsBehindAProxy(t *testing.T) {
	first := clientIP(request("172.18.0.4:52000", "203.0.113.7"))
	second := clientIP(request("172.18.0.4:52001", "198.51.100.9"))
	if first == second {
		t.Fatalf("both clients keyed as %q; the proxy address is not a client identity", first)
	}
	if first != "203.0.113.7" {
		t.Fatalf("clientIP = %q, want the forwarded client address", first)
	}
}

// A client can send any X-Forwarded-For prefix it likes; each hop appends to
// what it received. Only the rightmost entry was written by a proxy we trust,
// so trusting the leftmost lets a caller pick its own bucket — and mint as
// many as it wants.
func TestClientIPIgnoresSpoofedForwardedPrefix(t *testing.T) {
	got := clientIP(request("172.18.0.4:52000", "1.2.3.4, 5.6.7.8, 203.0.113.7"))
	if got != "203.0.113.7" {
		t.Fatalf("clientIP = %q, want the rightmost (proxy-written) entry", got)
	}
}

// A direct connection from outside is nobody's trusted proxy, so its header is
// just an attacker-supplied string.
func TestClientIPIgnoresForwardedFromUntrustedPeer(t *testing.T) {
	got := clientIP(request("203.0.113.50:41000", "10.0.0.1"))
	if got != "203.0.113.50" {
		t.Fatalf("clientIP = %q, want the peer address for an untrusted peer", got)
	}
}

func TestClientIPFallsBackToPeer(t *testing.T) {
	for _, tc := range []struct{ name, remote, fwd, want string }{
		{"no header", "172.18.0.4:52000", "", "172.18.0.4"},
		{"unparseable header", "172.18.0.4:52000", "not-an-ip", "172.18.0.4"},
		{"no port", "172.18.0.4", "", "172.18.0.4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := clientIP(request(tc.remote, tc.fwd)); got != tc.want {
				t.Fatalf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLimiterStoreThrottlesPerKey(t *testing.T) {
	s := newLimiterStore(rate.Limit(1), 2)
	for i := range 2 {
		if !s.allow("a") {
			t.Fatalf("request %d for key a rejected inside the burst", i)
		}
	}
	if s.allow("a") {
		t.Fatal("key a allowed past its burst")
	}
	if !s.allow("b") {
		t.Fatal("key b was throttled by key a's traffic")
	}
}

// Without a sweep the map grows for the life of the process: one entry per
// client that has ever connected.
func TestLimiterStoreEvictsIdleBuckets(t *testing.T) {
	now := time.Now()
	s := newLimiterStore(rate.Limit(1), 1)
	s.now = func() time.Time { return now }

	s.allow("idle")
	now = now.Add(limiterGCInterval + time.Second)
	s.allow("active")
	if len(s.entries) != 2 {
		t.Fatalf("entries = %d, want both keys before the TTL elapses", len(s.entries))
	}

	now = now.Add(limiterIdleTTL + time.Second)
	s.allow("active")
	if _, ok := s.entries["idle"]; ok {
		t.Fatal("idle bucket survived the sweep")
	}
	if _, ok := s.entries["active"]; !ok {
		t.Fatal("active bucket was swept")
	}
}

// A client must not be able to shed a throttled bucket by pausing: the TTL has
// to outlast the time a full burst takes to refill.
func TestLimiterIdleTTLOutlastsBurstRefill(t *testing.T) {
	refill := time.Duration(float64(authBurst)/float64(authRPS)) * time.Second
	if limiterIdleTTL <= refill {
		t.Fatalf("idle TTL %s does not outlast a burst refill of %s", limiterIdleTTL, refill)
	}
}
