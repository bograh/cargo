# Cargo — Product Requirements Document

- **Version:** 1.0 (v1 scope)
- **Date:** 2026-07-17
- **Status:** Approved
- **Companion document:** `docs/superpowers/specs/2026-07-17-cargo-design.md` (technical design)

---

## 1. Product Overview

Cargo is a self-hosted Platform-as-a-Service (PaaS) in the spirit of Dokploy and Coolify, giving teams a Vercel/Railway-like deployment experience on infrastructure they own. An administrator installs Cargo on a single server (VPS or EC2) via one Docker Compose command. Users then sign up, create organizations, and deploy applications from GitHub repositories or container registries — each app receiving an HTTPS URL with automatic SSL, live deployment logs, environment variable management, custom domains, and one-click rollback.

Cargo v1 is an **internal team tool**: it optimizes for practicality, operability, and a clean path to later phases (managed databases, OIDC, multi-server, Kubernetes) rather than for commercial polish or billing.

## 2. Problem Statement

Teams running their own infrastructure face a gap between two unsatisfying options:

1. **Raw Docker/SSH workflows** — every deploy is a manual sequence of SSH, git pull, docker build, and reverse-proxy edits. Slow, error-prone, and gated on the one person who knows the setup.
2. **Hosted PaaS (Vercel, Railway, Render)** — excellent UX, but code and data leave your infrastructure, pricing scales per-seat/per-app, and compliance-sensitive teams may not be able to use them.

Existing self-hosted options (Dokploy, Coolify) prove the model but come with their own stacks, opinions, and limitations. Cargo gives the team its **own** platform: one install command, then any team member can ship an app from a GitHub repo to a running HTTPS URL in a few clicks — no SSH, no YAML, no proxy config.

## 3. Target Users & Personas

| Persona | Description | Key needs |
|---|---|---|
| **Instance Admin** ("Priya") | Owns the infrastructure; installs and operates Cargo | 10-minute install, sensible defaults, upgrade = pull + restart, visibility across orgs |
| **Org Owner / Team Lead** ("Marcus") | Creates an organization, invites the team, owns apps and domains | Member management, role control, org-scoped visibility |
| **Developer** ("Aisha") | Ships applications daily | Connect repo → deploy in <5 clicks, live logs, env vars, rollback when things break |
| **Viewer** ("Sam") | PM/stakeholder who needs status, not deploy rights | Read-only view of apps, deployments, domains |

## 4. User Stories

**Instance administration**
- As an instance admin, I can install the entire platform with one script so that I don't need to assemble components by hand.
- As an instance admin, the first registered account becomes the admin, so bootstrap requires no CLI user-seeding.
- As an instance admin, I can configure the apps-domain suffix, SMTP, and GitHub App credentials from a UI so I don't have to reinstall to change settings.
- As an instance admin, upgrades are `docker compose pull && up -d` with automatic migrations.

**Organizations & membership**
- As a user, I can create an organization and become its owner.
- As an owner/admin, I can invite users by shareable link with a chosen role; SMTP is optional.
- As a user, I can belong to multiple organizations and switch between them.
- As a viewer, I can see apps and deployments but cannot change anything.

**Deploying applications**
- As a developer, I can connect my org's GitHub account and pick a repo and branch to deploy.
- As a developer, if my repo has a Dockerfile it just works; if it doesn't, Cargo builds it with Nixpacks automatically.
- As a developer, I can deploy a plain registry image (e.g. `ghcr.io/org/app:tag`) without any git integration.
- As a developer, every push to the tracked branch auto-deploys (if enabled), and I can also deploy manually.
- As a developer, I see build and deploy logs streaming live, and retained per deployment.
- As a developer, I can set encrypted environment variables; values are write-only after saving.
- As a developer, my app immediately gets `https://<app>.apps.<domain>`; I can attach custom domains with automatic SSL.
- As a developer, if a deploy goes bad, I can roll back to any previous deployment in one click.

## 5. Functional Requirements

