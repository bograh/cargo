package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/bograh/cargo/internal/oidc"
	"github.com/bograh/cargo/internal/settings"
)

const (
	oidcStateCookie = "cargo_oidc_state"
	oidcCookiePath  = "/api/v1/auth/oidc"
	oidcStateTTL    = 10 * time.Minute
)

// oidcState is sealed into the state cookie — no server-side state.
type oidcState struct {
	State string `json:"state"`
	Nonce string `json:"nonce"`
	Exp   int64  `json:"exp"`
}

func (s *Server) handleAuthProviders(w http.ResponseWriter, r *http.Request) {
	configured := false
	if s.oidc != nil {
		var err error
		if configured, err = s.oidc.Configured(r.Context()); err != nil {
			Error(w, http.StatusInternalServerError, "internal", "provider lookup failed")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"password": true, "oidc": configured})
}

func (s *Server) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	state, err := randToken()
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "sso start failed")
		return
	}
	nonce, err := randToken()
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "sso start failed")
		return
	}
	authURL, err := s.oidc.StartURL(r.Context(), oidcRedirectURL(r), state, nonce)
	if errors.Is(err, oidc.ErrNotConfigured) {
		Error(w, http.StatusConflict, "oidc_not_configured", "SSO is not configured")
		return
	}
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "sso start failed")
		return
	}
	raw, err := json.Marshal(oidcState{State: state, Nonce: nonce, Exp: time.Now().Add(oidcStateTTL).Unix()})
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "sso start failed")
		return
	}
	sealed, err := s.box.Seal(raw)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "sso start failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookie, Value: base64.RawURLEncoding.EncodeToString(sealed),
		Path: oidcCookiePath, MaxAge: int(oidcStateTTL.Seconds()),
		HttpOnly: true, Secure: s.cfg.Env == "production", SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	s.clearOIDCStateCookie(w)
	st, ok := s.unsealOIDCState(r)
	if !ok {
		redirectOIDCErr(w, r)
		return
	}
	gotState := r.URL.Query().Get("state")
	if subtle.ConstantTimeCompare([]byte(gotState), []byte(st.State)) != 1 {
		redirectOIDCErr(w, r)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		redirectOIDCErr(w, r)
		return
	}
	user, err := s.oidc.ResolveCallback(r.Context(), oidcRedirectURL(r), code, st.Nonce)
	if err != nil {
		redirectOIDCErr(w, r)
		return
	}
	tok, err := s.auth.IssueSession(r.Context(), user.ID)
	if err != nil {
		redirectOIDCErr(w, r)
		return
	}
	s.setAuthCookies(w, tok)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) unsealOIDCState(r *http.Request) (oidcState, bool) {
	c, err := r.Cookie(oidcStateCookie)
	if err != nil {
		return oidcState{}, false
	}
	sealed, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return oidcState{}, false
	}
	raw, err := s.box.Open(sealed)
	if err != nil {
		return oidcState{}, false
	}
	var st oidcState
	if err := json.Unmarshal(raw, &st); err != nil {
		return oidcState{}, false
	}
	if time.Now().Unix() > st.Exp {
		return oidcState{}, false
	}
	return st, true
}

func (s *Server) clearOIDCStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookie, Value: "", Path: oidcCookiePath, MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.Env == "production", SameSite: http.SameSiteLaxMode,
	})
}

func redirectOIDCErr(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/login?error=oidc", http.StatusFound)
}

// oidcRedirectURL reconstructs the externally-visible callback URL.
func oidcRedirectURL(r *http.Request) string {
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	return proto + "://" + r.Host + "/api/v1/auth/oidc/callback"
}

func randToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Server) handleGetOIDC(w http.ResponseWriter, r *http.Request) {
	issuer, clientID, configured, err := s.oidc.PublicConfig(r.Context())
	if err != nil {
		oidcError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": configured,
		"issuer_url": issuer,
		"client_id":  clientID,
		// client secret intentionally omitted (write-only)
	})
}

func (s *Server) handlePutOIDC(w http.ResponseWriter, r *http.Request) {
	var cfg settings.OIDCConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if err := s.oidc.SetConfig(r.Context(), cfg); err != nil {
		oidcError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleDeleteOIDC(w http.ResponseWriter, r *http.Request) {
	if err := s.oidc.ClearConfig(r.Context()); err != nil {
		oidcError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func oidcError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, oidc.ErrValidation), errors.Is(err, settings.ErrValidation):
		Error(w, http.StatusBadRequest, "validation_failed", err.Error())
	case errors.Is(err, oidc.ErrNotConfigured):
		Error(w, http.StatusConflict, "oidc_not_configured", "SSO is not configured")
	default:
		Error(w, http.StatusInternalServerError, "internal", "oidc operation failed")
	}
}
