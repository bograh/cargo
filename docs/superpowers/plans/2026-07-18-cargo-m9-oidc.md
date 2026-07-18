# Cargo M9 — OIDC Login Implementation Plan (PhasedPlans 8.2)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans.

**Goal:** Generic OIDC login beside password auth (spec: `docs/superpowers/specs/2026-07-18-cargo-oidc-design.md`): admin-configurable issuer/client, authorization-code flow with state+nonce, verified-email account linking, SSO user provisioning — ending in the same session cookies as password login.

**Architecture:** New `internal/oidc` package wraps `go-oidc` (discovery/JWKS) + `x/oauth2` (code exchange) behind a small service that also owns the settings lifecycle (stored encrypted via `internal/settings`, same `enc` wrapper as SMTP). User resolution keys on a new `auth_identities(issuer, subject)` table; `users.password_hash` becomes nullable for SSO-only accounts. The API layer owns the sealed `cargo_oidc_state` cookie (AES-GCM via `crypto.Box`, no server-side state) and issues sessions through a newly-public `auth.Service.IssueSession`. Frontend: "Sign in with SSO" button driven by a public `/auth/providers` endpoint; OIDC card in admin settings.

**Tech Stack:** `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2` (new deps; `golang-jwt/jwt/v5` already indirect, used by tests); everything else existing patterns.

## Global Constraints

- Client secret stored ONLY encrypted (`crypto.Box`) in `instance_settings` key `oidc` as `{"enc":"<base64>"}`; no GET ever returns it
- Callback requires `email` claim AND `email_verified=true`; anything else → generic `302 /login?error=oidc`
- State+nonce: 128-bit `crypto/rand`, sealed in the cookie with a 10-min expiry, single-use (cookie cleared on callback); state compared with `subtle.ConstantTimeCompare`
- Cookie `cargo_oidc_state`: `HttpOnly`, `Secure` when `cfg.Env == "production"`, `SameSite=Lax`, `Path=/api/v1/auth/oidc`
- id_tokens/codes/secrets never logged; no IdP tokens persisted
- `start`/`callback` behind the existing `authRateLimiter()`
- Password auth behavior unchanged; NULL `password_hash` → same constant-time `ErrInvalidCredentials` as a wrong password
- Verification bar: full Go suite, golangci-lint 0 issues, vitest, web build

## Tasks

### Task 1: Schema — nullable `password_hash` + `auth_identities`
- `internal/db/migrations/00007_oidc.sql`: `ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;` then `CREATE TABLE auth_identities (id UUID PK DEFAULT gen_random_uuid(), user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, issuer TEXT NOT NULL, subject TEXT NOT NULL, email TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (issuer, subject))`
- `internal/db/queries/oidc.sql`: `GetIdentityByIssuerSubject :one` (by issuer+subject), `CreateIdentity :one` (user_id, issuer, subject, email) `RETURNING *`
- `sqlc generate` → `User.PasswordHash` and `CreateUserParams.PasswordHash` become `pgtype.Text`
- Fix compile fallout in `internal/auth`: `Register` passes `pgtype.Text{String: hash, Valid: true}`; `passwordProvider.Authenticate` — `if !u.PasswordHash.Valid { burn argon2 verify; return ErrInvalidCredentials }`, else verify `u.PasswordHash.String`
- Test (auth suite, testcontainers): `CreateUser` with `pgtype.Text{}` (NULL) succeeds; `Login` for that account → `ErrInvalidCredentials`; identity round-trip via the new queries
- Commit `feat: add oidc identities schema and nullable password hash`

### Task 2: `internal/settings` — encrypted OIDC config storage
- `internal/settings/oidc.go`: `OIDCConfig{IssuerURL, ClientID, ClientSecret string}`; `keyOIDC = "oidc"`
  - `OIDC(ctx) (*OIDCConfig, error)` — nil when unset (same `enc` decode as `SMTP()`)
  - `SetOIDC(ctx, cfg)` — field validation only (all three non-empty; `IssuerURL` parses as https URL, or http for loopback — dev Keycloak); seal + upsert
  - `ClearOIDC(ctx)` — writes `enc:""`