### FR-1 — Authentication & Accounts
- FR-1.1 Email/password registration and login; argon2id password hashing.
- FR-1.2 Cookie-based sessions with refresh-token rotation and reuse detection.
- FR-1.3 The first registered user is flagged instance admin.
- FR-1.4 Auth endpoints are rate-limited.
- FR-1.5 Authentication is implemented behind an `AuthProvider` interface so OIDC can be added without refactoring callers.

### FR-2 — Organizations & Roles
- FR-2.1 Any user can create organizations; the creator becomes **owner**.
- FR-2.2 Roles: **owner** (all permissions incl. delete org), **admin** (manage members, apps, domains), **member** (create/deploy apps), **viewer** (read-only).
- FR-2.3 Org-scoped invites as shareable links with role and expiry; revocable.
- FR-2.4 All org resources are invisible and inaccessible to non-members (enforced by query scoping, not just UI).
- FR-2.5 The instance admin can list all orgs and users but is not implicitly a member of any org.

### FR-3 — Applications
- FR-3.1 Create an app from (a) a GitHub repo+branch or (b) a registry image reference.
- FR-3.2 Apps have a unique slug, exposed port, healthcheck path, and auto-deploy toggle.
- FR-3.3 Builder auto-detection: Dockerfile if present, otherwise Nixpacks.
- FR-3.4 Dockerfile builds support custom context path, Dockerfile path, and build args.
- FR-3.5 Private registry images support stored, encrypted pull credentials.

### FR-4 — Deployments
- FR-4.1 Deployment triggers: GitHub webhook (HMAC-validated), manual button, rollback.
- FR-4.2 Status machine: `queued → building → deploying → live | failed | cancelled`.
- FR-4.3 Live log streaming over SSE; logs persisted per deployment (last N retained).
- FR-4.4 Builds are concurrency-capped (default 2); deploys are serialized per app.
- FR-4.5 A failed build never affects the currently running app.
- FR-4.6 Rollback redeploys a previous deployment's retained image without rebuilding.
- FR-4.7 Known limitation: the apply step recreates the container — brief downtime per deploy; a failed healthcheck leaves the app down until rollback (zero-downtime is Phase 3).

### FR-5 — Domains & SSL
- FR-5.1 Every app receives an auto-generated subdomain `<slug>.<apps-suffix>` at creation.
- FR-5.2 Users can attach/remove custom domains; changes apply on next reconcile.
- FR-5.3 SSL is automatic via Let's Encrypt: wildcard DNS-01 (recommended) or per-domain HTTP-01, chosen at install; custom domains always HTTP-01.
- FR-5.4 Domain status (`active / pending / misconfigured`) is shown based on periodic DNS + HTTPS checks.

### FR-6 — Environment Variables
- FR-6.1 Per-app key/value env vars, AES-256-GCM encrypted at rest.
- FR-6.2 Values are write-only in the UI after saving.
- FR-6.3 Changing env vars triggers a reconcile on next deploy.

### FR-7 — Instance Administration
- FR-7.1 Instance settings UI: apps-domain suffix, SMTP, GitHub App credentials.
- FR-7.2 List all organizations and users.
- FR-7.3 Automatic DB migrations at startup; upgrades via image pull.

### FR-8 — Installation
- FR-8.1 Single install script: dependency checks → prompts (domains, ACME email, optional DNS token, optional SMTP) → generates secrets (mode 0600) → `docker compose up -d`.
- FR-8.2 Documented prerequisites: Docker + compose plugin, wildcard DNS A record, platform domain record, open ports 80/443.

## 6. Non-Functional Requirements

- **NFR-1 Installability:** from bare VM to working platform in ≤ 10 minutes, one command.
- **NFR-2 Time to first deploy:** from first login to a running HTTPS app in ≤ 5 minutes (excluding build time).
- **NFR-3 Operability:** entire platform = 3 containers (controlplane, Postgres, Traefik); no Redis, no external dependencies.
- **NFR-4 Performance:** API p95 < 200 ms for CRUD on a modest VPS (2 vCPU/4 GB); log streaming latency < 1 s.
- **NFR-5 Security:** secrets encrypted at rest; env files mode 0600; webhook HMAC validation; auth rate limiting; refresh-token rotation.
- **NFR-6 Reliability:** transient job failures retried with backoff; failed deploys preserve logs and never corrupt platform state.
- **NFR-7 Capacity (internal-tool scale):** tens of users, low hundreds of apps, single server.
- **NFR-8 Upgrade safety:** rolling platform upgrades must not require app redeploys.

