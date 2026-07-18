# Cargo M10 — Managed Databases Implementation Plan (PhasedPlans 8.1)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans.

**Goal:** Per-org managed Postgres/Redis instances (spec: `docs/superpowers/specs/2026-07-18-cargo-managed-databases-design.md`): provision as own compose projects, per-app logical databases via attach/detach, `DATABASE_URL`/`REDIS_URL` injected at deploy time, manual snapshots, status in UI, optional host port.

**Architecture:** New `internal/databases` service owns instances + attachments (`database_instances`, `database_attachments`); `internal/reconciler` gains a `DatabaseProvider` seam (Docker impl: compose up/exec/snapshot/down -v) and `Spec.Networks`; provisioning runs as a River job; the deploy pipeline merges attachment URLs into `spec.Env` and adds `cargo-data` to the app's networks. API + org-page Databases tab mirror the apps patterns.

**Tech Stack:** No new Go deps — docker CLI via the existing `run`/`output` helpers, River jobs, `crypto.Box`, sqlc, chi, React Query.

## Global Constraints

- All generated passwords: 128-bit `crypto/rand`, base64-rawurl; stored ONLY Box-encrypted as `{"enc":"<base64>"}` JSONB; never logged; full connection URLs returned exactly once (attach response)
- Engines/versions: `postgres` `16`|`17`, `redis` `7`; images `postgres:<v>-alpine` / `redis:<v>-alpine`
- Redis `redis_mode` `acl`|`shared` chosen at provision, immutable; max 16 attachments per redis instance (DB indexes 0–15); acl-mode attachments must not read another attachment's keys or authenticate as it
- Instance names: `^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`, unique per org; identifiers used in SQL/ACL are derived only from validated slugs (`[a-z0-9_]`), passwords passed via stdin, never argv
- Instances join only the `cargo-data` docker network; apps join it only when they have ≥1 attachment; `host_port` published only when the admin enabled it at provision
- Attach rejected with 409 when the app already defines `DATABASE_URL`/`REDIS_URL` as a user env var, belongs to another org, is already attached, or the redis index cap is hit
- Detach revokes credentials but keeps the pg database's data; instance delete blocked (409) while attachments exist; delete runs `compose down -v`
- Role gating mirrors apps: org member read, admin/owner mutate (`roleRank`)
- Verification bar: full Go suite, golangci-lint 0 issues, vitest, web build

## Tasks

### Task 1: Schema + queries
- `internal/db/migrations/00008_databases.sql`:
  ```sql
  CREATE TABLE database_instances (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
      name TEXT NOT NULL,
      engine TEXT NOT NULL CHECK (engine IN ('postgres','redis')),
      version TEXT NOT NULL,
      redis_mode TEXT CHECK (redis_mode IN ('acl','shared')),
      host_port INT,
      status TEXT NOT NULL DEFAULT 'provisioning' CHECK (status IN ('provisioning','running','error','stopped')),
      admin_secret JSONB NOT NULL,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      UNIQUE (org_id, name)
  );
  CREATE TABLE database_attachments (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      instance_id UUID NOT NULL REFERENCES database_instances(id) ON DELETE CASCADE,
      app_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
      db_name TEXT,
      role_name TEXT,
      acl_user TEXT,
      db_index INT,
      secret JSONB,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      UNIQUE (instance_id, app_id)
  );
  ```
  (down: drop both)
- `internal/db/queries/databases.sql`: `CreateDatabaseInstance :one`, `GetDatabaseInstance :one`, `ListDatabaseInstancesByOrg :many`, `SetDatabaseInstanceStatus :exec`, `DeleteDatabaseInstance :exec`, `CreateDatabaseAttachment :one`, `GetDatabaseAttachment :one` (instance+app), `ListAttachmentsByInstance :many`, `ListAttachmentsByApp :many` (join instances for engine/name/status), `DeleteDatabaseAttachment :exec`, `CountAttachmentsByInstance :one`, `ListUsedDbIndexes :many` (instance, non-null db_index ordered)
- `sqlc generate`; build must stay green (no callers yet)
- Migration round-trip is already covered by every testcontainers suite (Migrate runs all)
- Commit `feat: add managed database schema and queries`