- Tests mirror the SMTP tests: round-trip; raw `instance_settings` row contains neither secret nor client ID; clear → `OIDC()` returns nil
- Commit `feat: store oidc config encrypted in instance settings`

### Task 3: `internal/oidc` — verifier, flow helpers, user resolution
- `go get github.com/coreos/go-oidc/v3 golang.org/x/oauth2`
- `internal/oidc/oidc.go`:
  - `var ErrNotConfigured, ErrValidation, ErrExchange = errors.New(...)` (sentinels)
  - `type Service struct { q *sqlc.Queries; pool *pgxpool.Pool; settings *settings.Service; httpClient *http.Client }`; `NewService(pool, settingsSvc) *Service`
  - `Configured(ctx) (bool, error)`; `PublicConfig(ctx) (issuerURL, clientID string, configured bool, err error)`
  - `SetConfig(ctx, cfg settings.OIDCConfig) error` — 5 s timeout, `oidc.NewProvider` discovery fetch (fail → `ErrValidation` with cause), then `settings.SetOIDC`
  - `ClearConfig(ctx) error`
  - `StartURL(ctx, redirectURL, state, nonce string) (string, error)` — `oauth2.Config{...}.AuthCodeURL(state, oidc.Nonce(nonce))`, scopes `openid email`; `ErrNotConfigured` when unset
  - `ResolveCallback(ctx, redirectURL, code, wantNonce string) (sqlc.User, error)` — `Exchange` → `id_token` extra → `verifier.Verify` → claims `{Email string; EmailVerified bool; Nonce string}`; nonce/email checks; then resolve (below)
  - resolve (tx via `pool.Begin` + `q.WithTx`): identity hit → user; else email match → `CreateIdentity` link; else `CreateUser` (NULL hash — the `NOT EXISTS` first-user rule makes the first SSO user instance admin) + `CreateIdentity`; commit. `23505` on CreateIdentity → re-`GetIdentityByIssuerSubject` and load that user (concurrent-callback race)
- `internal/oidc/mockidp_test.go`: httptest mock IdP — per-test RSA key, `/.well-known/openid-configuration`, `/jwks`, `/token` returning a signed RS256 id_token with controllable `sub/email/email_verified/nonce`
- Tests (testcontainers, settings service backed by a real `crypto.Box` with a test master key): resolve-by-identity; link-by-verified-email (password login still works after); provision first user → `is_instance_admin`, second → not; `email_verified=false` → error; wrong nonce → error; `SetConfig` with undiscoverable issuer → `ErrValidation`; `StartURL` contains `state`, `nonce`, `redirect_uri`; unconfigured → `ErrNotConfigured`
- Commit `feat: add oidc service with mock-idp tests`

### Task 4: API — providers endpoint, start/callback, admin settings
- `internal/auth/service.go`: `IssueSession(ctx, userID pgtype.UUID) (Tokens, error)` — exported thin wrapper over `newSession` (the exact path `Login` uses)
- `internal/api/server.go`: `Server` gains `box *crypto.Box` (set in `NewServer`) and `oidc OIDCService`; interface:
  ```go
  // OIDCService is satisfied by *oidc.Service.
  type OIDCService interface {
      Configured(ctx context.Context) (bool, error)
      PublicConfig(ctx context.Context) (issuerURL, clientID string, configured bool, err error)
      SetConfig(ctx context.Context, cfg settings.OIDCConfig) error
      ClearConfig(ctx context.Context) error
      StartURL(ctx context.Context, redirectURL, state, nonce string) (string, error)
      ResolveCallback(ctx context.Context, redirectURL, code, wantNonce string) (sqlc.User, error)
  }
  ```
  `AuthService` interface gains `IssueSession(ctx, pgtype.UUID) (auth.Tokens, error)`; `NewServer` wires `oidc.NewService(pool, settings.NewService(pool, box))`