## 7. Key UX Flows

**Bootstrap:** install script → open platform domain → register (becomes instance admin) → create org → (optional) configure GitHub App in admin settings.

**First app (GitHub):** New App → GitHub source → authorize/install GitHub App → pick repo + branch → builder auto-detected → set port (+ env vars) → Deploy → watch live logs → `https://<slug>.<apps-suffix>` live.

**Push-to-deploy:** push to tracked branch → webhook → new deployment appears with live logs → healthy → traffic switches.

**Rollback:** app → Deployments → pick previous successful deployment → Rollback → live again within seconds (no build).

## 8. Out of Scope (v1)

- ~~Managed databases (Postgres/Redis/MySQL/Mongo) — Phase 2~~ **shipped (8.1)**
- ~~User-supplied docker-compose app source — Phase 2~~ **shipped (13.1)**
- ~~Multi-server / remote Docker hosts — Phase 3~~ **shipped (12a)**
- ~~Zero-downtime (blue/green) deployments — Phase 3~~ **shipped (Phase 11)**
- ~~App metrics/monitoring dashboards — Phase 3~~ **shipped (13.2)**
- ~~Notifications (email/Slack) for deployment events — later~~ **shipped (10.8)**
- ~~OIDC provider — Phase 2~~ **shipped (8.2)**
- ~~Master-key rotation~~ **shipped (13.3)**
- Kubernetes target — future, behind `DeployProvider`
- GitLab/Bitbucket/Gitea integration — later (GitHub-only v1)
- Billing, plans, quotas — not applicable to internal tool

## 9. Success Metrics

| Metric | Target |
|---|---|
| Install → platform reachable | ≤ 10 min |
| First login → first app live | ≤ 5 min (+ build time) |
| Deployment success rate (excluding app-code failures) | ≥ 95% |
| Rollback → service restored | ≤ 1 min |
| Platform upgrade duration | ≤ 2 min downtime of the UI (apps unaffected) |

## 10. Roadmap

| Phase | Contents | Status |
|---|---|---|
| **v1** | Auth/orgs/invites · GitHub + image sources · Dockerfile/Nixpacks builders · wildcard + custom domains with auto SSL · env vars · live logs · rollback · install script | ✅ shipped |
| **Phase 2** | Managed databases (Postgres, Redis first) · OIDC provider · docker-compose app source | ✅ shipped |
| **Phase 3** | Multi-server (Docker-over-SSH) · zero-downtime blue/green deploys · metrics/monitoring | ✅ shipped (12a on workers) |
| **Production Hardening** | Security & performance audit: invite-only signup, git-clone SSRF screening, compose validation, instance resource ceilings, Redis ACL isolation, DB connection pooling, indexed queries, rate limiting, SSE stream caps, Slowloris protection, govulncheck CI | ✅ shipped |
| **Remaining** | Additional git providers (GitLab/Bitbucket/Gitea) · per-host metrics (12b) · worker-host bootstrap (12c) | ⬜ planned |

## 11. Technical Constraints & Dependencies

- **Stack:** Go (chi, River, goose, sqlc) + React (Vite, TypeScript, Tailwind, shadcn/ui, TanStack Query) — single Go binary serving the embedded SPA.
- **Runtime dependency:** Docker Engine with the compose plugin on the host; the controlplane mounts the Docker socket.
- **Third-party services:** GitHub (App + webhooks), Let's Encrypt (certificates), optional DNS provider API (wildcard mode), optional SMTP.
- **State:** Postgres 16 (control state + job queue) — no Redis.
- **Proxy:** Traefik v3, configured entirely via container labels.

## 12. Naming Note

The project is named **Cargo** and lives at `~/Code/cargo`. The name collides with Rust's `cargo` tool on developer machines; this is harmless in practice because Cargo-the-platform runs inside containers and is never installed into a developer `PATH`, but the Go module/binary may use `cargod` if disambiguation is ever needed.
