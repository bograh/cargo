# Cargo

Cargo is a self-hosted Platform-as-a-Service in the spirit of Dokploy and Coolify: a Vercel/Railway-like deployment experience on infrastructure you own. Install it on a single server with one command, then anyone on the team can ship an app from a GitHub repo or a container registry to a running HTTPS URL in a few clicks — no SSH, no YAML, no proxy config.

- Apps deploy from GitHub repos (Dockerfile or Nixpacks auto-detected), plain registry images, or a Docker Compose file in your repo
- Every app gets `https://<app>.<apps-domain>` with automatic SSL; custom domains supported
- Live build/deploy logs, encrypted environment variables, one-click rollback
- Organizations with roles (owner/admin/member/viewer) and shareable invite links
- Push-to-deploy webhooks via a GitHub App

## Architecture at a glance

Exactly three platform containers (plus one per deployed app):

| Container | Role |
|---|---|
| `controlplane` | Single Go binary: API, embedded React UI, job queue, deploy engine. The only stateful piece besides the DB. |
| `db` | Postgres 16 — all platform state, job queue, and migrations (run automatically at startup). |
| `traefik` | Reverse proxy; the only container publishing host ports (80/443). Issues certs via Let's Encrypt. |

## Prerequisites

- An Ubuntu/Debian Linux host with `sudo` access (the installer sets up Docker
  Engine and the compose plugin)
- Ports 80 and 443 open
- For a production install: DNS records pointing at the host (`<platform-domain>` and `*.<apps-domain>`). No domain? The installer falls back to a local install on localhost / the server's IP.

## Quick install

```bash
curl -fsSL https://usecargo.vercel.app/install.sh | sh
```

The bootstrap installs prerequisites, clones Cargo to `/opt/cargo`, then prompts
for your platform domain, apps-domain suffix, and Let's Encrypt email (optionally
a DNS provider for wildcard certificates), generates secrets into a mode-0600
`.env`, and starts the stack. Leave the platform domain empty for a local install
(plain HTTP + self-signed HTTPS, no DNS or certificates needed). Non-interactive
installs can set `CARGO_PLATFORM_DOMAIN`, `CARGO_APPS_SUFFIX`, and
`CARGO_ACME_EMAIL` in the environment instead.

> **Back up `CARGO_MASTER_KEY` from `.env` somewhere safe.** Environment
> variables and credentials are encrypted with it and are unrecoverable
> without it.

Open `https://<platform-domain>` (or `http://localhost` / `http://<server-ip>` for a local install) and register — the first account becomes the instance admin. Instance settings (apps-domain suffix, SMTP, GitHub App credentials) are managed from the admin area in the UI.

## Upgrade

```bash
docker compose pull && docker compose up -d   # add your TLS overlay's -f flag for production installs
```

Migrations run automatically at startup; running user apps are not touched.

## Local development

```bash
cd deploy
docker compose -f docker-compose.dev.yml up --build
```

Serves the platform on http://localhost:8080 without Traefik/SSL. Backend tests: `go test ./...`; frontend: `npm test` in `web/`. An end-to-end smoke test (install → register → deploy → live) lives at `scripts/smoke.sh`.

## Managed databases

Each organization can provision managed Postgres (16/17) and Redis (7) instances directly from the UI. Apps attach to an instance to get per-app, isolated credentials — attaching injects `DATABASE_URL` (Postgres) or `REDIS_URL` (Redis) into the app's environment at deploy time. One attachment per engine per app.

Instance volumes live under `<dataDir>/databases/<id>`; manual snapshots (`pg_dumpall --clean` for Postgres, `BGSAVE` + `LASTSAVE` copy of `dump.rdb` for Redis) are written to `<dataDir>/db-backups/<id>/` and downloadable from the UI; provisioning logs land in `<dataDir>/db-logs/`.

> Instances can optionally expose a host port for external clients (e.g. a local `psql`/`redis-cli`). Only enable this if you understand the instance will be reachable from outside the docker network.

## Operations & hardening

