# Cargo M3 — Deployment Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement PhasedPlans.md Phase 2 — applications CRUD, AES-256-GCM env vars/registry creds, River job queue, Dockerfile/Nixpacks builders, per-app Compose reconciler with Traefik labels, deployments with a status machine, live SSE logs, and rollback (FR-3, FR-4 minus webhooks, FR-6).

**Architecture:** New packages `crypto` (AES-GCM), `apps` (app + env var CRUD), `events` (SSE hub), `deployments` (records + log files), `builder` (git clone + dockerfile/nixpacks via CLI), `reconciler` (compose generation + `DeployProvider` seam, only package touching Docker), `jobs` (River `deploy` worker running the pipeline). Sources in M3: registry image and **public** git URL (GitHub App auth is Phase 3). All Docker interaction is via the `docker`/`docker compose` CLIs already bundled in the controlplane image.

**Tech Stack:** Go 1.25, chi, pgx/sqlc/goose, `github.com/riverqueue/river` (+ riverpgxv5, rivermigrate), docker CLI + compose plugin, nixpacks CLI, testcontainers.

## Global Constraints

- Module path `github.com/bograh/cargo`; error envelope via `api.Error`; success via `writeJSON`
- Deployment statuses exactly: `queued, building, deploying, live, failed, cancelled` (FR-4.2)
- Triggers exactly: `webhook, manual, rollback` (FR-4.1)
- Source types: `git, image`; builders: `auto, dockerfile, nixpacks`
- Secrets (env values, registry creds) stored only AES-256-GCM-encrypted with `cfg.MasterKey`; `.env` files written mode 0600 (NFR-5)
- Build concurrency default 2; deploys serialized per app via Postgres advisory lock (FR-4.4)
- Retention: last 5 deployments' logs + images per app (design §4)
- Role rules: member+ mutates apps/deploys; viewer reads; non-members 404 (FR-2.4)
- errcheck enforced; `gofmt` everything; sqlc regen: `sqlc generate` (CLI installed)
- Docker-dependent tests: guard with `testing.Short()` skip so `go test -short` stays fast; CI runs full
- App slugs are instance-unique (they become subdomains)

---

### Task 1: Schema migration 00004 + sqlc queries (applications, env_vars, deployments)

**Files:**
- Create: `internal/db/migrations/00004_apps_deployments.sql`
- Create: `internal/db/queries/apps.sql`, `internal/db/queries/envvars.sql`, `internal/db/queries/deployments.sql`
- Test: `internal/db/apps_deployments_test.go`

**Interfaces:**
- Produces sqlc methods used by Tasks 3–10: `CreateApplication`, `GetApplication`, `ListApplicationsForOrg`, `UpdateApplication`, `DeleteApplication`, `UpsertEnvVar`, `ListEnvVars`, `DeleteEnvVar`, `CreateDeployment`, `GetDeployment`, `ListDeploymentsForApp`, `SetDeploymentBuildInfo`, `MarkDeploymentStatus`, `FinishDeployment`, `ListPrunableDeployments`

- [ ] **Step 1: Write the failing test**

`internal/db/apps_deployments_test.go`:

```go
package db

import (
	"context"
	"testing"
)

func TestAppsDeploymentsTablesExist(t *testing.T) {
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

	for _, table := range []string{"applications", "env_vars", "deployments"} {
		var exists bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT FROM information_schema.tables WHERE table_name = $1)`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s missing after migrate", table)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db/ -run TestAppsDeploymentsTablesExist -v` → FAIL (`table applications missing`)

- [ ] **Step 3: Write the migration**

`internal/db/migrations/00004_apps_deployments.sql`:

```sql
-- +goose Up
CREATE TABLE applications (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    slug               TEXT NOT NULL UNIQUE,
    source_type        TEXT NOT NULL CHECK (source_type IN ('git', 'image')),
    builder            TEXT NOT NULL DEFAULT 'auto' CHECK (builder IN ('auto', 'dockerfile', 'nixpacks')),
    git_repo_url       TEXT NOT NULL DEFAULT '',
    git_branch         TEXT NOT NULL DEFAULT '',
    image_ref          TEXT NOT NULL DEFAULT '',
    registry_creds_enc BYTEA,
    exposed_port       INTEGER NOT NULL DEFAULT 8080 CHECK (exposed_port BETWEEN 1 AND 65535),
    healthcheck_path   TEXT NOT NULL DEFAULT '/',
    auto_deploy        BOOLEAN NOT NULL DEFAULT true,
    build_context      TEXT NOT NULL DEFAULT '.',
    dockerfile_path    TEXT NOT NULL DEFAULT 'Dockerfile',
    build_args         JSONB NOT NULL DEFAULT '{}',
    key_version        INTEGER NOT NULL DEFAULT 1,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX applications_org_idx ON applications (org_id);

CREATE TABLE env_vars (
    app_id      UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    key         TEXT NOT NULL,
    value_enc   BYTEA NOT NULL,
    key_version INTEGER NOT NULL DEFAULT 1,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, key)
);

CREATE TABLE deployments (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id      UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    trigger     TEXT NOT NULL CHECK (trigger IN ('webhook', 'manual', 'rollback')),
    status      TEXT NOT NULL DEFAULT 'queued'
                CHECK (status IN ('queued', 'building', 'deploying', 'live', 'failed', 'cancelled')),
    commit_sha  TEXT NOT NULL DEFAULT '',
    image_tag   TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    actor       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
CREATE INDEX deployments_app_idx ON deployments (app_id, created_at DESC);

-- +goose Down
DROP TABLE deployments;
DROP TABLE env_vars;
DROP TABLE applications;
```

- [ ] **Step 4: Write the queries**

`internal/db/queries/apps.sql`:

```sql
-- name: CreateApplication :one
INSERT INTO applications (
    org_id, name, slug, source_type, builder, git_repo_url, git_branch, image_ref,
    registry_creds_enc, exposed_port, healthcheck_path, auto_deploy,
    build_context, dockerfile_path, build_args
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
RETURNING *;

-- name: GetApplication :one
SELECT * FROM applications WHERE id = $1;

-- name: ListApplicationsForOrg :many
SELECT * FROM applications WHERE org_id = $1 ORDER BY created_at;

-- name: UpdateApplication :one
UPDATE applications SET
    name = $2, builder = $3, git_branch = $4, image_ref = $5,
    exposed_port = $6, healthcheck_path = $7, auto_deploy = $8,
    build_context = $9, dockerfile_path = $10, build_args = $11,
    registry_creds_enc = $12, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteApplication :exec
DELETE FROM applications WHERE id = $1;
```

`internal/db/queries/envvars.sql`:

