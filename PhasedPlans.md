# Cargo — Phased Plans

Roadmap from empty repo to full PRD scope. Phases 0–7 deliver **v1**; Phases 8–9 are the PRD's Phase 2/3. Each phase is independently shippable and ends with the platform in a working state.

Status legend: ✅ done · 🔜 next · ⬜ not started

Source documents: [PRD.md](PRD.md) · [design spec](docs/superpowers/specs/2026-07-17-cargo-design.md)

---

## Phase 0 — Foundation ✅

*Plan: `docs/superpowers/plans/2026-07-17-cargo-m1-foundation.md` (complete)*

### 0.1 Go service skeleton ✅
Single Go binary: chi router, config from env, JSON logging, health endpoint.
- ✅ `GET /healthz` returns 200
- ✅ Config validates `CARGO_DATABASE_URL`, `CARGO_MASTER_KEY` (32-byte hex), env mode
- ✅ Consistent API error envelope `{ "error": { code, message, fields? } }`

### 0.2 Postgres + migrations + typed queries ✅
goose migrations embedded in the binary, run automatically at startup (FR-7.3); sqlc for type-safe queries.
- ✅ Fresh DB migrates on boot with no manual step
- ✅ `instance_settings` key/value store with `GET /api/v1/instance/info`
- ✅ Integration tests run against real Postgres (testcontainers)

### 0.3 Embedded React SPA shell ✅
Vite + React + TS + Tailwind app embedded via `go:embed`, SPA route fallback.
- ✅ `npm run build` output served by the Go binary at `/`
- ✅ Client-side routes fall back to `index.html`

### 0.4 Dockerfile, dev compose, CI ✅
- ✅ Multi-stage Dockerfile (web build → Go build → alpine with docker CLI + compose plugin)
- ✅ `deploy/docker-compose.dev.yml` runs db + controlplane
- ✅ CI: golangci-lint, go tests, web tests/build, docker build

---

## Phase 1 — Authentication & Organizations (backend) ✅

*Plan: `docs/superpowers/plans/2026-07-17-cargo-m2-auth-orgs.md` (complete). Covers FR-1, FR-2, FR-7.2.*

### 1.1 Email/password auth ✅ (FR-1.1, FR-1.3, FR-1.5)
argon2id hashing; register/login; first registered user becomes instance admin; password auth behind an `AuthProvider` interface so OIDC slots in later.
- ✅ Passwords stored as argon2id PHC strings; verified in constant time
- ✅ First `POST /auth/register` yields `is_instance_admin: true`, all later users false (atomic in SQL)
- ✅ Duplicate email → 409; weak password (<10 chars) → 400
- ✅ Login via `Provider.Authenticate` seam; unknown email indistinguishable from wrong password (timing + response)

### 1.2 Cookie sessions with refresh rotation ✅ (FR-1.2)
Access (15 min) + refresh (30 d) tokens as HttpOnly cookies, stored hashed; refresh rotation with family-wide revocation on reuse.
- ✅ Reusing a rotated refresh token returns 401 **and** revokes every session in the family
- ✅ Logout revokes the family; access token unusable afterwards
- ✅ Cookies `HttpOnly`, `SameSite=Lax`, `Secure` in production

### 1.3 Auth rate limiting ✅ (FR-1.4)
- ✅ register/login/refresh limited per IP (10/min, burst 10) → 429 beyond

### 1.4 Organizations & roles ✅ (FR-2.1, FR-2.2, FR-2.4)
Org CRUD; roles owner/admin/member/viewer enforced in the service layer; strict query scoping.
- ✅ Creator becomes owner; unique DNS-safe slug (collision → random suffix)
- ✅ Non-members get 404 (invisible), never 403, on org resources
- ✅ Only owner deletes an org; last owner can't be demoted/removed
- ✅ Admin+ manages members and roles; users can leave

### 1.5 Invite links ✅ (FR-2.3)
Shareable org invites with role + expiry; token shown once, stored hashed; revocable.
- ✅ Admin+ creates/lists/revokes invites; viewer/member cannot
- ✅ Accepting a valid invite adds membership with the invite's role; re-accept is a no-op
- ✅ Revoked/expired/bogus tokens → 400

### 1.6 Instance admin listing ✅ (FR-2.5, FR-7.2)
- ✅ `GET /api/v1/admin/users|orgs` for instance admin only (403 otherwise)
- ✅ Password hashes never serialized

---

## Phase 2 — Deployment Core ✅

*The heart of the product: create an app, deploy it, watch logs, roll back. Registry-image and public-git sources; GitHub App auth arrives in Phase 3. Covers FR-3, FR-4 (except webhooks), FR-6.*

### 2.1 Secrets encryption ✅ (FR-6.1)
AES-256-GCM helper keyed by `CARGO_MASTER_KEY`, with key-version column for future rotation.
- Round-trip seal/open; tampered ciphertext rejected
- Env var values and registry credentials stored only encrypted; never logged