### Task 2: Reconciler — Spec.Networks, DB compose, DatabaseProvider
- `internal/reconciler/spec.go`: add `Networks []string` to `Spec` (empty ⇒ `["cargo-proxy"]`); add
  ```go
  type DBSpec struct {
      InstanceID string
      Engine     string // postgres | redis
      Version    string
      AdminPass  string // superuser / requirepass credential
      HostPort   int32  // 0 = not published
  }
  type DatabaseProvider interface {
      ProvisionDB(ctx context.Context, spec DBSpec, log io.Writer) error
      ExecDB(ctx context.Context, instanceID, engine string, stdin string, args ...string) (string, error)
      SnapshotDB(ctx context.Context, instanceID, engine, adminPass, destPath string) error
      TeardownDB(ctx context.Context, instanceID string, log io.Writer) error
  }
  ```
- `internal/reconciler/compose.go`: `GenerateCompose` renders `spec.Networks` (empty ⇒ output byte-identical to today — existing golden tests must not change); new `GenerateDBCompose(spec DBSpec) string` — project `cargo-db-<id>`, service `db`, `container_name: cargo-db-<id>` (stable DNS name on `cargo-data`), named volume `data`, network `cargo-data` (external). Secrets never appear in compose.yaml: postgres gets `POSTGRES_PASSWORD` via `env_file: .env` (0600); redis gets a `redis.conf` (0600, contains `requirepass <pass>`) mounted read-only with `command: ["redis-server","/etc/cargo/redis.conf"]`. `ports: ["<HostPort>:5432|6379"]` only when `HostPort > 0`
- `internal/reconciler/dbprovider.go`: Docker impl on the existing `Docker` struct — `dbDir(id) = <dataDir>/databases/<id>`; `ProvisionDB` writes `.env`/`redis.conf` + `compose.yaml`, `ensureNetwork("cargo-data")`, `compose up -d`, readiness loop (60 s): `docker compose exec -T db pg_isready -U postgres` or `redis-cli -a <pass> PING` via `ExecDB`; `ExecDB` = `docker compose -f <dir>/compose.yaml exec -T db <args...>` with stdin; `SnapshotDB`: postgres — `exec -T db pg_dumpall --clean -U postgres` streamed to `<destPath>.sql` (whole instance, plain SQL); redis — `BGSAVE` via ExecDB, poll `LASTSAVE` until it advances (10 s cap), then `docker compose cp db:/data/dump.rdb <destPath>.rdb`; `TeardownDB`: `compose down -v --remove-orphans` + `os.RemoveAll(dir)`
- Tests (`compose_test.go` style, no Docker): golden output for `GenerateDBCompose` postgres with/without host port + redis; secrets absent from compose text; `GenerateCompose` with `Networks: ["cargo-proxy","cargo-data"]` renders both and default output unchanged
- Commit `feat: add database compose generation and provider seam`

