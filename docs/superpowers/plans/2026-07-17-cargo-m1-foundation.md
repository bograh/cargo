# Cargo M1 — Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A booting Cargo controlplane: Go API with config, Postgres (goose migrations + sqlc), error envelope, embedded React shell, multi-stage Dockerfile, dev compose stack, and CI — the foundation every later milestone builds on.

**Architecture:** Single Go binary (`cargod`) serving a chi REST API under `/api/v1` and an embedded Vite/React SPA for all other routes. Postgres 16 is the only state store (goose runs migrations at startup; sqlc generates type-safe queries). Dockerized via multi-stage build; local dev via `deploy/docker-compose.dev.yml`.

**Tech Stack:** Go 1.24, chi v5, pgx v5, goose v3, sqlc, testcontainers-go · Node 22, Vite 6, React 19, TypeScript, Tailwind CSS v4, Vitest · Docker + compose plugin · GitHub Actions.

## Global Constraints

- All work happens on the `dev` branch of `~/Code/cargo` (remote: `git@github.com:bograh/cargo.git`).
- Go module path: `github.com/bograh/cargo`. Binary name: `cargod`.
- API error responses MUST use the envelope `{ "error": { "code", "message", "fields?" } }` from Task 5.
- Every task is TDD where a test cycle makes sense: failing test → minimal implementation → passing test → commit.
- Commit messages: conventional commits (`feat:`, `chore:`, `test:`, `ci:`).
- Do NOT push to origin unless the user explicitly asks.
- Tool versions: Go ≥ 1.24, Node ≥ 22, Postgres 16, golangci-lint v2 config format.

---

### Task 1: Go module + health endpoint

**Files:**
- Create: `go.mod`
- Create: `cmd/server/main.go`
- Create: `internal/api/router.go`
- Create: `internal/api/health.go`
- Test: `internal/api/health_test.go`

**Interfaces:**
- Produces: `api.NewRouter() *chi.Mux` (extended in Task 5), `api.HealthHandler` (`http.HandlerFunc`).

- [ ] **Step 1: Write the failing test**

`internal/api/health_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	r := NewRouter()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %q", body["status"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/`
Expected: FAIL — `go.mod` missing / `NewRouter` undefined.

- [ ] **Step 3: Initialize module and write minimal implementation**

```bash
go mod init github.com/bograh/cargo
go get github.com/go-chi/chi/v5@latest
```

`internal/api/router.go`:

```go
package api

import "github.com/go-chi/chi/v5"

func NewRouter() *chi.Mux {
	r := chi.NewMux()
	r.Get("/healthz", HealthHandler)
	return r
}
```

`internal/api/health.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
)

func HealthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
```

`cmd/server/main.go`:

```go
package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/bograh/cargo/internal/api"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	r := api.NewRouter()
	slog.Info("cargo listening", "addr", ":8080")
	if err := http.ListenAndServe(":8080", r); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/ -v`
Expected: PASS — `TestHealthz`.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum cmd/ internal/
git commit -m "feat: bootstrap Go module with health endpoint"
```

---

### Task 2: Config loading with validation

**Files:**
- Create: `internal/config/config.go`
- Modify: `cmd/server/main.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config` with fields `HTTPAddr string`, `DatabaseURL string`, `MasterKey []byte`, `DataDir string`, `Env string`; `config.Load(getenv func(string) string) (Config, error)`. Consumed by `cmd/server/main.go` (this task) and `api.NewServer` (Task 5).

- [ ] **Step 1: Write the failing test**

`internal/config/config_test.go`:

```go
package config

import "testing"

const validMasterKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func envWith(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(envWith(map[string]string{
		"CARGO_DATABASE_URL": "postgres://cargo:cargo@localhost:5432/cargo",
		"CARGO_MASTER_KEY":   validMasterKey,
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.DataDir != "/var/lib/cargo" {
		t.Errorf("DataDir = %q, want /var/lib/cargo", cfg.DataDir)
	}
	if cfg.Env != "development" {
		t.Errorf("Env = %q, want development", cfg.Env)
	}
	if len(cfg.MasterKey) != 32 {
		t.Errorf("MasterKey len = %d, want 32", len(cfg.MasterKey))
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	_, err := Load(envWith(map[string]string{"CARGO_MASTER_KEY": validMasterKey}))
	if err == nil {
		t.Fatal("expected error for missing CARGO_DATABASE_URL")
	}
}

func TestLoadRejectsShortMasterKey(t *testing.T) {
	_, err := Load(envWith(map[string]string{
		"CARGO_DATABASE_URL": "postgres://x",
		"CARGO_MASTER_KEY":   "abcd",
	}))
	if err == nil {
		t.Fatal("expected error for short master key")
	}
}

func TestLoadRejectsInvalidEnv(t *testing.T) {
	_, err := Load(envWith(map[string]string{
		"CARGO_DATABASE_URL": "postgres://x",
		"CARGO_MASTER_KEY":   validMasterKey,
		"CARGO_ENV":          "staging",
	}))
	if err == nil {
		t.Fatal("expected error for invalid CARGO_ENV")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL — `Load` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/config/config.go`:

```go
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	MasterKey   []byte
	DataDir     string
	Env         string
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		HTTPAddr:    ":8080",
		DatabaseURL: getenv("CARGO_DATABASE_URL"),
		DataDir:     "/var/lib/cargo",
		Env:         "development",
	}
	if v := getenv("CARGO_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	if v := getenv("CARGO_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := getenv("CARGO_ENV"); v != "" {
		cfg.Env = v
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("CARGO_DATABASE_URL is required")
	}
	keyHex := getenv("CARGO_MASTER_KEY")
	if keyHex == "" {
		return Config{}, errors.New("CARGO_MASTER_KEY is required (64 hex chars)")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return Config{}, fmt.Errorf("CARGO_MASTER_KEY must decode to 32 bytes")
	}
	cfg.MasterKey = key
	if cfg.Env != "development" && cfg.Env != "production" {
		return Config{}, fmt.Errorf("CARGO_ENV must be development or production, got %q", cfg.Env)
	}
	return cfg, nil
}
```

`cmd/server/main.go` (replace entire file):

```go
package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/bograh/cargo/internal/api"
	"github.com/bograh/cargo/internal/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	r := api.NewRouter()
	slog.Info("cargo listening", "addr", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, r); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS — all 4 config tests + health test.

- [ ] **Step 5: Commit**

```bash
git add internal/config/ cmd/
git commit -m "feat: add env config loading with validation"
```

---

### Task 3: Postgres pool + goose migrations

**Files:**
- Create: `internal/db/db.go`
- Create: `internal/db/migrate.go`
- Create: `internal/db/migrations/00001_init.sql`
- Create: `internal/db/migrations/00002_instance_settings.sql`
- Test: `internal/db/db_test.go`

**Interfaces:**
- Produces: `db.Open(ctx context.Context, url string) (*pgxpool.Pool, error)`, `db.Migrate(ctx context.Context, url string) error`. Consumed by `cmd/server/main.go` (Task 5 rewires main) and all later DB tests.

- [ ] **Step 1: Write the failing test**

`internal/db/db_test.go`:

```go
package db

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("cargo"),
		tcpostgres.WithUsername("cargo"),
		tcpostgres.WithPassword("cargo"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return url
}

func TestOpenAndMigrate(t *testing.T) {
	url := startPostgres(t)
	ctx := context.Background()

	if err := Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	var exists bool
	err = pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT FROM information_schema.tables
		WHERE table_name = 'instance_settings')`).Scan(&exists)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !exists {
		t.Fatal("instance_settings table missing after migrate")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go mod tidy && go test ./internal/db/ -v`
Expected: FAIL — `Open`/`Migrate` undefined (deps: `go get github.com/jackc/pgx/v5@latest github.com/pressly/goose/v3@latest github.com/testcontainers/testcontainers-go/modules/postgres@latest`).

- [ ] **Step 3: Write minimal implementation**

`internal/db/db.go`:

```go
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}
```

`internal/db/migrate.go`:

```go
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Migrate(ctx context.Context, url string) error {
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("dialect: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, "migrations"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
```

`internal/db/migrations/00001_init.sql`:

```sql
-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- +goose Down
DROP EXTENSION IF EXISTS pgcrypto;
```

`internal/db/migrations/00002_instance_settings.sql`:

```sql
-- +goose Up
CREATE TABLE instance_settings (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE instance_settings;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/db/ -v`
Expected: PASS — `TestOpenAndMigrate` (requires Docker running locally).

- [ ] **Step 5: Commit**

```bash
git add internal/db/ go.mod go.sum
git commit -m "feat: add postgres pool and goose migrations"
```

---

### Task 4: sqlc codegen + settings queries

**Files:**
- Create: `sqlc.yaml`
- Create: `internal/db/queries/settings.sql`
- Create: `internal/db/sqlc/` (generated — committed)
- Test: `internal/db/settings_test.go`

**Interfaces:**
- Produces: `sqlc.New(pool) *sqlc.Queries` (import path `github.com/bograh/cargo/internal/db/sqlc`); `(*Queries).GetInstanceSetting(ctx, key string) (sqlc.InstanceSetting, error)`; `(*Queries).UpsertInstanceSetting(ctx, sqlc.UpsertInstanceSettingParams{Key string, Value []byte}) (sqlc.InstanceSetting, error)`. `InstanceSetting` has fields `Key string`, `Value []byte`, `UpdatedAt pgtype.Timestamptz`.

- [ ] **Step 1: Write the failing test**

`internal/db/settings_test.go`:

```go
package db

import (
	"context"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
)

func TestSettingsRoundTrip(t *testing.T) {
	url := startPostgres(t)
	ctx := context.Background()
	if err := Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	q := sqlc.New(pool)
	_, err = q.UpsertInstanceSetting(ctx, sqlc.UpsertInstanceSettingParams{
		Key:   "apps_domain_suffix",
		Value: []byte(`"apps.example.com"`),
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	row, err := q.GetInstanceSetting(ctx, "apps_domain_suffix")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(row.Value) != `"apps.example.com"` {
		t.Fatalf("value = %s", row.Value)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db/ -run TestSettingsRoundTrip -v`
Expected: FAIL — `internal/db/sqlc` package does not exist.

- [ ] **Step 3: Configure sqlc, write query, generate**

`sqlc.yaml`:

```yaml
version: "2"
sql:
  - engine: postgresql
    schema: internal/db/migrations
    queries: internal/db/queries
    gen:
      go:
        package: sqlc
        out: internal/db/sqlc
        sql_package: pgx/v5
```

`internal/db/queries/settings.sql`:

```sql
-- name: GetInstanceSetting :one
SELECT key, value, updated_at FROM instance_settings WHERE key = $1;

-- name: UpsertInstanceSetting :one
INSERT INTO instance_settings (key, value)
VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
RETURNING key, value, updated_at;
```

Generate (installs sqlc if needed):

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
sqlc generate
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go mod tidy && go test ./internal/db/ -v`
Expected: PASS — both db tests.

- [ ] **Step 5: Commit**

```bash
git add sqlc.yaml internal/db/
git commit -m "feat: add sqlc codegen with instance settings queries"
```

---

### Task 5: API plumbing — Server, error envelope, middleware, instance info endpoint

**Files:**
- Create: `internal/api/errors.go`
- Create: `internal/api/server.go`
- Create: `internal/api/instance.go`
- Modify: `internal/api/router.go`
- Modify: `internal/api/health_test.go` (router signature change)
- Modify: `cmd/server/main.go`
- Test: `internal/api/errors_test.go`
- Test: `internal/api/instance_test.go`

**Interfaces:**
- Consumes: `config.Config`, `db.Open`, `db.Migrate`, `sqlc.Queries.GetInstanceSetting`.
- Produces: `api.NewServer(cfg config.Config, pool *pgxpool.Pool) *Server`; `api.NewRouter(s *Server) *chi.Mux`; `api.Error(w http.ResponseWriter, status int, code, message string)`; `Server.Handler() *chi.Mux`. Later milestones mount routes inside `NewRouter`.

- [ ] **Step 1: Write the failing tests**

`internal/api/errors_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	Error(rec, http.StatusBadRequest, "validation_failed", "name is required")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "validation_failed" || body.Error.Message != "name is required" {
		t.Fatalf("unexpected body: %+v", body.Error)
	}
}
```

`internal/api/instance_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -v`
Expected: FAIL — `Error`, `Server`, `NewRouter(s)` undefined; `TestHealthz` also breaks (router signature change) — expected at this step.

- [ ] **Step 3: Write implementation**

`internal/api/errors.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
)

type errorEnvelope struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields,omitempty"`
	} `json:"error"`
}

// Error writes the standard API error envelope.
func Error(w http.ResponseWriter, status int, code, message string) {
	var env errorEnvelope
	env.Error.Code = code
	env.Error.Message = message
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
```

`internal/api/health_test.go` — update for the new router signature (`/healthz` does not touch the server, so nil is fine):

```go
	r := NewRouter(nil)
```

`internal/api/server.go`:

```go
package api

import (
	"context"

	"github.com/bograh/cargo/internal/config"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SettingsStore is satisfied by *dbsqlc.Queries.
type SettingsStore interface {
	GetInstanceSetting(ctx context.Context, key string) (sqlc.InstanceSetting, error)
}

type Server struct {
	cfg      config.Config
	pool     *pgxpool.Pool
	settings SettingsStore
}

// NewServer builds a Server. pool may be nil in tests that stub settings.
func NewServer(cfg config.Config, pool *pgxpool.Pool) *Server {
	s := &Server{cfg: cfg, pool: pool}
	if pool != nil {
		s.settings = sqlc.New(pool)
	}
	return s
}

func (s *Server) Handler() *chi.Mux { return NewRouter(s) }
```

`internal/api/instance.go`:

```go
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
)

const version = "0.1.0"

func (s *Server) getInstanceInfo(w http.ResponseWriter, r *http.Request) {
	suffix := s.settingOrDefault(r, "apps_domain_suffix", "apps.localhost")
	writeJSON(w, http.StatusOK, map[string]string{
		"version":            version,
		"apps_domain_suffix": suffix,
	})
}

// settingOrDefault reads a JSON-string setting, falling back on missing rows.
func (s *Server) settingOrDefault(r *http.Request, key, def string) string {
	row, err := s.settings.GetInstanceSetting(r.Context(), key)
	if errors.Is(err, pgx.ErrNoRows) {
		return def
	}
	if err != nil {
		slog.Warn("instance setting read failed", "key", key, "err", err)
		return def
	}
	var v string
	if err := json.Unmarshal(row.Value, &v); err != nil {
		slog.Warn("instance setting decode failed", "key", key, "err", err)
		return def
	}
	return v
}
```

`internal/api/router.go` (replace entire file):

```go
package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(s *Server) *chi.Mux {
	r := chi.NewMux()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	r.Get("/healthz", HealthHandler)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/instance/info", s.getInstanceInfo)
	})
	return r
}
```

`cmd/server/main.go` (replace entire file):

```go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/bograh/cargo/internal/api"
	"github.com/bograh/cargo/internal/config"
	"github.com/bograh/cargo/internal/db"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}
	ctx := context.Background()
	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		slog.Error("migrations failed", "err", err)
		os.Exit(1)
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database unavailable", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	srv := api.NewServer(cfg, pool)
	slog.Info("cargo listening", "addr", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, srv.Handler()); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go mod tidy && go test ./... -v`
Expected: PASS — health, config, db (with Docker), errors, instance tests. Note: `internal/api/instance_test.go` constructs `Server` directly with a stub, so it needs no database.

- [ ] **Step 5: Commit**

```bash
git add internal/api/ cmd/
git commit -m "feat: add server plumbing, error envelope, instance info endpoint"
```

---

### Task 6: React web scaffold (Vite + TS + Tailwind v4 + Vitest)

**Files:**
- Create: `web/` (Vite app — scaffolded)
- Create: `web/src/App.tsx` (placeholder), `web/src/App.test.tsx`
- Create: `web/vite.config.ts`, `web/src/index.css`, `web/src/test/setup.ts`

**Interfaces:**
- Produces: `npm run build` (in `web/`) emitting to `internal/webui/dist` (consumed by Task 7 embed); `npm test -- --run` green in CI.

- [ ] **Step 1: Scaffold and write the failing test**

```bash
npm create vite@latest web -- --template react-ts
cd web
npm install
npm install tailwindcss @tailwindcss/vite
npm install -D vitest jsdom @testing-library/react @testing-library/jest-dom
```

`web/src/test/setup.ts`:

```ts
import "@testing-library/jest-dom/vitest";
```

`web/src/App.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import App from "./App";

test("renders cargo placeholder", () => {
  render(<App />);
  expect(screen.getByRole("heading", { name: /cargo/i })).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run`
Expected: FAIL — Vitest config missing (`test` block), `./App` renders Vite starter content (no `cargo` heading).

- [ ] **Step 3: Configure and implement**

`web/vite.config.ts` (replace entire file):

```ts
/// <reference types="vitest/config" />
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
  },
});
```

`web/src/index.css` (replace entire file):

```css
@import "tailwindcss";
```

`web/src/App.tsx` (replace entire file):

```tsx
export default function App() {
  return (
    <main className="min-h-screen bg-slate-950 text-slate-100 flex items-center justify-center">
      <h1 className="text-4xl font-bold tracking-tight">Cargo</h1>
    </main>
  );
}
```

Replace the starter markup in `web/src/main.tsx` only if it imports `App.css` — remove that import; delete `web/src/App.css` and `web/src/assets/react.svg`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run && npm run build`
Expected: PASS — 1 test; build emits `internal/webui/dist/index.html` + assets.

- [ ] **Step 5: Commit**

```bash
printf 'internal/webui/dist/*\n!internal/webui/dist/index.html\n' >> .gitignore
git add web/ internal/webui/dist/index.html .gitignore
git commit -m "feat: scaffold react web app with tailwind and vitest"
```

(The Vite scaffold already provides `web/.gitignore` with `node_modules`/`dist` — leave it as-is.)

Note: the committed placeholder `internal/webui/dist/index.html` (from the build) keeps `go build`/`go test` working without Node. Vite overwrites it on every real build.

---

### Task 7: Embed the SPA in the Go binary

**Files:**
- Create: `internal/webui/webui.go`
- Modify: `internal/api/router.go`
- Test: `internal/webui/webui_test.go`

**Interfaces:**
- Consumes: `web/` build output at `internal/webui/dist` (Task 6).
- Produces: `webui.Handler() http.Handler` — static files + SPA fallback (unknown paths without a file extension serve `index.html`; missing paths with an extension return 404). Mounted last in `NewRouter`.

- [ ] **Step 1: Write the failing test**

`internal/webui/webui_test.go`:

```go
package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	Handler().ServeHTTP(rec, req)
	return rec
}

func TestServesIndexAtRoot(t *testing.T) {
	rec := get(t, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(strings.ToLower(string(body)), "<html") {
		t.Fatal("expected html document")
	}
}

func TestSPAFallbackForClientRoutes(t *testing.T) {
	rec := get(t, "/orgs/some-org/apps")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMissingAssetReturns404(t *testing.T) {
	rec := get(t, "/assets/does-not-exist.js")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/webui/`
Expected: FAIL — `Handler` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/webui/webui.go`:

```go
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the embedded SPA. Paths that look like files are served
// directly (404 if missing); all other paths fall back to index.html so
// client-side routing works.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(r.URL.Path)
		if clean == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		if strings.Contains(path.Base(clean), ".") {
			if _, err := fs.Stat(sub, strings.TrimPrefix(clean, "/")); err != nil {
				http.NotFound(w, r)
				return
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}
```

`internal/api/router.go` — add mount at the end of `NewRouter` (import `github.com/bograh/cargo/internal/webui`):

```go
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/instance/info", s.getInstanceInfo)
	})
	r.Mount("/", webui.Handler())
	return r
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS — all packages.

- [ ] **Step 5: Commit**

```bash
git add internal/webui/webui.go internal/webui/webui_test.go internal/api/router.go
git commit -m "feat: embed react spa with client-side route fallback"
```

---

### Task 8: Dockerfile + dev compose stack

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`
- Create: `deploy/docker-compose.dev.yml`

**Interfaces:**
- Consumes: everything so far. Produces: `docker compose -f deploy/docker-compose.dev.yml up -d --build` → controlplane on `:8080` answering `/healthz` and `/api/v1/instance/info`.

- [ ] **Step 1: Write the files**

`Dockerfile`:

```dockerfile
FROM node:22-alpine AS web
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.24-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -o /bin/cargod ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates docker-cli docker-cli-compose
COPY --from=build /bin/cargod /usr/local/bin/cargod
ENTRYPOINT ["cargod"]
```

`.dockerignore`:

```
node_modules
web/node_modules
internal/webui/dist
.git
docs
```

`deploy/docker-compose.dev.yml`:

```yaml
services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: cargo
      POSTGRES_PASSWORD: cargo
      POSTGRES_DB: cargo
    volumes:
      - cargo-dev-db:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U cargo"]
      interval: 2s
      timeout: 3s
      retries: 20

  controlplane:
    build:
      context: ..
      dockerfile: Dockerfile
    depends_on:
      db:
        condition: service_healthy
    environment:
      CARGO_DATABASE_URL: postgres://cargo:cargo@db:5432/cargo
      CARGO_MASTER_KEY: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
      CARGO_HTTP_ADDR: ":8080"
      CARGO_DATA_DIR: /var/lib/cargo
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - cargo-dev-data:/var/lib/cargo
    ports:
      - "8080:8080"

volumes:
  cargo-dev-db:
  cargo-dev-data:
```

- [ ] **Step 2: Verify the stack boots and answers**

```bash
docker compose -f deploy/docker-compose.dev.yml up -d --build
curl -s http://localhost:8080/healthz
curl -s http://localhost:8080/api/v1/instance/info
curl -s http://localhost:8080/ | head -c 120
```

Expected: `{"status":"ok"}` · JSON with `version` + `apps_domain_suffix` · HTML document. Then leave the stack running or `docker compose -f deploy/docker-compose.dev.yml down`.

- [ ] **Step 3: Commit**

```bash
git add Dockerfile .dockerignore deploy/
git commit -m "feat: add multi-stage dockerfile and dev compose stack"
```

---

### Task 9: CI + lint config

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `.golangci.yml`

**Interfaces:**
- Consumes: all tasks. Produces: green CI on every push/PR to `dev`.

- [ ] **Step 1: Write the files**

`.golangci.yml`:

```yaml
version: "2"
linters:
  enable:
    - errcheck
    - govet
    - staticcheck
    - gofmt
    - goimports
```

`.github/workflows/ci.yml`:

```yaml
name: ci
on:
  push:
    branches: [dev]
  pull_request:

jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.24"
      - uses: golangci/golangci-lint-action@v7
      - run: go test ./... -count=1

  web:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: "22"
          cache: npm
          cache-dependency-path: web/package-lock.json
      - run: npm ci
        working-directory: web
      - run: npx vitest run
        working-directory: web
      - run: npm run build
        working-directory: web

  docker:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: docker build .
```

- [ ] **Step 2: Verify locally**

Run: `golangci-lint run ./... && go test ./... && (cd web && npx vitest run && npm run build)`
Expected: all green.

- [ ] **Step 3: Commit**

```bash
git add .github/ .golangci.yml
git commit -m "ci: add lint, test, and docker build workflows"
```

---

## M1 Done Criteria

- `go test ./...` and `npx vitest run` green locally and in CI.
- `docker compose -f deploy/docker-compose.dev.yml up -d --build` boots; `/healthz`, `/api/v1/instance/info`, and `/` (SPA) all respond.
- Single `cargod` binary contains API + embedded SPA; Postgres is the only state store.