```sql
-- name: UpsertEnvVar :exec
INSERT INTO env_vars (app_id, key, value_enc)
VALUES ($1, $2, $3)
ON CONFLICT (app_id, key) DO UPDATE SET value_enc = EXCLUDED.value_enc, updated_at = now();

-- name: ListEnvVars :many
SELECT * FROM env_vars WHERE app_id = $1 ORDER BY key;

-- name: DeleteEnvVar :exec
DELETE FROM env_vars WHERE app_id = $1 AND key = $2;
```

`internal/db/queries/deployments.sql`:

```sql
-- name: CreateDeployment :one
INSERT INTO deployments (app_id, trigger, actor, image_tag, commit_sha)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetDeployment :one
SELECT * FROM deployments WHERE id = $1;

-- name: ListDeploymentsForApp :many
SELECT * FROM deployments WHERE app_id = $1 ORDER BY created_at DESC LIMIT 50;

-- name: SetDeploymentBuildInfo :exec
UPDATE deployments SET commit_sha = $2, image_tag = $3 WHERE id = $1;

-- name: MarkDeploymentStatus :exec
UPDATE deployments SET status = $2,
    started_at = COALESCE(started_at, now())
WHERE id = $1;

-- name: FinishDeployment :exec
UPDATE deployments SET status = $2, error = $3, finished_at = now() WHERE id = $1;

-- name: ListPrunableDeployments :many
SELECT * FROM deployments WHERE app_id = $1 ORDER BY created_at DESC OFFSET $2;
```

- [ ] **Step 5: `sqlc generate`, then `go build ./...` and re-run the test** → PASS

- [ ] **Step 6: Commit**

```bash
git add internal/db
git commit -m "feat: add applications, env vars, and deployments schema"
```

---

### Task 2: AES-256-GCM crypto box

**Files:**
- Create: `internal/crypto/box.go`
- Test: `internal/crypto/box_test.go`

**Interfaces:**
- Produces: `crypto.New(key []byte) (*Box, error)` (key must be 32 bytes); `(*Box).Seal(plaintext []byte) ([]byte, error)` returns `nonce||ciphertext`; `(*Box).Open(data []byte) ([]byte, error)`

- [ ] **Step 1: Write the failing test**

`internal/crypto/box_test.go`:

```go
package crypto

import (
	"bytes"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ct, err := box.Seal([]byte("secret-value"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(ct, []byte("secret-value")) {
		t.Fatal("ciphertext contains plaintext")
	}
	pt, err := box.Open(ct)
	if err != nil || string(pt) != "secret-value" {
		t.Fatalf("Open = %q, %v", pt, err)
	}
	ct[len(ct)-1] ^= 0xFF
	if _, err := box.Open(ct); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("expected error for short key")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/crypto/ -v` → FAIL to build

- [ ] **Step 3: Implement**

`internal/crypto/box.go`:

```go
// Package crypto provides AES-256-GCM sealing for secrets at rest.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

type Box struct {
	aead cipher.AEAD
}

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal returns nonce||ciphertext.
func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (b *Box) Open(data []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(data) < ns {
		return nil, fmt.Errorf("crypto: ciphertext too short")
	}
	return b.aead.Open(nil, data[:ns], data[ns:], nil)
}
```

- [ ] **Step 4: Run** `go test ./internal/crypto/ -v` → PASS

- [ ] **Step 5: Commit** `git add internal/crypto && git commit -m "feat: add aes-256-gcm crypto box"`

---

### Task 3: Apps service (CRUD + env vars, role-scoped)

**Files:**
- Create: `internal/apps/service.go`, `internal/apps/slug.go`
- Test: `internal/apps/service_test.go`, helper `internal/apps/testdb_test.go` (copy of `internal/orgs/testdb_test.go` with `package apps`)

**Interfaces:**
- Consumes: sqlc (Task 1), `crypto.Box` (Task 2), membership rules like `orgs` package
- Produces (also the `api.AppService` surface):
  - `apps.NewService(pool *pgxpool.Pool, box *crypto.Box) *Service`
  - `apps.RegistryCreds{Server, Username, Password string}`
  - `apps.CreateInput{Name, SourceType, Builder, GitRepoURL, GitBranch, ImageRef, HealthcheckPath string; ExposedPort int32; AutoDeploy bool; BuildContext, DockerfilePath string; BuildArgs map[string]string; RegistryCreds *RegistryCreds}`
  - `apps.UpdateInput` — pointer fields for partial update: `Name, Builder, GitBranch, ImageRef, HealthcheckPath, BuildContext, DockerfilePath *string; ExposedPort *int32; AutoDeploy *bool; BuildArgs map[string]string; RegistryCreds *RegistryCreds`
  - Methods (actor-scoped):
    - `Create(ctx, orgID, actor pgtype.UUID, in CreateInput) (sqlc.Application, error)` — member+
    - `List(ctx, orgID, actor pgtype.UUID) ([]sqlc.Application, error)` — any member
    - `Get(ctx, appID, actor pgtype.UUID) (sqlc.Application, error)` — any member; non-member → `ErrNotFound`
    - `Update(ctx, appID, actor pgtype.UUID, in UpdateInput) (sqlc.Application, error)` — member+
    - `Delete(ctx, appID, actor pgtype.UUID) error` — admin+
    - `SetEnvVars(ctx, appID, actor pgtype.UUID, vars map[string]string) error` — member+
    - `ListEnvKeys(ctx, appID, actor pgtype.UUID) ([]string, error)` — any member (keys only, FR-6.2)
    - `DeleteEnvVar(ctx, appID, actor pgtype.UUID, key string) error` — member+
  - Pipeline-facing (no actor): `DecryptedEnv(ctx, appID pgtype.UUID) (map[string]string, error)`, `DecryptedRegistryCreds(ctx context.Context, app sqlc.Application) (*RegistryCreds, error)`
  - Sentinels: `apps.ErrNotFound`, `apps.ErrForbidden`, `apps.ErrValidation` (wrap with detail via `fmt.Errorf("%w: ...", ErrValidation)`)

- [ ] **Step 1: Write failing tests** — `internal/apps/service_test.go`:

