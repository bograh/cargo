# Cargo — Phased Plans v2

Forward roadmap from the current shipped state (**2026-07-23**). Supersedes the
forward-looking half of [PhasedPlans.md](PhasedPlans.md), which remains the historical
record of Phases 0–9. v2 inserts a **Production Hardening** milestone ahead of the
remaining feature roadmap, because operational safety now gates real-world use more than any
new feature does.

Status legend: ✅ done · ◑ partial · 🔜 next · ⬜ not started

Source documents: [PRD.md](PRD.md) · [v1 design](docs/superpowers/specs/2026-07-17-cargo-design.md) · [production-readiness findings](docs/superpowers/specs/2026-07-23-cargo-production-readiness.md)

---

## Shipped so far (recap)

Verified against the code, not just the plan docs. Full detail in [PhasedPlans.md](PhasedPlans.md).

| Area | Phase | Status |
|---|---|---|
| Foundation, auth/orgs/invites, deployment core, GitHub, domains/SSL, frontend, install | 0–7 (v1) | ✅ |
| Managed databases (PG/Redis/MySQL/Mongo) | 8.1 | ✅ |
| OIDC login | 8.2 | ✅ |
| App metrics (docker stats + Traefik Prometheus, live charts + 48h history) | 9.3 | ◑ app-level only |
| Web UI redesign · app stop/start · deployment supersede | extra | ✅ |

**Still open from the original roadmap:** docker-compose app source (8.3), multi-server
(9.1), zero-downtime blue/green (9.2), instance/host monitoring (rest of 9.3), plus the
whole class of operational gaps captured below.

---

## Phase 10 — Production Hardening (m11) ✅

*Spec: `docs/superpowers/specs/2026-07-23-cargo-production-hardening-design.md` · Plan:
`docs/superpowers/plans/2026-07-23-cargo-m11-production-hardening.md`. New milestone m11 —
operational safety, not new product surface. Precedes 9.2 because blue/green briefly doubles
containers per app, making resource limits and network isolation matter more.*

The 14 audited gaps (`2026-07-23-cargo-production-readiness.md`) resolved as ten tasks.

