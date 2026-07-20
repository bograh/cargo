# Cargo Web UI — "Freight" Redesign & Sidebar Rework

Date: 2026-07-20 · Status: Approved design, pre-implementation
Inspiration: [cargo-web landing page](https://github.com/bograh/cargo-web) ("dark industrial freight" identity)

Full visual redesign of the `web/` React app: port the landing page's design language into a Tailwind v4 design system, replace the top-bar layout with a Vercel-style context-switching sidebar, and polish every existing flow (toasts, skeletons, live status, refined empty states). No backend changes; full feature parity with the current UI.

## 1. Goals / non-goals

**Goals**
- A coherent "Freight" design system inside the app, derived from the cargo-web landing tokens: charcoal ramp, single amber accent, Space Grotesk display type, JetBrains Mono as a stylistic device, hazard-stripe / corrugated / amber-glow motifs.
- Vercel-like **context-switching sidebar**: org scope (Apps, Databases, Members, Org Settings) swaps to app scope (Overview, Deployments, Environment, Domains, Settings) when inside an app. Org switcher + user menu live in the sidebar.
- Full polish layer: toasts, skeleton loaders, pulsing live-status dots, sticky page headers, modals replacing `window.confirm`, amber focus rings, responsive collapse (icon rail `<lg`, drawer on mobile).
- Every existing route/feature keeps working (12 routes; see §4).
- Existing Vitest tests updated to the new structure; suite stays green.

**Non-goals**
- No backend/API changes (frontend consumes the existing REST surface only).
- No new product features (no command palette, no DB logs viewer, no activity feeds).
- No light mode (landing identity is dark-only; a toggle doubles theme QA).
- No component-library dependencies (Toast/Modal/Dropdown/Tooltip hand-rolled; only new deps are the two `@fontsource` font packages).
- `docker-compose.dev.yml` flow and the Go embed path stay as-is.

## 2. Design system

### 2.1 Tokens (`web/src/index.css`, Tailwind v4 `@theme`)

Verbatim from cargo-web's `global.css`:

| Token | Value | Use |
|---|---|---|
| `bg` | `#0b0c0e` | app background |
| `surface` | `#121316` | cards, sidebar |
| `raised` | `#17181c` | hover fills, inputs, dropdowns |
| `border` | `#23252b` | universal 1px separator |
| `text` | `#e8e9eb` | primary text |
| `muted` | `#9aa0a8` | secondary text |
| `amber` | `#f5a524` | the single accent (primary buttons, active nav, focus) |
| `amber-deep` | `#c47f12` | amber hover/darker |
| `green` | `#3fb950` | live/ok status |
| `danger` | `#e5484d` | destructive, failed status |
| terminal bg | `#0d0f12` | log viewer, code blocks |
| terminal blue | `#58a6ff` | links/URLs inside terminal contexts |
| primary-btn text | `#14100a` | warm near-black on amber buttons |

- Type: `--font-display: 'Space Grotesk'` (700 for headings/wordmark, tight tracking), body = system stack, `--font-mono: 'JetBrains Mono'` used stylistically (eyebrow labels, badges, table headers, SHAs, section labels). Self-hosted via `@fontsource/space-grotesk` (500/600/700) and `@fontsource/jetbrains-mono` (400/500/700) — the only new dependencies.
- Radius: one base token, 10px (cards/buttons/modals); pills for badges; circles for dots.
- Depth comes from the bg→surface→raised layering plus 1px borders, **not** shadows (single exception: dropdowns/modals get a deep drop shadow like the landing's terminal window).
- Tints via `color-mix(in srgb, …)` exactly like the landing (e.g. amber 6% chip backgrounds, 45% hover borders).
- Amber text selection (`rgba(245,165,36,0.28)`); `:focus-visible` 2px amber outline everywhere.

### 2.2 Signature motifs (used purposefully, not everywhere)

- **Hazard stripe** (6px-tall 45° amber-striped bar): under page titles, in empty states, on the auth card.
- **Corrugated ribs** (`repeating-linear-gradient(90deg, rgba(255,255,255,0.025) 0 2px, transparent 2px 14px)`): auth pages background, the App Overview hero card.
- **Amber radial glow**: auth pages (top), final-CTA-style empty dashboard (subtle).
- **Glassmorphism**: sticky page headers (`backdrop-filter: blur(10px)` over 82% bg) like the landing nav.

### 2.3 Component primitives (`web/src/components/ui/`, replacing `ui.tsx`)

`Button` (primary amber / secondary raised / danger / ghost), `Input`, `Select`, `Textarea`, `Label`, `Card`, `Badge` (color-mix tints, mono micro-label styling), `StatusDot` (pulsing, green glow for live), `Spinner`, `Skeleton`, `Tooltip`, `Modal` (portal, focus trap, Esc/backdrop close — replaces `window.confirm` and the one-off DB URL modal), `DropdownMenu` (org switcher, user menu), `Toast` + `ToastProvider` (bottom-right, amber/green/danger variants, auto-dismiss), `EmptyState` (hazard motif), `PageHeader` (eyebrow + title + actions, glass sticky), `Breadcrumb`, `FieldError`, and an `Icon` component (inline lucide-style SVG paths; no icon dependency).

## 3. Shell & navigation

`RequireAuth` wraps a new `Shell`: fixed left sidebar (`w-60`) + content column.

**Org scope** (`/orgs/:orgId*` except app pages):
- Sidebar header: org switcher `DropdownMenu` — current org (Space Grotesk), list of orgs with role badge, "New organization" item.
- Nav items (icon + label, active = amber-tinted bg + left amber notch): **Apps**, **Databases**, **Members**, **Org Settings**.
- Sidebar footer: user `DropdownMenu` (email, "Instance admin" link when `is_instance_admin`, log out).

**App scope** (`/apps/:appId*`):
- Sidebar swaps to: back-link "← All apps", app name + live `StatusDot` (from latest deployment status), nav: **Overview, Deployments, Environment, Domains, Settings**.

**Admin** (`/admin`): org-scope sidebar with an amber "Instance" section (Users, Orgs, Settings).

**Responsive:** sidebar collapses to an icon rail below `lg` (labels tooltipped); below `md` it becomes a slide-over drawer with a hamburger in the page header. Content column `max-w-6xl` with consistent `px-6 py-6` rhythm. Each page renders a `PageHeader` (mono eyebrow, Space Grotesk title, hazard bar, right-aligned actions).

Route changes: only additions. Current tab navigation is component-local state (no tab URLs exist), so nothing needs redirects. `/orgs/:orgId` keeps serving Apps (dashboard); Databases becomes `/orgs/:orgId/databases`, Members `/orgs/:orgId/members`, Org Settings stays `/orgs/:orgId/settings`. App pages: `/apps/:appId` (Overview), `/apps/:appId/deployments`, `/apps/:appId/env`, `/apps/:appId/domains`, `/apps/:appId/settings`. `/apps/:appId` previously defaulted to the Deployments tab; it now shows Overview (Deployments is one click away in the sidebar).

## 4. Pages (all 12 existing routes ported)

| Route | Treatment |
|---|---|
| `/login`, `/register` | Centered card on corrugated bg + amber glow; hazard bar; SSO link preserved; `?error=oidc` handling preserved |
| `/` | Redirector (unchanged logic) |
| `/orgs/new` | Centered narrow card, new skin |
| `/orgs/:orgId` | Apps grid: cards with name, git/image badge, live `StatusDot` from latest deployment, primary domain, relative "last deployed" time; skeleton grid while loading; `EmptyState` with hazard motif |
| `/orgs/:orgId/databases` | Current `DatabasesTab` content promoted to a page (provision form, instance cards, attach/detach, one-time URL `Modal`, snapshots, type-to-confirm delete) |
| `/orgs/:orgId/members` | Members table + invites card (from current OrgSettings) |
| `/orgs/:orgId/settings` | GitHub connection card + danger zone (delete org, owner-only, `Modal` type-to-confirm). No org rename — the API has no PATCH for orgs |
| `/orgs/:orgId/apps/new` | Same multi-part form flow (source toggle, repo/branch pickers, builder, port, healthcheck, env rows), restyled; submit chain unchanged (`POST app → PUT env → POST deploy → logs`) |
| `/apps/:appId` | Overview: corrugated hero card (status dot, primary domain link with copy, source summary, exposed port), quick actions (Deploy now, Visit site, View logs of latest), recent-deployments strip (last 5) |
| `/apps/:appId/deployments` | Current `DeploymentsTab` content: table with 3s polling while active, Deploy/Rollback actions, logs links |
| `/apps/:appId/env` | Current `EnvTab` content (masked values, add/update/delete) |
| `/apps/:appId/domains` | Current `DomainsTab` content (auto subdomain card + custom domains) |
| `/apps/:appId/settings` | Current `SettingsTab` content (edit app, danger-zone delete via `Modal`) |
| `/deployments/:id` | Log viewer restyled as a **terminal window**: `#0d0f12` bg, JetBrains Mono, window chrome dots, SSE append + auto-scroll preserved, failure banner, blue URLs |
| `/invite/:token` | Auto-accept flow, new skin |
| `/admin` | Instance settings, GitHub App, SMTP, OIDC cards + Users/Orgs tables, new skin, amber section accents |

## 5. Polish layer

- **Toasts** replace all inline "Saved"/"Copied" text and bare error paragraphs (mutation errors still surface inline in forms where field-scoped).
- **Skeletons** on every `useQuery` loading path (cards, tables, page headers), no spinners-in-void.
- **Live status**: `StatusDot` pulses while a deployment is active (queued/building/deploying), steady green when live, red when failed — same 3s polling cadence as today.
- **Confirmations**: all destructive actions (delete app/org/db, remove member, revoke invite, detach, rollback) use `Modal`; delete-org/delete-app/delete-db keep type-to-confirm.
- 160ms ease transitions on hover (border-color toward amber 45%, translateY(-2px) on cards), matching the landing's restraint — no keyframe animations beyond the status-dot pulse and spinner.
- Keyboard: focus-visible amber rings; dropdowns/menus Esc-dismiss and arrow-navigate; modals trap focus.

## 6. Architecture / files

```
web/src/
  index.css                 # @theme tokens, motifs, font imports (only global css)
  lib/                      # api.ts, types.ts, utils (relative time, cn)   [unchanged logic]
  auth.tsx                  # unchanged
  components/
    ui/                     # §2.3 primitives (one file per component)
    layout/                 # Shell.tsx, Sidebar.tsx, OrgScopeNav.tsx, AppScopeNav.tsx,
                            # OrgSwitcher.tsx (moved), UserMenu.tsx, PageHeader wiring
    (feature components stay, restyled: DeploymentsTab→pages/deployments/, etc.)
  pages/                    # one file per route from §4
```

- `api.ts`, `auth.tsx`, `types.ts`, react-query keys, SSE log streaming, refresh-retry: **unchanged**.
- sqlc-uppercase normalization stays in `OrgSwitcher`'s `normalizeOrgs` (moved to `lib/`).
- New deps: `@fontsource/space-grotesk`, `@fontsource/jetbrains-mono` only.
- Build/embed path unchanged: `vite build` → `internal/webui/dist` → `//go:embed`.

## 7. Testing & verification

- Update the existing Vitest tests (Login, NewApp, Admin, OrgSettings, DeploymentLogs, DatabasesTab, DomainsTab, lib/api) to the new page structure; keep behavior-level assertions (queries by role/label) so most survive. Add tests for the new `Shell` scope switching and `Toast`/`Modal` primitives.
- Gates: `npm run lint` (oxlint), `npx tsc -b`, `npm run test`, `npm run build`.
- Final: rebuild the controlplane image and verify the real stack at http://localhost (login → create org → create app → deploy → logs) renders the new UI.

## 8. Risks / mitigations

- **Test breakage from structure splits** (tabs → routes/pages): existing tests are mostly behavior-level; rewrite the ones that target merged pages (AppDetail tabs, OrgDashboard tabs) against the new page components.
- **Scope creep** ("bells and whistles"): polish list in §5 is the ceiling; anything beyond (command palette, new endpoints) is a follow-up spec.
- **Font flash**: fonts self-hosted via fontsource with `font-display: swap`; Space Grotesk fallback stack matches metrics closely enough for the wordmark not to shift layout.
