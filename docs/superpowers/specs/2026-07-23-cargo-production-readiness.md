# Cargo — Production-Readiness Hardening (undiscussed gaps)

- **Date:** 2026-07-23
- **Status:** Draft for review
- **Scope:** Gaps found by auditing the running system that are **not** in `PRD.md`,
  `PhasedPlans.md`, or any existing spec. These are about *operating* Cargo safely for
  real teams, distinct from the feature roadmap (blue/green, multi-server, etc.).

The PRD optimizes v1 for "practicality and operability" and explicitly defers billing
quotas. But several things it never accounted for stand between "feature-complete" and
"I'd trust this to hold my team's apps and secrets." Ordered by blast radius.

---

## Tier 0 — Data-loss / unrecoverable (fix before any real use)

### 0.1 No control-plane backup or disaster recovery
The platform's own Postgres holds **everything**: users, orgs, apps, all deployments,
AES-GCM-encrypted env vars, and GitHub/OIDC/SMTP secrets. There is **no backup job and no
documented restore** (`grep` across `deploy/`, `scripts/`, README — only *managed-DB*
snapshots exist, which are a different thing). A disk failure or a bad `docker compose
down -v` erases the entire platform with no recovery.

Also unprotected: **`acme.json`** (all issued certs) and — most critically — the
**master key**. Lose the master key and *every encrypted secret is permanently
unrecoverable*; the install script prints a "back this up" warning but there's no
mechanism, no escrow guidance, no verification.

**Do:** a scheduled `pg_dump` of the control DB → `<dataDir>/platform-backups/` with
retention (reuse the housekeeping job runner); bundle `acme.json` and a
key-fingerprint check into the same artifact; write a restore runbook; add
`cargod restore` or documented steps. This is the single highest-priority gap.

### 0.2 Network isolation break: tenant apps can reach the platform database
`deploy/docker-compose.yml` puts the platform `db` service on the **`cargo-proxy`**
network (line ~102). `internal/reconciler/compose.go` attaches **every tenant app** to
that same `cargo-proxy` network by default. Result: any app a user deploys can open a
socket to `db:5432` and attack the control-plane Postgres (which stores all the encrypted
secrets) — password brute-force, CVE exploitation, connection exhaustion.

**Do:** move the platform DB (and controlplane↔DB traffic) onto a dedicated
`cargo-system` network that tenant apps never join. Traefik reaches app containers over
`cargo-proxy`; the controlplane reaches the DB over `cargo-system`; the two don't mix.
Verify managed-DB (`cargo-data`) segmentation at the same time.

---

## Tier 1 — Host stability & multi-tenant fairness

### 1.1 App containers have no resource limits
`compose.go` emits no memory, CPU, or PID limits for the `app` service (verified — none).
A single app with a memory leak or fork bomb OOM-kills the host, taking down **the
control plane and every other tenant's apps**. The PRD deferred *billing* quotas, but
per-container stability limits are a different concern and are missing.

**Do:** render sensible per-app defaults (e.g. `mem_limit`, `cpus`, `pids_limit`) into
the compose, overridable per app in Settings. Managed-DB containers need the same.

### 1.2 No container log rotation → disk exhaustion
App and DB containers use Docker's default `json-file` driver with **no size cap**. A
chatty app grows its log file until the host disk fills, which corrupts the platform
Postgres. Nothing rotates or caps it.

**Do:** set `logging: { driver: json-file, options: { max-size, max-file } }` on
generated app and DB compose services.

### 1.3 No disk-space guardrail
Retained images (prune keeps last N), deploy logs, app logs, and DB volumes all grow, but
nothing watches **actual free disk**. A full disk silently corrupts Postgres and wedges
Docker. There's no monitor, no alert, no emergency prune.

**Do:** a periodic disk-usage check that warns (and can trigger aggressive prune) below a
threshold; surface it in the admin area.

---

## Tier 2 — Security hardening

