# Cargo — Managed Databases Design (v2 · Phase 8.1 · milestone m10)

Date: 2026-07-18 · Status: Approved design, pre-implementation
Parent docs: [PRD](../../../PRD.md) · [v1 design spec](2026-07-17-cargo-design.md)

Adds managed Postgres and Redis instances per org (PRD Phase 2, "managed databases"). Second of six v2 features; execution order: 8.2 OIDC (✅) → **8.1 managed databases** → 8.3 compose app source → 9.2 blue/green → 9.1 multi-server → 9.3 metrics.

## 1. Goals / non-goals

**Goals**
- Org admins provision a managed Postgres or Redis instance for their org from the UI, picking a major version (Postgres 16/17, Redis 7).
- One instance serves many apps: attaching an app creates its own logical database + role (Postgres) or ACL user / DB index (Redis) on the shared instance and injects `DATABASE_URL`/`REDIS_URL` env vars through the existing per-app `.env` path.
- Detach revokes credentials and removes the env vars on next deploy.
- Manual snapshots (`pg_dump` / RDB copy), listed and downloadable.
- Instance status (running state, version, disk usage, attached apps) visible in the UI.
- Optional per-instance host-port publication for external clients (psql/GUI from a laptop).

**Non-goals (deferred)**
- Scheduled/automatic backups and restore-from-snapshot via UI (snapshot files are restorable by hand).
- In-place version upgrades (provision new + migrate manually).
- Other engines (MySQL, Mongo, …) — the schema's `engine` column leaves room.
- Connection pooling (pgbouncer), replicas, HA.
- Per-app resource limits/quotas on shared instances.

## 2. Model

One **instance** = one Docker Compose project (`cargo-db-<id>`) with a named volume, owned by an org. One **attachment** = one app's logical slice of an instance.

```
org ──< database_instances ──< database_attachments >── applications
```

**`database_instances`**
- `id UUID PK`, `org_id → organizations ON DELETE RESTRICT`, `name` (unique per org, slug-like), `engine` (`postgres`|`redis`), `version` (`16`|`17` / `7`), `redis_mode` (`acl`|`shared`, NULL for postgres), `host_port INT NULL` (published port when external access enabled), `status` (`provisioning`|`running`|`error`|`stopped`), `admin_secret JSONB` (`{"enc": …}` — Box-encrypted superuser/requirepass credential), `created_at`.

**`database_attachments`**
- `id UUID PK`, `instance_id → database_instances ON DELETE CASCADE`, `app_id → applications ON DELETE CASCADE`, `db_name` + `role_name` (postgres) or `acl_user` + `db_index` (redis), `secret JSONB` (Box-encrypted password; NULL for redis `shared` mode, which reuses the instance credential), `created_at`, `UNIQUE (instance_id, app_id)`.

**Redis modes** (chosen at provision, immutable):
- `acl` — each attachment gets its own ACL user (own password) restricted to its numbered database. Apps cannot read each other's keys or authenticate as each other.
- `shared` — attachments share the instance password and differ only by database index. Simpler, no per-app isolation.
- Both modes cap at 16 attachments (Redis default database count).

## 3. Lifecycle

**Provision** (org admin/owner): API inserts the row with `status=provisioning` and enqueues a `provision_database` job on the existing jobs queue. The worker renders the compose project (image `postgres:<v>-alpine` / `redis:<v>-alpine`, named volume, healthcheck, internal `cargo-data` network, optional `ports:` entry), `docker compose up -d`, waits for engine readiness (`pg_isready` / `PING`), then sets `status=running`. Failure → `status=error` with the job log viewable in the UI (same log-file pattern as deploy logs).

**Attach** (org admin/owner): synchronous API call —
- Postgres: `CREATE ROLE app_<slug> LOGIN PASSWORD …; CREATE DATABASE app_<slug> OWNER app_<slug>;` via `docker exec psql`. `REVOKE CONNECT ON DATABASE … FROM PUBLIC` keeps neighbors out.
- Redis `acl`: `ACL SETUSER app_<slug> on ><password> ~* +@all -@admin` scoped by requiring `SELECT <index>`… in practice: ACL user limited to its index via `resetchannels`/key patterns is weak, so isolation is enforced by giving each user `+select` only to its own index through the URL (`redis://user:pass@host:6379/<index>`) plus `-select` in the ACL. The plan phase pins the exact ACL rule set; the spec requirement is: *an acl-mode attachment must not be able to read another attachment's keys or authenticate as it.*
- Redis `shared`: assign the lowest free `db_index`; no new credential.
- Rejected when the app already has a user-defined env var named `DATABASE_URL`/`REDIS_URL` (collision), when the app belongs to a different org, or when the index cap is reached.
- Response includes the full connection URL **once**; afterwards GETs return only non-secret parts (host, db name, user).

**Injection**: `deployments` assembles the deploy spec env as *user env vars + attachment URLs*; attachment URLs are recomputed from stored credentials at deploy time (never stored in `app_env_vars`). Host is the instance's container DNS name on `cargo-data`; the generated app compose joins `cargo-data` in addition to `cargo-proxy` whenever the app has attachments.

