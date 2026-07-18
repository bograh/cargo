package oidc

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// mockIDP is an httptest OIDC provider: discovery, JWKS, and a token endpoint
// returning an RS256 id_token with controllable claims.
type mockIDP struct {
	server *httptest.Server
	key    *rsa.PrivateKey

	// claims embedded in the next id_token
	Sub           string
	Email         string
	EmailVerified bool
	Nonce         string
	ClientID      string
}

func newMockIDP(t *testing.T) *mockIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &mockIDP{key: key, EmailVerified: true, ClientID: "cargo"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"issuer":                                idp.server.URL,
			"authorization_endpoint":                idp.server.URL + "/authorize",
			"token_endpoint":                        idp.server.URL + "/token",
			"jwks_uri":                              idp.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "test",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss":            idp.server.URL,
			"aud":            idp.ClientID,
			"sub":            idp.Sub,
			"email":          idp.Email,
			"email_verified": idp.EmailVerified,
			"nonce":          idp.Nonce,
			"iat":            now.Unix(),
			"exp":            now.Add(time.Hour).Unix(),
		})
		tok.Header["kid"] = "test"
		signed, err := tok.SignedString(idp.key)
		if err != nil {
			t.Errorf("sign id_token: %v", err)
			http.Error(w, "sign failed", http.StatusInternalServerError)
			return
		}
		writeJSON(t, w, map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     signed,
		})
	})
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (m *mockIDP) issuer() string { return m.server.URL }

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode json: %v", err)
	}
}