### Task 3: `internal/databases` service
- `internal/databases/service.go`:
  - `var ErrNotFound, ErrForbidden, ErrValidation, ErrConflict = errors.New(...)`
  - `type Service struct { q *sqlc.Queries; pool *pgxpool.Pool; box *crypto.Box; provider reconciler.DatabaseProvider; dataDir string }`, `NewService(pool, box, provider, dataDir)`
  - role helpers copied from apps (`roleRank`, `orgRole(ctx, orgID, actor)`); `instFor(ctx, id, actor, minRole)`
  - `Create(ctx, orgID, actor, in CreateInput) (sqlc.DatabaseInstance, error)` — validate name regex/engine/version/redis_mode (required iff redis)/expose flag; generate admin password, seal into `admin_secret`; when `in.ExposePort`, pick a free host port with a `freePort()` helper (bind `:0`, read the port, close); insert row `status=provisioning`
  - `Provision(ctx, id) error` — called by the job: decrypt admin secret, build `DBSpec`, `provider.ProvisionDB` with a log file at `<dataDir>/db-logs/<id>.log`, set status `running`/`error`
  - `Attach(ctx, instanceID, appID, actor) (url string, att sqlc.DatabaseAttachment, err error)` — checks (same org via app lookup, instance `running`, no existing attachment, no user env var `DATABASE_URL`/`REDIS_URL` via `q.ListEnvVars`, redis cap); postgres: role/db name `app_<slug>` (slug `-`→`_`), executed as one `psql -v ON_ERROR_STOP=1` stdin script — `CREATE ROLE app_<slug> LOGIN PASSWORD '<quoteLiteral(pass)>'; CREATE DATABASE app_<slug> OWNER app_<slug>; REVOKE CONNECT ON DATABASE app_<slug> FROM PUBLIC;` (`quoteLiteral` helper doubles single quotes; identifiers come only from the validated slug); redis acl: next free index + `ACL SETUSER app_<slug> on ><pass> allkeys allchannels +@all -@admin -acl -select +select|<idx>` — the isolation test pins this: user A cannot `SELECT` into B's index, cannot `AUTH` as B, can read/write its own keys; redis shared: next free index only, secret NULL; store secret sealed; returned URLs use the stable container name from Task 2 as host — postgres `postgres://app_<slug>:<pass>@cargo-db-<id>:5432/app_<slug>?sslmode=disable`, redis acl `redis://app_<slug>:<pass>@cargo-db-<id>:6379/<idx>`, redis shared `redis://:<adminpass>@cargo-db-<id>:6379/<idx>`
  - `Detach(ctx, instanceID, appID, actor) error` — postgres: `REVOKE`, terminate sessions (`pg_terminate_backend`), `REASSIGN OWNED BY … TO postgres` then `DROP ROLE`; keep database; redis acl: `ACL DELUSER`; delete row
  - `Delete(ctx, id, actor) error` — 409 `ErrConflict` when `CountAttachmentsByInstance > 0`; `provider.TeardownDB`; delete row
  - `EnvFor(ctx, appID) (map[string]string, error)` — for the deploy pipeline: rebuild URLs from stored secrets for all attachments of the app (postgres → `DATABASE_URL`, redis → `REDIS_URL`). Attach enforces one attachment per engine per app (second postgres attach → `ErrConflict`), so keys never collide; `EnvFor` errors defensively if they somehow do
  - `Snapshot(ctx, id, actor) (name string, err error)`; `ListSnapshots(ctx, id, actor) ([]SnapshotInfo, error)` (name/size/created from dir listing); `SnapshotPath(ctx, id, actor, name) (string, error)` — name must exist in listing (no traversal); `DeleteSnapshot(ctx, id, actor, name) error`; dir `<dataDir>/db-backups/<id>/`
  - `List(ctx, orgID, actor)`, `Get(ctx, id, actor)` — detail includes attachments and `size_bytes` read from the engine via `ExecDB`: postgres `SELECT sum(pg_database_size(datname)) FROM pg_database`, redis `INFO memory` → `used_memory`; a failed size read degrades to 0, never fails the request
- `internal/databases/service_test.go` (testcontainers pg for the control plane + **real local Docker** for instances; skip with `t.Skip` if `docker` unavailable): provision postgres 16 → status running; attach two apps → each connects (pgx) only to its own db, cross-connect fails; user env `DATABASE_URL` collision → `ErrConflict`; detach → login fails, second attach of same app after detach works; redis acl: A can't read B's keys nor AUTH as B; shared: distinct indexes, cap at 16 (unit-test the index picker, not 16 real attaches); delete with attachment → `ErrConflict`; snapshot creates a non-empty file listed by `ListSnapshots`; `SnapshotPath("../etc/passwd")` → error
- Commit `feat: add databases service with attach isolation tests`

### Task 4: Provision job
- `internal/jobs/dbprovision.go`: `DBProvisionArgs{InstanceID string}` kind `provision_database`, worker calls `databases.Service.Provision`; register in `client.go` workers + `Enqueuer.EnqueueDBProvision(ctx, instanceID string)`
- `cmd/server` wiring: construct `databases.NewService(pool, box, dockerProvider, dataDir)` next to the existing reconciler wiring; pass to jobs pipeline struct and API server (Task 5)
- Test (`dbprovision_test.go`, fake provider): worker flips status provisioning→running; provider error → status error and job returns nil (no retry storm — provisioning is not idempotent-safe; mark failed and surface via UI)
- Commit `feat: add database provisioning job`

### Task 5: Deploy-time injection
- `internal/jobs/deploy.go` `Pipeline`: add `DBEnv func(ctx context.Context, appID pgtype.UUID) (map[string]string, error)` (nil-safe); in `run()` after `DecryptedEnv`: merge `DBEnv` result (attachment keys overwrite nothing — Attach already prevents collisions; on conflict here, fail the deploy with a clear error), and when non-empty set `spec.Networks = []string{"cargo-proxy", "cargo-data"}`
- Wire `DBEnv: dbSvc.EnvFor` in `cmd/server`
- Test (`deploy_test.go` additions, fake provider capture): deploy with stubbed `DBEnv` returning `DATABASE_URL` → spec.Env contains it and spec.Networks includes `cargo-data`; without attachments → Networks empty (default)
- Commit `feat: inject managed database urls into deploys`

