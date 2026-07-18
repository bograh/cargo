# Cargo M5 — GitHub Integration Implementation Plan (PhasedPlans Phase 3)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans.

**Goal:** GitHub App connection per org, private-repo deploys via installation tokens, HMAC-validated push-to-deploy webhooks, and GitHub App credentials managed (encrypted) from the instance admin API/UI.

**Architecture:** New `internal/github` package: encrypted credential store on top of `instance_settings` (+ crypto.Box), an App-JWT/installation-token client with configurable base URL (httptest-able), repo/branch listing. `github_installations` maps org → installation. The webhook handler matches push events to apps by repo URL + branch and enqueues `webhook`-triggered deployments. The pipeline clones private GitHub repos by injecting `x-access-token:<installation-token>` into the clone URL when the app's org has an installation.

**Tech Stack:** `github.com/golang-jwt/jwt/v5` (RS256 App JWT); everything else stdlib.

## Global Constraints
- GitHub App creds (`app_id`, `app_slug`, `private_key` PEM, `webhook_secret`) stored ONLY encrypted (crypto.Box) inside `instance_settings` key `github_app` as `{"enc":"<base64>"}`; GET endpoints never return the key or secret
- Webhook: `X-Hub-Signature-256` HMAC verified with `hmac.Equal` before any parsing; bad/missing signature → 401, no side effects
- Only `push` events to an app's tracked branch with `auto_deploy=true` create deployments; others → 200 `{"status":"ignored"}`
- Client base URLs (`https://api.github.com`, `https://github.com`) are struct fields, overridable in tests
- Installation callback requires an authenticated org admin+ (state = orgID); never trust `state` alone
- Same verification bar as prior milestones: full Go suite, golangci-lint 0 issues, vitest, web build

## Tasks

### Task 1: Schema + queries (`github_installations`, webhook app lookup)
- `internal/db/migrations/00005_github.sql`: `github_installations(org_id UUID PK REFERENCES organizations ON DELETE CASCADE, installation_id BIGINT NOT NULL, account_login TEXT NOT NULL DEFAULT '', created_at)`
- `internal/db/queries/github.sql`: `UpsertGithubInstallation`, `GetGithubInstallation` (by org), `DeleteGithubInstallation`
- `internal/db/queries/apps.sql` add: `ListGitAppsByBranch :many` — `SELECT * FROM applications WHERE source_type = 'git' AND git_branch = $1`
- DB test asserts table exists; sqlc regen; commit `feat: add github installations schema`

### Task 2: `internal/github` — encrypted settings + App client
- `settings.go`: `AppConfig{AppID int64; AppSlug, PrivateKey, WebhookSecret string}`; `SaveAppConfig(ctx, q, box, cfg)`, `LoadAppConfig(ctx, q, box) (*AppConfig, error)` (nil when unset)
- `client.go`: `Client{Config AppConfig; APIBase, HTTPBase string; HTTP *http.Client}`
  - `appJWT()` RS256, 9-min expiry, iss=AppID
  - `InstallationToken(ctx, installationID int64) (string, error)` — POST `/app/installations/{id}/access_tokens`
  - `ListRepos(ctx, installationID) ([]Repo{FullName, CloneURL, DefaultBranch})` — GET `/installation/repositories` with installation token (paginate `per_page=100`, follow `page` until short page)
  - `ListBranches(ctx, installationID, fullName) ([]string)` — GET `/repos/{fullName}/branches`
  - `InstallURL(state string) string` — `{HTTPBase}/apps/{slug}/installations/new?state={state}`
- Tests: settings round-trip encrypted (testcontainers); client tests against `httptest.Server` (JWT sent as Bearer, token exchanged, repos parsed)
- Commit `feat: add github app client with encrypted credentials`

### Task 3: Admin settings API + install flow + repo/branch endpoints
- `PUT /api/v1/admin/settings/github-app` (instance admin) body `{app_id, app_slug, private_key, webhook_secret}` → SaveAppConfig; `GET` → `{configured, app_slug, app_id}` only
- `GET /api/v1/orgs/{orgID}/github` (member+) → `{configured, connected, account_login, install_url}` (install_url only for admin+ and configured)
- `GET /api/v1/github/setup?installation_id&state` (authed; actor must be admin+ of org `state`) → upsert installation (account_login fetched best-effort from GitHub), redirect 302 to `/orgs/{state}/settings`
- `GET /api/v1/orgs/{orgID}/github/repos`, `GET /api/v1/orgs/{orgID}/github/repos/{owner}/{repo}/branches` (member+) → proxy via client
- Server gains `github GitHubService` interface (stubbed in tests) built from pool+box in NewServer
- Handler tests with stubs; commit `feat: add github admin settings and installation endpoints`

### Task 4: Webhook receiver + auto-deploy
- `POST /api/v1/webhooks/github` (no auth; HMAC): verify sig over raw body; event `push` (header `X-GitHub-Event`): extract `ref` → branch, `repository.clone_url` + `html_url`; `ListGitAppsByBranch(branch)`; match apps whose `git_repo_url` normalizes to the same repo (strip scheme, host stays, strip `.git`, lowercase); for each match with `auto_deploy`: create deployment (trigger `webhook`, actor NULL) + enqueue
- deployments service gains `CreateSystem(ctx, appID, trigger)` (no actor — webhook has none)
- Tests: bad HMAC → 401 & no deployment; matching push → deployment created + enqueued; auto_deploy=false → ignored; other branch → ignored
- Commit `feat: add hmac-validated github webhook with push-to-deploy`

### Task 5: Private-repo clones in the pipeline
- `Pipeline` gains `CloneAuth func(ctx, orgID pgtype.UUID, repoURL string) (string, error)` returning a possibly-credentialed URL; wired in main: if GitHub configured AND org has installation AND URL host is github.com → `https://x-access-token:<token>@github.com/owner/repo.git`; else URL unchanged. Token never written to logs (log the original URL)
- Test: pipeline uses rewritten URL (fake CloneAuth), log contains original URL not the token
- Commit `feat: clone private github repos with installation tokens`

### Task 6: Frontend — admin GitHub form, org connect card, wizard repo picker
- Admin page: GitHub App section (app id/slug/private key/webhook secret form → PUT; shows "configured" state)
- Org settings: "GitHub" card — connected account or "Install GitHub App" link (admin+)
- NewApp wizard: when org connected, repo `<Select>` (from repos endpoint) + branch `<Select>`; manual URL entry still available ("or enter a public URL")
- Vitest: wizard shows repo select when connected (mocked endpoints)
- Commit `feat(web): add github connection ui and repo picker`

### Task 7: Verification + roadmap
- Full: `go build ./... && go vet ./... && go test ./... && golangci-lint run`; `npx vitest run && npm run build`; mark PhasedPlans Phase 3 ✅
- Commit `docs: mark phase 3 github integration complete`

## Self-review
3.1 → Tasks 2/3/6; 3.2 → Task 5; 3.3 → Task 4; 3.4 → Task 3 (+6). Deviation: apps reference the installation via their org (no per-app installation column) — one installation per org, revisit if multi-installation orgs appear.
