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

Redis instances choose between two modes at creation. Both give each attached app its own ACL user and its own logical database index, so no app can read another's keys or flush the instance. They differ only in pub/sub: `acl` denies channels entirely (redis channels are global, not scoped to an index), while `shared` grants them, so apps on that instance can publish and subscribe to each other's channels. Pick `shared` only if an app needs pub/sub.

> Instances can optionally expose a host port for external clients (e.g. a local `psql`/`redis-cli`). Only enable this if you understand the instance will be reachable from outside the docker network.

## Operations & hardening

Cargo runs several background safeguards, all configurable via env vars in `deploy/.env` (sane defaults shown):

- **Backups & DR** — a daily job dumps the control-plane database (`pg_dump -Fc`) plus a copy of the TLS certificates into `<dataDir>/platform-backups/`, keeping the last `CARGO_PLATFORM_BACKUP_KEEP` (14). Run one on demand from **Admin → Backups**. Restoring also needs your `CARGO_MASTER_KEY` — store it in a password manager. See [deploy/README.md](deploy/README.md) for the restore runbook.
- **Tenant isolation & stability** — the platform database sits on a private `cargo-system` network unreachable from tenant apps; each app runs with memory/CPU/PID caps (`CARGO_DEFAULT_MEM_LIMIT` / `_CPU_LIMIT` / `_PIDS_LIMIT`, overridable per app — set `CARGO_MAX_MEM_LIMIT` / `_CPU_LIMIT` / `_PIDS_LIMIT` on a shared instance to cap how far a tenant can raise their own), `no-new-privileges`, and rotated container logs.
- **Zero-downtime deploys** — by default (`CARGO_DEPLOY_STRATEGY=bluegreen`) a new version starts alongside the running one and takes over only once it passes its healthcheck; a version that never gets healthy is discarded and the old one keeps serving, so a bad deploy needs no rollback. Set an app's healthcheck path for the full guarantee, or switch an app to `recreate` in its Settings if it can't tolerate two instances at once.
- **Disk guardrail** — a periodic check warns/alerts below `CARGO_DISK_MIN_FREE_PCT` (10%) free and reclaims dangling images below 5%; shown as a gauge in the admin area.
- **Alerts** — configure a Slack/Discord-compatible incoming webhook under **Admin → Alerts webhook** to be notified of deploy failures, low disk, and backup failures (plus per-app opt-in success notifications). Email is also sent when SMTP is configured.
- **Health & observability** — `GET /readyz` reports readiness (Postgres + Docker); a Prometheus endpoint is served on an internal-only listener (`CARGO_METRICS_ADDR`, `:9090`) exposing deploy, queue, and DB-pool metrics.
- **Instance monitoring** — the admin area charts whole-server CPU, memory, disk, and container count on the same 15s tick as app metrics, plus an all-apps table spanning every organization.
- **Master-key rotation** — if `CARGO_MASTER_KEY` leaks, `cargod rotate-key` re-seals every stored secret (env vars, registry credentials, database passwords, SMTP/GitHub/OIDC/webhook settings) under a new key in one transaction; `cargod gen-key` produces one. Stop the control plane, rotate, update `.env`, start. See [Operations](https://usecargo.vercel.app/docs/operations/).
- **Who can sign up** — an account on a Cargo instance can deploy containers on the host, so registration is **invite-only by default**: the first account becomes the instance admin, and everyone after it needs an invite link from an organization admin. Change it under **Admin → Instance settings → Who can sign up** (`Anyone` / `Invited people only` / `Nobody`). Instances upgrading from an earlier version become invite-only on restart — set it back to `Anyone` if you were relying on open sign-up.
- **Where apps may clone from** — a repository URL is tenant-controlled and reaches `git clone` running on the control plane's own network, so Cargo accepts only `https://`, `ssh://` and `git@host:path`, and refuses hosts that resolve inside the deployment (loopback, RFC1918, link-local — the cloud metadata endpoint among them). If your git server is self-hosted on that same private network, set `CARGO_ALLOW_PRIVATE_GIT_HOSTS=true`; it widens *where*, never *which transports*.
- **Audit log** — every state-changing action is recorded (who/what/when), viewable under **Admin → Audit log** and per-org; retained `CARGO_AUDIT_RETENTION_DAYS` (180) days.

## Worker hosts (multi-server, early access)

Apps are not limited to the control-plane machine. Under **Admin → Worker hosts**, an instance admin can register any machine reachable over SSH that runs Docker Engine with the compose plugin: paste the host's address (`user@host`), SSH port, and a private key. Cargo seals the key with the master key, pins the host's fingerprint on first contact (a later mismatch aborts every operation to that host), and never touches its own `~/.ssh` or `~/.docker` — each host gets an isolated docker context under `<dataDir>/hosts/<id>/`.

When creating an app, pick a worker host in the form; its domains must resolve to that host's IP (each worker runs its own Traefik/proxy stack — see [the multi-server design](docs/superpowers/specs/2026-08-22-cargo-multi-server-design.md)). Blue/green deploys work identically on workers; if a worker goes unreachable mid-deploy the deployment fails cleanly and other hosts are unaffected.

Notes for this release: managed databases remain single-host; per-host metrics collection arrives with slice 12b; log streaming already follows the app's host. If Docker or the compose plugin is missing on the worker, verification reports `degraded` with a copy-paste install command — Cargo never installs software on your hosts by itself.

## Threat model

Worth stating explicitly, because it is the assumption everything else rests on.

**The control plane mounts the Docker socket read-write, and runs as root.** It has to: a PaaS whose job is to deploy containers needs the daemon, and there is no subset of the Docker API that both allows that and prevents container escape. The consequence follows directly — **write access to that socket is root on the host**, so any remote code execution in the control plane is host compromise, not container compromise. Traefik gets the socket read-only; nothing else on the host should get it at all.

That is why the boundary Cargo actually defends is the one *below* the control plane: what a tenant may put into a compose file, a build path, a container label, a repository URL or a resource cap. Those are the inputs a tenant controls, and every one of them is validated (`internal/compose/validate.go`, `internal/apps/validate.go`, `internal/giturl`) rather than trusted. Deployed apps run on `cargo-proxy` with `no-new-privileges` and resource caps, and cannot reach the platform database, which lives on a separate internal network.

What follows for an operator:

- **Treat an account on the instance as a host-level grant.** Registration is invite-only by default for this reason. Give admin to people you would give SSH to.
- **Do not publish the control plane's port directly.** Only Traefik publishes host ports; keep it that way.
- **Back up `CARGO_MASTER_KEY` separately from the database.** An attacker with both has every stored secret; an operator with neither has none of them.
- **Rotate after any suspected exposure.** `cargod rotate-key` re-seals every stored secret under a new key.
- **A registered worker host is the same grant again.** Cargo drives it through Docker over SSH, so its stored key is root on that machine too. The key is sealed with the master key and the host fingerprint is pinned on first contact, but only register machines you are willing to hand over entirely.

Known accepted risk: the control-plane process is root inside its own container. With a read-write Docker socket already mounted, dropping it to a non-root user buys nothing an attacker could not undo through the socket in one command, and doing it would need a data-directory ownership migration on every existing install. It is recorded here rather than fixed.

## Documentation

- [PRD](PRD.md) — product requirements
- [Phased plans](PhasedPlans.md) — roadmap and phase status
- [deploy/README.md](deploy/README.md) — SSL modes (HTTP-01 vs wildcard DNS-01) and production details
