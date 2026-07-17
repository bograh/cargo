package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/db/sqlc"
)

type stubAuth struct {
	user   sqlc.User
	tokens auth.Tokens
	err    error
}

func (s stubAuth) Register(_ context.Context, _, _ string) (sqlc.User, auth.Tokens, error) {
	return s.user, s.tokens, s.err
}
func (s stubAuth) Login(_ context.Context, _, _ string) (sqlc.User, auth.Tokens, error) {
	return s.user, s.tokens, s.err
}
func (s stubAuth) Refresh(_ context.Context, _ string) (auth.Tokens, error) {
	return s.tokens, s.err
}
func (s stubAuth) Logout(_ context.Context, _ string) error { return s.err }
func (s stubAuth) UserForAccessToken(_ context.Context, tok string) (sqlc.User, error) {
	if s.err != nil || tok == "" {
		return sqlc.User{}, auth.ErrUnauthenticated
	}
	return s.user, nil
}

func testTokens() auth.Tokens {
	return auth.Tokens{
		Access: "acc", AccessExpiresAt: time.Now().Add(15 * time.Minute),
		Refresh: "ref", RefreshExpiresAt: time.Now().Add(720 * time.Hour),
	}
}

func TestRegisterSetsCookies(t *testing.T) {
	s := &Server{auth: stubAuth{user: sqlc.User{Email: "a@b.co"}, tokens: testTokens()}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
		strings.NewReader(`{"email":"a@b.co","password":"password-123"}`))
	NewRouter(s).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	cookies := rec.Result().Cookies()
	var names []string
	for _, c := range cookies {
		names = append(names, c.Name)
		if !c.HttpOnly {
			t.Fatalf("cookie %s not HttpOnly", c.Name)
		}
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "cargo_access") || !strings.Contains(joined, "cargo_refresh") {
		t.Fatalf("cookies = %v", names)
	}
}

func TestRegisterValidation(t *testing.T) {
	s := &Server{auth: stubAuth{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
		strings.NewReader(`{"email":"","password":""}`))
	NewRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestLoginInvalidCredentials(t *testing.T) {
	s := &Server{auth: stubAuth{err: auth.ErrInvalidCredentials}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"email":"a@b.co","password":"wrong-password"}`))
	NewRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMeRequiresAuth(t *testing.T) {
	s := &Server{auth: stubAuth{err: auth.ErrUnauthenticated}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	NewRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMeReturnsUser(t *testing.T) {
	s := &Server{auth: stubAuth{user: sqlc.User{Email: "a@b.co", IsInstanceAdmin: true}}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
	NewRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["email"] != "a@b.co" || body["is_instance_admin"] != true {
		t.Fatalf("body = %v", body)
	}
}

func TestAuthRateLimited(t *testing.T) {
	s := &Server{auth: stubAuth{err: auth.ErrInvalidCredentials}}
	r := NewRouter(s)
	var last int
	for i := 0; i < 15; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"email":"a@b.co","password":"wrong-password"}`))
		req.RemoteAddr = "10.9.9.9:1234"
		r.ServeHTTP(rec, req)
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("15th request status = %d, want 429", last)
	}
}
