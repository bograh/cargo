# Cargo M11 — Production Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans.

**Goal:** Ship the operational-safety milestone (spec:
`docs/superpowers/specs/2026-07-23-cargo-production-hardening-design.md`): network
isolation of the platform DB, tenant-app stability/hardening limits, control-plane backup
& DR, disk guardrail, HTTP security middleware, readiness probe, orphaned-deploy reaper,
control-plane self-metrics + alerting, and an audit log.

**Architecture:** Infra changes land in `deploy/*.yml` + `install.sh`; reconciler
compose generation gains limits/logging/security_opt; two new periodic River jobs
(`platform_backup`, `disk_check`) plus a startup reaper; a middleware bundle in
`internal/api`; new `internal/notify` and `internal/audit` packages; new `readyz`/`metrics`
endpoints; small schema additions (`applications` limit columns, `audit_log` table).

**Tech stack:** No new runtime services. Only one new Go dep: `prometheus/client_golang`
(self-metrics). `golang.org/x/time` is **already in `go.mod`** (v0.15.0) — the general
limiter uses its `rate` subpackage, no `go get` needed. Everything else reuses docker CLI
helpers, River, `crypto.Box`, sqlc, chi, the existing `mailer`, React Query. Backups and the
control-DB dump go through the Docker socket (`docker exec`/`docker cp`), since the image
ships no `pg_dump` and certs live in Traefik's `cargo-acme` volume — not the controlplane FS.

## Global Constraints

- Every config addition has a safe default (see spec §3) and is validated in
  `internal/config`; missing/blank env falls back to the default, never errors.
- No change may alter existing golden compose output *unless* the task explicitly updates
  the expected files (Tasks 1–2); reference `compose_test.go` before/after.
- Secrets (webhook URLs, master key) never logged, never returned by any GET; outbound
  webhook URL stored Box-encrypted like SMTP/OIDC.
- Audit/notify writes are best-effort: failure logs at WARN and never fails the user action.
- Role gating mirrors existing patterns (`roleRank`, `requireInstanceAdmin`); audit views
  org-scoped for org admins, instance-wide for instance admin.
- Backups/alerts must degrade safely: a failed backup or unreachable webhook logs + alerts,
  never crashes the worker or blocks deploys.
- Verification bar per task: `go test ./...`, `golangci-lint run ./...` (0 issues),
  `npx vitest run`, `npm run build`; Docker-touching tasks also `docker build .`.

## Tasks

### Task 1: Network isolation — `cargo-system` (spec §2.1)
- `deploy/docker-compose.yml`: add a **compose-managed `internal: true`** network
  `cargo-system` (NOT external — see deviation below); move the platform `db` service off
  `cargo-proxy` onto **only** `cargo-system`; add `cargo-system` to the `controlplane`
  service (keep `cargo-proxy`). Traefik unchanged (`cargo-proxy` only).
- `deploy/docker-compose.dev.yml`: mirror — `db` on `cargo-system` only; controlplane on
  `default` (for outbound internet, since dev has no `cargo-proxy` at startup) + `cargo-system`.
- `*.tls.yml`, `*.dns01.yml`: no change needed — they only override Traefik command +
  controlplane labels, so they inherit the base network topology (verified via
  `docker compose config`).
- **No `install.sh` change** — `cargo-system` is compose-managed, so `docker compose up`
  creates it; unlike `cargo-proxy`/`cargo-data` it is never referenced by externally-created
  app containers, so it needs no external lifecycle.
- No `reconciler/compose.go` change — apps already default to `cargo-proxy`; isolation comes
  from the DB leaving that network.
- Verified per spec §5: `docker compose config` topology + a runtime throwaway-container
  proof (controlplane on both nets reaches `db:5432`; a container on `cargo-proxy` only
  cannot even resolve `db`). Documented in `deploy/README.md`.
- **Deliberate deviation:** `cargo-system` is compose-managed + `internal: true` rather than
  the external network the pre-implementation plan named. Rationale: it carries only the
  platform stack's own controlplane↔DB traffic (no dynamically-created app container ever
  joins it), so external lifecycle management is unnecessary, and `internal: true` denies the
  DB any outbound route as extra defense in depth. Net effect matches the spec's isolation
  goal with less operational surface (no install.sh step).
- Commit `fix(deploy): isolate platform database on cargo-system network`

