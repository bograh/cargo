# Cargo — OIDC Login Design (v2 · Phase 8.2 · milestone m9)

Date: 2026-07-18 · Status: Approved design, pre-implementation
Parent docs: [PRD](../../../PRD.md) · [v1 design spec](2026-07-17-cargo-design.md)

Adds generic OIDC login alongside password auth (PRD Phase 2, "OIDC provider implements the existing AuthProvider interface"). First of six v2 features; execution order agreed: **8.2 OIDC → 8.1 managed databases → 8.3 compose app source → 9.2 blue/green → 9.1 multi-server → 9.3 metrics.**

## 1. Goals / non-goals

**Goals**
- Instance admin configures any standards-compliant OIDC provider (Keycloak, Authentik, Okta, Google, …) from the admin UI — issuer URL, client ID, client secret.
- Users see a "Sign in with SSO" button when configured; authorization-code flow ends in the same session cookies as password login.
- SSO identities link to existing accounts by verified email; new SSO users are provisioned automatically.
- FR-1.5 fulfilled: no changes to password auth, session, or token code paths.

**Non-goals (deferred)**
- IdP-group → org/role mapping (identity only; membership stays invite-governed).
- SSO-only enforcement toggle (password login stays always on — no lockout risk).
- Multiple simultaneous IdPs (data model permits it; config/UI is single-IdP).
- OIDC logout (RP-initiated logout); Cargo sessions behave exactly as today.

## 2. Flow

```
browser                controlplane                     IdP
  │  GET /auth/oidc/start │                               │
  │──────────────────────►│ state,nonce (128-bit rand)    │
  │  302 authorize URL    │ seal {state,nonce,exp} ──┐    │
  │◄──────────────────────│ into cargo_oidc_state    │    │
  │                        cookie (Lax, HttpOnly) ◄──┘    │
  │────────────────────── authorize ─────────────────────►│
  │◄───────────────────── 302 callback?code&state ────────│
  │  GET /auth/oidc/callback                              │
  │──────────────────────►│ unseal cookie, state match    │
  │                       │ POST code ───────────────────►│
  │                       │◄── id_token                   │
  │                       │ verify sig/iss/aud/exp/nonce; │
  │                       │ require email_verified=true   │
  │                       │ resolve user (§4)             │
  │  302 / + session cookies (same as password login)     │
  │◄──────────────────────│                               │
```

- `start` and `callback` are public endpoints behind the existing auth rate limiter. Unconfigured → `409 oidc_not_configured`.
- State cookie: `cargo_oidc_state`, value = base64(`crypto.Box.Seal({state, nonce, exp}`)) — tamper-proof without server state; 10-minute expiry enforced from the payload; `HttpOnly`, `Secure` in production, `SameSite=Lax` (required: the IdP redirect back is a cross-site top-level GET). Cookie is cleared on callback, success or failure.
- Callback failure (bad state, exchange failure, unverified email, …) → `302 /login?error=oidc` with detail only in server logs. No claim data reaches the browser.
- No IdP tokens are persisted; the id_token is verified and discarded.

## 3. API surface

| Method & path | Auth | Behavior |
|---|---|---|
| `GET /api/v1/auth/providers` | public | `{"password": true, "oidc": <configured>}` — drives the login page |
| `GET /api/v1/auth/oidc/start` | public, rate-limited | 302 to IdP authorize URL; sets state cookie; 409 when unconfigured |
| `GET /api/v1/auth/oidc/callback` | public, rate-limited | completes the flow; 302 `/` on success, 302 `/login?error=oidc` on failure |
| `GET /api/v1/admin/settings/oidc` | instance admin | `{configured, issuer_url, client_id}` — **never the secret** |
| `PUT /api/v1/admin/settings/oidc` | instance admin | validates via discovery fetch (5 s timeout), then saves encrypted |
| `DELETE /api/v1/admin/settings/oidc` | instance admin | clears config; identities and users remain; SSO-only users lose login until reconfigured |

## 4. User resolution & data model

Migration `00007_oidc.sql`:

```sql
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

CREATE TABLE auth_identities (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issuer     TEXT NOT NULL,
    subject    TEXT NOT NULL,
    email      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issuer, subject)
);
```

Resolution on callback (single transaction where writes occur):

1. `auth_identities` row for `(issuer, sub)` exists → load and return its user.
2. Else a `users` row matches the id_token `email` → insert identity (link), return user. Existing password keeps working; both login methods now serve the same account.
3. Else create user (`email`, NULL `password_hash`, `is_instance_admin = (no users yet)` — the v1 first-user rule, same atomic SQL) plus identity row.