### 2.2 Applications CRUD ✅ (FR-3.1, FR-3.2, FR-3.4, FR-3.5)
Org-scoped apps: source (`git` public URL + branch | `image` ref), unique slug, exposed port, healthcheck path, auto-deploy toggle, builder override, Dockerfile context/path/build-args, optional encrypted registry credentials.
- Member+ creates/updates/deletes apps; viewer read-only; non-members 404
- Slug unique across the instance (it becomes the subdomain)
- Registry credentials write-only after saving
- Deleting an app tears down its containers, compose project, logs

### 2.3 Environment variables ✅ (FR-6.1–6.3)
Per-app key/value vars, encrypted at rest, write-only after save.
- Bulk set/delete; GET returns keys only, never values
- Values land decrypted only in the app's `.env` file (mode 0600) at reconcile
- Changed vars apply on next deploy

### 2.4 Job queue ✅ (NFR-3, NFR-6, FR-4.4)
River (Postgres-backed, no Redis) embedded in the binary: `deploy` job kind, build concurrency cap (default 2), per-app serialization, backoff retries.
- Two deploys of different apps run concurrently; two of the same app serialize
- Transient failure retried with backoff; final failure marks deployment `failed`
- Queue survives controlplane restart (jobs resume from Postgres)

### 2.5 Builders ✅ (FR-3.3, FR-3.4)
`Builder` interface: shallow git clone → Dockerfile build (BuildKit via docker CLI) or Nixpacks (CLI bundled in image); auto-detection.
- Repo with Dockerfile → dockerfile builder; without → nixpacks; explicit override respected
- Image tagged `app-<slug>:<deployment-id>`, kept in local daemon
- Build args, custom context and Dockerfile path honored
- All build output streams to the deployment log

### 2.6 Reconciler + Traefik labels ✅ (FR-5.1 partially, design §5–6)
Per-app generated Compose project under `<data-dir>/apps/<id>/`: image, `.env` (0600), restart policy, healthcheck, shared `cargo-proxy` network, Traefik labels for `<slug>.<apps-suffix>`. The only package touching Docker, behind a `DeployProvider` interface.
- `docker compose up -d` applies; healthcheck gate (2 min default) decides live/failed
- A failed build or failed healthcheck never touches the previously running container's config on the next successful deploy path (FR-4.5)
- App reachable through Traefik at its auto subdomain once Phase 4 wires the proxy (labels correct now, verified by golden-file tests)

### 2.7 Deployments + live logs ✅ (FR-4.1–4.3, FR-4.6)
Deployment records with status machine `queued → building → deploying → live | failed | cancelled`; one log file per deployment; SSE streaming; one-click rollback reusing a retained image.
- Manual deploy → deployment visible immediately as `queued`, transitions observable
- `GET /deployments/:id/logs` (SSE) replays existing log then streams live, <1 s latency
- Rollback re-applies an old deployment's image with no build; restores service
- Last 5 deployments' logs + images retained per app; daily prune job removes older

**Phase exit demo:** register → create org → create app from `ghcr.io/…` image or public repo → deploy → watch live logs → container running with Traefik labels → break it → rollback.

---

## Phase 3 — GitHub Integration ✅

*Covers FR-3.1(a) fully, FR-4.1 webhooks, the GitHub parts of FR-7.1.*

### 3.1 GitHub App connection ✅
Org-scoped GitHub App installation flow (start/callback); installation IDs stored; repo/branch lists fetched live.
- Admin+ connects a GitHub account/installation to an org from the API
- `GET /github/repos` and `/branches` list live data for the installation
- Installation tokens used for clones; never persisted beyond their TTL

### 3.2 Private repo deploys ✅
- App created from an installation repo+branch clones with an installation token at the exact commit
- Token failure → clear `failed` deployment with actionable log line

### 3.3 Push-to-deploy webhooks ✅ (FR-4.1, NFR-5)
`POST /webhooks/github`, HMAC-validated, respecting the auto-deploy toggle.
- Push to tracked branch with auto-deploy on → new deployment within seconds
- Bad HMAC → 401 and no side effects; pushes to other branches ignored
- Auto-deploy off → webhook recorded but no deployment

### 3.4 GitHub App credentials in instance settings ✅ (part of FR-7.1)
- Instance admin sets App ID / private key / webhook secret via API; stored encrypted

---

## Phase 4 — Domains & SSL ✅

*Covers FR-5 and the production Traefik topology (NFR-3).*

### 4.1 Traefik v3 in the stack ✅ (deploy/docker-compose.yml)
Traefik joins the compose stack as the only container publishing 80/443; controlplane UI routed via the same label mechanism (dogfooded).
- Platform UI reachable at its domain through Traefik; 80→443 redirect
- App containers never publish host ports

