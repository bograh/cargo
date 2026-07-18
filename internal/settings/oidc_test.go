package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
)

func TestOIDCRoundTripEncrypted(t *testing.T) {
	svc, pool := startService(t)
	ctx := context.Background()

	cfg, err := svc.OIDC(ctx)
	if err != nil || cfg != nil {
		t.Fatalf("unset = %v, %v", cfg, err)
	}
	in := OIDCConfig{IssuerURL: "https://keycloak.example.com/realms/cargo", ClientID: "cargo-web-client", ClientSecret: "oidc-client-secret"}
	if err := svc.SetOIDC(ctx, in); err != nil {
		t.Fatalf("set: %v", err)
	}
	out, err := svc.OIDC(ctx)
	if err != nil || out == nil || *out != in {
		t.Fatalf("get = %+v, %v", out, err)
	}
	row, err := sqlc.New(pool).GetInstanceSetting(ctx, "oidc")
	if err != nil {
		t.Fatal(err)
	}
	raw := string(row.Value)
	if strings.Contains(raw, "oidc-client-secret") {
		t.Fatal("oidc client secret stored in plaintext")
	}
	if strings.Contains(raw, "cargo-web-client") {
		t.Fatal("oidc client id stored in plaintext")
	}
	if err := svc.ClearOIDC(ctx); err != nil {
		t.Fatal(err)
	}
	if out, err := svc.OIDC(ctx); err != nil || out != nil {
		t.Fatalf("after clear = %v, %v", out, err)
	}
}

func TestSetOIDCValidation(t *testing.T) {
	svc, _ := startService(t)
	ctx := context.Background()

	good := OIDCConfig{IssuerURL: "https://keycloak.example.com/realms/cargo", ClientID: "id", ClientSecret: "secret"}
	bad := []OIDCConfig{
		{IssuerURL: "", ClientID: "id", ClientSecret: "secret"},
		{IssuerURL: good.IssuerURL, ClientID: "", ClientSecret: "secret"},
		{IssuerURL: good.IssuerURL, ClientID: "id", ClientSecret: ""},
		{IssuerURL: "http://keycloak.example.com/realms/cargo", ClientID: "id", ClientSecret: "secret"},
		{IssuerURL: "ftp://keycloak.example.com/realms/cargo", ClientID: "id", ClientSecret: "secret"},
		{IssuerURL: "not a url", ClientID: "id", ClientSecret: "secret"},
		{IssuerURL: "https://", ClientID: "id", ClientSecret: "secret"},
	}
	for _, cfg := range bad {
		if err := svc.SetOIDC(ctx, cfg); !errors.Is(err, ErrValidation) {
			t.Fatalf("cfg %+v err = %v", cfg, err)
		}
	}
	// http is allowed for loopback hosts only (dev Keycloak).
	for _, u := range []string{"http://localhost:8080/realms/cargo", "http://127.0.0.1:8080/realms/cargo", "http://[::1]:8080/realms/cargo"} {
		cfg := good
		cfg.IssuerURL = u
		if err := svc.SetOIDC(ctx, cfg); err != nil {
			t.Fatalf("loopback %q err = %v", u, err)
		}
	}
}
