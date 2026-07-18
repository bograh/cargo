# Cargo M6 — Domains & SSL Implementation Plan (PhasedPlans Phase 4)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans.

**Goal:** Custom domains per app (attach/remove, applied at next reconcile), periodic DNS+HTTPS status checks, and the production Traefik v3 topology with automatic Let's Encrypt SSL in both install modes (wildcard DNS-01 / per-domain HTTP-01).

**Architecture:** `domains` table stores **custom** domains (the auto subdomain stays derived from slug+suffix). The pipeline includes custom domains in `reconciler.Spec.Domains`, so Traefik's `Host()` rule covers them on the next deploy (FR-5.2 semantics). A River periodic job (10 min) resolves DNS and probes HTTPS to set `active|pending|misconfigured`. `deploy/docker-compose.yml` adds Traefik as the only host-port container with ACME configured by env vars; the controlplane is routed via its own labels (dogfooded).

## Global Constraints
- Domain statuses exactly: `active`, `pending`, `misconfigured` (FR-5.4)
- Hostname validation: lowercase RFC-1123 (`[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+`), max 253 chars; reject anything under the instance apps-suffix (collision with auto subdomains)
- Attach/remove = admin+ (FR-2.2); list = any member; non-members 404
- Custom domains route via the same per-app router; certs: HTTP-01 resolver `le` for custom domains, wildcard covers the apps-suffix in DNS-01 mode
- Status checker never marks `active` unless an HTTPS (or HTTP during local dev) response arrives; DNS NXDOMAIN → `misconfigured`
- Same verification bar: full Go suite, lint 0 issues, vitest, web build

## Tasks

### Task 1: `domains` schema + queries + service methods
- Migration `00006_domains.sql`: `domains(id UUID PK, app_id UUID REFERENCES applications ON DELETE CASCADE, hostname TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('active','pending','misconfigured')), last_checked_at TIMESTAMPTZ, created_at)`
- Queries: `CreateDomain`, `ListDomainsForApp`, `DeleteDomain (id+app_id)`, `ListAllDomains`, `UpdateDomainStatus`
- `apps.Service` gains `AddDomain(ctx, appID, actor, hostname, appsSuffix string) (sqlc.Domain, error)` (admin+, validation), `ListDomains(ctx, appID, actor)` (viewer+), `RemoveDomain(ctx, appID, actor, domainID)` (admin+); pipeline-facing `CustomDomains(ctx, appID) ([]string)`
- Tests: validation (bad hostname, suffix collision), role enforcement, round trip
- Commit `feat: add custom domains schema and service`

### Task 2: API endpoints + pipeline wiring
- `GET/POST /api/v1/apps/{appID}/domains`, `DELETE /api/v1/apps/{appID}/domains/{domainID}`; POST body `{hostname}`; responses lowercase-JSON via `domainJSON`
- Pipeline: `Spec.Domains = [auto] + CustomDomains(appID)`
- Tests: handler stubs; pipeline test asserts custom domain reaches provider spec
- Commit `feat: add domain endpoints and reconcile custom domains`

### Task 3: Domain status check job
- `internal/jobs/domaincheck.go`: `DomainCheckArgs` kind `domain_check`, periodic 10 min (RunOnStart true); for each row in `ListAllDomains`: `net.LookupHost(hostname)` → fail = `misconfigured`; else GET `https://hostname` (5 s, TLS verify on; on TLS/conn error retry `http://`) → response = `active`, no response = `pending`; `UpdateDomainStatus` + `last_checked_at=now()`
- `RunDomainCheck(ctx, pool, lookup, probe)` exported with injectable lookup/probe for tests
- Tests: fake lookup/probe drive all three statuses
- Commit `feat: add periodic domain status checks`

### Task 4: Production Traefik stack
- `deploy/docker-compose.yml`: services `traefik` (v3.3, ports 80/443, docker socket ro, `acme.json` volume, 80→443 redirect, providers.docker with `exposedByDefault=false`, resolver `le` HTTP-01; DNS-01 wildcard via `CARGO_ACME_MODE=dns01` + `CARGO_DNS_PROVIDER` + provider creds env), `controlplane` (labels routing `CARGO_PLATFORM_DOMAIN`, joins `cargo-proxy`), `db`; external network `cargo-proxy`
- `deploy/README.md`: DNS prerequisites, both ACME modes, env reference (`.env` consumed by compose)
- Verify: `docker compose -f deploy/docker-compose.yml config` renders with a sample `.env`
- Commit `feat: add production compose stack with traefik and acme`

### Task 5: Frontend Domains tab + verification + roadmap
- `web/src/components/DomainsTab.tsx` in AppDetail tabs: auto subdomain shown (from `/instance/info` suffix), custom domain list with status chips (`active`=green, `pending`=amber, `misconfigured`=red), add form + remove (admin-capable users see controls; server enforces), hint "applies on next deploy"
- Vitest: renders auto domain + custom rows with status colors
- Full verification; mark PhasedPlans Phase 4 ✅ (note: zero-downtime cert rotation etc. unchanged)
- Commit `feat(web): add domains tab` + `docs: mark phase 4 domains and ssl complete`

## Self-review
4.1 → Task 4 (traefik only host-port container, controlplane dogfooded); 4.2 → Task 4 (two modes, acme.json persisted via volume); 4.3 → Tasks 1/2/5; 4.4 → Task 3. Deviation: domain changes apply on next deploy (FR-5.2 allows "next reconcile"); a reconcile-without-build button can come later.