### 10.1 Network isolation — `cargo-system` ✅ (Tier-0)
Move the platform DB off `cargo-proxy` onto a dedicated `cargo-system` network so tenant
apps can no longer reach `db:5432`.
- ✅ A tenant app container cannot open the control-DB port (runtime-verified: can't even resolve `db`); the controlplane still connects
- ✅ `cargo-system` is compose-managed + `internal: true` (deviation from "external"; carries only platform stack traffic — no install.sh step needed); overlays inherit the topology

### 10.2 App stability limits, log rotation, hardening ✅ (Tier-1)
Per-app memory/CPU/PID caps (config defaults, per-app override), `json-file` log rotation,
and `no-new-privileges` on every tenant + DB container.
- ✅ One app can no longer OOM/fork-bomb/log-flood the host (mem_limit/cpus/pids_limit + json-file max-size:10m×3)
- ✅ Migration 00014 adds nullable limit columns; App Settings exposes overrides; `no-new-privileges` always emitted
- ✅ Golden compose tests updated; null columns fall back to config defaults (`CARGO_DEFAULT_MEM/CPU/PIDS_LIMIT`)

### 10.3 Control-plane backup & disaster recovery ✅ (Tier-0)
Daily `pg_dump -Fc` of the control DB + `acme.json` bundle + master-key fingerprint into
`<dataDir>/platform-backups/`, retained N (default 14); tested restore runbook; admin panel.
- ✅ Backup files appear on the daily schedule and via "Run backup now"; retention keeps exactly N (unit-tested)
- ✅ CI job (`backup-restore`) proves `pg_dump` → `pg_restore` round-trips a known row (also run locally, green)
- ✅ README documents restore + hard master-key-custody guidance (key never written; only a SHA-256 fingerprint)
- Approach: dump via `docker exec` into the DB container + certs via `docker cp` from Traefik (image ships no pg_dump; certs live in the `cargo-acme` volume); containers found by compose project+service labels (self-discovered). Alert-on-failure seam (`Alerter`) wired nil until 10.8.

### 10.4 Disk-space guardrail ✅ (Tier-1)
Periodic `statfs` on `<dataDir>` (reflects the host fs for the default local-volume install);
warn/alert below threshold (default 10%), aggressive dangling-image prune below 5%; admin gauge.
- ✅ Crossing the threshold logs, records status (`instance_settings`), and fires **one** `disk_low` alert; recovery clears the flag (pure `evalDisk` unit-tested)
- ✅ 10-min periodic `disk_check` job; `GET /admin/disk` live gauge in the Admin UI; `CARGO_DISK_MIN_FREE_PCT` config; reuses the 10.3 `Alerter` seam (nil until 10.8)

### 10.5 HTTP security middleware ✅ (Tier-2)
Security headers (incl. HSTS in production, CSP for the SPA), `MaxBytesReader` body cap,
Origin/Referer check on cookie-authed mutations, general per-user/IP rate limit.
- ✅ Oversize body → 413; cross-origin POST → 403; over-limit → 429; webhook exempt; no-Origin (curl) allowed
- ✅ Headers on every response (nosniff/DENY/no-referrer/CSP; HSTS only in production); CSP verified against the built SPA (no inline scripts/CDN); `CARGO_API_RATELIMIT_RPS` config; 10 middleware tests

### 10.6 Readiness probe, bounded shutdown, orphan reaper ✅ (Tier-3)
`/readyz` (pings Postgres + Docker); 30 s-bounded shutdown; startup reaper fails deployments
stuck in non-terminal states older than a 15-min grace.
- ✅ `/readyz` 503s naming the down dependency (db/docker); a stale stuck deploy becomes `failed`, fresh ones untouched (real-DB tested)
- ✅ Controlplane compose healthcheck now curls `/readyz`; `httpServer.Shutdown` + `client.Stop` bounded to 30 s; reaper runs at startup

### 10.7 Control-plane self-metrics ✅ (Tier-3)
`GET /metrics` (Prometheus) on a separate internal-only listener (`CARGO_METRICS_ADDR`,
default `:9090`): River queue depth by state, deploy success/failure counters + duration
histogram, pgx pool stats, Go/process collectors.
- ✅ Series exposed (`cargo_deploys_total`, `cargo_deploy_duration_seconds`, `cargo_river_jobs`, `cargo_db_pool_*`); recorded from the deploy pipeline
- ✅ Not on the public `:8080` router (verified: falls through to the SPA, not the registry); leaf `internal/obs` package avoids the api→jobs import cycle

### 10.8 Notifications & alerting ✅ (Tier-3)
`internal/notify`: email (existing `mailer`) + outbound Slack/Discord webhook (URL stored
encrypted). Events: deploy failed, disk low, backup failed; deploy-success opt-in per app.
- ✅ Deploy failure notifies org owners/admins + webhook; success only when the app opted in (`notify_on_success`, migration 00015); platform alerts (disk/backup) → instance admins + webhook
- ✅ Webhook URL write-only (GET returns only `{configured}`); every sink best-effort (failure logged, never blocks the deploy/backup/disk path); the `Alerter`/`Notifier` seams from 10.3/10.4 now wired live

### 10.9 Audit log ✅ (Tier-3)
Append-only `audit_log` (actor/org/action/target/detail, migration 00016); best-effort writes;
org-scoped + instance-wide read views; housekeeping retention.
- ✅ Every successful authed mutation is recorded via a uniform middleware (actor + method + target path + status); reads and non-2xx are skipped; failed audit write never fails the action
- ✅ `GET /admin/audit` (instance admin, all) + `GET /orgs/{orgID}/audit` (org admin); Admin "Audit log" table; housekeeping purge with `CARGO_AUDIT_RETENTION_DAYS` (180)
- Design note: middleware gives complete, uniform coverage at coarse granularity (action = HTTP method, target = path) rather than per-handler semantic actions — chosen for completeness/forensics; can be enriched per-site later

### 10.10 Docs, version bump, close m11 ✅
- ✅ README "Operations & hardening" section + deploy/README network topology & restore runbook; compose documents every new env var; version bumped to 1.3.0; full verification green (`go test -p 2`, lint 0, vitest, build, docker build)

**Phase exit demo:** deploy an abusive app (memory hog / log flood) → host stays healthy,
other apps unaffected → kill the DB volume → restore from backup → platform back with certs
and secrets intact → every action visible in the audit log.

---

## Phase 11 — Zero-downtime blue/green deploys (9.2) ✅

*Removes FR-4.7's known limitation. Design sketch in
[next-phase proposal](docs/superpowers/specs/2026-07-23-cargo-next-phase.md) §2.*

One **compose project per color** (`cargo-app-<slug>-<color>`) rather than two services in one
project: the new color is brought up beside the running one, health-gated, and the old color
reaped only on success. Both colors declare the **same Traefik router and service**, so Traefik
merges them into one backend pool and the hand-off needs no proxy reconfiguration.
- ✅ A redeploy of a healthy app serves every request throughout (real-Docker test polls every app container across a hand-off and asserts zero outages)
- ✅ A failing new image is torn down and leaves the **old** version live with the deployment `failed` — no manual rollback (real-Docker test)
- ✅ Only one color remains after success; prune is color-aware — an image is never untagged while any retained or live deployment still references it (also fixes the pre-existing rollback-reuse case)
- ✅ Per-app `deploy_strategy` (migration 00017) over instance-wide `CARGO_DEPLOY_STRATEGY` (default `bluegreen`); `recreate` keeps the original in-place behaviour for apps that can't run two instances at once
- ✅ Active color is reconciler-owned state (`state.json` beside the compose projects), so logs/stats/stop/start follow the serving color and a restored control-plane backup can't disagree with what's on the host
- Design note: a container's Traefik labels are fixed at creation, so a container cannot move from "not serving" to "serving" without being recreated. The new color therefore joins the pool already labelled, and **Traefik's own load-balancer healthcheck** is what holds traffic back until it answers. With no healthcheck path there is nothing to probe, so a request can briefly reach a booting container — the UI warns when blue/green is selected without one.

- ✅ Legacy migration: an app deployed before this phase runs the unsuffixed project, whose labels declare the same Traefik service *without* the healthcheck options the colored projects add. Two containers defining one service with conflicting options makes Traefik drop the service, so that project is retired **before** the first color starts — a one-time restart (logged as such), after which every deploy is a real hand-off.

---

## Phase 12 — Multi-server (Docker-over-SSH, 9.1) ⬜

*Largest architectural step; validates the existing `DeployProvider` seam. Own spec → plan
cycle.*

`hosts` table (encrypted SSH creds, status, capacity); apps/deployments gain a target host;
a second `DeployProvider` running the same compose ops over `DOCKER_HOST=ssh://…`; explicit
per-app host selection (no auto-bin-packing in v1); per-host Traefik + `cargo-proxy`;
metrics/logs sample the app's host; install gains a "join a worker host" flow.
- An app deploys to and runs on a remote host, reachable via that host's Traefik
- Cross-host isolation and per-host networks verified

---

## Phase 13 — Remaining roadmap & finishers ⬜

Independent, each a small-to-medium own cycle; sequence by demand.

### 13.1 docker-compose app source (8.3) ⬜
Users deploy a repo containing their own compose file; Cargo layers networking/labels/limits
over it. Last open PRD "Phase 2" item.

### 13.2 Instance & host monitoring (finish 9.3) ⬜
All-apps + whole-server overview (host CPU/mem/disk, container count) reusing the existing
collector and chart components; complements 10.7's self-metrics.

### 13.3 Master-key rotation ⬜
`cargod rotate-key` re-seals all secrets under a new key using the existing `key_version`
column. Turns 10.3's "back up the key" into a recoverable story if a key is suspected leaked.

### 13.4 Additional git providers (GitLab/Bitbucket/Gitea) ⬜
Behind the same source abstraction as GitHub. PRD "later".

---

## Sequencing summary

1. **Phase 10 — Production Hardening (m11)** — next; gates real use.
2. **Phase 11 — Zero-downtime blue/green** — highest-value feature; needs 10.1/10.2.
3. **Phase 12 — Multi-server** — biggest architecture change.
4. **Phase 13 — finishers** — compose source, instance monitoring, key rotation, more git providers, by demand.

Each Phase 11–13 item follows the established spec → plan → ship cadence under
`docs/superpowers/{specs,plans}` with the SDD task breakdown in `.superpowers/sdd`.