- `internal/api/oidc.go`:
  - `handleAuthProviders` — `GET /api/v1/auth/providers` → `{"password": true, "oidc": configured}`
  - `handleOIDCStart` — unconfigured → `409 oidc_not_configured`; else state+nonce (16 `crypto/rand` bytes, base64-rawurl), seal `{"state","nonce","exp"}` via `s.box` into the cookie (Global Constraints), 302 to `StartURL`
  - `handleOIDCCallback` — unseal cookie (any failure → `redirectOIDCErr`), constant-time state check, clear cookie, `ResolveCallback` → `s.auth.IssueSession(user.ID)` → `setAuthCookies` → `302 /`; failure → `302 /login?error=oidc`
  - `oidcRedirectURL(r)` helper: `X-Forwarded-Proto` (default `https` when `r.TLS != nil`, else `http`) + `://` + `r.Host` + `/api/v1/auth/oidc/callback`
  - Admin: `handleGetOIDC` → `{configured, issuer_url, client_id}` (never secret); `handlePutOIDC` (body `{issuer_url, client_id, client_secret}`) → `SetConfig`; `handleDeleteOIDC` → `ClearConfig`; errors via the existing `settingsError`-style mapping (`oidc.ErrValidation` → 400, `ErrNotConfigured` → 409)
- `internal/api/router.go`: inside `r.Route("/auth")` public limiter group — `r.Get("/providers", …)`, `r.Get("/oidc/start", …)`, `r.Get("/oidc/callback", …)`; admin group — `r.Get("/settings/oidc", …)`, `r.Put("/settings/oidc", …)`, `r.Delete("/settings/oidc", …)`
- Handler tests (stub `OIDCService` + stub auth with `IssueSession`): providers true/false; start → 302 + cookie flags (Lax, HttpOnly, path) + URL passed through; start unconfigured → 409; callback with missing/tampered cookie → `302 /login?error=oidc`; callback happy path → `cargo_access`/`cargo_refresh` set + `302 /`; admin GET omits secret; PUT/DELETE round-trip on the stub
- Commit `feat: add oidc login endpoints and admin settings api`

### Task 5: Frontend — SSO button + admin OIDC card
- `web/src/pages/Login.tsx`: `useQuery` `api<{password: boolean; oidc: boolean}>("/auth/providers")`; when `oidc`, render below the form an `<a href="/api/v1/auth/oidc/start">` styled like the secondary Button: "Sign in with SSO"; `useSearchParams` — `error=oidc` → banner "SSO sign-in failed. Try again or use your password."
- `web/src/pages/Admin.tsx`: OIDC card (model on `GithubAppForm`): GET `/admin/settings/oidc` → configured badge with issuer; form `issuer_url`, `client_id`, `client_secret` (password input, write-only placeholder); save → PUT, clear secret field, invalidate; Clear button → DELETE; read-only hint showing the callback URL: `` `${window.location.origin}/api/v1/auth/oidc/callback` ``
- Tests: `Login.test.tsx` — providers `{oidc:true}` renders the SSO link with the right href; `?error=oidc` shows the banner; `{oidc:false}` renders no link. `Admin.test.tsx` — OIDC card save calls PUT with the three fields and never displays the secret afterwards; clear calls DELETE
- Commit `feat(web): add sso login button and oidc admin card`

### Task 6: Docs + close 8.2
- `PRD.md`: drop the "OIDC/SSO login — Phase 2" out-of-scope line; roadmap Phase 2 cell unchanged (historical record)
- `PhasedPlans.md`: 8.2 → ✅ with plan reference `(complete)`; Phase 8 header note "8.2 shipped 2026-07-18"
- `internal/api/instance.go` `version` → `1.1.0`
- Full verification: `go test ./...`, `golangci-lint run ./...` (0 issues), `npx vitest run` (all), `npm run build`
- Commit `docs: mark oidc complete`

## Self-review
- Spec §2 flow → Tasks 3–4 (cookie, redirects, limiter) · §3 API table → Task 4 (all six routes) · §4 data model → Tasks 1, 3 (nullable hash, identities, first-user rule) · §5 packages → Tasks 2–4 (settings storage, oidc service, api seams) · §6 frontend → Task 5 · §7 security → Global Constraints + Task 4 · §8 error table → Tasks 3–4 (409, error redirect, PUT validation) · §9 testing → per-task tests incl. mock IdP
- No placeholders; signatures consistent across tasks (`PublicConfig`, `StartURL`, `ResolveCallback`, `IssueSession` used identically in Tasks 3/4)
