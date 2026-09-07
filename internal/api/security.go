package api

import (
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/time/rate"
)

// maxRequestBody caps request bodies on the API (except the GitHub webhook,
// which has its own 5 MiB HMAC-validated cap). A generous limit for JSON.
const maxRequestBody = 1 << 20 // 1 MiB

// contentSecurityPolicy locks the embedded SPA to same-origin assets. Inline
// styles are allowed because the UI uses style="" attributes (e.g. gauges);
// everything else — scripts, fonts, XHR/SSE — must be same-origin.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; frame-ancestors 'none'; " +
	"base-uri 'self'; form-action 'self'"

// securityHeaders sets standard hardening headers on every response. HSTS is
// only emitted in production (over real TLS), never on plain-HTTP local installs.
func securityHeaders(production bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Content-Security-Policy", contentSecurityPolicy)
			if production {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bodyLimit rejects over-large request bodies with 413. The GitHub webhook is
// exempt (large pushes; it caps itself). Streaming download/SSE routes are GETs
// with no request body, so the cap is a harmless no-op there.
func bodyLimit(max int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isWebhook(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if r.ContentLength > max {
				Error(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, max)
			next.ServeHTTP(w, r)
		})
	}
}

// originCheck defends cookie-authed mutations against CSRF: a state-changing
// request whose Origin (or Referer) names a different host than the request is
// rejected. Requests with neither header (non-browser clients like curl, and
// server-to-server webhooks) are allowed — CSRF requires a browser that
// auto-attaches the session cookie, and browsers always send Origin on such
// cross-site writes. Complements the SameSite=Lax cookie attribute.
//
// The residual, stated plainly rather than left implicit: for any browser or
// embedded context that sends neither header on a cross-site write, the whole
// defence here is the SameSite=Lax attribute on the session cookie. That is
// the right trade today, because every caller is either that browser or a
// non-browser client with no cookie to abuse.
//
// It stops being the right trade the moment an API-token auth path is added.
// Then the two cases separate and should be treated differently: a
// cookie-authenticated mutation must carry an Origin, and only a
// token-authenticated one is exempt. Anyone adding that path should change
// this function in the same commit.
func originCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if isWebhook(r.URL.Path) {
				next.ServeHTTP(w, r) // HMAC-authenticated, no cookie
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				if !sameHost(o, r.Host) {
					Error(w, http.StatusForbidden, "forbidden", "cross-origin request rejected")
					return
				}
			} else if ref := r.Header.Get("Referer"); ref != "" {
				if !sameHost(ref, r.Host) {
					Error(w, http.StatusForbidden, "forbidden", "cross-origin request rejected")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isWebhook(path string) bool { return strings.HasSuffix(path, "/webhooks/github") }

func sameHost(rawURL, host string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return u.Host == host
}

// apiRateLimiter throttles authed API traffic per user (per client IP as a
// fallback), on top of the stricter auth-endpoint limiter.
func apiRateLimiter(rps float64) func(http.Handler) http.Handler {
	if rps <= 0 {
		rps = 20
	}
	burst := int(rps * 2)
	if burst < 1 {
		burst = 1
	}
	store := newLimiterStore(rate.Limit(rps), burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !store.allow(rateKey(r)) {
				Error(w, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// rateKey prefers the authenticated user id (stable across IPs), falling back
// to the client IP for any request that reaches the limiter unauthenticated.
func rateKey(r *http.Request) string {
	if u := userFrom(r.Context()); u.ID.Valid {
		return "u:" + uuidString(u.ID)
	}
	return "ip:" + clientIP(r)
}
