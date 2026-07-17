# Cargo — Design Spec

- **Date:** 2026-07-17
- **Status:** Approved via brainstorming
- **Project directory:** `~/Code/cargo`

## 1. Overview

Cargo is a self-hosted Platform-as-a-Service in the spirit of Dokploy/Coolify, built as an **internal tool for a team**. An admin installs Cargo on their own infrastructure (a VPS or EC2 instance) via a single Docker Compose install. Users then log in, create organizations, and deploy applications with a Vercel/Railway-like experience: connect a GitHub repo, click deploy, get an HTTPS URL.

**Core decisions (agreed during brainstorming):**

| Decision | Choice |
|---|---|
| Purpose | Internal team tool — practicality over polish |
| Deployment target | Plain Docker now; Kubernetes later behind a `DeployProvider` interface |
| Stack | Go backend + React frontend |
| Deploy sources (phased) | Git repos, registry images (v1) · docker-compose, managed databases (Phase 2) |
| Domains | Auto subdomain per app **and** custom domains, both with automatic SSL |
| Fleet | Single server first; multi-server in Phase 3 |
| Auth | Email/password + invites now; OIDC later behind an `AuthProvider` interface |
| Git provider | GitHub App integration only in v1 |

## 2. Architecture

**Approach A — single-binary monolith + Docker socket.** One Go binary contains the REST API, auth, and background job workers, backed by Postgres, with Traefik as reverse proxy. The whole platform installs as one `docker-compose.yml`. Every app — even single-container ones — is reconciled into a generated per-app Docker Compose project, giving one uniform execution model (networking, env files, Traefik labels, restart policies, healthchecks).

```
┌─────────────────────────────────────────────────────────────────┐
│  Docker host (VPS / EC2)                                        │
│                                                                 │
│  ┌──────────────┐   ┌──────────────────┐   ┌────────────────┐  │
│  │   Traefik    │   │  controlplane    │   │   Postgres     │  │
│  │  (proxy+SSL) │◄──│  (single Go bin) │──►│ (control state)│  │
│  └──────┬───────┘   │                  │   └────────────────┘  │
│         │           │  · REST API      │                       │
│         │ routes    │  · auth/orgs     │                       │
│         │ via labels│  · job workers   │                       │
│         ▼           │  · builder       │                       │
│  ┌──────────────┐   │  · reconciler ───┼──► Docker socket      │
│  │ user app     │◄──│  · SSE log hub   │    (mounted)          │
│  │ containers   │   │  · embedded SPA  │                       │
│  └──────────────┘   └──────────────────┘                       │
└─────────────────────────────────────────────────────────────────┘
```

**Forward-looking seams** (make later phases additive, not rewrites):

- `DeployProvider` interface — Docker implementation today, K8s later
- `AuthProvider` interface — password today, OIDC later
- Per-app Compose model — makes user-supplied Compose a natural extension
- Multi-server later via Docker-over-SSH (same Docker client, different dialer)

## 3. Component Design (Go packages)

| Package | Responsibility |
|---|---|
| `api` | REST API (chi router), request validation, error envelope |
| `auth` | Email/password + invites behind `AuthProvider`; argon2id hashing; refresh-token rotation with reuse detection |
| `orgs` | Organizations, memberships, roles |
| `apps` | App CRUD, config, encrypted env vars |
| `deployments` | Deployment records, status machine, log storage |
| `builder` | Git clone → Dockerfile (BuildKit) or Nixpacks build → tagged image; behind a `Builder` interface |
| `reconciler` | Generates per-app Compose project and applies it. **The only package that talks to Docker** (`DeployProvider` seam) |
| `jobs` | River queue (Postgres-backed — no Redis): `deploy`, `prune_images` |
| `events` | SSE hub streaming build logs and status to the UI |
| `proxy` | Traefik integration via container labels |
| `config` | Env/flag configuration |
| `db` | Migrations (goose), queries (sqlc) |

**Frontend:** Vite + React + TypeScript + Tailwind + shadcn/ui + TanStack Query + React Router, embedded into the Go binary via `go:embed`.

## 4. Data Model

Postgres 16, goose migrations, sqlc type-safe queries.