### 4.2 Automatic SSL ✅ (FR-5.3; dns01 via compose overlay)
Two install-time modes: wildcard DNS-01 (provider API token) or per-domain HTTP-01.
- Wildcard mode: one cert covers `*.apps.<domain>`; new apps HTTPS-ready instantly
- HTTP-01 mode: cert issued on first valid request per domain
- `acme.json` persisted across restarts/upgrades

### 4.3 Custom domains ✅ (FR-5.2; applied on next deploy)
Attach/remove custom domains per app; applied on next reconcile; always HTTP-01.
- Admin+ attaches a domain; router `Host()` rule includes it after reconcile
- Removing a domain stops routing it

### 4.4 Domain status checks ✅ (FR-5.4; every 10 min)
Periodic job checks DNS resolution + HTTPS response per domain.
- Each domain shows `active / pending / misconfigured`, refreshed on a schedule
- Wrong DNS target → `misconfigured` with the observed A record in details

---

## Phase 5 — Frontend ✅

*The full SPA over the Phase 1–4 APIs. React Router, TanStack Query, shadcn/ui. Covers the UI side of every FR plus FR-6.2.*

### 5.1 Auth pages & session handling ✅
- Register/login forms with field-level validation errors from the API envelope
- Silent refresh on 401 then retry; logout clears state
- First-user registration lands on "create your first organization"

### 5.2 Org dashboard & switcher ✅
- App cards with status; org switcher for multi-org users (FR-2 user stories)
- Org settings: members list, role changes, invite creation with copyable link
- Viewers see everything read-only; mutation controls hidden *and* API-rejected

### 5.3 New App wizard ✅ (git-URL/image sources; GitHub repo picker lands with Phase 3)
- Source step (GitHub repo+branch picker / image ref) → builder auto-detected with override → port, healthcheck, env vars → Deploy
- ≤5 clicks from "New App" to first deployment for the GitHub path (NFR-2)

### 5.4 App detail: Deployments + live log viewer ✅
- Tabs: Overview, Deployments, Environment, Domains, Settings
- Deployment list with status badges; log viewer streams SSE live, auto-scroll, replay for past deployments
- Rollback button on any previous successful deployment with confirm

### 5.5 Environment & Domains tabs ✅ (env editor done; Domains tab lands with Phase 4)
- Env editor: keys visible, values write-only after save (FR-6.2)
- Domains tab: auto subdomain shown, custom domain add/remove, live status chip

### 5.6 Instance admin area ✅ (listings done; settings form lands with Phase 6)
- Settings form: apps-domain suffix, SMTP, GitHub App creds (FR-7.1)
- All-orgs and all-users listings (admin only)

---

## Phase 6 — Instance Settings & Operations 🔜

*Backend for FR-7.1 plus operational hardening. (Small; can merge into Phase 5 if convenient.)*

### 6.1 Instance settings write API (FR-7.1)
- Instance admin updates apps-domain suffix, SMTP config; secrets stored encrypted
- Changed suffix applies to newly created apps; existing domains unchanged

### 6.2 Housekeeping jobs (design §10)
- Daily prune keeps last N (default 5) images + logs per app
- Expired sessions/invites purged

---

## Phase 7 — Installation & Packaging ⬜

*Covers FR-8, NFR-1, NFR-8. v1 ships at the end of this phase.*

### 7.1 Production compose + install script (FR-8.1)
`install.sh`: dependency checks → prompts (platform domain, apps suffix, ACME email, optional DNS token, optional SMTP) → generates master key + DB password into mode-0600 `.env` → `docker compose up -d`.
- Bare Ubuntu VM with Docker → reachable platform in ≤10 min, one command (NFR-1)
- Script warns to back up the master key; refuses to run without Docker/compose
- Exactly 3 platform containers: controlplane, Postgres, Traefik (NFR-3)

### 7.2 Upgrade path (FR-1.4 admin story, NFR-8)
- `docker compose pull && up -d` upgrades; migrations run automatically
- Running user apps unaffected by a platform upgrade
- UI downtime during upgrade ≤2 min

### 7.3 Docs + E2E smoke
- README: prerequisites (DNS records, ports 80/443), install, upgrade, backup
- Smoke script: installs the platform and deploys a sample app end-to-end

---

## Phase 8 — PRD "Phase 2" ⬜

### 8.1 Managed databases (Postgres, Redis first)
- One-click provision of a managed Postgres/Redis per org; connection string injected as env vars; backed by the same per-app compose model

### 8.2 OIDC login
- OIDC provider implements the existing `AuthProvider` interface; no caller refactor
- Instance admin configures issuer/client from settings

### 8.3 docker-compose app source
- Users deploy a repo containing their own compose file; Cargo layers networking/labels

*(Acceptance criteria to be detailed when the phase is specced.)*

## Phase 9 — PRD "Phase 3" ⬜

### 9.1 Multi-server (Docker-over-SSH) — remote hosts as deploy targets
### 9.2 Zero-downtime blue/green deploys — removes FR-4.7's known limitation
### 9.3 Metrics & monitoring dashboards

*(To be specced after v1 ships.)*
