# Cargo M4 — Frontend Implementation Plan (PhasedPlans Phase 5)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The full SPA over the Phase 0–2 APIs: auth pages with silent refresh, org dashboard + switcher, members/invites management, New App wizard (git URL / image sources), app detail with live SSE log viewer, env editor (write-only values), rollback, and the instance admin area.

**Architecture:** React 19 + React Router 7 + TanStack Query 5 + Tailwind v4, hand-rolled UI primitives (`src/components/ui.tsx`) instead of shadcn/ui — an internal-tool simplification; shadcn can replace primitives later without page rewrites. One `api()` fetch wrapper owns the error envelope and 401→refresh→retry. Served embedded by the Go binary (`internal/webui`); dev via Vite proxy to :8080.

**Tech Stack:** react-router-dom@7, @tanstack/react-query@5, Vitest + Testing Library, EventSource for SSE.

## Global Constraints

- All requests same-origin with cookies (`credentials: "same-origin"`); no tokens in JS
- API errors are the envelope `{error:{code,message,fields?}}` — surface `message` in the UI
- On 401 (except auth endpoints): call `POST /api/v1/auth/refresh` once, retry original request; second 401 → redirect to /login
- Env var values are write-only: the editor never displays saved values (FR-6.2)
- Viewers see read-only UI (mutation controls hidden), but enforcement stays server-side
- Deployment status colors: queued/building/deploying = amber, live = green, failed = red, cancelled = gray
- GitHub picker and Domains tab are **out of scope** (Phases 3/4); wizard takes a public git URL or image ref
- Verify with: `npx vitest run`, `npm run build`, `npm run lint` (oxlint), Go embed still passing `go test ./internal/webui/`

## Route map

| Path | Page | Access |
|---|---|---|
| `/login`, `/register` | auth forms | public |
| `/` | redirect → first org or /orgs/new | authed |
| `/orgs/new` | create org | authed |
| `/orgs/:orgId` | org dashboard (app cards) | member |
| `/orgs/:orgId/settings` | members + invites | member (mutations admin+) |
| `/orgs/:orgId/apps/new` | New App wizard | member+ |
| `/apps/:appId` | app detail tabs (Overview, Deployments, Environment, Settings) | member |
| `/deployments/:deploymentId` | deployment log viewer | member |
| `/invite/:token` | accept invite | authed |
| `/admin` | instance admin (users, orgs) | instance admin |

---

### Task 1: API client + auth context + router shell
**Files:** `web/src/lib/api.ts`, `web/src/lib/types.ts`, `web/src/auth.tsx`, `web/src/components/ui.tsx`, `web/src/App.tsx` (rewrite), `web/src/main.tsx` (add providers), tests `web/src/lib/api.test.ts`.
- `api<T>(path, opts)` — JSON in/out, envelope errors thrown as `ApiError{code,message,fields}`; 401 → one refresh+retry
- `useAuth()` — `{user, loading, login, register, logout}`; `/auth/me` on mount; `<RequireAuth>` wrapper redirects to /login
- ui.tsx: `Button, Input, Label, Card, Badge, Spinner, PageTitle, FieldError, EmptyState`
- Router with all routes stubbed; authed layout with topbar (Cargo wordmark, org switcher placeholder, user email, logout)
- Tests: api() parses envelope error; 401 triggers refresh then retry (mock fetch); second 401 rejects
- Steps: write api.test.ts → fail → implement → pass → `npm run build` → commit `feat(web): add api client, auth context, and router shell`

### Task 2: Login & register pages
**Files:** `web/src/pages/Login.tsx`, `web/src/pages/Register.tsx`, test `web/src/pages/Login.test.tsx`.
- Forms with email/password, submit via useAuth; API error message shown; link between the two; register redirects to `/` (first-run → org create)
- Tests: renders form; shows API error on failed login (mock fetch); successful login navigates
- Commit `feat(web): add login and register pages`