### Task 2: App compose limits, log rotation, hardening (spec §2.2)
- `internal/db/migrations/00014_app_resource_limits.sql`: add nullable
  `mem_limit TEXT`, `cpu_limit TEXT`, `pids_limit INT` to `applications` (down: drop).
- `internal/db/queries/apps.sql`: include the new columns in create/update/get; `sqlc generate`.
- `internal/config`: `DefaultMemLimit`/`DefaultCPULimit`/`DefaultPidsLimit` from env with
  spec defaults; validation tolerant of blank.
- `internal/reconciler/spec.go`: add `MemoryLimit string`, `CPULimit string`,
  `PidsLimit int` to `Spec`.
- `internal/reconciler/compose.go`: `GenerateCompose` renders `mem_limit`, `cpus`,
  `pids_limit` when set; always renders
  `logging: {driver: json-file, options:{max-size: "10m", max-file: "3"}}` and
  `security_opt: [no-new-privileges:true]` on the app service. `GenerateDBCompose` gets the
  same `logging` block.
- `internal/apps` service + `internal/jobs/deploy.go`: populate `Spec` limit fields from the
  app row, falling back to config defaults when null.
- `internal/apps` validation: `mem_limit` matches `^\d+(b|k|m|g)?$`, `cpu_limit` a positive
  decimal, `pids_limit` a positive int; reject otherwise (`ErrValidation`).
- Tests: update `compose_test.go` golden output (app + DB); new cases for limits set vs
  null-defaults; validation table test.
- `web/src/pages/AppSettings.tsx`: add memory/CPU/PID fields with helper text; vitest for
  the PATCH payload.
- Commit `feat(apps): per-app resource limits, log rotation, no-new-privileges`

### Task 3: Control-plane backup & DR (spec §2.3)
- **Preflight (do first):** the runtime image has **no `pg_dump`** (`Dockerfile:20` = docker
  CLI + git/curl only) and `acme.json` is **not** under `<dataDir>` — it lives in the
  `cargo-acme` volume at `/acme` in the Traefik container (plus `acme-dns.json` in dns01
  mode). Both the dump and the cert copy therefore go through the Docker socket, not the
  controlplane FS. Add `CARGO_PLATFORM_DB_CONTAINER` (default `db`) to `internal/config`.
- `internal/jobs/platformbackup.go`: `PlatformBackupArgs{}` kind `platform_backup`; worker
  (a) dumps via `docker exec -i <CARGO_PLATFORM_DB_CONTAINER> pg_dump -Fc -U <user> <db>`
  (user/db parsed from `cfg.DatabaseURL`) streamed to
  `<dataDir>/platform-backups/<RFC3339>.dump` — same `docker exec` pattern as m10
  `SnapshotDB`, reusing the reconciler `run`/`output` helpers; (b) copies certs via
  `docker cp <traefik-container>:/acme/. <dataDir>/platform-backups/<RFC3339>.certs/`
  (best-effort — skip with a log line if the container/path is absent); (c) writes
  `<RFC3339>.keyfp` (SHA-256 of master key); (d) prunes to `CARGO_PLATFORM_BACKUP_KEEP`.
  Failures fire a `backup_failed` alert (Task 8) and return the error for retry.
- `internal/jobs/client.go`: register worker + daily `PeriodicJob` (`RunOnStart:false`).
- API: `POST /admin/backups` (run now, 202), `GET /admin/backups` (list name/size/time);
  handlers gated by `requireInstanceAdmin`.
- `web/src/pages/Admin.tsx`: read-only Backups panel with list + "Run backup now".
- `scripts/backup-restore-test.sh`: seed a row, `pg_dump`, `pg_restore` into a scratch DB,
  assert the row survives; wire into CI (`.github/workflows/ci.yml`).
- `README.md` + `docs/`: restore runbook (`pg_restore` via `docker exec`; restore the
  `cargo-acme` volume — both `acme.json` and `acme-dns.json` — from the `.certs/` copy; set
  the *same* master key) + a hard master-key-custody section.
- Tests: worker unit test with a temp dir (dump command stubbed/real against testcontainer),
  retention keeps exactly N; handler role-gating test.
- Commit `feat(ops): scheduled control-plane backups and restore runbook`