```go
package apps

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fixture struct {
	svc            *Service
	orgID          pgtype.UUID
	owner, viewer  pgtype.UUID
	outsider       pgtype.UUID
}

func setup(t *testing.T) (*fixture, *pgxpool.Pool) {
	t.Helper()
	pool := startPool(t)
	box, err := crypto.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(pool)
	orgSvc := orgs.NewService(pool)
	ctx := context.Background()
	reg := func(email string) pgtype.UUID {
		u, _, err := authSvc.Register(ctx, email, "password-123")
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		return u.ID
	}
	owner, viewer, outsider := reg("o@x.co"), reg("v@x.co"), reg("out@x.co")
	org, err := orgSvc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	if err := orgSvc.AddMember(ctx, org.ID, viewer, "viewer"); err != nil {
		t.Fatalf("add viewer: %v", err)
	}
	return &fixture{svc: NewService(pool, box), orgID: org.ID, owner: owner, viewer: viewer, outsider: outsider}, pool
}

func imageInput() CreateInput {
	return CreateInput{Name: "My API", SourceType: "image", ImageRef: "nginx:alpine",
		ExposedPort: 80, HealthcheckPath: "/", AutoDeploy: true}
}

func TestCreateAndGetApp(t *testing.T) {
	f, _ := setup(t)
	ctx := context.Background()
	app, err := f.svc.Create(ctx, f.orgID, f.owner, imageInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if app.Slug != "my-api" || app.SourceType != "image" {
		t.Fatalf("app = %+v", app)
	}
	if _, err := f.svc.Get(ctx, app.ID, f.viewer); err != nil {
		t.Fatalf("viewer get: %v", err)
	}
	if _, err := f.svc.Get(ctx, app.ID, f.outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider get = %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	f, _ := setup(t)
	ctx := context.Background()
	bad := imageInput()
	bad.ImageRef = ""
	if _, err := f.svc.Create(ctx, f.orgID, f.owner, bad); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing image_ref err = %v", err)
	}
	git := CreateInput{Name: "web", SourceType: "git", ExposedPort: 3000}
	if _, err := f.svc.Create(ctx, f.orgID, f.owner, git); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing repo err = %v", err)
	}
	if _, err := f.svc.Create(ctx, f.orgID, f.viewer, imageInput()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer create err = %v", err)
	}
}

func TestDeleteRequiresAdmin(t *testing.T) {
	f, pool := setup(t)
	ctx := context.Background()
	member, _, err := auth.NewService(pool).Register(ctx, "m@x.co", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	if err := orgs.NewService(pool).AddMember(ctx, f.orgID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	app, err := f.svc.Create(ctx, f.orgID, f.owner, imageInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Delete(ctx, app.ID, member.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member delete err = %v", err)
	}
	if err := f.svc.Delete(ctx, app.ID, f.owner); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
}

func TestEnvVarsEncryptedAndWriteOnly(t *testing.T) {
	f, pool := setup(t)
	ctx := context.Background()
	app, err := f.svc.Create(ctx, f.orgID, f.owner, imageInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetEnvVars(ctx, app.ID, f.owner, map[string]string{"DB_URL": "postgres://secret"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	keys, err := f.svc.ListEnvKeys(ctx, app.ID, f.viewer)
	if err != nil || len(keys) != 1 || keys[0] != "DB_URL" {
		t.Fatalf("keys = %v, %v", keys, err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT value_enc FROM env_vars WHERE key='DB_URL'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("postgres://secret")) {
		t.Fatal("env value stored in plaintext")
	}
	env, err := f.svc.DecryptedEnv(ctx, app.ID)
	if err != nil || env["DB_URL"] != "postgres://secret" {
		t.Fatalf("decrypted = %v, %v", env, err)
	}
	if err := f.svc.SetEnvVars(ctx, app.ID, f.viewer, map[string]string{"X": "y"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer set err = %v", err)
	}
	if err := f.svc.DeleteEnvVar(ctx, app.ID, f.owner, "DB_URL"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestRegistryCredsEncrypted(t *testing.T) {
	f, pool := setup(t)
	ctx := context.Background()
	in := imageInput()
	in.RegistryCreds = &RegistryCreds{Server: "ghcr.io", Username: "bot", Password: "hunter2"}
	app, err := f.svc.Create(ctx, f.orgID, f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT registry_creds_enc FROM applications WHERE id=$1`, app.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("hunter2")) {
		t.Fatal("registry password stored in plaintext")
	}
	creds, err := f.svc.DecryptedRegistryCreds(ctx, app)
	if err != nil || creds.Password != "hunter2" {
		t.Fatalf("creds = %v, %v", creds, err)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/apps/ -v` → FAIL to build

- [ ] **Step 3: Implement** — `internal/apps/slug.go` duplicates the tiny slugify/randomSuffix from `internal/orgs/slug.go` (package-local; keep in sync). `internal/apps/service.go`:

```go
package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound   = errors.New("application not found")
	ErrForbidden  = errors.New("insufficient role")
	ErrValidation = errors.New("validation failed")
)

var roleRank = map[string]int{"viewer": 0, "member": 1, "admin": 2, "owner": 3}

type RegistryCreds struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type CreateInput struct {
	Name            string
	SourceType      string
	Builder         string
	GitRepoURL      string
	GitBranch       string
	ImageRef        string
	ExposedPort     int32
	HealthcheckPath string
	AutoDeploy      bool
	BuildContext    string
	DockerfilePath  string
	BuildArgs       map[string]string
	RegistryCreds   *RegistryCreds
}

type UpdateInput struct {
	Name            *string
	Builder         *string
	GitBranch       *string
	ImageRef        *string
	HealthcheckPath *string
	BuildContext    *string
	DockerfilePath  *string
	ExposedPort     *int32
	AutoDeploy      *bool
	BuildArgs       map[string]string
	RegistryCreds   *RegistryCreds
}

type Service struct {
	q   *sqlc.Queries
	box *crypto.Box
}

func NewService(pool *pgxpool.Pool, box *crypto.Box) *Service {
	return &Service{q: sqlc.New(pool), box: box}
}

// roleIn returns the actor's role in org or ErrNotFound (scoping, FR-2.4).
func (s *Service) roleIn(ctx context.Context, orgID, actor pgtype.UUID) (string, error) {
	m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: actor})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return m.Role, err
}

// appFor loads an app and verifies the actor holds at least minRole in its org.
func (s *Service) appFor(ctx context.Context, appID, actor pgtype.UUID, minRole string) (sqlc.Application, error) {
	app, err := s.q.GetApplication(ctx, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Application{}, ErrNotFound
	}
	if err != nil {
		return sqlc.Application{}, err
	}
	role, err := s.roleIn(ctx, app.OrgID, actor)
	if err != nil {
		return sqlc.Application{}, err
	}
	if roleRank[role] < roleRank[minRole] {
		return sqlc.Application{}, ErrForbidden
	}
	return app, nil
}

func validateCreate(in CreateInput) error {
	if in.Name == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	if in.ExposedPort < 1 || in.ExposedPort > 65535 {
		return fmt.Errorf("%w: exposed_port must be 1-65535", ErrValidation)
	}
	switch in.SourceType {
	case "git":
		if in.GitRepoURL == "" || in.GitBranch == "" {
			return fmt.Errorf("%w: git source requires git_repo_url and git_branch", ErrValidation)
		}
	case "image":
		if in.ImageRef == "" {
			return fmt.Errorf("%w: image source requires image_ref", ErrValidation)
		}
	default:
		return fmt.Errorf("%w: source_type must be git or image", ErrValidation)
	}
	switch in.Builder {
	case "", "auto", "dockerfile", "nixpacks":
	default:
		return fmt.Errorf("%w: builder must be auto, dockerfile, or nixpacks", ErrValidation)
	}
	return nil
}