### 2.1 Container-escape blast radius is unmitigated
The controlplane mounts the full Docker socket (`docker.sock` — note `:ro` on the socket
file does *not* restrict Docker API calls; it's still root-on-host). Tenant apps run with
default Linux capabilities, no `no-new-privileges`, no read-only rootfs, no user
remapping. A container-breakout in any app, or a compromise of the controlplane, is
game-over for the host.

**Do (defense in depth):** add `security_opt: [no-new-privileges:true]`, drop
capabilities (`cap_drop: [ALL]` + add-back as needed) on tenant apps; evaluate a
**docker-socket-proxy** in front of the controlplane to whitelist only the Docker API
calls Cargo actually issues; document the trust boundary.

### 2.2 No HTTP security headers
No HSTS, CSP, `X-Content-Type-Options`, `X-Frame-Options`, or `Referrer-Policy` on the
control-plane UI/API (`router.go` uses only RequestID/Recoverer/Logger). The SPA that
manages secrets is clickjack-able and has no CSP.

**Do:** a headers middleware (or a Traefik middleware) applying the standard set,
HSTS gated on production.

### 2.3 CSRF defense is `SameSite=Lax` only
Sessions are cookie-based; the only CSRF mitigation is `SameSite=Lax` (confirmed in
`auth.go`/`oidc.go`). Lax blocks most cross-site POSTs but not all attack shapes, and
there's no token or Origin/Referer check on state-changing endpoints.

**Do:** validate `Origin`/`Referer` on mutating requests (cheap, no token plumbing), or
add a double-submit CSRF token.

### 2.4 Request body size is unbounded on JSON endpoints
Only the GitHub webhook caps the body (`io.LimitReader(..., 5<<20)`). Every other handler
decodes request JSON with no limit — a trivial memory-pressure DoS.

**Do:** a `http.MaxBytesReader` middleware with a sane global cap.

### 2.5 Rate limiting only covers auth
`authRateLimiter` protects register/login/refresh only. Deploy triggers, app CRUD, and
SSE subscriptions are unthrottled — an authenticated member can hammer deploys or open
unbounded SSE streams.

**Do:** a general per-user/per-IP limiter on mutating and streaming routes.

---

## Tier 3 — Operability & correctness

### 3.1 Shallow health check
`/healthz` returns 200 without checking DB or Docker reachability. Orchestration and
uptime monitors can't distinguish "serving" from "up but DB is down."

**Do:** a `/readyz` that pings Postgres and `docker version`; keep `/healthz` liveness-only.

### 3.2 Orphaned in-flight deployments after a crash/restart
Graceful shutdown uses `httpServer.Shutdown(context.Background())` (no timeout) and River
`client.Stop`. If the controlplane restarts mid-build/mid-apply, the deployment row is
left in `building`/`deploying` **forever** — no boot-time reconciliation marks it failed.

**Do:** a startup reaper that fails deployments stuck in non-terminal states owned by no
running job; bound the shutdown drain with a timeout.

### 3.3 No control-plane self-observability or alerting
The platform scrapes Traefik/Docker metrics *for tenant apps* but exposes **none about
itself**: job-queue depth, deploy success/failure rate, deploy duration, DB pool
saturation. Operators are blind to the platform's own health, and there's no alerting
path at all (ties into notifications, which are also unbuilt).

**Do:** a `/metrics` Prometheus endpoint for the controlplane; wire deploy outcomes to a
notification channel (email via existing `mailer`, plus an outbound Slack/Discord webhook).

### 3.4 No audit log
Nothing records **who** did what — role changes, deploys, deletions, secret edits, invite
creation. For a shared internal platform with real access to code and data, that's a
forensic and compliance gap.

**Do:** an append-only `audit_log` (actor, org, action, target, timestamp) written from
the service layer; surface a filtered view in org/admin settings.

---

## Suggested sequencing

Independent of the feature roadmap; several are hours-to-days, not weeks.

1. **0.1 backups + 0.2 network isolation** — do together; both are one-time infra/compose
   changes with outsized payoff. Nothing else matters if state is unrecoverable or the DB
   is reachable by tenants.
2. **1.1 resource limits + 1.2 log rotation + 1.3 disk guardrail** — one compose-generation
   pass in the reconciler covers the first two.
3. **2.1–2.5 security hardening** — mostly middleware + compose flags; batchable.
4. **3.1–3.4 operability** — readyz, crash reaper, self-metrics, audit log; each small and
   independently shippable.

Recommendation: fold Tier 0 and Tier 1 into the **next** cycle *before* the blue/green
feature work — blue/green briefly runs two containers per app, which makes the missing
resource limits (1.1) and network isolation (0.2) matter more, not less.