`password_hash` NULL handling: `passwordProvider.Authenticate` treats a NULL/empty hash exactly like a wrong password — same argon2id time-burn, same `ErrInvalidCredentials`. Registration stays password-only; SSO users are created only through the flow.

## 5. Packages & seams

- **`internal/oidc`** (new): verifier wrapper around `coreos/go-oidc/v3` + `golang.org/x/oauth2` (both new deps; `golang-jwt/v5` already indirect). Issuer URL, client ID, and an `http.Client` are injectable for tests. Owns claim extraction and the §4 resolution logic against sqlc queries.
- **`internal/settings`**: new `oidc` key — `OIDCConfig{IssuerURL, ClientID, ClientSecret}` with the same AES-GCM `enc` wrapper as SMTP; `SetOIDC` validates by fetching `/.well-known/openid-configuration` before persisting (fail fast on typos); `ClearOIDC` writes `enc:""`.
- **`internal/api`**: thin handlers; cookie read/write; redirects. Session issuance on success calls the exact code path password login uses.
- **`internal/auth`**: unchanged except NULL-hash handling in `passwordProvider` (§4). The `Provider` interface stays as-is — OIDC is a parallel HTTP flow, which is the seam FR-1.5 actually requires (a redirect flow cannot fit `Authenticate(email, password)`).

## 6. Frontend

- Login page: fetches `GET /auth/providers`; when `oidc` is true, renders **Sign in with SSO** as a plain link (full-page navigation) to `/api/v1/auth/oidc/start`. `?error=oidc` shows a generic "SSO sign-in failed" banner.
- Admin settings: new OIDC card beside GitHub App / SMTP — issuer URL, client ID, write-only client secret, configured indicator, read-only copyable callback URL (`https://<platform-domain>/api/v1/auth/oidc/callback`, derived from the current origin), and a disable (DELETE) button.
- Callback target is `/`; the SPA boots, `/auth/me` succeeds, routing proceeds normally.

## 7. Security

- `state` and `nonce` are 128-bit random values from `crypto/rand`; state compared in constant time; both single-use via cookie clearing + 10-min expiry.
- id_token verified against discovered JWKS with issuer, audience, and expiry checks (go-oidc defaults) plus explicit nonce comparison.
- `email` claim required; `email_verified` must be `true` — anything else is rejected (account-takeover vector otherwise).
- Client secret stored AES-GCM-encrypted, write-only in every API response, never logged; authorization codes and id_tokens are never logged.
- Rate limiting: `start`/`callback` join the existing per-IP auth limiter (10/min, burst 10).

## 8. Error handling

| Failure | Result |
|---|---|
| OIDC not configured (start) | `409 oidc_not_configured` |
| Missing/expired/tampered state cookie, state mismatch | 302 `/login?error=oidc` |
| Code exchange failure, id_token verification failure, nonce mismatch | 302 `/login?error=oidc` |
| Missing email or `email_verified=false` | 302 `/login?error=oidc` |
| Discovery fetch failure on PUT | `400 validation_failed` with cause |
| IdP outage at login time | 302 `/login?error=oidc`; password login unaffected |

## 9. Testing

- **Mock IdP** (httptest): serves discovery, JWKS (RSA key generated per test), and a token endpoint issuing signed id_tokens with controllable `sub`, `email`, `email_verified`, `nonce`.
- **Service tests** (testcontainers, existing pattern): resolve-by-identity; link-by-verified-email; provision-new (first user → instance admin, second → not); unverified email rejected; NULL-password account cannot password-login; password login for linked accounts still works.
- **Handler tests**: start redirect shape + cookie flags/expiry; callback with bad/expired state → error redirect; full happy path against the mock IdP → session cookies set, redirect `/`; unconfigured → 409; admin GET never serializes the secret (mirrors SMTP tests).
- **Frontend tests**: SSO button visibility from `/auth/providers`; `?error=oidc` banner; admin OIDC card save/clear flow.
- Existing suites stay green (migration touches `users`).

## 10. Acceptance criteria (PhasedPlans 8.2)

- Admin configures OIDC (issuer, client ID, secret) from the UI; secret stored encrypted, never returned by any GET.
- "Sign in with SSO" appears only when configured; full code-flow login yields a working session.
- Verified-email linking attaches SSO to an existing account; new SSO users provision automatically; first-ever user via SSO is instance admin.
- Unverified-email and tampered-state logins are rejected with a generic UI error.
- Password login is unchanged and always available.
