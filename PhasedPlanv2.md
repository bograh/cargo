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

## Phase 10 — Production Hardening (m11) 🔜

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

### 10.2 App stability limits, log rotation, hardening ⬜ (Tier-1)
Per-app memory/CPU/PID caps (config defaults, per-app override), `json-file` log rotation,
and `no-new-privileges` on every tenant + DB container.
- One app can no longer OOM/fork-bomb/log-flood the host
- Migration 00014 adds nullable limit columns; App Settings exposes overrides
- Golden compose tests updated; null columns fall back to config defaults

### 10.3 Control-plane backup & disaster recovery ⬜ (Tier-0)
Daily `pg_dump -Fc` of the control DB + `acme.json` bundle + master-key fingerprint into
`<dataDir>/platform-backups/`, retained N (default 14); tested restore runbook; admin panel.
- Backup files appear on schedule and via "Run backup now"; retention keeps exactly N
- CI script proves `pg_dump` → `pg_restore` round-trips a known row
- README documents restore + hard master-key-custody guidance (key never written by the job)

### 10.4 Disk-space guardrail ⬜ (Tier-1)
Periodic `statfs` on `<dataDir>` + Docker root; warn/alert below threshold (default 10%),
aggressive prune below 5%; admin gauge.
- Crossing the threshold logs, records status, and fires one alert; recovery clears it

### 10.5 HTTP security middleware ⬜ (Tier-2)
Security headers (incl. HSTS in production, CSP for the SPA), `MaxBytesReader` body cap,
Origin/Referer check on cookie-authed mutations, general per-user/IP rate limit.
- Oversize body → 413; cross-origin POST → 403; over-limit → 429; webhook + GET callbacks exempt

### 10.6 Readiness probe, bounded shutdown, orphan reaper ⬜ (Tier-3)
`/readyz` (pings Postgres + Docker); 30 s-bounded shutdown; startup reaper fails deployments
stuck in non-terminal states with no active job.
- `/readyz` 503s naming the down dependency; a stale stuck deploy becomes `failed`, fresh ones untouched
- Compose healthcheck switches to `/readyz`

### 10.7 Control-plane self-metrics ⬜ (Tier-3)
`GET /metrics` (Prometheus, internal-only): River queue depth/job counts, deploy
success/failure + duration, DB pool stats.
- Series exposed; endpoint not reachable on the public entrypoint

### 10.8 Notifications & alerting ⬜ (Tier-3)
`internal/notify`: email (existing `mailer`) + outbound Slack/Discord webhook (URL stored
encrypted). Events: deploy failed, disk low, backup failed; deploy-success opt-in per app.
- Deploy failure notifies owners/admins + webhook; success only when opted in; URL never returned by GET; sink failure never blocks the action

### 10.9 Audit log ⬜ (Tier-3)
Append-only `audit_log` (actor/org/action/target/detail); best-effort writes from the
service layer; org-scoped + instance-wide read views; housekeeping retention.
- Every wired mutation writes one attributed row; env audit records keys only; failed write never fails the action

### 10.10 Docs, version bump, close m11 ⬜
- README/deploy docs cover cargo-system, limits, backup/restore, alerting, `/readyz`, `/metrics`, audit; version bumped; full verification green

**Phase exit demo:** deploy an abusive app (memory hog / log flood) → host stays healthy,
other apps unaffected → kill the DB volume → restore from backup → platform back with certs
and secrets intact → every action visible in the audit log.

---

## Phase 11 — Zero-downtime blue/green deploys (9.2) ⬜

*Removes FR-4.7's known limitation. Design sketch in
[next-phase proposal](docs/superpowers/specs/2026-07-23-cargo-next-phase.md) §2; own spec →
plan cycle to follow.*

Two color-suffixed services per app; bring up the idle color with the new image, health-gate
it, flip the Traefik load-balancer target, then reap the old color. Rollback flips color.
- A redeploy of a healthy app serves every request throughout (continuous-curl smoke test)
- A failing new image leaves the **old** version live and the deployment `failed` — no manual rollback
- Only one color remains after success; prune/housekeeping is color-aware
- Depends on 10.2 (limits) + 10.1 (isolation) being in place first

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
