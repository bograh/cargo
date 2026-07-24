# Cargo — Next Phase Proposal

- **Date:** 2026-07-23
- **Status:** Draft for review
- **Author:** investigation of current implementation vs `PRD.md` and `PhasedPlans.md`
- **Companion docs:** [PRD.md](../../../PRD.md) · [PhasedPlans.md](../../../PhasedPlans.md)

---

## 1. Current state (verified against the code)

Everything the PRD scoped for **v1** is shipped, plus two of the three PRD "Phase 2"
items and a head-start on PRD "Phase 3" monitoring. This was confirmed by reading the
source, not just the plan docs — 146 Go files, 56 Go test files, 13 migrations, a full
embedded SPA, three-job CI (`go` / `web` / `docker`).

| Area | PRD / Phase | Status | Evidence |
|---|---|---|---|
| Auth (argon2id, cookie sessions, refresh rotation, rate limit, `AuthProvider` seam) | FR-1, Ph1 | ✅ | `internal/auth`, migration 00003 |
| Orgs, roles, invites (query-scoped, invisible to non-members) | FR-2, Ph1 | ✅ | `internal/orgs`, migration 00003/00010 |
| Apps CRUD (git/image source, slug, port, healthcheck, build args, registry creds) | FR-3, Ph2 | ✅ | `internal/apps`, migration 00004 |
| Deployments (status machine, SSE logs, rollback, supersede) | FR-4, Ph2 | ✅ | `internal/deployments`, migration 00013 |
| Builders (Dockerfile/BuildKit + Nixpacks, Go/Java/Node generators, auto-detect) | FR-3.3, Ph2 | ✅ | `internal/builder` |
| Job queue (River, build cap, per-app serialize, backoff) | FR-4.4, Ph2 | ✅ | `internal/jobs`, `internal/db` |
| Reconciler + Traefik labels (per-app compose project) | FR-5.1, Ph2/4 | ✅ | `internal/reconciler` |
| Env vars (AES-256-GCM, write-only) | FR-6, Ph2 | ✅ | `internal/crypto`, `internal/apps` |
| GitHub App (install flow, private clones, HMAC webhooks) | FR-3.1/4.1, Ph3 | ✅ | `internal/github` |
| Domains & SSL (wildcard DNS-01 / HTTP-01, custom domains, status checks) | FR-5, Ph4 | ✅ | migration 00006, `internal/jobs/domaincheck.go` |
| Frontend SPA (all tabs, live log viewer, redesign) | Ph5 | ✅ | `web/src` (24 pages) |
| Instance settings + housekeeping (prune, purge) | FR-7, Ph6 | ✅ | `internal/settings`, `internal/jobs/housekeeping.go` |
| Install / upgrade / smoke | FR-8, Ph7 | ✅ | `deploy/install.sh`, `scripts/smoke.sh` |
| **Managed databases** (PG/Redis/MySQL/Mongo, attach/detach, snapshots) | PRD-P2 / 8.1 | ✅ | `internal/databases`, migration 00008/00009 |
| **OIDC login** (config UI, auth-code flow, account linking) | PRD-P2 / 8.2 | ✅ | `internal/oidc`, migration 00007 |
| App metrics (docker stats + Traefik Prometheus, live charts + 48h history) | PRD-P3 / 9.3 | ◑ | `internal/metrics`, migration 00012 |
| App stop/start | (extra) | ✅ | `internal/reconciler` Stop/Start |

### What is *not* done

| Item | Source | Notes |
|---|---|---|
| **Zero-downtime blue/green deploys** | 9.2 / removes FR-4.7 | The apply step recreates the container in place → brief downtime every deploy. |
| **Multi-server (Docker-over-SSH)** | 9.1 | Single-host only; `DeployProvider` interface exists but only a local `Docker` impl. |
| **docker-compose app source** | PRD-P2 / 8.3 | Users can't deploy their own compose file yet. |
| **Notifications** (deploy events → email/Slack) | PRD "later" | No outbound notification path anywhere in the code (grep-verified). |
| **Instance/host-level monitoring** | part of 9.3 | Metrics are per-app only; no whole-server or all-apps overview dashboard. |
| GitLab/Bitbucket/Gitea | PRD "later" | GitHub-only. |

