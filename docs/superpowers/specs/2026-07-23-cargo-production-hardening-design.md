# Cargo — Production Hardening Design (milestone m11)

Date: 2026-07-23 · Status: Approved design, pre-implementation
Parent docs: [PRD](../../../PRD.md) · [v1 design spec](2026-07-17-cargo-design.md) · [findings](2026-07-23-cargo-production-readiness.md)

Turns the production-readiness audit into a buildable milestone. These are **operational
safety** concerns not covered by the PRD or roadmap — the gap between feature-complete and
"trusted to hold a team's apps and secrets." Ordered by blast radius; every item is
independently shippable.

## 1. Goals / non-goals

**Goals**
- The platform's own state (control DB, certs, master key) is recoverable after disk loss.
- A tenant app can neither reach the control-plane database nor destabilize the host
  (memory/CPU/PID/log/disk exhaustion).
- Standard web-security posture: headers, Origin checks, body caps, broad rate limiting.
- Operators can see the platform's own health, get told when deploys fail, and recover
  from a mid-deploy crash without hand-editing rows.
- Every state change is attributable (audit log).

**Non-goals (deferred)**
- Off-host / object-storage backup targets (local `<dataDir>` backups only in m11; S3 is a
  documented follow-up).
- Automatic restore-from-backup UI (restore is a documented, tested runbook).
- Rootless Docker / gVisor / full container sandboxing (we add cheap defense-in-depth:
  `no-new-privileges`, cap-drop, socket-proxy — not a new isolation runtime).
- Per-org billing quotas (PRD-excluded); m11 adds *stability* limits, not commercial ones.
- A full alerting rules engine — m11 fires on deploy failure and disk-low only.

## 2. Work items & design decisions

### 2.1 Network isolation — tenant apps must not reach the platform DB (Tier-0)
Today `deploy/docker-compose.yml` puts the platform `db` on `cargo-proxy`, and every
tenant app joins `cargo-proxy` (`reconciler/compose.go`). Any deployed app can dial
`db:5432`.

**Design:** introduce a dedicated **`cargo-system`** network for controlplane↔platform-DB
traffic. The platform `db` service leaves `cargo-proxy` entirely and joins only
`cargo-system`; the controlplane joins `cargo-proxy` (to be routed by Traefik and to reach
app containers for health probes), `cargo-system` (DB), and `cargo-data` (managed DBs).
Tenant apps join `cargo-proxy` (+ `cargo-data` only when they have attachments) and never
`cargo-system`. No app-compose change needed — the isolation comes from removing the
platform DB from the shared network. `install.sh` creates `cargo-system` (external, like
`cargo-proxy`/`cargo-data`).

*Verification:* from a tenant app container, `nc -z db 5432` / the control-DB host must
fail; controlplane→DB must still work.

### 2.2 App container stability limits (Tier-1: resource limits + log rotation + hardening)
`reconciler/compose.go` currently emits no limits and no security options.

**Design — extend `Spec` and the generated compose:**
- New `Spec` fields: `MemoryLimit string` (e.g. `512m`), `CPULimit string` (e.g. `1.0`),
  `PidsLimit int` (default 512). Rendered as top-level compose `mem_limit`, `cpus`,
  `pids_limit` (compose v2 non-swarm honors these).
- **Log rotation** always emitted:
  `logging: {driver: json-file, options: {max-size: "10m", max-file: "3"}}` on the app
  service (and on `GenerateDBCompose`).
- **Hardening** always emitted on the app service:
  `security_opt: ["no-new-privileges:true"]`. Capabilities are left at Docker defaults in
  m11 (dropping ALL breaks many app images); `no-new-privileges` is the safe universal win.
  Document `cap_drop` as an opt-in per-app follow-up.
- **Persistence:** new nullable columns on `applications` —
  `mem_limit TEXT`, `cpu_limit TEXT`, `pids_limit INT` — with instance-wide defaults from
  config (`CARGO_DEFAULT_MEM_LIMIT` default `512m`, `CARGO_DEFAULT_CPU_LIMIT` default `1`,
  `CARGO_DEFAULT_PIDS_LIMIT` default `512`). App Settings tab exposes overrides.
- Existing golden compose tests get new expected output; a spec with zero limits (all
  defaults applied by the service) still renders deterministically.

### 2.3 Control-plane backup & disaster recovery (Tier-0)
No backup of the control DB, `acme.json`, or the master key exists.

**Design:**
- **Scheduled logical backup:** a new periodic River job `platform_backup` (daily,
  `RunOnStart` false) dumps the control database into
  `<dataDir>/platform-backups/<RFC3339>.dump`, keeping the last N (default 14, config
  `CARGO_PLATFORM_BACKUP_KEEP`). **The runtime image does not bundle `pg_dump`** (Dockerfile
  installs only the docker CLI + git/curl), so — mirroring how m10 managed-DB snapshots work
  — the job runs `pg_dump -Fc` **via `docker exec` into the platform `db` container**
  (`docker exec -i <db> pg_dump -Fc -U <user> <db>` streamed to the dump file), not from the
  controlplane's own filesystem. The db container/service name is resolved from the platform
  compose project (config `CARGO_PLATFORM_DB_CONTAINER`, default the compose service `db`).
  *Alternative considered and rejected:* adding `postgresql-client` to the image — heavier
  image, and the `docker exec` path already exists and is proven for managed DBs.