func (s *Service) sealCreds(c *RegistryCreds) ([]byte, error) {
	if c == nil {
		return nil, nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return s.box.Seal(raw)
}

func (s *Service) Create(ctx context.Context, orgID, actor pgtype.UUID, in CreateInput) (sqlc.Application, error) {
	role, err := s.roleIn(ctx, orgID, actor)
	if err != nil {
		return sqlc.Application{}, err
	}
	if roleRank[role] < roleRank["member"] {
		return sqlc.Application{}, ErrForbidden
	}
	if err := validateCreate(in); err != nil {
		return sqlc.Application{}, err
	}
	if in.Builder == "" {
		in.Builder = "auto"
	}
	if in.HealthcheckPath == "" {
		in.HealthcheckPath = "/"
	}
	if in.BuildContext == "" {
		in.BuildContext = "."
	}
	if in.DockerfilePath == "" {
		in.DockerfilePath = "Dockerfile"
	}
	if in.BuildArgs == nil {
		in.BuildArgs = map[string]string{}
	}
	argsJSON, err := json.Marshal(in.BuildArgs)
	if err != nil {
		return sqlc.Application{}, err
	}
	creds, err := s.sealCreds(in.RegistryCreds)
	if err != nil {
		return sqlc.Application{}, err
	}
	params := sqlc.CreateApplicationParams{
		OrgID: orgID, Name: in.Name, Slug: slugify(in.Name),
		SourceType: in.SourceType, Builder: in.Builder,
		GitRepoUrl: in.GitRepoURL, GitBranch: in.GitBranch, ImageRef: in.ImageRef,
		RegistryCredsEnc: creds, ExposedPort: in.ExposedPort,
		HealthcheckPath: in.HealthcheckPath, AutoDeploy: in.AutoDeploy,
		BuildContext: in.BuildContext, DockerfilePath: in.DockerfilePath, BuildArgs: argsJSON,
	}
	app, err := s.q.CreateApplication(ctx, params)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		params.Slug = fmt.Sprintf("%s-%s", params.Slug, randomSuffix())
		app, err = s.q.CreateApplication(ctx, params)
	}
	return app, err
}

func (s *Service) List(ctx context.Context, orgID, actor pgtype.UUID) ([]sqlc.Application, error) {
	if _, err := s.roleIn(ctx, orgID, actor); err != nil {
		return nil, err
	}
	rows, err := s.q.ListApplicationsForOrg(ctx, orgID)
	if rows == nil {
		rows = []sqlc.Application{}
	}
	return rows, err
}

func (s *Service) Get(ctx context.Context, appID, actor pgtype.UUID) (sqlc.Application, error) {
	return s.appFor(ctx, appID, actor, "viewer")
}

func (s *Service) Update(ctx context.Context, appID, actor pgtype.UUID, in UpdateInput) (sqlc.Application, error) {
	app, err := s.appFor(ctx, appID, actor, "member")
	if err != nil {
		return sqlc.Application{}, err
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&app.Name, in.Name)
	set(&app.Builder, in.Builder)
	set(&app.GitBranch, in.GitBranch)
	set(&app.ImageRef, in.ImageRef)
	set(&app.HealthcheckPath, in.HealthcheckPath)
	set(&app.BuildContext, in.BuildContext)
	set(&app.DockerfilePath, in.DockerfilePath)
	if in.ExposedPort != nil {
		app.ExposedPort = *in.ExposedPort
	}
	if in.AutoDeploy != nil {
		app.AutoDeploy = *in.AutoDeploy
	}
	if in.BuildArgs != nil {
		raw, err := json.Marshal(in.BuildArgs)
		if err != nil {
			return sqlc.Application{}, err
		}
		app.BuildArgs = raw
	}
	if in.RegistryCreds != nil {
		creds, err := s.sealCreds(in.RegistryCreds)
		if err != nil {
			return sqlc.Application{}, err
		}
		app.RegistryCredsEnc = creds
	}
	return s.q.UpdateApplication(ctx, sqlc.UpdateApplicationParams{
		ID: app.ID, Name: app.Name, Builder: app.Builder, GitBranch: app.GitBranch,
		ImageRef: app.ImageRef, ExposedPort: app.ExposedPort,
		HealthcheckPath: app.HealthcheckPath, AutoDeploy: app.AutoDeploy,
		BuildContext: app.BuildContext, DockerfilePath: app.DockerfilePath,
		BuildArgs: app.BuildArgs, RegistryCredsEnc: app.RegistryCredsEnc,
	})
}

func (s *Service) Delete(ctx context.Context, appID, actor pgtype.UUID) error {
	app, err := s.appFor(ctx, appID, actor, "admin")
	if err != nil {
		return err
	}
	return s.q.DeleteApplication(ctx, app.ID)
}