**Identity & tenancy**

- `users` — email, password hash, `is_instance_admin` (first registered user becomes instance admin)
- `organizations` — name, slug; creator becomes owner
- `memberships` — `user_id × org_id` + role: **owner** (everything incl. delete org) · **admin** (manage members, apps, domains) · **member** (create/deploy apps) · **viewer** (read-only)
- `invites` — org-scoped token, role, expiry; shareable link. SMTP optional — without a mail server, admins copy the invite link manually

**Apps & deployments**

- `applications` — org-scoped: name, unique slug (→ subdomain), source type (`git` | `image`), builder (`dockerfile` | `nixpacks`), git repo/branch/installation ref, image ref, exposed port, healthcheck path, auto-deploy-on-push toggle, status
- `env_vars` — app-scoped key/value, encrypted at rest (AES-256-GCM, platform master key from mounted secret file; key-version column for future rotation). Values are **write-only** in the UI after saving
- `domains` — app-scoped: one auto-generated subdomain plus any number of custom domains, each with status
- `deployments` — per-app history: commit sha, image tag, trigger (`webhook` | `manual` | `rollback`), actor, status (`queued → building → deploying → live | failed | cancelled`), timestamps. **Rollback = redeploy an old deployment's image tag** (the last 5 deployments' images are retained per app by default, configurable; pruned by a daily job)
- Deployment logs: one file per deployment on disk, streamed live via SSE, retained for the last 5 deployments (same retention window as images)

