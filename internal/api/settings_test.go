package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/settings"
)

type stubInstanceSettings struct {
	suffix       string
	smtp         *settings.SMTPConfig
	registration string
	err          error
}

func (s *stubInstanceSettings) Suffix(context.Context) (string, error) { return s.suffix, s.err }
func (s *stubInstanceSettings) SetSuffix(_ context.Context, suffix string) error {
	s.suffix = suffix
	return s.err
}
func (s *stubInstanceSettings) SMTP(context.Context) (*settings.SMTPConfig, error) {
	return s.smtp, s.err
}
func (s *stubInstanceSettings) SetSMTP(_ context.Context, cfg settings.SMTPConfig) error {
	s.smtp = &cfg
	return s.err
}
func (s *stubInstanceSettings) ClearSMTP(context.Context) error {
	s.smtp = nil
	return s.err
}
func (s *stubInstanceSettings) Registration(context.Context) (string, error) {
	if s.registration == "" {
		return settings.DefaultRegistration, nil
	}
	return s.registration, s.err
}
func (s *stubInstanceSettings) SetRegistration(_ context.Context, mode string) error {
	if !settings.ValidRegistrationMode(mode) {
		return settings.ErrValidation
	}
	s.registration = mode
	return s.err
}

func settingsRequest(t *testing.T, isAdmin bool, method, path, body string) (*httptest.ResponseRecorder, *stubInstanceSettings) {
	t.Helper()
	st := &stubInstanceSettings{
		suffix: "apps.example.com",
		smtp:   &settings.SMTPConfig{Host: "smtp.example.com", Port: 587, Username: "m", Password: "SECRET-PW", From: "c@x.co"},
	}
	s := &Server{
		auth:             stubAuth{user: sqlc.User{Email: "a@b.co", IsInstanceAdmin: isAdmin}},
		instanceSettings: st,
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
	NewRouter(s).ServeHTTP(rec, req)
	return rec, st
}

func TestGetSettingsOmitsSMTPPassword(t *testing.T) {
	rec, _ := settingsRequest(t, true, http.MethodGet, "/api/v1/admin/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "apps.example.com") || !strings.Contains(body, "smtp.example.com") {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(body, "SECRET-PW") {
		t.Fatal("smtp password leaked")
	}
}

func TestSettingsForbiddenForNonAdmin(t *testing.T) {
	rec, _ := settingsRequest(t, false, http.MethodGet, "/api/v1/admin/settings", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestPutSuffix(t *testing.T) {
	rec, st := settingsRequest(t, true, http.MethodPut, "/api/v1/admin/settings/apps-domain-suffix",
		`{"suffix":"apps.new.example.com"}`)
	if rec.Code != http.StatusOK || st.suffix != "apps.new.example.com" {
		t.Fatalf("status = %d suffix = %s", rec.Code, st.suffix)
	}
}

func TestDeleteSMTP(t *testing.T) {
	rec, st := settingsRequest(t, true, http.MethodDelete, "/api/v1/admin/settings/smtp", "")
	if rec.Code != http.StatusOK || st.smtp != nil {
		t.Fatalf("status = %d smtp = %v", rec.Code, st.smtp)
	}
}
