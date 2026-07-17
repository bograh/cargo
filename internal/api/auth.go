package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/db/sqlc"
)

type credentialsBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func userJSON(u sqlc.User) map[string]any {
	return map[string]any{
		"id":                u.ID,
		"email":             u.Email,
		"is_instance_admin": u.IsInstanceAdmin,
	}
}

func (s *Server) setAuthCookies(w http.ResponseWriter, tok auth.Tokens) {
	secure := s.cfg.Env == "production"
	http.SetCookie(w, &http.Cookie{
		Name: "cargo_access", Value: tok.Access, Path: "/",
		Expires: tok.AccessExpiresAt, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: "cargo_refresh", Value: tok.Refresh, Path: "/api/v1/auth",
		Expires: tok.RefreshExpiresAt, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearAuthCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "cargo_access", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.SetCookie(w, &http.Cookie{Name: "cargo_refresh", Value: "", Path: "/api/v1/auth", MaxAge: -1, HttpOnly: true})
}

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentialsBody, bool) {
	var body credentialsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return body, false
	}
	if body.Email == "" || body.Password == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "email and password are required")
		return body, false
	}
	return body, true
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	u, tok, err := s.auth.Register(r.Context(), body.Email, body.Password)
	switch {
	case errors.Is(err, auth.ErrEmailTaken):
		Error(w, http.StatusConflict, "email_taken", err.Error())
		return
	case errors.Is(err, auth.ErrWeakPassword):
		Error(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	case err != nil:
		Error(w, http.StatusInternalServerError, "internal", "registration failed")
		return
	}
	s.setAuthCookies(w, tok)
	writeJSON(w, http.StatusCreated, userJSON(u))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	u, tok, err := s.auth.Login(r.Context(), body.Email, body.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		Error(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "login failed")
		return
	}
	s.setAuthCookies(w, tok)
	writeJSON(w, http.StatusOK, userJSON(u))
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("cargo_refresh")
	if err != nil {
		Error(w, http.StatusUnauthorized, "unauthenticated", "refresh token missing")
		return
	}
	tok, err := s.auth.Refresh(r.Context(), c.Value)
	if err != nil {
		s.clearAuthCookies(w)
		Error(w, http.StatusUnauthorized, "unauthenticated", "session expired, log in again")
		return
	}
	s.setAuthCookies(w, tok)
	writeJSON(w, http.StatusOK, map[string]string{"status": "refreshed"})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("cargo_access"); err == nil {
		_ = s.auth.Logout(r.Context(), c.Value)
	}
	s.clearAuthCookies(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userJSON(userFrom(r.Context())))
}
