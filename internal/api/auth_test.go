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
	"github.com/bograh/cargo/internal/orgs"
	"github.com/bograh/cargo/internal/settings"
	"github.com/jackc/pgx/v5/pgtype"
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
func (s stubAuth) IssueSession(_ context.Context, _ pgtype.UUID) (auth.Tokens, error) {
	return s.tokens, s.err
}
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

// registerServer builds the minimal server the register handler needs. An
// instance with no users yet is the bootstrap case, where policy does not
// apply — the first account becomes the instance admin.
func registerServer(hasUsers bool, mode string) *Server {
	return &Server{
		auth:             stubAuth{user: sqlc.User{Email: "a@b.co"}, tokens: testTokens()},
		admin:            stubAdmin{hasUsers: hasUsers},
		orgs:             stubOrgs{},
		instanceSettings: &stubInstanceSettings{registration: mode},
	}
}

func registerRequest(s *Server, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	NewRouter(s).ServeHTTP(rec, req)
	return rec
}

func TestRegisterSetsCookies(t *testing.T) {
	s := registerServer(false, settings.RegistrationOpen)
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

// A Cargo account carries the ability to run containers on the host, so an
// instance that has not chosen a policy must not be an open door.
func TestRegisterHonoursInstancePolicy(t *testing.T) {
	const creds = `{"email":"a@b.co","password":"password-123"}`
	const withInvite = `{"email":"a@b.co","password":"password-123","invite_token":"tok"}`

	for _, tc := range []struct {
		name     string
		hasUsers bool
		mode     string
		body     string
		want     int
	}{
		{"first account always allowed", false, settings.RegistrationInvite, creds, http.StatusCreated},
		{"open instance", true, settings.RegistrationOpen, creds, http.StatusCreated},
		{"invite-only without a token", true, settings.RegistrationInvite, creds, http.StatusForbidden},
		{"invite-only with a token", true, settings.RegistrationInvite, withInvite, http.StatusCreated},
		{"closed", true, settings.RegistrationClosed, creds, http.StatusForbidden},
		{"closed ignores a token", true, settings.RegistrationClosed, withInvite, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := registerRequest(registerServer(tc.hasUsers, tc.mode), tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

// An instance that has never been configured is invite-only, so upgrading does
// not leave a previously reachable sign-up form open by omission.
func TestRegisterDefaultsToInviteOnly(t *testing.T) {
	s := registerServer(true, "")
	rec := registerRequest(s, `{"email":"a@b.co","password":"password-123"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 on an unconfigured instance (body %s)", rec.Code, rec.Body)
	}
}

// A token the orgs service rejects must not mint an account: the holder would
// have no organisation to join and no way to get one.
func TestRegisterRejectsUnusableInvite(t *testing.T) {
	s := registerServer(true, settings.RegistrationInvite)
	s.orgs = stubOrgs{err: orgs.ErrInviteInvalid}
	rec := registerRequest(s, `{"email":"a@b.co","password":"password-123","invite_token":"stale"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body)
	}
}