**Designed now, built later:** `managed_databases` (Phase 2), `github_installations` (org-scoped GitHub App installation IDs; repo lists fetched live from GitHub's API).

**Tenancy rules:** every org-scoped row carries `org_id`; all queries filter by caller's membership + role. Instance admin sees all orgs but is not implicitly a member.

## 5. Build & Deploy Engine

**Job processing:** River workers embedded in the Go binary. Build concurrency capped (default 2); deploys serialized per app. Transient failures retried with exponential backoff; permanent failure marks the deployment `failed`.

**Git deploy pipeline** (each step appends to the deployment log + publishes SSE events):

1. **Clone** — shallow clone at exact commit using a GitHub App installation token
2. **Build** — `dockerfile` builder (BuildKit via Docker socket; configurable context path, Dockerfile path, build args) or `nixpacks` (CLI bundled in the controlplane image; auto-selected when no Dockerfile exists). Image tagged `app-<slug>:<deployment-id>`, stored in the local daemon — no registry needed on single server
3. **Reconcile** — (re)generates the app's Compose project under `/var/lib/cargo/apps/<id>/`: compose file + `.env` (mode 0600, decrypted env vars), `restart: unless-stopped`, healthcheck, shared proxy network, Traefik labels covering all the app's domains
4. **Apply** — `docker compose up -d`, then wait for healthcheck (default 2 min timeout)
5. **Result** — healthy → `live`; failed → `failed` with log retained

**Registry-image source:** same pipeline minus clone/build; compose references the image directly; optional encrypted registry credentials.

**Triggers:** GitHub webhook (HMAC-validated push to tracked branch, respects auto-deploy toggle) · manual Deploy button · rollback (skips build).

**Known trade-off (accepted):** builds fail safely, but apply recreates the container — brief downtime per deploy, and a failed healthcheck leaves the app down until rollback. Zero-downtime blue/green deploys are Phase 3.

## 6. Networking, Domains & SSL

- Traefik v3 is the only container publishing host ports (80/443); 80 redirects to 443. It watches the Docker socket for labeled containers on the shared `cargo-proxy` network. App containers never publish host ports.
- The platform UI itself gets a domain chosen at install (e.g. `cargo.yourdomain.com`), routed via the same label mechanism (dogfooded).
- Each app gets an auto subdomain `<app-slug>.apps.yourdomain.com` (suffix configurable). Custom domains are added to the router's `Host()` rule on next reconcile.
- **Certificates, two install-time modes:**
  1. **Wildcard via DNS-01 (recommended)** — one cert for `*.apps.yourdomain.com`; requires DNS provider API token (Cloudflare, Route53, etc.)
  2. **Per-domain HTTP-01** — no DNS API needed; first-hit latency and Let's Encrypt rate limits
  - Custom domains always use HTTP-01 once DNS points at the server.
- **Domain status is best-effort:** a periodic job checks DNS resolution + HTTPS response and shows `active / pending / misconfigured`.
- **Install-time DNS requirements (documented):** wildcard A record `*.apps.yourdomain.com` → server IP, plus a record for the platform domain.

## 7. API Surface

REST, `/api/v1`, JSON, cookie-based session with refresh tokens. Consistent error envelope `{ "error": { "code", "message", "fields?" } }`.

| Group | Endpoints |
|---|---|
| Auth | `register`, `login`, `logout`, `refresh`, `me` |
| Orgs | CRUD org · list/add/remove members · create/revoke invites |
| Apps | CRUD app · `POST /apps/:id/deploy` · `POST /apps/:id/rollback` · list deployments · SSE `GET /deployments/:id/logs` |
| Env vars | bulk get/set/delete per app |
| Domains | attach/remove/list per app + status |
| GitHub | start/callback App installation · list repos · list branches |
| Webhooks | `POST /webhooks/github` (HMAC-validated) |
| Instance admin | list orgs/users · instance settings (domain suffix, SMTP, GitHub App creds) |

**Onboarding flow:** register → (first user becomes instance admin) → create org or accept invite → org dashboard → New App wizard: source (GitHub repo / image) → repo+branch → builder auto-detect → port + env vars → Deploy.

**Frontend pages:** login/register · org dashboard (app cards) · app detail tabs (Overview, Deployments + live log viewer, Environment, Domains, Settings) · org settings (members/invites) · instance admin area.

## 8. Installation & Packaging

```bash
curl -fsSL https://get.cargo.dev | sh   # or: git clone … && ./install.sh
```

Install script: checks Docker + compose plugin → prompts (platform domain, apps-domain suffix, ACME email, optional DNS-provider token, optional SMTP; GitHub App creds can be added later in admin UI) → generates master key + DB password into mode-0600 `.env` → `docker compose up -d`. First registered account becomes instance admin.

**Upgrades:** `docker compose pull && up -d`; goose migrations run automatically at startup.

**Host data layout** — `/var/lib/cargo/`: Postgres volume, Traefik `acme.json`, per-app compose projects, deployment logs.

**Repo layout (monorepo):**

```
cargo/
  cmd/server/          # Go entrypoint
  internal/            # api, auth, orgs, apps, deployments, builder,
                       # reconciler, jobs, events, proxy, config, db
  web/                 # React SPA
  migrations/          # goose SQL migrations
  deploy/              # docker-compose.yml, install.sh, traefik config
  Dockerfile           # multi-stage: builds web, then Go binary
  PRD.md
```

## 9. Phasing

- **v1** — auth/orgs/invites, GitHub + image sources, Dockerfile/Nixpacks builders, wildcard + custom domains with auto SSL, env vars, live logs, rollback, install script
- **Phase 2** — managed databases (Postgres + Redis first), OIDC provider, docker-compose app source
- **Phase 3** — multi-server (Docker-over-SSH), zero-downtime blue/green deploys, app metrics/monitoring

## 10. Error Handling & Security

- API: consistent error envelope; validation errors name offending fields
- Deployments: failures keep full logs and land in `failed`; builds never touch the running app; failed healthcheck → one-click rollback; v1 surfaces failures in UI only (no email/Slack)
- Jobs: backoff retry for transient errors
- Security: argon2id passwords, refresh-token rotation + reuse detection, webhook HMAC, rate-limited auth endpoints, env files mode 0600
- Housekeeping: daily prune (last N images per app); app deletion tears down compose project, logs, domains
- Documented caveat: losing the master key makes stored env vars unrecoverable — install script warns to back it up

## 11. Testing

- Go: table-driven unit tests per package; integration tests via testcontainers-go (real Docker + Postgres) for reconciler and deploy pipeline
- Frontend: Vitest + Testing Library on critical flows (login, app wizard, env editor, log viewer)
- E2E: smoke script installing the platform and deploying a sample app, runnable on demand
- CI (GitHub Actions): golangci-lint, go test, web build + tests, Docker image build