### Task 3: Orgs — dashboard, create, switcher, settings (members + invites), accept invite
**Files:** `web/src/pages/Orgs.tsx` (redirect logic + create form), `web/src/pages/OrgDashboard.tsx`, `web/src/pages/OrgSettings.tsx`, `web/src/pages/AcceptInvite.tsx`, `web/src/components/OrgSwitcher.tsx`, test `web/src/pages/OrgSettings.test.tsx`.
- Queries: `GET /orgs` (list+role), `GET /orgs/:id` , `GET /orgs/:id/members`, invites CRUD, `POST /invites/accept`
- Dashboard: app cards (name, slug, source, updated) + "New App" (member+)
- Settings: members table with role select (admin+) and remove; invite creation (role picker) → shows copyable link `${origin}/invite/${token}` once; invite list with revoke
- Accept invite page: posts token, redirects to org
- Test: members render; role select hidden for viewer role
- Commit `feat(web): add org dashboard, settings, and invite flows`

### Task 4: New App wizard
**Files:** `web/src/pages/NewApp.tsx`, test `web/src/pages/NewApp.test.tsx`.
- Source toggle git|image → fields (git URL+branch | image ref); builder select (auto/dockerfile/nixpacks) for git; port, healthcheck path, auto-deploy toggle; env var rows (key/value, added on create); Create & Deploy button → `POST /orgs/:id/apps`, then `PUT /apps/:id/env` (if vars), then `POST /apps/:id/deploy` → navigate to deployment logs
- Test: image mode submits image_ref; validation requires name
- Commit `feat(web): add new app wizard with create-and-deploy`

### Task 5: App detail + deployments + live SSE logs + env editor + settings
**Files:** `web/src/pages/AppDetail.tsx` (tabs), `web/src/components/DeploymentsTab.tsx`, `web/src/components/EnvTab.tsx`, `web/src/components/SettingsTab.tsx`, `web/src/pages/DeploymentLogs.tsx`, `web/src/components/StatusBadge.tsx`, test `web/src/pages/DeploymentLogs.test.tsx`.
- Deployments tab: list (status badge, trigger, sha, times), Deploy button, Rollback on previous live rows (confirm), refetchInterval 3s while any deployment active
- Logs page: EventSource to `/api/v1/deployments/:id/logs`, appends lines, auto-scroll, closes on unmount; header shows live status (poll deployment every 3s until terminal)
- Env tab: existing keys listed with delete; add/update rows (values never read back); save calls PUT bulk
- Settings tab: editable port/healthcheck/branch/image/auto-deploy → PATCH; danger zone delete app (confirm) → navigate to org
- Test: log viewer renders replayed lines via mocked EventSource
- Commit `feat(web): add app detail with live logs, env editor, and rollback`

### Task 6: Admin page + polish + full verification
**Files:** `web/src/pages/Admin.tsx`, `web/index.html` (title "Cargo"), remove `web/src/App.test.tsx` placeholder, update `PhasedPlans.md`.
- Admin: users table, orgs table (visible only if `user.is_instance_admin`; route guarded)
- Verify: `npx vitest run` all green; `npm run lint`; `npm run build`; `go test ./internal/webui/`; manual smoke via `go run ./cmd/server` + built assets
- Mark PhasedPlans 5.x ✅ (except GitHub-picker and Domains bullets — annotate deferred to Phases 3/4)
- Commit `feat(web): add instance admin page and finish frontend milestone`

## Self-review notes
- 5.1 auth pages/silent refresh → Tasks 1–2; 5.2 dashboard/switcher/settings/viewer-read-only → Task 3; 5.3 wizard (≤5 clicks: New App → fill → Create & Deploy) → Task 4; 5.4 tabs/logs/rollback → Task 5; 5.5 env write-only → Task 5 (Domains deferred); 5.6 admin → Task 6.
- Deviation from design doc: no shadcn/ui (documented above); GitHub repo picker deferred with Phase 3.
