# Cargo

Cargo is a self-hosted Platform-as-a-Service in the spirit of Dokploy and Coolify: a Vercel/Railway-like deployment experience on infrastructure you own. Install it on a single server with one command, then anyone on the team can ship an app from a GitHub repo or a container registry to a running HTTPS URL in a few clicks — no SSH, no YAML, no proxy config.

- Apps deploy from GitHub repos (Dockerfile or Nixpacks auto-detected) or plain registry images
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

- A Linux host with Docker Engine + the compose plugin
- Ports 80 and 443 open
- For a production install: DNS records pointing at the host (`<platform-domain>` and `*.<apps-domain>`). No domain? The installer falls back to a local install on localhost / the server's IP.

## Quick install

```bash
cd deploy
./install.sh
```

The installer checks dependencies, prompts for your platform domain, apps-domain suffix, and Let's Encrypt email (optionally a DNS provider for wildcard certificates), generates secrets into a mode-0600 `.env`, and starts the stack. Leave the platform domain empty for a local install (plain HTTP + self-signed HTTPS, no DNS or certificates needed). Non-interactive installs can set `CARGO_PLATFORM_DOMAIN`, `CARGO_APPS_SUFFIX`, and `CARGO_ACME_EMAIL` in the environment instead.

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

## Documentation

- [PRD](PRD.md) — product requirements
- [Phased plans](PhasedPlans.md) — roadmap and phase status
- [deploy/README.md](deploy/README.md) — SSL modes (HTTP-01 vs wildcard DNS-01) and production details