**Detach**: revokes access — Postgres `ALTER ROLE … NOLOGIN` then `DROP ROLE` after terminating its sessions; Redis `ACL DELUSER` (acl) or index freed (shared). The Postgres *database and its data are kept* (dropped only with the instance); the UI says so. Next deploy loses the env vars.

**Delete instance**: blocked with `409` while attachments exist; UI requires typing the instance name; `docker compose down -v` (volume removed), then the row is deleted. Snapshot files are kept on disk.

**Stop/start** are not exposed in v1 (`status=stopped` is reserved for a future toggle; the reconciler treats a manually stopped container as `error`).

## 4. Reconciler seam

`internal/reconciler` gains a `DatabaseProvider` interface (Docker impl in the same package, K8s slots in later):

```go
type DatabaseProvider interface {
    ProvisionDB(ctx, DBSpec, log io.Writer) error   // compose up + readiness gate
    ExecDB(ctx, instanceID string, engine, cmd …) (string, error) // psql/redis-cli exec
    SnapshotDB(ctx, instanceID string, engine, destPath string) error
    TeardownDB(ctx, instanceID string, log io.Writer) error       // down -v
}
```

`Spec` (apps) gains `Networks []string` (default `["cargo-proxy"]`); `GenerateCompose` renders them. Database compose generation is a sibling of `GenerateCompose` — fully platform-controlled strings, no user YAML.

## 5. Backups

- `POST /databases/{id}/snapshots` → runs `pg_dump -Fc` (postgres) or `BGSAVE` + copy of `dump.rdb` (redis) into `<dataDir>/db-backups/<instanceID>/<RFC3339 timestamp>.{dump,rdb}`.
- `GET /databases/{id}/snapshots` lists name/size/created; `GET …/snapshots/{name}` streams the file (org-gated); `DELETE …/snapshots/{name}` removes it.
- Snapshot path names are server-generated; the name parameter is validated against the listing (no path traversal).

## 6. API surface

| Method & path | Auth | Behavior |
|---|---|---|
| `GET /api/v1/orgs/{orgID}/databases` | org member | list instances with status + attachment counts |
| `POST /api/v1/orgs/{orgID}/databases` | org admin | `{name, engine, version, redis_mode?, expose_port?}` → 202, provisioning job |
| `GET /api/v1/databases/{id}` | org member | detail: status, version, disk usage, attachments (no secrets), host port |
| `DELETE /api/v1/databases/{id}` | org admin | 409 while attachments exist; tears down compose project + volume |
| `GET /api/v1/databases/{id}/logs` | org member | provisioning job log |
| `POST /api/v1/databases/{id}/attachments` | org admin | `{app_id}` → creates credentials, returns the URL **once** |
| `DELETE /api/v1/databases/{id}/attachments/{appID}` | org admin | revoke + env removal on next deploy |
| `POST /api/v1/databases/{id}/snapshots` | org admin | take snapshot |
| `GET /api/v1/databases/{id}/snapshots` | org member | list snapshots |
| `GET /api/v1/databases/{id}/snapshots/{name}` | org member | download |
| `DELETE /api/v1/databases/{id}/snapshots/{name}` | org admin | delete snapshot |

Role gating mirrors apps (member read, admin/owner mutate). Errors use the standard envelope; validation failures → 400, attach collisions → 409.

## 7. Security

- All instance/attachment passwords are 128-bit `crypto/rand`, stored only Box-encrypted (same `{"enc": …}` pattern as SMTP/OIDC); never logged; full URLs appear exactly once in the attach response.
- Instances join only `cargo-data` (internal). The optional host port is the single exposure path, off by default, shown with a warning in the UI.
- `cargo-data` is attachment-driven: apps join it only when they have at least one attachment, so unrelated apps have no network path to org databases. Cross-org isolation relies on credentials (all orgs share `cargo-data` in v1; per-org networks are a noted future hardening).
- exec'd SQL/ACL commands are built from validated identifiers (slug-derived, `[a-z0-9_]` only) and passwords passed via stdin/env, not argv.

## 8. Frontend

- Org page gains a **Databases** tab: instance cards (engine badge, version, status dot, disk usage, attached-app chips, host port), provision form (name, engine, version, redis mode, expose toggle), delete-with-typed-name dialog.
- Instance detail: attach picker (org's apps not yet attached), one-time URL modal with copy button ("you won't see this again"), detach buttons, snapshot list with take/download/delete.
- App page: read-only "Attached databases" note listing injected env var names.

## 9. Testing

- `internal/databases` service tests with testcontainers + real Docker: provision postgres → attach two apps → verify each role connects only to its own database; redis acl mode cross-access rejected; shared mode index assignment; detach revokes login; delete blocked with attachments; snapshot file appears and is a valid dump.
- Reconciler: golden tests for database compose generation and for app compose with `Networks` including `cargo-data`.
- API handler tests on a stubbed service (role gating, 409s, one-time secret shape, snapshot name validation).
- Web: vitest for the Databases tab (provision form payload, one-time URL modal, delete confirmation gating).
- Bar: full `go test ./...`, `golangci-lint` 0 issues, `npx vitest run`, `npm run build`.
