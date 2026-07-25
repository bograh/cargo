package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	h := securityHeaders(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, k := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy"} {
		if rec.Header().Get(k) == "" {
			t.Errorf("missing header %s", k)
		}
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be set outside production")
	}
}

func TestSecurityHeadersHSTSInProduction(t *testing.T) {
	rec := httptest.NewRecorder()
	h := securityHeaders(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Header().Get("Strict-Transport-Security"), "max-age=") {
		t.Error("HSTS should be set in production")
	}
}

func TestBodyLimitRejectsOversize(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := bodyLimit(16)(next)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orgs", strings.NewReader(strings.Repeat("x", 100)))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestBodyLimitAllowsSmall(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	bodyLimit(1024)(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/orgs", strings.NewReader("small")))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestBodyLimitExemptsWebhook(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", strings.NewReader(strings.Repeat("x", 100)))
	bodyLimit(16)(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook should be exempt from the body cap; status = %d", rec.Code)
	}
}

func TestOriginCheckRejectsCrossOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://cargo.example.com/api/v1/orgs", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	originCheck(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for cross-origin POST", rec.Code)
	}
}

func TestOriginCheckAllowsSameOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://cargo.example.com/api/v1/orgs", nil)
	req.Header.Set("Origin", "http://cargo.example.com")
	originCheck(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for same-origin POST", rec.Code)
	}
}

func TestOriginCheckAllowsNoOriginHeader(t *testing.T) {
	// Non-browser clients (curl) send no Origin/Referer and are not a CSRF vector.
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	originCheck(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://cargo.example.com/api/v1/orgs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when no Origin/Referer present", rec.Code)
	}
}

func TestOriginCheckExemptsWebhook(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://cargo.example.com/api/v1/webhooks/github", nil)
	req.Header.Set("Origin", "http://api.github.com")
	originCheck(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook should be exempt from origin checks; status = %d", rec.Code)
	}
}

func TestAPIRateLimiter(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := apiRateLimiter(1)(next) // 1 rps, burst 2
	var got429 bool
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs", nil)
		req.RemoteAddr = "203.0.113.9:5555"
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("expected a 429 after exhausting the burst")
	}
}
