package api

import (
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

// authRateLimiter returns per-IP limiting middleware for auth endpoints (FR-1.4).
func authRateLimiter() func(http.Handler) http.Handler {
	var mu sync.Mutex
	limiters := map[string]*rate.Limiter{}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
			mu.Lock()
			lim, ok := limiters[ip]
			if !ok {
				lim = rate.NewLimiter(rate.Limit(10.0/60.0), 10)
				limiters[ip] = lim
			}
			mu.Unlock()
			if !lim.Allow() {
				Error(w, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