func (s *Service) SetEnvVars(ctx context.Context, appID, actor pgtype.UUID, vars map[string]string) error {
	app, err := s.appFor(ctx, appID, actor, "member")
	if err != nil {
		return err
	}
	for k, v := range vars {
		if k == "" {
			return fmt.Errorf("%w: env var key must not be empty", ErrValidation)
		}
		enc, err := s.box.Seal([]byte(v))
		if err != nil {
			return err
		}
		if err := s.q.UpsertEnvVar(ctx, sqlc.UpsertEnvVarParams{AppID: app.ID, Key: k, ValueEnc: enc}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ListEnvKeys(ctx context.Context, appID, actor pgtype.UUID) ([]string, error) {
	app, err := s.appFor(ctx, appID, actor, "viewer")
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListEnvVars(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, r.Key)
	}
	return keys, nil
}

func (s *Service) DeleteEnvVar(ctx context.Context, appID, actor pgtype.UUID, key string) error {
	app, err := s.appFor(ctx, appID, actor, "member")
	if err != nil {
		return err
	}
	return s.q.DeleteEnvVar(ctx, sqlc.DeleteEnvVarParams{AppID: app.ID, Key: key})
}

// DecryptedEnv is pipeline-facing: no actor check.
func (s *Service) DecryptedEnv(ctx context.Context, appID pgtype.UUID) (map[string]string, error) {
	rows, err := s.q.ListEnvVars(ctx, appID)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string, len(rows))
	for _, r := range rows {
		pt, err := s.box.Open(r.ValueEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt %s: %w", r.Key, err)
		}
		env[r.Key] = string(pt)
	}
	return env, nil
}

func (s *Service) DecryptedRegistryCreds(ctx context.Context, app sqlc.Application) (*RegistryCreds, error) {
	if len(app.RegistryCredsEnc) == 0 {
		return nil, nil
	}
	pt, err := s.box.Open(app.RegistryCredsEnc)
	if err != nil {
		return nil, err
	}
	var c RegistryCreds
	if err := json.Unmarshal(pt, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
```

- [ ] **Step 4: Run** `go test ./internal/apps/ -v` → PASS
- [ ] **Step 5: Commit** `git add internal/apps && git commit -m "feat: add role-scoped applications service with encrypted env vars"`

---

### Task 4: Apps + env var API endpoints

**Files:**
- Create: `internal/api/apps.go`
- Modify: `internal/api/server.go` (add `AppService` iface + `apps` field + `box`), `internal/api/router.go`, `cmd/server/main.go` (build `crypto.Box`, pass to NewServer)
- Test: `internal/api/apps_test.go`

**Interfaces:**
- `NewServer` signature becomes `NewServer(cfg config.Config, pool *pgxpool.Pool, box *crypto.Box) *Server`; when pool != nil and box != nil, wires `s.apps = apps.NewService(pool, box)`. Update `cmd/server/main.go` (`crypto.New(cfg.MasterKey)`) and the integration test's `startServer`.
- `api.AppService` interface mirrors the 8 actor-scoped methods from Task 3 (not the pipeline-facing ones).
- Routes (inside existing authed group):
  - `POST/GET /api/v1/orgs/{orgID}/apps`
  - `GET/PATCH/DELETE /api/v1/apps/{appID}`
  - `GET /api/v1/apps/{appID}/env` (keys only) · `PUT /api/v1/apps/{appID}/env` (bulk set `{"vars": {...}}`) · `DELETE /api/v1/apps/{appID}/env/{key}`
- `appJSON(a sqlc.Application) map[string]any` — lowercase keys, **never** includes `registry_creds_enc`
- Error mapping `appError(w, err)`: ErrNotFound→404, ErrForbidden→403, ErrValidation→400 (message from `err.Error()`), else 500

- [ ] **Step 1: Write failing tests** — `internal/api/apps_test.go` (stub pattern as before):

```go
package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

type stubApps struct {
	app sqlc.Application
	err error
}

func (s stubApps) Create(_ context.Context, _, _ pgtype.UUID, _ apps.CreateInput) (sqlc.Application, error) {
	return s.app, s.err
}
func (s stubApps) List(_ context.Context, _, _ pgtype.UUID) ([]sqlc.Application, error) {
	return []sqlc.Application{}, s.err
}
func (s stubApps) Get(_ context.Context, _, _ pgtype.UUID) (sqlc.Application, error) {
	return s.app, s.err
}
func (s stubApps) Update(_ context.Context, _, _ pgtype.UUID, _ apps.UpdateInput) (sqlc.Application, error) {
	return s.app, s.err
}
func (s stubApps) Delete(_ context.Context, _, _ pgtype.UUID) error { return s.err }
func (s stubApps) SetEnvVars(_ context.Context, _, _ pgtype.UUID, _ map[string]string) error {
	return s.err
}
func (s stubApps) ListEnvKeys(_ context.Context, _, _ pgtype.UUID) ([]string, error) {
	return []string{"DB_URL"}, s.err
}
func (s stubApps) DeleteEnvVar(_ context.Context, _, _ pgtype.UUID, _ string) error { return s.err }

func appServer(a AppService) *Server {
	return &Server{auth: stubAuth{user: sqlc.User{Email: "a@b.co"}}, apps: a}
}

const testUUID = "5f4c1c9e-0000-0000-0000-000000000000"

func TestCreateApp(t *testing.T) {
	s := appServer(stubApps{app: sqlc.Application{Name: "api", Slug: "api", SourceType: "image"}})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/orgs/"+testUUID+"/apps",
		`{"name":"api","source_type":"image","image_ref":"nginx:alpine","exposed_port":80}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "registry_creds") {
		t.Fatal("registry creds leaked")
	}
}

func TestCreateAppValidationError(t *testing.T) {
	s := appServer(stubApps{err: apps.ErrValidation})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/orgs/"+testUUID+"/apps", `{"name":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestGetAppNotFound(t *testing.T) {
	s := appServer(stubApps{err: apps.ErrNotFound})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/apps/"+testUUID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestEnvKeysOnly(t *testing.T) {
	s := appServer(stubApps{})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/apps/"+testUUID+"/env", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "DB_URL") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}

func TestSetEnvForbidden(t *testing.T) {
	s := appServer(stubApps{err: apps.ErrForbidden})
	rec := doAuthed(t, s, http.MethodPut, "/api/v1/apps/"+testUUID+"/env", `{"vars":{"A":"b"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run** → FAIL to build
- [ ] **Step 3: Implement** `internal/api/apps.go` with handlers `handleCreateApp`, `handleListApps`, `handleGetApp`, `handleUpdateApp`, `handleDeleteApp`, `handleListEnvKeys`, `handleSetEnvVars`, `handleDeleteEnvVar`. Request bodies use snake_case JSON mapped onto `apps.CreateInput`/`UpdateInput`. `appIDParam` mirrors `orgIDParam`. Wire `AppService` interface + field into `server.go`; change `NewServer` to accept `box *crypto.Box`; update `main.go` and `integration_test.go` accordingly. Routes per the Interfaces block.
- [ ] **Step 4: Run** `go test ./internal/api/ && go build ./...` → PASS
- [ ] **Step 5: Commit** `git add internal/api cmd && git commit -m "feat: add application and env var endpoints"`

---

### Task 5: SSE events hub

**Files:**
- Create: `internal/events/hub.go`
- Test: `internal/events/hub_test.go`

**Interfaces:**
- `events.NewHub() *Hub`
- `(*Hub).Publish(topic string, data []byte)` — non-blocking; slow subscribers drop messages
- `(*Hub).Subscribe(topic string) (<-chan []byte, func())` — returns channel + cancel

- [ ] **Step 1: Failing test** `internal/events/hub_test.go`:

```go
package events

import (
	"testing"
	"time"
)

func TestPublishSubscribe(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("dep-1")
	defer cancel()
	h.Publish("dep-1", []byte("line one"))
	h.Publish("dep-2", []byte("other topic"))
	select {
	case msg := <-ch:
		if string(msg) != "line one" {
			t.Fatalf("msg = %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("no message received")
	}
	select {
	case msg := <-ch:
		t.Fatalf("unexpected cross-topic message: %s", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCancelUnsubscribes(t *testing.T) {
	h := NewHub()
	_, cancel := h.Subscribe("dep-1")
	cancel()
	h.Publish("dep-1", []byte("x")) // must not panic or block
}
```

- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement** `internal/events/hub.go`:

```go
// Package events is an in-memory pub/sub hub for streaming deployment
// logs and status to SSE clients.
package events

import "sync"

type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan []byte]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[string]map[chan []byte]struct{}{}}
}

func (h *Hub) Subscribe(topic string) (<-chan []byte, func()) {
	ch := make(chan []byte, 256)
	h.mu.Lock()
	if h.subs[topic] == nil {
		h.subs[topic] = map[chan []byte]struct{}{}
	}
	h.subs[topic][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[topic], ch)
		if len(h.subs[topic]) == 0 {
			delete(h.subs, topic)
		}
		h.mu.Unlock()
	}
}

// Publish never blocks; a full subscriber buffer drops the message.
func (h *Hub) Publish(topic string, data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[topic] {
		select {
		case ch <- data:
		default:
		}
	}
}
```

- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `git add internal/events && git commit -m "feat: add sse event hub"`

---

### Task 6: Deployments service (records, status machine, log store)

**Files:**
- Create: `internal/deployments/service.go`, `internal/deployments/logs.go`
- Test: `internal/deployments/service_test.go`, helper `internal/deployments/testdb_test.go` (copy startPool, `package deployments`)

**Interfaces:**
- `deployments.NewService(pool *pgxpool.Pool, hub *events.Hub, dataDir string) *Service`
- Actor-scoped (the `api.DeploymentService` surface):
  - `Create(ctx, appID, actor pgtype.UUID, trigger string) (sqlc.Deployment, error)` — member+; trigger `manual`/`webhook`
  - `Rollback(ctx, appID, actor, targetID pgtype.UUID) (sqlc.Deployment, error)` — member+; target must belong to app, have status `live` or previously-live (`finished` with image tag) — rule: target status ∈ {live, failed→no}: require `target.ImageTag != "" && target.Status == "live" || (target.Status == "failed" && ...)` — **exact rule: target.ImageTag != "" and target status is `live`** in v1; new deployment gets trigger `rollback`, `image_tag` copied
  - `List(ctx, appID, actor pgtype.UUID) ([]sqlc.Deployment, error)` — any member
  - `Get(ctx, deploymentID, actor pgtype.UUID) (sqlc.Deployment, error)` — any member of the app's org
- Pipeline-facing:
  - `SetStatus(ctx, id pgtype.UUID, status string) error` — validates transitions: queued→building|deploying|cancelled, building→deploying|failed|cancelled, deploying→live|failed; returns `ErrBadTransition` otherwise
  - `Finish(ctx, id pgtype.UUID, status, errMsg string) error` — status must be live/failed/cancelled
  - `SetBuildInfo(ctx, id pgtype.UUID, commitSHA, imageTag string) error`
  - `GetRaw(ctx, id pgtype.UUID) (sqlc.Deployment, error)` — no actor check (worker)
- Logs (`logs.go`):
  - `LogPath(id string) string` → `<dataDir>/deployments/<id>.log`
  - `LogWriter(id string) (io.WriteCloser, error)` — appends to file AND publishes each Write to hub topic `deploy:<id>`
- Sentinels: `ErrNotFound`, `ErrForbidden`, `ErrBadTransition`, `ErrBadRollbackTarget`

- [ ] **Step 1: Failing tests** — cover: Create by member → queued; viewer Create → ErrForbidden; outsider Get → ErrNotFound; SetStatus legal chain queued→building→deploying + Finish(live); illegal `live→building` → ErrBadTransition; Rollback copies image_tag + trigger=rollback, rejects target without image tag (`ErrBadRollbackTarget`); LogWriter writes file at LogPath and hub subscriber receives the bytes. Reuse the fixture style from Task 3 (auth+orgs+apps services to create an app).
- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement.** Status machine as a `map[string]map[string]bool` of allowed transitions; `LogWriter` opens `os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)` after `os.MkdirAll(dir, 0o755)` and wraps it in a writer whose `Write` also calls `hub.Publish("deploy:"+id, bytes.Clone(p))`. Role checks identical in style to `apps.appFor` (load deployment → app → membership).
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `git add internal/deployments && git commit -m "feat: add deployments service with status machine and log store"`

---### Task 7: Builder package (git clone, detection, dockerfile, nixpacks)

**Files:**
- Create: `internal/builder/git.go`, `internal/builder/builder.go`, `internal/builder/dockerfile.go`, `internal/builder/nixpacks.go`
- Test: `internal/builder/builder_test.go`

**Interfaces:**
- `builder.CloneAtBranch(ctx, repoURL, branch, dest string, log io.Writer) (sha string, err error)` — `git clone --depth 1 --single-branch --branch <branch>`, then `git -C dest rev-parse HEAD`
- `builder.Detect(workDir, dockerfilePath string) string` — returns `dockerfile` if the file exists, else `nixpacks` (FR-3.3)
- `builder.Input{WorkDir, ImageTag, ContextPath, DockerfilePath string; BuildArgs map[string]string; Log io.Writer}`
- `builder.Builder` interface: `Build(ctx context.Context, in Input) error`
- `builder.Dockerfile{}` — runs `docker build -t <tag> -f <workdir/dockerfilePath> [--build-arg k=v...] <workdir/contextPath>` with stdout/stderr → `in.Log`
- `builder.Nixpacks{}` — runs `nixpacks build <workdir/contextPath> --name <tag>`; `builder.NixpacksAvailable() bool` checks `exec.LookPath`
- `builder.ForName(name string) Builder`

- [ ] **Step 1: Failing tests** (docker/git-dependent tests guarded by `testing.Short()`):

```go
package builder

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	if got := Detect(dir, "Dockerfile"); got != "nixpacks" {
		t.Fatalf("no dockerfile → %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Detect(dir, "Dockerfile"); got != "dockerfile" {
		t.Fatalf("with dockerfile → %q", got)
	}
}

func TestCloneAtBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("needs git")
	}
	src := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")

	dest := filepath.Join(t.TempDir(), "clone")
	var log bytes.Buffer
	sha, err := CloneAtBranch(context.Background(), "file://"+src, "main", dest, &log)
	if err != nil {
		t.Fatalf("clone: %v\n%s", err, log.String())
	}
	if len(sha) != 40 {
		t.Fatalf("sha = %q", sha)
	}
	if _, err := os.Stat(filepath.Join(dest, "hello.txt")); err != nil {
		t.Fatalf("cloned file missing: %v", err)
	}
}

func TestDockerfileBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"),
		[]byte("FROM alpine:3.21\nARG GREETING=hey\nRUN echo $GREETING\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	err := Dockerfile{}.Build(context.Background(), Input{
		WorkDir: dir, ImageTag: "cargo-test-build:t1", ContextPath: ".",
		DockerfilePath: "Dockerfile", BuildArgs: map[string]string{"GREETING": "cargo-42"}, Log: &log,
	})
	if err != nil {
		t.Fatalf("build: %v\n%s", err, log.String())
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", "cargo-test-build:t1").Run() })
}
```

- [ ] **Step 2: Run** `go test ./internal/builder/ -v` → FAIL
- [ ] **Step 3: Implement** — all exec-based; every command's stdout+stderr wired to `in.Log`/`log`. `dockerfile.go`:

```go
package builder

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
)

type Dockerfile struct{}

func (Dockerfile) Build(ctx context.Context, in Input) error {
	args := []string{"build", "-t", in.ImageTag, "-f", filepath.Join(in.WorkDir, in.DockerfilePath)}
	keys := make([]string, 0, len(in.BuildArgs))
	for k := range in.BuildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", fmt.Sprintf("%s=%s", k, in.BuildArgs[k]))
	}
	args = append(args, filepath.Join(in.WorkDir, in.ContextPath))
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = in.Log, in.Log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	return nil
}
```

`git.go`, `nixpacks.go`, `builder.go` analogous (`builder.go` holds `Input`, `Builder`, `Detect`, `ForName`).
- [ ] **Step 4: Run** → PASS (short-guarded tests run locally since docker+git present)
- [ ] **Step 5: Commit** `git add internal/builder && git commit -m "feat: add git clone and dockerfile/nixpacks builders"`

---

### Task 8: Reconciler — compose generation + Docker provider

**Files:**
- Create: `internal/reconciler/spec.go`, `internal/reconciler/compose.go`, `internal/reconciler/provider.go`
- Test: `internal/reconciler/compose_test.go` (golden string), `internal/reconciler/provider_test.go` (real docker, short-guarded)

**Interfaces:**
- `reconciler.Spec{AppID, Slug, Image string; Port int32; HealthcheckPath string; Env map[string]string; Domains []string}`
- `reconciler.DeployProvider` interface (the K8s seam):
  - `Apply(ctx context.Context, spec Spec, log io.Writer) error`
  - `Teardown(ctx context.Context, appID, slug string, log io.Writer) error`
- `reconciler.NewDocker(dataDir string) *Docker` — implements DeployProvider:
  - project dir `<dataDir>/apps/<appID>/` with `compose.yaml` + `.env` (mode **0600**, keys sorted)
  - compose project name `cargo-app-<slug>`; service `app`; `restart: unless-stopped`; external network `cargo-proxy` (created if missing via `docker network create`, "already exists" ignored); Traefik labels:
    - `traefik.enable=true`
    - `traefik.http.routers.app-<slug>.rule=Host(` + backtick-quoted domains joined with `) || Host(`+`)`
    - `traefik.http.routers.app-<slug>.entrypoints=websecure`
    - `traefik.http.routers.app-<slug>.tls=true`
    - `traefik.http.services.app-<slug>.loadbalancer.server.port=<port>`
  - `docker compose -f <dir>/compose.yaml up -d --remove-orphans`, then health gate: resolve container ID (`docker compose ... ps -q app`), poll every 2 s up to `HealthTimeout` (default 2 min): container must be `running`; probe `GET http://<containerIP>:<port><healthcheckPath>` (IP from `docker inspect` on the cargo-proxy network); any HTTP status < 500 = healthy; container exited = immediate failure
  - Teardown: `docker compose -f ... down --remove-orphans` + `os.RemoveAll(dir)`
- `reconciler.GenerateCompose(spec Spec) string` — pure function returning the YAML (exported for the golden test)

- [ ] **Step 1: Failing golden test** — `GenerateCompose` for a fixed Spec must equal an exact YAML string (assert full string, including labels and network block). Second test `TestApplyRunsContainer` (short-guarded): `NewDocker(t.TempDir()).Apply` with `Spec{AppID: "test-1", Slug: "recon-test", Image: "nginx:alpine", Port: 80, HealthcheckPath: "/", Env: map[string]string{"FOO": "bar"}, Domains: []string{"recon-test.apps.localhost"}}` succeeds; `.env` file has mode 0600 and contains `FOO=bar`; container responds; then `Teardown` removes container and dir. Cleanup with Teardown in `t.Cleanup`.
- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement.** `.env` rendering: `KEY=VALUE` lines, values with newlines rejected with an error. Compose YAML built with a `strings.Builder` (no YAML lib — output is fully controlled). Probe uses `http.Client{Timeout: 3 * time.Second}`.
- [ ] **Step 4: Run** `go test ./internal/reconciler/ -v` → PASS
- [ ] **Step 5: Commit** `git add internal/reconciler && git commit -m "feat: add compose reconciler with traefik labels and health gate"`

---

### Task 9: River queue + deploy pipeline worker

**Files:**
- Create: `internal/jobs/client.go`, `internal/jobs/deploy.go`
- Modify: `cmd/server/main.go` (river migrate + client start/stop)
- Test: `internal/jobs/deploy_test.go` (pipeline with fake builder/provider against real DB), helper `internal/jobs/testdb_test.go`

**Interfaces:**
- Deps: `go get github.com/riverqueue/river github.com/riverqueue/river/riverdriver/riverpgxv5`
- `jobs.DeployArgs{DeploymentID string}` with `Kind() = "deploy"`
- `jobs.Pipeline` struct — fields: `Pool *pgxpool.Pool`, `Apps *apps.Service`, `Deployments *deployments.Service`, `Provider reconciler.DeployProvider`, `NewBuilder func(name string) builder.Builder`, `Clone func(ctx, url, branch, dest string, log io.Writer) (string, error)`, `DataDir string`, `AppsDomainSuffix func(ctx context.Context) string`
- `(*Pipeline).Run(ctx context.Context, deploymentID string) error`:
  1. load deployment (`GetRaw`) + app; skip if status not `queued` (idempotent retry entry: reset `failed→queued` not allowed; a retried job with status `building`/`deploying` restarts by design — log a "retrying" line)
  2. per-app advisory lock: `pool.Acquire` → `SELECT pg_advisory_lock(hashtext($1))` on app ID string; unlock + release deferred (FR-4.4 per-app serialization)
  3. open `LogWriter`; every subsequent step writes progress lines `==> cloning`, `==> building`, `==> deploying`, `==> live`
  4. `SetStatus(building)`; if trigger == `rollback` → skip to 6 (image tag already set, FR-4.6)
  5. source `git`: clone to `<DataDir>/builds/<deploymentID>` (removed after), `SetBuildInfo(sha, tag)` with tag `app-<slug>:<8-char deployment id prefix>`, builder = app.Builder or `Detect`; source `image`: tag = app.ImageRef; if registry creds present run `docker login <server> -u <user> --password-stdin`
  6. `SetStatus(deploying)`; build `reconciler.Spec` (env from `DecryptedEnv`, domains `[<slug>.<AppsDomainSuffix()>]`); `Provider.Apply`
  7. success → `Finish(live, "")`; any error → `Finish(failed, err.Error())` and **return the error** (River retries; retry restarts the pipeline which re-marks building — acceptable v1 behavior, noted in log)
- `jobs.NewClient(pool *pgxpool.Pool, p *Pipeline) (*river.Client[pgx.Tx], error)` — queue `deploy` MaxWorkers **2**, workers: DeployWorker wrapping `p.Run`
- `jobs.Migrate(ctx context.Context, pool *pgxpool.Pool) error` — `rivermigrate` up
- `jobs.Client` also exposes `EnqueueDeploy(ctx context.Context, deploymentID string) error` via a thin wrapper type `Enqueuer` so the API can depend on an interface
- `cmd/server/main.go`: after goose migrate → `jobs.Migrate` → build box/services/pipeline → `client.Start(ctx)`; graceful `client.Stop` on shutdown signal

- [ ] **Step 1: Failing pipeline test** — real Postgres, fake builder/provider:

```go
func TestPipelineImageDeploySuccess(t *testing.T) { /* create org+app(image)+deployment via services;
    fake provider records Spec and returns nil; run Pipeline.Run; assert deployment live,
    provider got Image=="nginx:alpine", Domains==["<slug>.apps.localhost"], env decrypted;
    log file contains "==> deploying" */ }

func TestPipelineFailureMarksFailed(t *testing.T) { /* fake provider returns error;
    Run returns error; deployment status failed with error text; log retained */ }

func TestPipelineRollbackSkipsBuild(t *testing.T) { /* deployment with trigger=rollback and
    image_tag preset; fake builder must NOT be called; provider Apply called with that tag */ }
```

(Write these fully in the test file; fakes are ~10 lines each.)
- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement** `deploy.go` + `client.go` + main wiring per the Interfaces block
- [ ] **Step 4: Run** `go test ./internal/jobs/ -v && go build ./...` → PASS
- [ ] **Step 5: Commit** `git add internal/jobs cmd go.mod go.sum && git commit -m "feat: add river deploy queue and pipeline worker"`

---

### Task 10: Deploy/rollback/list + SSE log endpoints

**Files:**
- Create: `internal/api/deployments.go`
- Modify: `internal/api/server.go` (DeploymentService iface, `deps`, `enqueue Enqueuer`, `hub *events.Hub`, `logPath func(string) string`), `internal/api/router.go`, `cmd/server/main.go`
- Test: `internal/api/deployments_test.go`

**Interfaces:**
- `api.Enqueuer` interface: `EnqueueDeploy(ctx context.Context, deploymentID string) error` (satisfied by `jobs.Enqueuer`)
- Routes (authed group):
  - `POST /api/v1/apps/{appID}/deploy` → `deps.Create(..., "manual")` then `enqueue.EnqueueDeploy` → 202 with deployment JSON
  - `POST /api/v1/apps/{appID}/rollback` body `{"deployment_id": "..."}` → `deps.Rollback` + enqueue → 202
  - `GET /api/v1/apps/{appID}/deployments` → list
  - `GET /api/v1/deployments/{deploymentID}` → get
  - `GET /api/v1/deployments/{deploymentID}/logs` → SSE (FR-4.3): authorize via `deps.Get`; set `Content-Type: text/event-stream`, `Cache-Control: no-cache`; **subscribe to hub topic `deploy:<id>` first**, then replay the log file line-by-line as `data:` events, then stream hub messages; flush after every event; exit on client disconnect (`r.Context().Done()`), and after replay exit immediately if the deployment status is already terminal (live/failed/cancelled)
- `deploymentJSON(d sqlc.Deployment) map[string]any` — lowercase keys: id, app_id, trigger, status, commit_sha, image_tag, error, created_at, started_at, finished_at
- Enqueue failure after Create → `Finish(failed, "enqueue failed")` best-effort + 500

- [ ] **Step 1: Failing tests** — stub `DeploymentService` + stub `Enqueuer` (records IDs): deploy → 202 and enqueuer called; rollback bad target (`ErrBadRollbackTarget`) → 400; SSE handler: temp log file with two lines, stub Get returns terminal `live` deployment → response contains both `data:` lines and header `text/event-stream`; non-member (`ErrNotFound`) → 404.
- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement + wire** router, server fields, main.go (pass `jobs` enqueuer, hub, `deployments.LogPath`)
- [ ] **Step 4: Run** `go test ./internal/api/ && go build ./...` → PASS
- [ ] **Step 5: Commit** `git add internal/api cmd && git commit -m "feat: add deploy, rollback, and sse log endpoints"`

---

### Task 11: Prune job, Dockerfile tooling, full-suite verification

**Files:**
- Create: `internal/jobs/prune.go` (+ test in `internal/jobs/prune_test.go`)
- Modify: `Dockerfile` (add `git`; install nixpacks binary), `deploy/docker-compose.dev.yml` (no change needed — verify), `.golangci.yml` (none expected)

**Interfaces:**
- `jobs.PruneArgs{}` kind `prune`, registered as a River periodic job (daily): for every app, `ListPrunableDeployments(appID, 5)` → for each: delete log file, `docker rmi` its image tag (ignore errors), delete row (add `DeleteDeployment :exec` query — include it in this task with `sqlc generate`)
- Dockerfile final stage adds: `apk add --no-cache git curl` and nixpacks:
  ```dockerfile
  ARG NIXPACKS_VERSION=1.29.1
  RUN curl -fsSL "https://github.com/railwayapp/nixpacks/releases/download/v${NIXPACKS_VERSION}/nixpacks-v${NIXPACKS_VERSION}-x86_64-unknown-linux-musl.tar.gz" \
      | tar -xz -C /usr/local/bin nixpacks
  ```

- [ ] **Step 1: Prune test** — real DB: create app + 7 finished deployments with log files; run prune logic (call the worker's `Work` body via an exported `RunPrune(ctx, deps)` helper); assert 5 newest remain, old log files gone
- [ ] **Step 2: Implement prune + Dockerfile changes**
- [ ] **Step 3: Full verification**

Run: `go build ./... && go vet ./... && go test ./... && golangci-lint run && docker build .`
Expected: all green
- [ ] **Step 4: Update PhasedPlans.md** — mark Phase 2 features ✅ (leave 2.6's "reachable through Traefik" note pointing to Phase 4)
- [ ] **Step 5: Commit** `git add -A && git commit -m "feat: add retention prune job and bundle git/nixpacks in image"`

---

## Out of scope (later phases)

- GitHub App auth, private repos, webhooks → Phase 3
- Traefik container in the stack, ACME, custom domains, domain status → Phase 4
- All UI → Phase 5 · Instance settings write API → Phase 6 · install.sh → Phase 7

## Self-review notes

- FR-3.1–3.5 → Tasks 3/4 (creds encrypted, write-only) + 7 (detection, build args); FR-4.1 manual+rollback → Tasks 6/10 (webhook trigger value exists in schema for Phase 3); FR-4.2 status machine → Task 6; FR-4.3 SSE + persisted logs → Tasks 5/6/10; FR-4.4 caps → Task 9 (MaxWorkers 2 + advisory lock); FR-4.5 → failed builds never call Apply; FR-4.6 rollback skips build → Tasks 6/9; FR-6.1–6.3 → Tasks 2/3/4 + reconcile env in 8/9; retention → Task 11.
- Known v1 trade-offs (documented in code comments): River retry restarts a pipeline from the top; health probe is controlplane→container HTTP, requiring shared Docker network reachability (true in the compose stack and on Linux dev hosts).
- Type caveat: adapt to sqlc-generated param structs exactly (`GitRepoUrl` casing etc.) after Task 1's generation.