### Task 4: Disk-space guardrail (spec §2.4)
- `internal/jobs/diskcheck.go`: `DiskCheckArgs{}` kind `disk_check`; `statfs` on
  `cfg.DataDir` (and Docker root when resolvable); below `CARGO_DISK_MIN_FREE_PCT` → WARN log
  + persist status + fire `disk_low` alert; below 5% → enqueue aggressive image prune.
- `internal/jobs/client.go`: register + 10-min `PeriodicJob`.
- Persist latest free-space per mount (small `instance_settings` key or a `disk_status` row);
  `GET /admin/disk` returns it; `web/src/pages/Admin.tsx` shows a gauge.
- Tests: threshold logic (fake statfs) → alert fired once when crossing; recovery clears.
- Commit `feat(ops): disk-space guardrail with admin surface and alert`

### Task 5: HTTP middleware bundle (spec §2.5)
- `internal/api/middleware.go`: `securityHeaders(cfg)`, `bodyLimit(max int64)`,
  `originCheck` (state-changing + cookie-authed only; exempt webhook + GET callbacks),
  `apiRateLimiter(rps, burst)` (per-user via auth context, per-IP fallback; `x/time/rate`).
- `internal/api/router.go`: apply `securityHeaders` at the top; `bodyLimit` +
  `apiRateLimiter` + `originCheck` inside the `/api/v1` group *excluding* `/webhooks/github`
  and the streaming download routes from `bodyLimit`.
- `internal/config`: `APIRateLimitRPS` (default 20, burst 40).
- Tests (`middleware_test.go`, `httptest`): header presence; `production` adds HSTS; 413 on
  oversize body; 403 on cross-origin POST; 429 past the limit; webhook exempt from origin +
  body cap.
- Commit `feat(api): security headers, origin checks, body cap, general rate limit`

### Task 6: Readiness probe + shutdown/reaper (spec §2.6, §2.7)
- `internal/api/health.go`: `ReadyHandler` pings `pool.Ping(ctx)` + `docker version`
  (2 s timeout); 200 both-up else 503 `{error:{code, message}}` naming the failed dep.
- `internal/api/router.go`: `r.Get("/readyz", s.ReadyHandler)`.
- `deploy/docker-compose.yml`: controlplane healthcheck → `/readyz`.
- `internal/deployments`: `ReapOrphaned(ctx, grace)` marks `queued|building|deploying`
  deployments older than grace with no active River job → `failed`
  (error "interrupted by a platform restart").
- `cmd/server/main.go`: call `ReapOrphaned` after River starts; bound shutdown with a 30 s
  context for `httpServer.Shutdown` and `client.Stop` (replace `context.Background()`).
- Tests: readyz 200/503 (stub deps); reaper fails a stale stuck row, leaves a fresh one and
  terminal rows untouched.
- Commit `feat(ops): readiness probe, bounded shutdown, orphaned-deploy reaper`

### Task 7: Control-plane self-metrics (spec §2.8, metrics half)
- Add `github.com/prometheus/client_golang` (only new dep — `x/time` is already in `go.mod`).
- `internal/api/metrics_self.go`: register collectors — River queue depth/job counts
  (query River tables), deploy success/failure counters + duration histogram (incremented
  from `internal/jobs/deploy.go` on terminal transitions), `pgxpool` stats.
- **Exposure:** the controlplane's `:8080` is routed publicly by Traefik, so `/metrics` must
  NOT be registered on the main router. Instead start a **second internal-only HTTP server**
  bound to `CARGO_METRICS_ADDR` (default `:9090`, add to `internal/config`) in
  `cmd/server/main.go`, serving only `promhttp.Handler()` at `/metrics`. The compose stack
  publishes no host port for it (reachable only inside the docker networks by a scraper);
  mirror Traefik's `:8082` internal pattern. Bound its shutdown with the same 30 s context as
  the main server (Task 6).
- Tests: the metrics server exposes expected series names; `/metrics` is absent from the main
  `:8080` router (assert 404 on the public mux).
- Commit `feat(ops): prometheus self-metrics on an internal listener`

### Task 8: Notifications / alerting (spec §2.8, notify half)
- `internal/db/migrations/00015_notify.sql`: `webhook_url_enc BYTEA` in instance settings
  (or a settings key) + optional per-org recipients; per-app `notify_on_success BOOLEAN
  DEFAULT false`.
