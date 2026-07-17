package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

type stubSettings struct{ values map[string]string }

func (s stubSettings) GetInstanceSetting(_ context.Context, key string) (sqlc.InstanceSetting, error) {
	v, ok := s.values[key]
	if !ok {
		return sqlc.InstanceSetting{}, pgx.ErrNoRows
	}
	return sqlc.InstanceSetting{Key: key, Value: []byte(v)}, nil
}

func TestGetInstanceInfo(t *testing.T) {
	s := &Server{settings: stubSettings{values: map[string]string{
		"apps_domain_suffix": `"apps.example.com"`,
	}}}
	r := NewRouter(s)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/info", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["apps_domain_suffix"] != "apps.example.com" {
		t.Fatalf("apps_domain_suffix = %q", body["apps_domain_suffix"])
	}
	if body["version"] == "" {
		t.Fatal("version missing")
	}
}

func TestGetInstanceInfoDefaults(t *testing.T) {
	s := &Server{settings: stubSettings{values: map[string]string{}}}
	r := NewRouter(s)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/info", nil)
	r.ServeHTTP(rec, req)

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["apps_domain_suffix"] != "apps.localhost" {
		t.Fatalf("default = %q", body["apps_domain_suffix"])
	}
}