- **Cert + key safety:** certs do **not** live under `<dataDir>` — they're in the separate
  `cargo-acme` volume mounted at `/acme` in the **Traefik** container (`acme.json`, plus
  `acme-dns.json` in dns01 mode). To reach them, the backup job copies via
  `docker cp <traefik>:/acme/. <dataDir>/platform-backups/<RFC3339>.certs/` (or the compose
  stack mounts `cargo-acme` read-only into the controlplane at `/acme-ro` — decide in the
  plan; `docker cp` avoids a compose change). The job also writes a `master-key.fingerprint`
  (SHA-256 of the key) so an operator can verify which key a backup set belongs to. The
  **master key itself is never written to disk by the job** — the install script remains the
  source of truth and the README gets a hard "store the key in a password manager; a backup
  is useless without it" section.
- **Restore runbook** (`docs/` + README): stop stack → `pg_restore` the dump into a fresh DB
  (via `docker exec` into the db container) → restore the `cargo-acme` volume from the
  `.certs/` copy (both `acme.json` and `acme-dns.json` when present) → set the *same* master
  key → `up -d`. A test in `scripts/` exercises dump→`pg_restore` into a scratch database in CI.
- Admin UI: a read-only "Backups" panel listing the backup files with size/time and a
  "run backup now" button (`POST /admin/backups`).

### 2.4 Disk-space guardrail (Tier-1)
Nothing watches free disk; a full disk corrupts Postgres.

**Design:** a periodic job `disk_check` (every 10 min) `statfs`-es `<dataDir>` and the
Docker root; when free space drops below a threshold (config
`CARGO_DISK_MIN_FREE_PCT`, default 10%) it (a) logs a warning, (b) records the condition
in `instance_settings`/a small status row for the admin UI, and (c) fires a platform alert
(§2.8). At <5% it triggers an aggressive image prune. Admin dashboard shows current
free-space per mount.

### 2.5 HTTP middleware bundle (Tier-2: headers + Origin/CSRF + body cap + rate limit)
`router.go` uses only RequestID/Recoverer/Logger.

