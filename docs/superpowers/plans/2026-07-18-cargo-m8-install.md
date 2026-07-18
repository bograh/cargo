# Cargo M8 — Installation & Packaging Plan (PhasedPlans Phase 7, closes v1)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans.

**Goal:** FR-8: one-command install (`deploy/install.sh`) with dependency checks, prompts, secret generation into a mode-0600 `.env`, and `docker compose up -d`; root README with prerequisites/upgrade docs; on-demand E2E smoke script proving install → register → deploy → live.

## Tasks

### Task 1: `deploy/install.sh`
- Checks: docker present, `docker compose` plugin, daemon reachable; refuses to overwrite an existing `.env` unless `--force`
- Prompts (or env-var overrides for non-interactive use: `CARGO_PLATFORM_DOMAIN`, `CARGO_APPS_SUFFIX`, `CARGO_ACME_EMAIL`, optional `CARGO_DNS_PROVIDER` → dns01 mode, optional SMTP note pointing at the admin UI)
- Generates `CARGO_MASTER_KEY` (32 random bytes hex) + `CARGO_DB_PASSWORD` via openssl/urandom; writes `.env` `chmod 600`; prints a loud "BACK UP THE MASTER KEY" warning (design §10)
- Runs `docker compose up -d` (adding `-f docker-compose.dns01.yml` when a DNS provider was given); `--no-up` flag skips the launch (used by tests/smoke)
- Verify: run non-interactively with `--no-up` in a temp dir → `.env` exists, mode 600, all keys present, key is 64 hex chars
- Commit `feat: add one-command install script`

### Task 2: Root `README.md` + version bump
- README: what Cargo is, architecture at a glance (3 containers), prerequisites (FR-8.2), quick install, upgrade (`docker compose pull && up -d`), local development, links to PRD/PhasedPlans/deploy docs; note master-key backup caveat
- Bump `internal/api/instance.go` `version` to `1.0.0`
- Commit `docs: add readme and bump version to 1.0.0`

### Task 3: E2E smoke script + run it
- `scripts/smoke.sh`: builds the image, starts the dev compose stack (random project name + host port), waits for `/healthz`, then via curl: register admin → create org → create nginx image app → deploy → poll deployment until `live` (≤120 s) → assert container `cargo-app-*` running → teardown (compose down + app teardown). Exits non-zero on any failure
- Run it locally end-to-end; fix whatever it uncovers
- Commit `feat: add e2e smoke script`

### Task 4: Close v1
- PhasedPlans: Phase 7 ✅, header note "v1 complete 2026-07-18"
- Full verification suite one last time
- Commit `docs: mark v1 complete`

## Self-review
FR-8.1 → Task 1 (script prompts+secrets+compose up); FR-8.2 → Task 2 README; NFR-1 demonstrated by smoke (Task 3); success-metric spot checks noted in PhasedPlans.