Cargo runs several background safeguards, all configurable via env vars in `deploy/.env` (sane defaults shown):

- **Backups & DR** — a daily job dumps the control-plane database (`pg_dump -Fc`) plus a copy of the TLS certificates into `<dataDir>/platform-backups/`, keeping the last `CARGO_PLATFORM_BACKUP_KEEP` (14). Run one on demand from **Admin → Backups**. Restoring also needs your `CARGO_MASTER_KEY` — store it in a password manager. See [deploy/README.md](deploy/README.md) for the restore runbook.
- **Tenant isolation & stability** — the platform database sits on a private `cargo-system` network unreachable from tenant apps; each app runs with memory/CPU/PID caps (`CARGO_DEFAULT_MEM_LIMIT` / `_CPU_LIMIT` / `_PIDS_LIMIT`, overridable per app), `no-new-privileges`, and rotated container logs.
- **Zero-downtime deploys** — by default (`CARGO_DEPLOY_STRATEGY=bluegreen`) a new version starts alongside the running one and takes over only once it passes its healthcheck; a version that never gets healthy is discarded and the old one keeps serving, so a bad deploy needs no rollback. Set an app's healthcheck path for the full guarantee, or switch an app to `recreate` in its Settings if it can't tolerate two instances at once.
- **Disk guardrail** — a periodic check warns/alerts below `CARGO_DISK_MIN_FREE_PCT` (10%) free and reclaims dangling images below 5%; shown as a gauge in the admin area.
- **Alerts** — configure a Slack/Discord-compatible incoming webhook under **Admin → Alerts webhook** to be notified of deploy failures, low disk, and backup failures (plus per-app opt-in success notifications). Email is also sent when SMTP is configured.
- **Health & observability** — `GET /readyz` reports readiness (Postgres + Docker); a Prometheus endpoint is served on an internal-only listener (`CARGO_METRICS_ADDR`, `:9090`) exposing deploy, queue, and DB-pool metrics.
- **Instance monitoring** — the admin area charts whole-server CPU, memory, disk, and container count on the same 15s tick as app metrics, plus an all-apps table spanning every organization.
- **Master-key rotation** — if `CARGO_MASTER_KEY` leaks, `cargod rotate-key` re-seals every stored secret (env vars, registry credentials, database passwords, SMTP/GitHub/OIDC/webhook settings) under a new key in one transaction; `cargod gen-key` produces one. Stop the control plane, rotate, update `.env`, start. See [Operations](https://usecargo.vercel.app/docs/operations/).
- **Audit log** — every state-changing action is recorded (who/what/when), viewable under **Admin → Audit log** and per-org; retained `CARGO_AUDIT_RETENTION_DAYS` (180) days.

## Worker hosts (multi-server, early access)

Apps are not limited to the control-plane machine. Under **Admin → Worker hosts**, an instance admin can register any machine reachable over SSH that runs Docker Engine with the compose plugin: paste the host's address (`user@host`), SSH port, and a private key. Cargo seals the key with the master key, pins the host's fingerprint on first contact (a later mismatch aborts every operation to that host), and never touches its own `~/.ssh` or `~/.docker` — each host gets an isolated docker context under `<dataDir>/hosts/<id>/`.

When creating an app, pick a worker host in the form; its domains must resolve to that host's IP (each worker runs its own Traefik/proxy stack — see [the multi-server design](docs/superpowers/specs/2026-08-22-cargo-multi-server-design.md)). Blue/green deploys work identically on workers; if a worker goes unreachable mid-deploy the deployment fails cleanly and other hosts are unaffected.

Notes for this release: managed databases remain single-host; per-host metrics collection arrives with slice 12b; log streaming already follows the app's host. If Docker or the compose plugin is missing on the worker, verification reports `degraded` with a copy-paste install command — Cargo never installs software on your hosts by itself.

## Documentation

- [PRD](PRD.md) — product requirements
- [Phased plans](PhasedPlans.md) — roadmap and phase status
- [deploy/README.md](deploy/README.md) — SSL modes (HTTP-01 vs wildcard DNS-01) and production details
