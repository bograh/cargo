# Cargo M7 — Instance Settings & Operations Plan (PhasedPlans Phase 6)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans.

**Goal:** Instance admin can edit the apps-domain suffix and SMTP settings from the API/UI (FR-7.1); expired sessions and dead invites are purged automatically (design §10 housekeeping).

**Architecture:** Reuse existing seams. `apps_domain_suffix` stays a plain JSON string in `instance_settings` (already read by `/instance/info` and the deploy pipeline per-run, so a change applies to the next deploy with no restart). SMTP config is stored encrypted with the same `{"enc": base64}` wrapper the GitHub App creds use, generalized into a tiny `settings` helper. Housekeeping is one more River periodic job.

## Global Constraints
- SMTP password never returned by any GET; suffix validated as a bare DNS name (reuse hostname regex rules)
- All settings endpoints instance-admin-only (`requireInstanceAdmin`)
- Purge rules: sessions with `refresh_expires_at < now()` OR `revoked_at < now() - 24h` deleted; invites with `expires_at < now() - 7d` OR `revoked_at < now() - 7d` deleted
- Same verification bar as prior milestones

## Tasks

### Task 1: Settings service + admin API
- `internal/settings/settings.go`: `Service{q, box}` with `Suffix Get/Set` (plain JSON string, hostname-validated) and `SMTP Get/Set` — `SMTPConfig{Host string; Port int; Username, Password, From string}` sealed like `github.AppConfig` under key `smtp`
- `GET /api/v1/admin/settings` → `{apps_domain_suffix, smtp: {configured, host, port, from}}`
- `PUT /api/v1/admin/settings/apps-domain-suffix` body `{suffix}`; `PUT /api/v1/admin/settings/smtp` body full config; `DELETE /api/v1/admin/settings/smtp` clears it
- Tests: suffix validation (reject `https://…`, spaces), SMTP round trip encrypted, GET omits password (stub-level handler tests + one integration)
- Commit `feat: add instance settings service and admin endpoints`

### Task 2: Housekeeping purge job
- Queries: `PurgeSessions :execrows`, `PurgeInvites :execrows` implementing the rules above; `internal/jobs/housekeeping.go` `HousekeepingArgs` kind `housekeeping`, daily periodic, exported `RunHousekeeping(ctx, pool) (sessions, invites int64, err)`
- Test: seed expired + valid rows, run, assert only stale rows gone
- Commit `feat: purge expired sessions and invites daily`

### Task 3: Admin UI + verification + roadmap
- Admin page gains "Instance settings" card: suffix input (with "applies to new deployments" hint) + SMTP form (password write-only) + clear button
- Vitest: settings card renders current suffix, saving posts body (mock)
- Full verification; mark PhasedPlans Phase 6 ✅ (SMTP is stored/managed; actually *sending* mail is the Notifications feature, explicitly out of v1 scope)
- Commit `feat(web): add instance settings ui` + roadmap update

## Self-review
6.1 → Tasks 1/3 (suffix change affects only new deploys — matches acceptance); 6.2 → Task 2 (image/log prune already shipped in M3). SMTP is config-only in v1 (PRD lists email notifications as out of scope; invites are copy-link).
