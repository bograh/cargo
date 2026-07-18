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
- DNS records pointing at the host: `<platform-domain>` and `*.<apps-domain>`
- Ports 80 and 443 open

## Quick install

```bash
cd deploy
./install.sh
```

The installer checks dependencies, prompts for your platform domain, apps-domain suffix, and Let's Encrypt email (optionally a DNS provider for wildcard certificates), generates secrets into a mode-0600 `.env`, and starts the stack. Non-interactive installs can set `CARGO_PLATFORM_DOMAIN`, `CARGO_APPS_SUFFIX`, and `CARGO_ACME_EMAIL` in the environment instead.

> **Back up `CARGO_MASTER_KEY` from `.env` somewhere safe.** Environment
> variables and credentials are encrypted with it and are unrecoverable
> without it.

Open `https://<platform-domain>` and register — the first account becomes the instance admin. Instance settings (apps-domain suffix, SMTP, GitHub App credentials) are managed from the admin area in the UI.

## Upgrade

```bash
docker compose pull && docker compose up -d
```

Migrations run automatically at startup; running user apps are not touched.

## Local development

```bash
cd deploy
docker compose -f docker-compose.dev.yml up --build
```

Serves the platform on http://localhost:8080 without Traefik/SSL. Backend tests: `go test ./...`; frontend: `npm test` in `web/`. An end-to-end smoke test (install → register → deploy → live) lives at `scripts/smoke.sh`.

## Documentation

- [PRD](PRD.md) — product requirements
- [Phased plans](PhasedPlans.md) — roadmap and phase status
- [deploy/README.md](deploy/README.md) — SSL modes (HTTP-01 vs wildcard DNS-01) and production details