**Design — add, in order, before route groups:**
- **Security headers** (all responses): `X-Content-Type-Options: nosniff`,
  `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, a conservative
  `Content-Security-Policy` for the SPA (self scripts/styles, `connect-src 'self'` for the
  API + SSE), and `Strict-Transport-Security` **only when `cfg.Env == "production"`**.
- **Body cap:** `http.MaxBytesReader` wrapper (default 1 MiB) on all `/api/v1` routes
  except the already-capped webhook and the snapshot/log *download* routes (which stream
  responses, not request bodies).
- **Origin/CSRF:** for state-changing methods (`POST/PUT/PATCH/DELETE`) on cookie-authed
  routes, require `Origin` (or `Referer`) to match the request host; reject mismatches
  with 403. The GitHub webhook (HMAC-authed, no cookie) and OIDC/GitHub callbacks (GET) are
  exempt. This complements the existing `SameSite=Lax`.
- **General rate limit:** a per-user (fallback per-IP) token-bucket on mutating routes and
  on SSE subscribe routes, separate from `authRateLimiter`. Config
  `CARGO_API_RATELIMIT_RPS` (default 20 rps, burst 40).

All middleware is unit-tested at the router level (header presence, oversized-body 413,
cross-origin POST 403, 429 past the limit, webhook exemption).

### 2.6 Readiness probe (Tier-3)
`/healthz` is liveness-only. Add **`GET /readyz`**: pings Postgres (`pool.Ping`) and Docker
(`docker version` with a short timeout); 200 only when both succeed, 503 with a JSON body
naming the failed dependency otherwise. `deploy/docker-compose.yml` controlplane
healthcheck switches to `/readyz`.

### 2.7 Orphaned-deployment reaper (Tier-3)
A crash mid-deploy leaves rows stuck in `building`/`deploying`. River re-runs the job on
restart, but rows for jobs that will never resume (e.g. cancelled) stall forever.

**Design:** on startup, a reaper marks deployments in non-terminal states (`queued`,
`building`, `deploying`) with no corresponding active River job as `failed`
(error: "interrupted by a platform restart"). Because River resumes in-flight jobs, the
reaper must run **after** River reports its recoverable jobs; simplest safe rule: fail only
deployments whose `started_at` is older than a grace window (default 15 min) and which have
no running job. Also add a bounded shutdown: `httpServer.Shutdown` and `client.Stop` get a
30 s context instead of `context.Background()`.

### 2.8 Control-plane self-observability & alerting (Tier-3)
The platform scrapes Traefik/Docker for tenant apps but exposes nothing about itself, and
has no alerting path.

**Design:**
- **`GET /metrics` (Prometheus)** on the controlplane: River queue depth & job counts,
  deploy success/failure counters, deploy duration histogram, DB pool stats
  (`prometheus/client_golang`). **Exposure mechanism:** the controlplane's single `:8080`
  listener is routed publicly by Traefik (entrypoints `web,websecure`), so `/metrics` cannot
  simply live there. Serve it on a **second, internal-only listener** — a dedicated metrics
  HTTP server bound to `CARGO_METRICS_ADDR` (default `:9090`) started alongside the main
  server — that Traefik does not route and that publishes no host port (reachable only from
  inside the `cargo-system`/`cargo-proxy` networks by a scraper container). This mirrors
  Traefik's own `:8082` internal metrics entrypoint. `/metrics` is therefore never registered
  on the public `:8080` router at all, which is stronger than source-IP gating.
- **Notifications/alerting:** a small `internal/notify` service with two sinks — email (via
  the existing `mailer`) and an outbound webhook (Slack/Discord-compatible JSON), URL
  stored encrypted in instance settings. Events in m11: **deployment failed**, **disk low**,
  **backup failed**. Per-org email recipients default to org owners/admins; the platform
  webhook is instance-level. Deploy-success notification is opt-in per app (off by default,
  noise control).

### 2.9 Audit log (Tier-3)
No record of who did what.

**Design:** append-only `audit_log` table (`id`, `actor_id`, `org_id NULL`, `action`,
`target_type`, `target_id`, `detail JSONB`, `created_at`). A tiny `internal/audit` helper
`Record(ctx, actor, org, action, target, detail)` called from the service layer at the
mutation sites that matter: login (success/failure), role change, invite create/revoke, app
create/update/delete, deploy/rollback/stop/start, env change (keys only, never values),
domain add/remove, database provision/attach/detach/delete, instance-settings changes,
OIDC/SMTP/GitHub-App config changes. Writes are best-effort (a failed audit write logs but
never fails the user action). Surfaced read-only: org-scoped view for org admins,
instance-wide for instance admin. Retention pruned by housekeeping (default 180 days,
config `CARGO_AUDIT_RETENTION_DAYS`).

## 3. Config additions (all with safe defaults; `internal/config`)

| Env var | Default | Purpose |
|---|---|---|
| `CARGO_DEFAULT_MEM_LIMIT` | `512m` | per-app memory cap default |
| `CARGO_DEFAULT_CPU_LIMIT` | `1` | per-app CPU cap default |
| `CARGO_DEFAULT_PIDS_LIMIT` | `512` | per-app PID cap default |
| `CARGO_PLATFORM_BACKUP_KEEP` | `14` | control-DB backups retained |
| `CARGO_PLATFORM_DB_CONTAINER` | `db` | platform DB container/service for `docker exec` dumps |
| `CARGO_DISK_MIN_FREE_PCT` | `10` | warn/alert threshold |
| `CARGO_API_RATELIMIT_RPS` | `20` | general API limiter |
| `CARGO_METRICS_ADDR` | `:9090` | internal-only listener for `/metrics` |
| `CARGO_AUDIT_RETENTION_DAYS` | `180` | audit-log retention |

## 4. Security model summary

- Platform DB unreachable from tenant network (`cargo-system` isolation).
- Tenant apps bounded (mem/cpu/pids/logs) and `no-new-privileges`.
- Control-plane recoverable: scheduled dump + cert bundle + key fingerprint + tested
  restore.
- Web surface: headers, Origin checks, body caps, broad rate limiting on top of existing
  auth limiter + `SameSite=Lax`.
- Every mutation attributable (audit log); operators alerted on failure conditions.
- Master key still never persisted by the platform; documented human custody.

## 5. Testing

- **Network:** integration/manual check that a tenant container cannot open the control-DB
  port; controlplane still connects. Compose golden/topology assertion.
- **Compose:** golden tests for new limits/logging/security_opt in app + DB compose; default
  application when app columns are null.
- **Backup:** CI script dumps a seeded DB and `pg_restore`s into a scratch DB, asserting a
  known row survives; retention keeps exactly N.
- **Middleware:** router-level tests — header presence, 413 oversize, 403 cross-origin POST,
  429 over limit, webhook/OIDC exemptions.
- **readyz:** 200 both-up, 503 with dependency name when DB/Docker down (stubbed).
- **Reaper:** seed a stuck `building` deployment older than grace with no job → becomes
  `failed`; a fresh one is untouched.
- **Self-metrics:** `/metrics` exposes expected series; access gating enforced.
- **Notify:** deploy-failure fires email + webhook (fake sinks); disk-low and backup-fail
  paths; webhook URL never returned by GET.
- **Audit:** each wired mutation writes exactly one row with the right actor/target; failed
  audit write doesn't fail the action; retention prune.
- Bar: full `go test ./...`, `golangci-lint` 0 issues, `npx vitest run`, `npm run build`,
  `docker build`.