### Task 6: API
- `internal/api/server.go`: `DatabaseService` interface (Create/List/Get/Delete/Provision-not-needed/Attach/Detach/Snapshot/ListSnapshots/SnapshotPath/DeleteSnapshot/EnvFor omitted — mirror exact `internal/databases` signatures), `Server.databases` field + `Enqueuer` gains `EnqueueDBProvision`
- `internal/api/databases.go` handlers + `databasesError` mapping (`ErrValidation`→400, `ErrNotFound`→404, `ErrForbidden`→403, `ErrConflict`→409):
  - `POST /orgs/{orgID}/databases` → create + enqueue provision → 202 `{id, status}`
  - `GET /orgs/{orgID}/databases` → list with `attachment_count`
  - `GET /databases/{id}` → detail incl. attachments `{app_id, app_name, db_name|db_index}` (no secrets), `size_bytes`
  - `DELETE /databases/{id}`; `GET /databases/{id}/logs` (stream the provision log file like deployment logs)
  - `POST /databases/{id}/attachments` `{app_id}` → 201 `{url, env_key}` (**only time url appears**)
  - `DELETE /databases/{id}/attachments/{appID}`
  - `POST /databases/{id}/snapshots` → 201 `{name}`; `GET …/snapshots` list; `GET …/snapshots/{name}` → `http.ServeFile`; `DELETE …/snapshots/{name}`
- `internal/api/router.go`: org routes inside the existing `/orgs/{orgID}` block; `/databases/{dbID}` block beside `/apps/{appID}` (requireAuth group)
- Handler tests with a stub `DatabaseService`: role/404/409 mappings, attach response contains url + subsequent GET detail does not, snapshot download path validated by the stub call, create → 202 and enqueue captured
- Commit `feat: add managed databases api`

### Task 7: Frontend — Databases tab
- `web/src/components/DatabasesTab.tsx` (rendered from `OrgDashboard.tsx` beside existing sections): instance cards (engine badge, version, StatusBadge-style dot for status, size, attached-app chips, host port when set), provision form (name, engine select, version select dependent on engine, redis mode select shown for redis, "expose port" checkbox with warning copy), delete dialog requiring the typed instance name before enabling the button, per-instance: attach picker (`useQuery` org apps minus attached), detach buttons, snapshots list (take/download link/delete)
- One-time URL modal after attach: shows `url` with a copy button and the line "Save this now — it will not be shown again."
- `web/src/pages/OrgDashboard.tsx`: add the tab/section; API helpers reuse `api/put/del/post` from `lib/api`
- Tests `DatabasesTab.test.tsx`: provision POST payload `{name, engine, version, redis_mode?, expose_port}`; attach shows the one-time modal with the url exactly from the response; delete button disabled until the typed name matches; detach calls DELETE
- `npx vitest run`, `npm run build`
- Commit `feat(web): add org databases tab`

### Task 8: Docs + close 8.1
- `PhasedPlans.md`: 8.1 → ✅ with spec+plan references, acceptance line replaced by shipped summary; Phase 8 note "8.1 shipped <date>"
- `PRD.md`: no change needed (roadmap cell is historical) — verify and leave
- `internal/api/instance.go` `version` → `1.2.0`
- README: short "Managed databases" section (provision, attach, snapshot paths under dataDir)
- Full verification: `go test -p 2 ./...`, `golangci-lint run ./...` (0 issues), `npx vitest run`, `npm run build`
- Commit `docs: mark managed databases complete`

## Self-review
- Spec §2 model → Task 1 (tables/constraints verbatim) · §3 lifecycle → Tasks 3–5 (provision job, attach/detach SQL+ACL, injection, delete guards) · §4 seam → Task 2 (DBSpec/DatabaseProvider, Networks) · §5 backups → Tasks 2–3, 6 (SnapshotDB, path validation, download route) · §6 API table → Task 6 (all 11 routes) · §7 security → Global Constraints + Tasks 2–3 (secrets in .env/redis.conf 0600, quoteLiteral, stdin passwords) · §8 frontend → Task 7 · §9 testing → per-task tests incl. real-Docker isolation checks
- Deliberate deviations from spec, all narrowing: one attachment per engine per app (prevents env-key collision); disk usage read from the engine (`pg_database_size` / `INFO memory`) instead of volume `du`; postgres snapshot = `pg_dumpall --clean` (whole instance, plain SQL); redis ACL rule set pinned in Task 3 with the isolation test the spec requires
- Signatures consistent: `EnvFor`, `EnqueueDBProvision`, `DBSpec`, `Networks` used identically across Tasks 2–6
