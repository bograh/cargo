package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// authRPS/authBurst throttle the auth endpoints (FR-1.4): ten attempts,
	// then one more per six seconds.
	authRPS   = rate.Limit(10.0 / 60.0)
	authBurst = 10
	// limiterIdleTTL is how long a bucket outlives its last request, and
	// limiterGCInterval how often idle ones are swept. The TTL must comfortably
	// exceed the time a full burst takes to refill, or a client could shed a
	// throttled bucket by pausing.
	limiterIdleTTL    = 15 * time.Minute
	limiterGCInterval = 5 * time.Minute
)

// limiterStore holds one rate limiter per key, dropping buckets that have gone
// idle. Without the sweep the map grows for the life of the process — one
// entry per distinct key, which for an IP-keyed limiter means one per client
// that has ever connected.
type limiterStore struct {
	mu      sync.Mutex
	entries map[string]*limiterEntry
	rps     rate.Limit
	burst   int
	lastGC  time.Time
	now     func() time.Time // test seam
}

type limiterEntry struct {
	limiter *rate.Limiter
	seen    time.Time
}

func newLimiterStore(rps rate.Limit, burst int) *limiterStore {
	return &limiterStore{
		entries: map[string]*limiterEntry{},
		rps:     rps,
		burst:   burst,
		now:     time.Now,
	}
}

// allow records a request against key and reports whether it may proceed.
func (s *limiterStore) allow(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweep(now)
	e, ok := s.entries[key]
	if !ok {
		e = &limiterEntry{limiter: rate.NewLimiter(s.rps, s.burst)}
		s.entries[key] = e
	}
	e.seen = now
	return e.limiter.Allow()
}

// sweep drops idle buckets. Called under the lock, at most every
// limiterGCInterval, so an ordinary request pays nothing for it.
func (s *limiterStore) sweep(now time.Time) {
	if now.Sub(s.lastGC) < limiterGCInterval {
		return
	}
	s.lastGC = now
	for key, e := range s.entries {
		if now.Sub(e.seen) > limiterIdleTTL {
			delete(s.entries, key)
		}
	}
}

// clientIP resolves the address to rate-limit an unauthenticated request on.
//
// RemoteAddr alone is wrong behind a proxy: every request arrives from
// Traefik's container address, so the whole instance shares one bucket and a
// single client hammering /login locks everyone else out. X-Forwarded-For
// carries the real address — but only its *last* entry is worth anything. A
// client can send whatever prefix it likes, and each hop appends to what it
// received, so the rightmost entry is the one the nearest proxy observed.
//
// The header is honoured only when the peer is itself loopback or private,
// which holds for a proxy sharing a container network and not for a direct
// connection from outside. That is what keeps a caller from choosing its own
// bucket — and from minting unbounded buckets to exhaust the store.
//
// With a second proxy in front (a CDN, say), the rightmost entry is that
// proxy's edge address rather than the visitor's, so its traffic shares a
// bucket. That is a coarser limit, not a weaker one.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !trustedPeer(host) {
		return host
	}
	fwd := r.Header.Get("X-Forwarded-For")
	if fwd == "" {
		return host
	}
	if i := strings.LastIndexByte(fwd, ','); i >= 0 {
		fwd = fwd[i+1:]
	}
	if ip := net.ParseIP(strings.TrimSpace(fwd)); ip != nil {
		return ip.String()
	}
	return host
}

// trustedPeer reports whether a directly-connected peer may set X-Forwarded-For
// on our behalf. Cargo's own proxy reaches it over a private container network.
func trustedPeer(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// authRateLimiter returns per-client limiting middleware for auth endpoints
// (FR-1.4). These run before anyone is authenticated, so the client address is
// the only key available — see clientIP for why RemoteAddr is not it.
func authRateLimiter() func(http.Handler) http.Handler {
	store := newLimiterStore(authRPS, authBurst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !store.allow(clientIP(r)) {
				Error(w, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