- `internal/notify/service.go`: `Notify(ctx, Event)` fans out to email (existing `mailer`,
  recipients = org owners/admins) and the instance webhook (JSON POST, Slack/Discord shape);
  events `deploy_failed`, `deploy_succeeded` (opt-in), `disk_low`, `backup_failed`.
  Best-effort; each sink failure logged, never propagated.
- Wire calls: `internal/jobs/deploy.go` (terminal status), Task 3 (backup fail), Task 4
  (disk low).
- API: `PUT/GET/DELETE /admin/settings/notify-webhook` (URL write-only in GET); per-app
  `notify_on_success` in the app PATCH.
- `web/src`: admin notify-webhook field; app-settings success-toggle.
- Tests: fake sinks — deploy failure fires both; success only when opt-in; webhook URL never
  returned by GET; sink error doesn't fail the caller.
- Commit `feat(notify): deploy/disk/backup alerts via email and webhook`

### Task 9: Audit log (spec §2.9)
- `internal/db/migrations/00016_audit_log.sql`: `audit_log(id, actor_id NULL, org_id NULL,
  action TEXT, target_type TEXT, target_id TEXT, detail JSONB, created_at)` + index on
  `(org_id, created_at)` and `(created_at)` for prune.
- `internal/db/queries/audit.sql`: `InsertAuditLog`, `ListAuditByOrg`, `ListAuditAll`,
  `PurgeAuditLog` (older than retention); `sqlc generate`.
- `internal/audit/audit.go`: `Record(ctx, actor, orgID, action, targetType, targetID,
  detail)` — best-effort insert, WARN on error.
- Wire `Record` at mutation sites (spec §2.9 list): auth, orgs/members/invites, apps
  (create/update/delete/deploy/rollback/stop/start), env (keys only), domains, databases,
  instance settings, OIDC/SMTP/GitHub-App config.
- `internal/jobs/housekeeping.go`: add `PurgeAuditLog` with `CARGO_AUDIT_RETENTION_DAYS`.
- API: `GET /orgs/{orgID}/audit` (org admin) and `GET /admin/audit` (instance admin), paged.
- `web/src`: audit table in org settings + admin area (actor, action, target, time).
- Tests: each wired mutation writes one row with correct actor/target; env audit never
  includes values; failed insert doesn't fail the action; retention prune.
- Commit `feat(audit): append-only audit log with org and admin views`

### Task 10: Docs, version, close milestone
- `PhasedPlans.md`: add a "Phase 10 — Production Hardening (m11)" section marked ✅ with
  spec+plan references and a shipped summary; note it precedes 9.2.
- `internal/api/instance.go`: bump `version` (minor).
- `README.md` / `deploy/README.md`: network topology (cargo-system), resource-limit env
  vars, backup/restore, alerting webhook, `/readyz` & `/metrics`, audit log.
- Full verification: `go test -p 2 ./...`, `golangci-lint run ./...` (0 issues),
  `npx vitest run`, `npm run build`, `docker build .`.
- Commit `docs: mark production-hardening milestone complete`

## Self-review
- Spec §2.1 → Task 1 · §2.2 → Task 2 · §2.3 → Task 3 · §2.4 → Task 4 · §2.5 → Task 5 ·
  §2.6/§2.7 → Task 6 · §2.8 → Tasks 7 (metrics) + 8 (alerting) · §2.9 → Task 9 · §3 config →
  Tasks 2–9 (each adds its own keys) · §4 security summary → cumulative · §5 testing →
  per-task tests + CI backup-restore.
- Ordering rationale: Tier-0 first (Tasks 1, 3), Tier-1 next (Tasks 2, 4), then Tier-2
  middleware (Task 5), then Tier-3 operability (Tasks 6–9). Tasks 1, 5, 6 are independent and
  parallelizable; Task 8 depends on `mailer` (exists) and is referenced by Tasks 3–4 (define
  the `Notify` seam in Task 8, no-op stub earlier if Tasks 3–4 land first — thread with an
  interface to avoid ordering coupling).
- Deliberate narrowings vs a maximal version: capabilities left at Docker defaults
  (`no-new-privileges` only) to avoid breaking app images; backups are local-disk only (S3
  deferred); restore is a tested runbook, not a UI; alerting is event-fixed, not a rules
  engine — all noted as follow-ups in the spec non-goals.
- Only one new dep (`prometheus/client_golang`); `x/time` already vendored; no new runtime container
  (NFR-3's "3 containers" invariant preserved).