**Agreed Phase 9 order (from PhasedPlans.md):** 9.2 → 9.1 → 9.3. Metrics (9.3) jumped
ahead. So the next unfinished item in the intended sequence is **9.2, zero-downtime
blue/green** — which is also the change that erases the most-cited known limitation
(FR-4.7) and is entirely within our control because Cargo owns the compose file.

---

## 2. Recommended next phase — 9.2 Zero-downtime blue/green deploys

### Why this first
- **Highest user-visible value, lowest architectural risk.** It removes the one caveat
  the PRD explicitly ships with (FR-4.7: "brief downtime per deploy; a failed
  healthcheck leaves the app down until rollback").
- **Self-contained.** It touches the reconciler and the deploy job — no new external
  dependency, no new container, no schema change to the auth/org core. Traefik already
  routes purely by labels, which is exactly the switchpoint we need.
- **Unblocks nothing else, blocks nothing else** — safe to do before the heavier
  multi-server work.

### The current downtime source (grounded)
`internal/reconciler/provider.go:Apply` writes one compose project per app
(`cargo-app-<slug>`, single service `app`) and runs `docker compose up -d`. Because the
service name is stable, Compose **recreates** the existing container — it stops the old
one before the new one is healthy. `waitHealthy` then gates *after* the swap has already
happened. Net: every deploy has a stop→start gap, and a failing new image takes the app
down until someone rolls back.

### Target design — two-color compose with a Traefik switchover

Keep the one-project-per-app model; make the service **color-suffixed** and switch the
Traefik load-balancer target only after the new color passes its health gate.

1. **Two services, one router.** Render `app-blue` and `app-green` in the compose file.
   The Traefik router (`app-<slug>`) points at a Traefik *service* whose
   `loadbalancer.server` is the currently-live color. The idle color has `traefik.enable=false`.
2. **Deploy sequence** (new `Apply` path):
   - Determine current live color from a persisted marker (new column on `deployments`
     or a `color` file in the project dir — prefer a column for queryability).
   - Bring up the *idle* color with the new image (`docker compose up -d app-<newcolor>`),
     old color still serving.
   - Run the existing `waitHealthy` against the **new** color's container.
   - On pass: rewrite labels so the router's service targets the new color, `up -d` to
     apply labels (Traefik re-reads within its poll interval), then `stop`/`rm` the old
     color. Record the new live color.
   - On fail: leave the old color serving, tear down the failed new color, mark the
     deployment `failed`. **The running app is never touched** — this is the real win
     over today's behavior and makes FR-4.5 hold for the apply step too, not just build.
3. **Rollback** stays image-based but now also flips color, so rollback is likewise
   zero-downtime.
4. **Traefik convergence.** Traefik's Docker provider watches label changes; a short,
   deterministic settle (poll the router config or a brief fixed delay) before removing
   the old color avoids dropping in-flight requests. Optionally drain via
   `loadbalancer.healthcheck` so Traefik stops routing to a color before we kill it.

### Changes by layer

**Backend / architecture (the bulk of the work)**
- `internal/reconciler/compose.go` — render two color services + color-aware Traefik
  service target; keep DB-compose untouched.
- `internal/reconciler/provider.go` — new blue/green `Apply` (bring up idle → health-gate
  → switch → reap old); `Stop`/`Start`/`Teardown` become color-aware; `Rollback` flips color.
- `internal/reconciler/spec.go` — add `LiveColor` / `TargetColor` to `Spec`.
- Migration `00014_deployment_color.sql` — `deployments.color` (and/or `apps.live_color`).
- `internal/jobs/deploy.go` — thread color through; on failure, no state change to live color.
- Golden-file tests for both-color compose output; provider tests for switch-then-reap
  and fail-then-preserve.

**Frontend (small)**
- Deployment status: add a transient "switching traffic" phase to the badge set so the
  UI reflects the health-gate-before-switch step (`web/src/pages/AppDeployments.tsx`,
  status enum in `web/src/lib`).
- Overview: note "zero-downtime" where the deploy-downtime caveat is currently implied.
- No new pages.

**Infra**
- No new container. Verify Traefik poll interval / provider settings in
  `deploy/docker-compose.yml` give fast, safe convergence; document the trade-off.
- Bump per-app resource headroom expectation in docs (two containers exist briefly).

### Acceptance criteria
- A redeploy of a healthy app serves every request throughout (measured by a
  continuous curl loop in the smoke script — zero non-2xx during deploy).
- A deploy whose new image fails its health gate leaves the **old** version live and the
  deployment `failed`, with logs — no manual rollback needed.
- Rollback restores the previous image with no request loss.
- Only one color remains running after a successful deploy (no container leak); prune/
  housekeeping accounts for color.

### Risks
- **Stateful single-instance apps** (e.g. an app holding a local socket/port, in-memory
  session) briefly run two copies. Mitigate: this is standard blue/green; document it,
  and keep an app-level "recreate (no overlap)" strategy toggle for apps that can't run
  two copies. Managed DBs already run as their own single-instance projects and are
  untouched by this.
- **Traefik convergence timing** is the subtle part — needs the drain/settle handled
  deliberately, covered by the smoke-loop acceptance test.

---

## 3. Then — 9.1 Multi-server (Docker-over-SSH)

The largest architectural change; do it after 9.2 so the deploy path is already
zero-downtime before it fans out across hosts.

- **Data model:** `hosts` table (address, SSH creds encrypted via existing `crypto`,
  status, capacity); apps/deployments gain a target `host_id`.
- **Provider:** a second `DeployProvider` implementation that runs the same compose
  operations over an SSH-tunneled Docker context (`docker --context` or
  `DOCKER_HOST=ssh://…`). The interface already exists — this validates that seam.
- **Scheduling:** simplest first — explicit host selection per app (no auto-bin-packing).
- **Networking:** per-host `cargo-proxy` network + a Traefik instance per host, or a
  central Traefik with cross-host routing (decide in the spec; per-host is simpler and
  matches the single-binary-per-host mental model).
- **Metrics/logs:** the collector must sample the app's host, not localhost.
- Infra: install script gains a "join a worker host" flow.

This is a full spec → plan → ship cycle of its own; not detailed further here.

---

## 4. Cross-cutting backlog (independent of phase order)

These are gaps worth scheduling regardless of which big phase runs next; several are
small and high-leverage.

1. **Deploy-event notifications** (PRD "later", but the plumbing is cheap now).
   `mailer` exists; add a notifications service that fires on deployment
   `live`/`failed` → email (reuse `mailer`) and an outbound webhook (Slack/Discord
   incoming-webhook URL per org, stored encrypted). New table + one settings panel.
   Naturally pairs with 9.2 (people want to know when a deploy flipped).
2. **Finish 9.3 — instance/host monitoring.** App metrics exist; add an all-apps +
   whole-server overview (host CPU/mem/disk, container count) on the admin/org dashboard.
   Reuses the existing collector and chart components.
3. **Master-key rotation.** The `key_version` column exists (Ph2.1) but there's no
   rotation command. Add a `cargod rotate-key` maintenance path re-sealing secrets under
   a new key. Security-relevant, currently a latent gap.
4. **docker-compose app source (8.3).** Still open from PRD Phase 2. Lower priority than
   zero-downtime for an internal tool, but it's the last unshipped PRD-P2 item.
5. **Observability of the control plane itself.** Traefik metrics are scraped for apps;
   the controlplane emits JSON logs but no Prometheus endpoint for its own job
   queue/queue depth/deploy durations. Small, aids operability (NFR-3/6).
6. **E2E/browser coverage.** Strong unit + integration coverage; no browser-level E2E of
   the SPA. A thin Playwright smoke (register → create app → deploy → rollback) would
   guard the critical flow, especially before the blue/green change.

---

## 5. Recommended sequence

1. **9.2 Zero-downtime blue/green** — next; removes FR-4.7, self-contained. *(spec → plan → ship)*
2. **Deploy-event notifications** — fast-follow, pairs with 9.2. *(small)*
3. **9.1 Multi-server** — the big architectural step; exercises `DeployProvider`. *(spec → plan → ship)*
4. **Finish 9.3 instance monitoring** + **key rotation** — hardening. *(small each)*
5. **8.3 docker-compose source** — last open PRD-P2 item. *(spec → plan → ship)*

Each large item follows the existing spec → plan → ship cadence under
`docs/superpowers/{specs,plans}` with the SDD task breakdown in `.superpowers/sdd`.
