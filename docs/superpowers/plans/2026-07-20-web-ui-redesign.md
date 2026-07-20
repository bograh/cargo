# Web UI "Freight" Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the Cargo web UI (`web/`) with the cargo-web landing page's "dark industrial freight" design language and a Vercel-style context-switching sidebar, with full polish (toasts, skeletons, live status dots, modals) and zero backend changes.

**Architecture:** In-place rebuild. Keep the stack (React 19, Tailwind v4, react-query, `lib/api.ts`, `auth.tsx`, `lib/types.ts`) and all data logic; replace the visual layer: theme tokens in `index.css`, a new `components/ui/` primitives library, a new `components/layout/` shell with context-switching sidebar, and page-by-page rewrites. Spec: `docs/superpowers/specs/2026-07-20-web-ui-redesign-design.md`.

**Tech Stack:** React 19, TypeScript, Vite 8, Tailwind CSS v4 (`@theme`, `@utility`), @tanstack/react-query 5, react-router-dom 7, Vitest + Testing Library, oxlint.

## Global Constraints

- Palette hex values verbatim from cargo-web: bg `#0b0c0e`, surface `#121316`, raised `#17181c`, border `#23252b`, text `#e8e9eb`, muted `#9aa0a8`, amber `#f5a524`, amber-deep `#c47f12`, green `#3fb950`, danger `#e5484d`, terminal `#0d0f12`, terminal-blue `#58a6ff`, on-amber `#14100a`.
- Dark-only. No light mode, no theme toggle.
- Only new npm deps: `@fontsource/space-grotesk`, `@fontsource/jetbrains-mono`. NO component libraries, NO icon packages (icons are hand-authored inline SVG).
- No backend/API changes. No new product features beyond what the spec lists.
- Feature parity: every existing route and behavior keeps working (SSE logs, refresh-retry, GitHub pickers, OIDC link, invites, admin).
- Commit style: `feat(web): …` / `fix(web): …` / `style(web): …`, matching repo history.
- After every task: `npm run lint && npx tsc -b` must pass and targeted vitest files must pass. Full suite runs in Task 13.
- Working directory for all commands: `/home/bograh/Code/cargo/web` unless noted.

## Global Restyle Mapping (applies to every "port/restyle" step)

Old → new class substitutions when porting existing components:

| Old (slate/indigo) | New (freight) |
|---|---|
| `bg-slate-950` | `bg-bg` |
| `bg-slate-900/60`, `bg-slate-900` (fills) | `bg-surface` |
| `bg-slate-800` (fills on dark) | `bg-raised` |
| `border-slate-800`, `border-slate-700` | `border-border` |
| `text-slate-100`/`text-slate-200` | `text-text` |
| `text-slate-400`/`text-slate-500` | `text-muted` |
| `text-indigo-400` (links) | `text-amber` |
| `bg-indigo-600 hover:bg-indigo-500` buttons | `Button` primitive (primary) |
| `text-red-400`, `border-red-800 bg-red-950` | `text-danger` / `border border-danger/40 bg-danger-tint` |
| `rounded-md`/`rounded-lg` | `rounded-lg` (10px via theme) |
| `window.confirm(...)` | `ConfirmModal` (Task 3) |
| inline "Saved ✓" / "Copied" text | `useToast()` push (Task 3) |
| `<Spinner />` full-page loads | `Skeleton` placeholders matching the final layout |

Import substitution in every touched file: `from "../components/ui"` stays valid (barrel re-exports), but new code imports primitives from the barrel: `import { Button, Card, … } from "../components/ui";` (path depth depends on file location).

## File Structure

Create:
- `web/src/lib/cn.ts` — class combiner
- `web/src/lib/hooks.ts` — `normalizeOrgs`, `useOrgs`, `useOrg`, `useApp`, `useInstanceInfo`
- `web/src/lib/time.ts` — `relativeTime(iso: string): string`
- `web/src/components/ui/` — `Button.tsx`, `Input.tsx`, `Select.tsx`, `Textarea.tsx`, `Label.tsx`, `Card.tsx`, `Badge.tsx`, `Spinner.tsx`, `Skeleton.tsx`, `FieldError.tsx`, `EmptyState.tsx`, `PageHeader.tsx`, `Icon.tsx`, `StatusDot.tsx`, `Tooltip.tsx`, `Dropdown.tsx`, `Modal.tsx`, `ConfirmModal.tsx`, `Toast.tsx`, `index.ts` (barrel + compat shims)
- `web/src/components/layout/` — `Shell.tsx`, `Sidebar.tsx`, `NavItem.tsx`, `OrgScopeNav.tsx`, `AppScopeNav.tsx`, `UserMenu.tsx`, `ContainerMark.tsx`
- `web/src/pages/OrgApps.tsx`, `OrgDatabases.tsx`, `OrgMembers.tsx`, `AppOverview.tsx`, `AppDeployments.tsx`, `AppEnv.tsx`, `AppDomains.tsx`, `AppSettings.tsx`
- `web/src/components/AuthShell.tsx`

Modify: `web/src/index.css` (full rewrite), `web/src/App.tsx` (routes), `web/src/main.tsx` (ToastProvider), `web/package.json` (2 font deps), pages/components being ported, `web/src/pages/Home.tsx` (import `useOrgs` from `lib/hooks`).

Delete: `web/src/components/ui.tsx` (replaced by `ui/`), `web/src/pages/OrgDashboard.tsx`, `web/src/pages/AppDetail.tsx`, `web/src/pages/OrgSettings.test.tsx` (moved to `OrgMembers.test.tsx`).

Keep as-is: `web/src/lib/api.ts`, `web/src/auth.tsx`, `web/src/lib/types.ts`, `web/src/test/*`.

---

### Task 1: Theme foundation — fonts, tokens, motifs

**Files:**
- Modify: `web/package.json`
- Rewrite: `web/src/index.css`
- Create: `web/src/lib/cn.ts`

**Interfaces:**
- Produces: Tailwind theme colors `bg|surface|raised|border|text|muted|amber|amber-deep|live|danger|terminal|terminal-blue|on-amber`; fonts `font-display`, `font-sans`, `font-mono`; utilities `corrugated`, `hazard`, `glow-top`, `glass`, `bg-amber-tint`, `bg-live-tint`, `bg-danger-tint`, `border-amber-dim`; `cn(...parts): string`. All later tasks consume these.

- [ ] **Step 1: Install font packages**

```bash
cd /home/bograh/Code/cargo/web && npm install @fontsource/space-grotesk @fontsource/jetbrains-mono
```

- [ ] **Step 2: Rewrite `web/src/index.css`**

```css
@import "@fontsource/space-grotesk/500.css";
@import "@fontsource/space-grotesk/600.css";
@import "@fontsource/space-grotesk/700.css";
@import "@fontsource/jetbrains-mono/400.css";
@import "@fontsource/jetbrains-mono/500.css";
@import "@fontsource/jetbrains-mono/700.css";
@import "tailwindcss";

@theme {
  /* palette — verbatim from cargo-web global.css */
  --color-bg: #0b0c0e;
  --color-surface: #121316;
  --color-raised: #17181c;
  --color-border: #23252b;
  --color-text: #e8e9eb;
  --color-muted: #9aa0a8;
  --color-amber: #f5a524;
  --color-amber-deep: #c47f12;
  --color-live: #3fb950;
  --color-danger: #e5484d;
  --color-terminal: #0d0f12;
  --color-terminal-blue: #58a6ff;
  --color-on-amber: #14100a;

  --font-display: "Space Grotesk", system-ui, sans-serif;
  --font-sans: system-ui, -apple-system, "Segoe UI", sans-serif;
  --font-mono: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace;

  --radius-lg: 10px;
}

@layer base {
  body {
    @apply bg-bg text-text font-sans antialiased;
  }
  h1, h2, h3, h4 {
    font-family: var(--font-display);
    letter-spacing: -0.01em;
  }
  ::selection {
    background: rgba(245, 165, 36, 0.28);
  }
  :focus-visible {
    outline: 2px solid var(--color-amber);
    outline-offset: 2px;
  }
}

/* Freight motifs (pure CSS, from cargo-web) */
@utility corrugated {
  background-image: repeating-linear-gradient(90deg, rgba(255, 255, 255, 0.025) 0 2px, transparent 2px 14px);
}
@utility hazard {
  background: repeating-linear-gradient(45deg, var(--color-amber) 0 12px, transparent 12px 24px);
}
@utility glow-top {
  background-image: radial-gradient(52rem 26rem at 50% -8rem, rgba(245, 165, 36, 0.13), transparent 65%);
}
@utility glass {
  background: color-mix(in srgb, var(--color-bg) 82%, transparent);
  backdrop-filter: blur(10px);
}
@utility bg-amber-tint {
  background: color-mix(in srgb, var(--color-amber) 6%, transparent);
}
@utility bg-live-tint {
  background: color-mix(in srgb, var(--color-live) 10%, transparent);
}
@utility bg-danger-tint {
  background: color-mix(in srgb, var(--color-danger) 10%, transparent);
}
@utility border-amber-dim {
  border-color: color-mix(in srgb, var(--color-amber) 45%, transparent);
}
```

- [ ] **Step 3: Create `web/src/lib/cn.ts`**

```ts
export function cn(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}
```

- [ ] **Step 4: Verify build**

Run: `npm run build`
Expected: builds successfully; output CSS contains `--color-amber: #f5a524` and `.hazard`.

- [ ] **Step 5: Commit**

```bash
git add package.json package-lock.json src/index.css src/lib/cn.ts
git commit -m "feat(web): freight theme tokens and motifs"
```

---

### Task 2: UI primitives — basics

**Files:**
- Create: `web/src/components/ui/Button.tsx`, `Input.tsx`, `Select.tsx`, `Textarea.tsx`, `Label.tsx`, `Card.tsx`, `Badge.tsx`, `Spinner.tsx`, `Skeleton.tsx`, `FieldError.tsx`, `EmptyState.tsx`, `PageHeader.tsx`
- Test: `web/src/components/ui/ui.test.tsx`
- Delete: `web/src/components/ui.tsx` (after barrel exists — Task 3 step)
- Note: do NOT create `index.ts` yet (Task 3 adds it with compat shims); pages still import the old `ui.tsx` until then.

**Interfaces:**
- Produces (exact signatures; Task 3+ consumes via barrel):
  - `Button({ variant?: "primary"|"secondary"|"danger"|"ghost", className?, ...buttonProps })`
  - `Input`, `Select`, `Textarea` ({ className?, ...nativeProps })
  - `Label({ className?, ...labelProps })`
  - `Card({ className?, ...divProps })`
  - `Badge({ tone?: "amber"|"live"|"danger"|"neutral", className?, ...spanProps })`
  - `Spinner({ className? })` — keeps `role="status"` aria-label `loading`
  - `Skeleton({ className? })`
  - `FieldError({ message?: string })`
  - `EmptyState({ title: string, hint?: string, action?: ReactNode })`
  - `PageHeader({ eyebrow?: string, title: ReactNode, actions?: ReactNode })`

- [ ] **Step 1: Write the failing test** — `web/src/components/ui/ui.test.tsx`

```tsx
import { render, screen } from "@testing-library/react";
import { Button } from "./Button";
import { Badge } from "./Badge";
import { EmptyState } from "./EmptyState";
import { PageHeader } from "./PageHeader";
import { Spinner } from "./Spinner";

describe("ui primitives", () => {
  it("renders primary button with amber styling", () => {
    render(<Button>Deploy</Button>);
    const btn = screen.getByRole("button", { name: "Deploy" });
    expect(btn.className).toContain("bg-amber");
  });

  it("renders badge tones", () => {
    render(<Badge tone="live">live</Badge>);
    expect(screen.getByText("live").className).toContain("text-live");
  });

  it("renders empty state with action", () => {
    render(<EmptyState title="No apps" hint="deploy one" action={<Button>New</Button>} />);
    expect(screen.getByText("No apps")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New" })).toBeInTheDocument();
  });

  it("renders page header with eyebrow and actions", () => {
    render(<PageHeader eyebrow="org" title="Acme" actions={<Button>New App</Button>} />);
    expect(screen.getByRole("heading", { name: "Acme" })).toBeInTheDocument();
    expect(screen.getByText("org")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New App" })).toBeInTheDocument();
  });

  it("spinner keeps status role", () => {
    render(<Spinner />);
    expect(screen.getByRole("status")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/components/ui/ui.test.tsx`
Expected: FAIL — modules not found.

- [ ] **Step 3: Implement primitives**

`Button.tsx`:
```tsx
import type { ButtonHTMLAttributes } from "react";
import { cn } from "../../lib/cn";

export type ButtonVariant = "primary" | "secondary" | "danger" | "ghost";

const VARIANTS: Record<ButtonVariant, string> = {
  primary: "bg-amber text-on-amber font-semibold hover:bg-amber-deep",
  secondary: "bg-raised text-text border border-border hover:border-amber-dim",
  danger: "bg-danger text-white font-semibold hover:brightness-110",
  ghost: "text-muted hover:text-text hover:bg-raised",
};

export function Button({
  variant = "primary",
  className,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant }) {
  return (
    <button
      className={cn(
        "inline-flex items-center justify-center gap-2 rounded-lg px-3 py-1.5 text-sm transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50",
        VARIANTS[variant],
        className,
      )}
      {...props}
    />
  );
}
```

`Input.tsx`:
```tsx
import type { InputHTMLAttributes } from "react";
import { cn } from "../../lib/cn";

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={cn(
        "w-full rounded-lg border border-border bg-raised px-3 py-1.5 text-sm text-text placeholder:text-muted/60 focus:border-amber-dim focus:outline-none",
        className,
      )}
      {...props}
    />
  );
}
```

`Select.tsx`:
```tsx
import type { SelectHTMLAttributes } from "react";
import { cn } from "../../lib/cn";

export function Select({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        "rounded-lg border border-border bg-raised px-2 py-1.5 text-sm text-text focus:border-amber-dim focus:outline-none",
        className,
      )}
      {...props}
    />
  );
}
```

`Textarea.tsx`:
```tsx
import type { TextareaHTMLAttributes } from "react";
import { cn } from "../../lib/cn";

export function Textarea({ className, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      className={cn(
        "w-full rounded-lg border border-border bg-raised px-3 py-1.5 text-sm text-text placeholder:text-muted/60 focus:border-amber-dim focus:outline-none",
        className,
      )}
      {...props}
    />
  );
}
```

`Label.tsx`:
```tsx
import type { LabelHTMLAttributes } from "react";
import { cn } from "../../lib/cn";

export function Label({ className, ...props }: LabelHTMLAttributes<HTMLLabelElement>) {
  return (
    <label
      className={cn("mb-1 block font-mono text-[0.68rem] font-medium uppercase tracking-[0.12em] text-muted", className)}
      {...props}
    />
  );
}
```

`Card.tsx`:
```tsx
import type { HTMLAttributes } from "react";
import { cn } from "../../lib/cn";

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("rounded-lg border border-border bg-surface p-4", className)} {...props} />;
}
```

`Badge.tsx`:
```tsx
import type { HTMLAttributes } from "react";
import { cn } from "../../lib/cn";

export type BadgeTone = "amber" | "live" | "danger" | "neutral";

const TONES: Record<BadgeTone, string> = {
  amber: "bg-amber-tint text-amber border-[color-mix(in_srgb,var(--color-amber)_35%,transparent)]",
  live: "bg-live-tint text-live border-[color-mix(in_srgb,var(--color-live)_35%,transparent)]",
  danger: "bg-danger-tint text-danger border-[color-mix(in_srgb,var(--color-danger)_35%,transparent)]",
  neutral: "bg-raised text-muted border-border",
};

/** @deprecated legacy color names from ui.tsx — removed in the cleanup task */
type LegacyColor = "green" | "amber" | "red" | "gray" | "indigo";
const LEGACY: Record<LegacyColor, BadgeTone> = {
  green: "live",
  amber: "amber",
  red: "danger",
  gray: "neutral",
  indigo: "amber",
};

export function Badge({
  tone,
  color,
  className,
  ...props
}: HTMLAttributes<HTMLSpanElement> & { tone?: BadgeTone; color?: LegacyColor }) {
  const resolved: BadgeTone = tone ?? (color ? LEGACY[color] : "neutral");
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono text-[0.7rem] font-medium uppercase tracking-[0.12em]",
        TONES[resolved],
        className,
      )}
      {...props}
    />
  );
}
```

`Spinner.tsx`:
```tsx
import { cn } from "../../lib/cn";

export function Spinner({ className }: { className?: string }) {
  return (
    <div
      role="status"
      aria-label="loading"
      className={cn("h-5 w-5 animate-spin rounded-full border-2 border-border border-t-amber", className)}
    />
  );
}
```

`Skeleton.tsx`:
```tsx
import { cn } from "../../lib/cn";

export function Skeleton({ className }: { className?: string }) {
  return <div aria-hidden="true" className={cn("animate-pulse rounded-md bg-raised", className)} />;
}
```

`FieldError.tsx`:
```tsx
export function FieldError({ message }: { message?: string }) {
  if (!message) return null;
  return <p className="mt-1 text-xs text-danger">{message}</p>;
}
```

`EmptyState.tsx`:
```tsx
import type { ReactNode } from "react";

export function EmptyState({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed border-border bg-surface/50 p-10 text-center">
      <div className="hazard mx-auto h-1.5 w-[72px] rounded-sm opacity-70" />
      <p className="mt-4 font-display text-lg font-semibold">{title}</p>
      {hint && <p className="mx-auto mt-1 max-w-md text-sm text-muted">{hint}</p>}
      {action && <div className="mt-5 flex justify-center">{action}</div>}
    </div>
  );
}
```

`PageHeader.tsx`:
```tsx
import type { ReactNode } from "react";

export function PageHeader({ eyebrow, title, actions }: { eyebrow?: string; title: ReactNode; actions?: ReactNode }) {
  return (
    <div className="glass sticky top-0 z-20 -mx-4 mb-6 border-b border-border px-4 py-3 sm:-mx-6 sm:px-6">
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          {eyebrow && (
            <div className="font-mono text-[0.68rem] uppercase tracking-[0.14em] text-amber">{eyebrow}</div>
          )}
          <h1 className="mt-0.5 truncate font-display text-xl font-bold tracking-tight">{title}</h1>
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </div>
      <div className="hazard mt-2 h-1 w-14 rounded-sm" />
    </div>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/components/ui/ui.test.tsx`
Expected: 5 passed.

- [ ] **Step 5: Commit**

```bash
git add src/components/ui src/lib/cn.ts
git commit -m "feat(web): freight ui primitives (buttons, cards, badges, headers)"
```

---

### Task 3: UI primitives — overlays, status, icons, barrel

**Files:**
- Create: `web/src/components/ui/Icon.tsx`, `StatusDot.tsx`, `Tooltip.tsx`, `Dropdown.tsx`, `Modal.tsx`, `ConfirmModal.tsx`, `Toast.tsx`, `index.ts`
- Delete: `web/src/components/ui.tsx`
- Modify: `web/src/components/StatusBadge.tsx` (use new Badge tones)
- Test: `web/src/components/ui/overlays.test.tsx`

**Interfaces:**
- Produces (via barrel `components/ui/index.ts`):
  - `Icon({ name: IconName, size?: number, className? })`, `type IconName`
  - `StatusDot({ status: Deployment["status"], className? })`
  - `Tooltip({ label: string, children })`
  - `Dropdown({ trigger: ReactNode, label: string, align?: "left"|"right", children })`, `DropdownItem({ icon?: IconName, danger?: boolean, onClick?, children })`
  - `Modal({ title: string, onClose: () => void, wide?: boolean, children })`
  - `ConfirmModal({ title, body: ReactNode, confirmLabel?: string, requireText?: string, busy?: boolean, onConfirm: () => void, onClose: () => void })`
  - `ToastProvider({ children })`, `useToast(): (text: string, tone?: "success"|"error"|"info") => void`
  - Barrel also re-exports Task 2 primitives plus compat shims `PageTitle` and legacy `Badge color` prop (both removed in Task 13).

- [ ] **Step 1: Write the failing test** — `web/src/components/ui/overlays.test.tsx`

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { ConfirmModal } from "./ConfirmModal";
import { Dropdown, DropdownItem } from "./Dropdown";
import { Modal } from "./Modal";
import { ToastProvider, useToast } from "./Toast";
import { StatusDot } from "./StatusDot";

describe("Modal", () => {
  it("renders title and closes on Escape", async () => {
    const onClose = vi.fn();
    render(<Modal title="Connection URL" onClose={onClose}>body</Modal>);
    expect(screen.getByRole("dialog", { name: "Connection URL" })).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalled();
  });
});

describe("ConfirmModal", () => {
  it("requires typed text before enabling confirm", async () => {
    const onConfirm = vi.fn();
    render(<ConfirmModal title="Delete app" body="gone forever" requireText="my-app" onConfirm={onConfirm} onClose={() => {}} />);
    const confirm = screen.getByRole("button", { name: "Delete" });
    expect(confirm).toBeDisabled();
    await userEvent.type(screen.getByRole("textbox"), "my-app");
    expect(confirm).toBeEnabled();
    await userEvent.click(confirm);
    expect(onConfirm).toHaveBeenCalled();
  });
});

describe("Dropdown", () => {
  it("opens on trigger click and closes on item click", async () => {
    const hit = vi.fn();
    render(
      <Dropdown trigger={<span>menu</span>} label="open menu">
        <DropdownItem onClick={hit}>Log out</DropdownItem>
      </Dropdown>,
    );
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "open menu" }));
    expect(screen.getByRole("menu")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("menuitem", { name: "Log out" }));
    expect(hit).toHaveBeenCalled();
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });
});

describe("Toast", () => {
  function Demo() {
    const toast = useToast();
    return <button onClick={() => toast("Saved", "success")}>push</button>;
  }
  it("shows pushed toast", async () => {
    render(<ToastProvider><Demo /></ToastProvider>);
    await userEvent.click(screen.getByRole("button", { name: "push" }));
    expect(await screen.findByText("Saved")).toBeInTheDocument();
  });
});

describe("StatusDot", () => {
  it("pulses for active statuses", () => {
    const { container } = render(<StatusDot status="building" />);
    expect(container.querySelector(".animate-ping")).toBeTruthy();
  });
  it("is steady for live", () => {
    const { container } = render(<StatusDot status="live" />);
    expect(container.querySelector(".animate-ping")).toBeFalsy();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/components/ui/overlays.test.tsx`
Expected: FAIL — modules not found.

- [ ] **Step 3: Implement**

`Icon.tsx` (all glyphs are stroke-based, `viewBox="0 0 24 24"`, `stroke-width 2`, hand-authored from simple shapes):
```tsx
import type { ReactNode } from "react";

const ICONS = {
  apps: (<><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" /></>),
  box: (<><path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z" /><path d="m3.3 7 8.7 5 8.7-5" /><path d="M12 22V12" /></>),
  database: (<><ellipse cx="12" cy="5" rx="8" ry="3" /><path d="M4 5v14c0 1.66 3.58 3 8 3s8-1.34 8-3V5" /><path d="M4 12c0 1.66 3.58 3 8 3s8-1.34 8-3" /></>),
  users: (<><circle cx="9" cy="8" r="3.5" /><path d="M2.5 20a6.5 6.5 0 0 1 13 0" /><path d="M16 4.6a3.5 3.5 0 0 1 0 6.8" /><path d="M17.5 14.4a6.5 6.5 0 0 1 4 5.6" /></>),
  settings: (<><line x1="4" y1="6" x2="20" y2="6" /><circle cx="9" cy="6" r="2" fill="currentColor" stroke="none" /><line x1="4" y1="12" x2="20" y2="12" /><circle cx="15" cy="12" r="2" fill="currentColor" stroke="none" /><line x1="4" y1="18" x2="20" y2="18" /><circle cx="7" cy="18" r="2" fill="currentColor" stroke="none" /></>),
  rocket: (<><path d="M22 2 11 13" /><path d="M22 2 15 22l-4-9-9-4Z" /></>),
  globe: (<><circle cx="12" cy="12" r="9" /><path d="M3 12h18" /><path d="M12 3a15 15 0 0 1 0 18 15 15 0 0 1 0-18Z" /></>),
  key: (<><circle cx="8" cy="15" r="4" /><path d="m10.85 12.15 8.5-8.5" /><path d="m18 5 2 2" /><path d="m15 8 2 2" /></>),
  list: (<><path d="M8 6h13" /><path d="M8 12h13" /><path d="M8 18h13" /><path d="M3 6h.01" /><path d="M3 12h.01" /><path d="M3 18h.01" /></>),
  "chevron-left": (<polyline points="15 18 9 12 15 6" />),
  "chevron-down": (<polyline points="6 9 12 15 18 9" />),
  plus: (<><line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" /></>),
  x: (<><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></>),
  check: (<polyline points="20 6 9 17 4 12" />),
  copy: (<><rect x="9" y="9" width="12" height="12" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></>),
  external: (<><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" /><polyline points="15 3 21 3 21 9" /><line x1="10" y1="14" x2="21" y2="3" /></>),
  alert: (<><path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z" /><path d="M12 9v4" /><path d="M12 17h.01" /></>),
  trash: (<><path d="M3 6h18" /><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" /><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" /></>),
  menu: (<><line x1="4" y1="6" x2="20" y2="6" /><line x1="4" y1="12" x2="20" y2="12" /><line x1="4" y1="18" x2="20" y2="18" /></>),
  "arrow-left": (<><line x1="19" y1="12" x2="5" y2="12" /><polyline points="12 19 5 12 12 5" /></>),
  "arrow-right": (<><line x1="5" y1="12" x2="19" y2="12" /><polyline points="12 5 19 12 12 19" /></>),
  refresh: (<><path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" /><path d="M21 3v5h-5" /></>),
  download: (<><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" /><polyline points="7 10 12 15 17 10" /><line x1="12" y1="15" x2="12" y2="3" /></>),
  camera: (<><path d="M14.5 4h-5L7 7H4a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2h-3l-2.5-3Z" /><circle cx="12" cy="13" r="3" /></>),
  link: (<><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" /><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" /></>),
  shield: (<><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1Z" /></>),
  logout: (<><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" /><polyline points="16 17 21 12 16 7" /><line x1="21" y1="12" x2="9" y2="12" /></>),
  search: (<><circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" /></>),
  "git-branch": (<><line x1="6" y1="3" x2="6" y2="15" /><circle cx="18" cy="6" r="3" /><circle cx="6" cy="18" r="3" /><path d="M18 9a9 9 0 0 1-9 9" /></>),
} as const satisfies Record<string, ReactNode>;

export type IconName = keyof typeof ICONS;

export function Icon({ name, size = 16, className }: { name: IconName; size?: number; className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      {ICONS[name]}
    </svg>
  );
}
```

`StatusDot.tsx`:
```tsx
import { isActive, type Deployment } from "../../lib/types";
import { cn } from "../../lib/cn";

export function StatusDot({ status, className }: { status: Deployment["status"]; className?: string }) {
  const active = isActive(status);
  const color =
    status === "live" ? "bg-live" : status === "failed" ? "bg-danger" : active ? "bg-amber" : "bg-muted";
  return (
    <span className={cn("relative inline-flex h-2 w-2 shrink-0", className)}>
      {active && <span className={cn("absolute inline-flex h-full w-full rounded-full opacity-60 animate-ping", color)} />}
      <span
        className={cn(
          "relative inline-flex h-2 w-2 rounded-full",
          color,
          status === "live" && "shadow-[0_0_8px_color-mix(in_srgb,var(--color-live)_60%,transparent)]",
        )}
      />
    </span>
  );
}
```

`Tooltip.tsx`:
```tsx
import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

export function Tooltip({ label, children, className }: { label: string; children: ReactNode; className?: string }) {
  return (
    <span className="group relative inline-flex">
      {children}
      <span
        role="tooltip"
        className={cn(
          "pointer-events-none absolute left-full top-1/2 z-50 ml-2 -translate-y-1/2 rounded-md border border-border bg-raised px-2 py-1 text-xs whitespace-nowrap text-text opacity-0 transition-opacity duration-150 group-hover:opacity-100 lg:group-hover:opacity-0",
          className,
        )}
      >
        {label}
      </span>
    </span>
  );
}
```
(The `lg:group-hover:opacity-0` makes tooltips rail-only; pass `className="lg:group-hover:opacity-100"` to override elsewhere.)

`Dropdown.tsx`:
```tsx
import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "../../lib/cn";
import { Icon, type IconName } from "./Icon";

export function Dropdown({
  trigger,
  label,
  align = "left",
  children,
}: {
  trigger: ReactNode;
  label: string;
  align?: "left" | "right";
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={label}
        onClick={() => setOpen((o) => !o)}
        className="w-full rounded-lg transition-colors duration-150 hover:bg-raised"
      >
        {trigger}
      </button>
      {open && (
        <div
          role="menu"
          onClick={() => setOpen(false)}
          className={cn(
            "absolute z-40 mt-1 min-w-52 rounded-lg border border-border bg-raised p-1 shadow-[0_24px_60px_-24px_rgba(0,0,0,0.7)]",
            align === "right" ? "right-0" : "left-0",
          )}
        >
          {children}
        </div>
      )}
    </div>
  );
}

export function DropdownItem({
  icon,
  danger,
  onClick,
  children,
}: {
  icon?: IconName;
  danger?: boolean;
  onClick?: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-sm transition-colors duration-150",
        danger ? "text-danger hover:bg-danger-tint" : "text-text hover:bg-surface",
      )}
    >
      {icon && <Icon name={icon} size={14} />}
      <span className="truncate">{children}</span>
    </button>
  );
}
```

`Modal.tsx`:
```tsx
import { useEffect, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { cn } from "../../lib/cn";

export function Modal({
  title,
  onClose,
  wide,
  children,
}: {
  title: string;
  onClose: () => void;
  wide?: boolean;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    ref.current?.querySelector<HTMLElement>("input, select, textarea, button, a")?.focus();
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);
  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/60" onClick={onClose} />
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={cn(
          "relative w-full rounded-lg border border-border bg-surface p-5 shadow-[0_24px_60px_-24px_rgba(0,0,0,0.7)]",
          wide ? "max-w-2xl" : "max-w-md",
        )}
      >
        <h2 className="font-display text-lg font-semibold">{title}</h2>
        <div className="mt-3">{children}</div>
      </div>
    </div>,
    document.body,
  );
}
```

`ConfirmModal.tsx`:
```tsx
import { useState, type ReactNode } from "react";
import { Button } from "./Button";
import { Input } from "./Input";
import { Label } from "./Label";
import { Modal } from "./Modal";

export function ConfirmModal({
  title,
  body,
  confirmLabel = "Delete",
  requireText,
  busy,
  onConfirm,
  onClose,
}: {
  title: string;
  body: ReactNode;
  confirmLabel?: string;
  requireText?: string;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const [typed, setTyped] = useState("");
  const ok = !requireText || typed === requireText;
  return (
    <Modal title={title} onClose={onClose}>
      <div className="text-sm text-muted">{body}</div>
      {requireText && (
        <div className="mt-3">
          <Label htmlFor="confirm-typed">
            Type <span className="font-mono text-text">{requireText}</span> to confirm
          </Label>
          <Input id="confirm-typed" value={typed} onChange={(e) => setTyped(e.target.value)} />
        </div>
      )}
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="ghost" onClick={onClose}>
          Cancel
        </Button>
        <Button variant="danger" disabled={!ok || busy} onClick={onConfirm}>
          {confirmLabel}
        </Button>
      </div>
    </Modal>
  );
}
```

`Toast.tsx`:
```tsx
import { createContext, useCallback, useContext, useState, type ReactNode } from "react";
import { cn } from "../../lib/cn";

type Tone = "success" | "error" | "info";
type ToastItem = { id: number; tone: Tone; text: string };

const ToastCtx = createContext<(text: string, tone?: Tone) => void>(() => {});

export function useToast() {
  return useContext(ToastCtx);
}

const TONE_STYLES: Record<Tone, string> = {
  success: "border-[color-mix(in_srgb,var(--color-live)_35%,transparent)] text-live",
  error: "border-[color-mix(in_srgb,var(--color-danger)_35%,transparent)] text-danger",
  info: "border-border text-text",
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const push = useCallback((text: string, tone: Tone = "success") => {
    const id = Date.now() + Math.random();
    setToasts((t) => [...t, { id, tone, text }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 4000);
  }, []);
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="fixed bottom-4 right-4 z-[60] flex w-72 flex-col gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            role="status"
            className={cn(
              "rounded-lg border bg-surface px-3 py-2 text-sm shadow-[0_24px_60px_-24px_rgba(0,0,0,0.7)]",
              TONE_STYLES[t.tone],
            )}
          >
            {t.text}
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}
```

`index.ts` (barrel with compat shims):
```tsx
export { Button } from "./Button";
export { Input } from "./Input";
export { Select } from "./Select";
export { Textarea } from "./Textarea";
export { Label } from "./Label";
export { Card } from "./Card";
export { Badge } from "./Badge";
export { Spinner } from "./Spinner";
export { Skeleton } from "./Skeleton";
export { FieldError } from "./FieldError";
export { EmptyState } from "./EmptyState";
export { PageHeader } from "./PageHeader";
export { Icon } from "./Icon";
export type { IconName } from "./Icon";
export { StatusDot } from "./StatusDot";
export { Tooltip } from "./Tooltip";
export { Dropdown, DropdownItem } from "./Dropdown";
export { Modal } from "./Modal";
export { ConfirmModal } from "./ConfirmModal";
export { ToastProvider, useToast } from "./Toast";

// --- compat shims (deleted in the final cleanup task) ---
import type { ReactNode } from "react";
import { PageHeader } from "./PageHeader";

/** @deprecated use PageHeader */
export function PageTitle({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
  return <PageHeader title={children} actions={actions} />;
}
```

- [ ] **Step 4: Update `StatusBadge.tsx` to new tones**

```tsx
import type { Deployment } from "../lib/types";
import { Badge, type BadgeTone } from "./ui/Badge";

const TONES: Record<Deployment["status"], BadgeTone> = {
  queued: "amber",
  building: "amber",
  deploying: "amber",
  live: "live",
  failed: "danger",
  cancelled: "neutral",
};

export function StatusBadge({ status }: { status: Deployment["status"] }) {
  return <Badge tone={TONES[status] ?? "neutral"}>{status}</Badge>;
}
```
(`BadgeTone` needs exporting from `Badge.tsx` — already exported as `export type BadgeTone`.)

- [ ] **Step 5: Delete old `ui.tsx`, run tests**

```bash
rm src/components/ui.tsx
npx vitest run src/components/ui
```
Expected: all ui tests pass. Then `npx tsc -b` — expect errors only in files still importing removed legacy pieces (`Badge color=` usages and `PageTitle` keep working via shims; fix any genuine breakage minimally by switching to the new prop).

- [ ] **Step 6: Commit**

```bash
git add -A src/components
git commit -m "feat(web): overlay primitives (modal, dropdown, toast), icons, status dots"
```

---

### Task 4: Hooks, shell, sidebar, routing

**Files:**
- Create: `web/src/lib/hooks.ts`, `web/src/lib/time.ts`, `web/src/components/layout/ContainerMark.tsx`, `NavItem.tsx`, `OrgScopeNav.tsx`, `AppScopeNav.tsx`, `UserMenu.tsx`, `Sidebar.tsx`, `Shell.tsx`
- Rewrite: `web/src/components/OrgSwitcher.tsx` (dropdown version), `web/src/App.tsx`
- Modify: `web/src/pages/Home.tsx` (import `useOrgs` from `../lib/hooks`), `web/src/main.tsx` (wrap in `ToastProvider`)
- Test: `web/src/components/layout/Shell.test.tsx`

**Interfaces:**
- Consumes: all ui primitives (Tasks 2–3), `useAuth()` from `auth.tsx`.
- Produces:
  - `lib/hooks.ts`: `normalizeOrgs(rows)`, `useOrgs()`, `useOrg(orgId)`, `useApp(appId)`, `useInstanceInfo()`
  - `lib/time.ts`: `relativeTime(iso: string): string`
  - `Shell` layout route in `App.tsx`; routes: `/orgs/:orgId` (OrgApps — placeholder until Task 6 renders `<div/>`), `/orgs/:orgId/databases`, `/orgs/:orgId/members`, `/apps/:appId` + `/deployments|/env|/domains|/settings` (placeholders until their tasks). Keep existing page imports for routes not yet rewritten.
  - `OrgSwitcher` still exports nothing else; `useOrgs` moves to `lib/hooks`.

- [ ] **Step 1: Create `lib/hooks.ts` and `lib/time.ts`**

`lib/hooks.ts`:
```ts
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import type { App, Org } from "./types";

interface OrgRow {
  ID: string;
  Name: string;
  Slug: string;
  Role: string;
}

// The /orgs list endpoint returns sqlc rows (uppercase fields); normalize here.
export function normalizeOrgs(rows: OrgRow[]): Org[] {
  return rows.map((r) => ({ id: r.ID, name: r.Name, slug: r.Slug, role: r.Role }));
}

export function useOrgs() {
  return useQuery({
    queryKey: ["orgs"],
    queryFn: async () => normalizeOrgs(await api<OrgRow[]>("/orgs")),
  });
}

export function useOrg(orgId: string | undefined) {
  return useQuery({
    queryKey: ["org", orgId],
    queryFn: () => api<{ organization: Org; role: string }>(`/orgs/${orgId}`),
    enabled: !!orgId,
  });
}

export function useApp(appId: string | undefined) {
  return useQuery({
    queryKey: ["app", appId],
    queryFn: () => api<App>(`/apps/${appId}`),
    enabled: !!appId,
  });
}

export function useInstanceInfo() {
  return useQuery({
    queryKey: ["instance-info"],
    queryFn: () => api<{ apps_domain_suffix: string }>("/instance/info"),
  });
}
```

`lib/time.ts`:
```ts
export function relativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "—";
  const m = Math.floor((Date.now() - then) / 60000);
  if (m < 1) return "just now";
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}d ago`;
  return new Date(iso).toLocaleDateString();
}
```

- [ ] **Step 2: Write the failing shell test** — `web/src/components/layout/Shell.test.tsx`

```tsx
import { screen } from "@testing-library/react";
import { mockApi, renderPage } from "../../test/utils";
import { Shell } from "./Shell";

const ME = { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: false } };
const ORGS = { status: 200, body: [{ ID: "o1", Name: "Acme", Slug: "acme", Role: "owner" }] };
const ORG = { status: 200, body: { organization: { id: "o1", name: "Acme", slug: "acme" }, role: "owner" } };
const APP = {
  status: 200,
  body: { id: "a1", org_id: "o1", name: "web", slug: "web", source_type: "git", auto_deploy: true },
};

describe("Shell", () => {
  it("shows org-scope nav on org pages", async () => {
    mockApi({
      "GET /auth/me": ME,
      "GET /orgs": ORGS,
      "GET /orgs/o1": ORG,
      "GET /orgs/o1/apps": { status: 200, body: [] },
    });
    renderPage(<Shell />, { path: "/orgs/:orgId", route: "/orgs/o1" });
    expect(await screen.findByRole("link", { name: /apps/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /databases/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /members/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /settings/i })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /deployments/i })).not.toBeInTheDocument();
  });

  it("switches to app-scope nav on app pages", async () => {
    mockApi({
      "GET /auth/me": ME,
      "GET /orgs": ORGS,
      "GET /apps/a1": APP,
      "GET /apps/a1/deployments": { status: 200, body: [] },
    });
    renderPage(<Shell />, { path: "/apps/:appId/*", route: "/apps/a1/deployments" });
    expect(await screen.findByRole("link", { name: /overview/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /deployments/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /environment/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /domains/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /all apps/i })).toBeInTheDocument();
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `npx vitest run src/components/layout/Shell.test.tsx`
Expected: FAIL — `./Shell` not found.

- [ ] **Step 4: Implement layout components**

`ContainerMark.tsx` (the landing's container logo):
```tsx
export function ContainerMark({ size = 20, className }: { size?: number; className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="var(--color-amber)"
      strokeWidth={1.8}
      className={className}
      aria-hidden="true"
    >
      <rect x="3" y="5" width="18" height="14" rx="1.5" />
      <line x1="7.5" y1="5" x2="7.5" y2="19" />
      <line x1="12" y1="5" x2="12" y2="19" />
      <line x1="16.5" y1="5" x2="16.5" y2="19" />
    </svg>
  );
}
```

`NavItem.tsx`:
```tsx
import { NavLink } from "react-router-dom";
import { cn } from "../../lib/cn";
import { Icon, type IconName } from "../ui/Icon";
import { Tooltip } from "../ui/Tooltip";

export function NavItem({ to, icon, label, end }: { to: string; icon: IconName; label: string; end?: boolean }) {
  return (
    <Tooltip label={label}>
      <NavLink
        to={to}
        end={end}
        className={({ isActive }) =>
          cn(
            "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors duration-150 md:justify-center lg:justify-start",
            isActive ? "bg-amber-tint text-amber" : "text-muted hover:bg-raised hover:text-text",
          )
        }
      >
        <Icon name={icon} size={16} className="shrink-0" />
        <span className="truncate md:hidden lg:inline">{label}</span>
      </NavLink>
    </Tooltip>
  );
}
```

`OrgScopeNav.tsx`:
```tsx
import { useMatch } from "react-router-dom";
import { NavItem } from "./NavItem";

export function OrgScopeNav() {
  const match = useMatch("/orgs/:orgId/*");
  const orgId = match?.params.orgId;
  if (!orgId) return null;
  return (
    <nav className="flex flex-col gap-0.5 px-2">
      <NavItem to={`/orgs/${orgId}`} icon="apps" label="Apps" end />
      <NavItem to={`/orgs/${orgId}/databases`} icon="database" label="Databases" />
      <NavItem to={`/orgs/${orgId}/members`} icon="users" label="Members" />
      <NavItem to={`/orgs/${orgId}/settings`} icon="settings" label="Org Settings" />
    </nav>
  );
}
```

`AppScopeNav.tsx`:
```tsx
import { useQuery } from "@tanstack/react-query";
import { Link, useMatch } from "react-router-dom";
import { api } from "../../lib/api";
import { isActive, type Deployment } from "../../lib/types";
import { useApp } from "../../lib/hooks";
import { StatusDot } from "../ui/StatusDot";
import { Icon } from "../ui/Icon";
import { NavItem } from "./NavItem";

export function AppScopeNav() {
  const match = useMatch("/apps/:appId/*");
  const appId = match?.params.appId;
  const { data: app } = useApp(appId);
  const { data: deployments } = useQuery({
    queryKey: ["deployments", appId],
    queryFn: () => api<Deployment[]>(`/apps/${appId}/deployments`),
    enabled: !!appId,
    refetchInterval: (q) => (q.state.data?.some((d) => isActive(d.status)) ? 3000 : false),
  });
  if (!appId) return null;
  const latest = deployments?.[0];
  return (
    <div className="flex flex-col gap-3 px-2">
      {app && (
        <>
          <Link
            to={`/orgs/${app.org_id}`}
            className="flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs text-muted transition-colors duration-150 hover:text-text md:justify-center lg:justify-start"
          >
            <Icon name="arrow-left" size={14} />
            <span className="md:hidden lg:inline">All apps</span>
          </Link>
          <div className="flex items-center gap-2 px-3 md:justify-center lg:justify-start">
            {latest && <StatusDot status={latest.status} />}
            <span className="truncate font-display text-sm font-semibold md:hidden lg:inline">{app.name}</span>
          </div>
        </>
      )}
      <nav className="flex flex-col gap-0.5">
        <NavItem to={`/apps/${appId}`} icon="box" label="Overview" end />
        <NavItem to={`/apps/${appId}/deployments`} icon="rocket" label="Deployments" />
        <NavItem to={`/apps/${appId}/env`} icon="key" label="Environment" />
        <NavItem to={`/apps/${appId}/domains`} icon="globe" label="Domains" />
        <NavItem to={`/apps/${appId}/settings`} icon="settings" label="Settings" />
      </nav>
    </div>
  );
}
```

`UserMenu.tsx`:
```tsx
import { useNavigate } from "react-router-dom";
import { useAuth } from "../../auth";
import { Dropdown, DropdownItem } from "../ui/Dropdown";
import { Icon } from "../ui/Icon";

export function UserMenu() {
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  if (!user) return null;
  return (
    <div className="border-t border-border p-2">
      <Dropdown
        label="account menu"
        trigger={
          <span className="flex items-center gap-2.5 px-2 py-1.5">
            <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-amber-tint font-mono text-xs font-semibold text-amber">
              {user.email[0]?.toUpperCase()}
            </span>
            <span className="truncate text-sm text-text md:hidden lg:inline">{user.email}</span>
          </span>
        }
      >
        {user.is_instance_admin && (
          <DropdownItem icon="shield" onClick={() => navigate("/admin")}>
            Instance admin
          </DropdownItem>
        )}
        <DropdownItem icon="logout" danger onClick={() => logout().then(() => navigate("/login"))}>
          Log out
        </DropdownItem>
      </Dropdown>
    </div>
  );
}
```

`Sidebar.tsx`:
```tsx
import { useEffect, useState } from "react";
import { Link, useLocation } from "react-router-dom";
import { cn } from "../../lib/cn";
import { useAuth } from "../../auth";
import { Icon } from "../ui/Icon";
import { AppScopeNav } from "./AppScopeNav";
import { ContainerMark } from "./ContainerMark";
import { OrgScopeNav } from "./OrgScopeNav";
import { UserMenu } from "./UserMenu";

export function Sidebar() {
  const { pathname } = useLocation();
  const { user } = useAuth();
  const [open, setOpen] = useState(false);
  const isAppScope = pathname.startsWith("/apps/");
  useEffect(() => setOpen(false), [pathname]);
  return (
    <>
      {/* mobile top bar */}
      <div className="glass fixed inset-x-0 top-0 z-30 flex h-14 items-center gap-3 border-b border-border px-4 md:hidden">
        <button aria-label="Open navigation" onClick={() => setOpen(true)} className="text-muted hover:text-text">
          <Icon name="menu" size={20} />
        </button>
        <Link to="/" className="flex items-center gap-2 font-display font-bold tracking-tight">
          <ContainerMark size={18} /> Cargo
        </Link>
      </div>
      {open && <div className="fixed inset-0 z-30 bg-black/60 md:hidden" onClick={() => setOpen(false)} />}
      <aside
        className={cn(
          "fixed inset-y-0 left-0 z-40 flex w-60 flex-col border-r border-border bg-surface transition-transform duration-150 md:w-16 md:translate-x-0 lg:w-60",
          open ? "translate-x-0" : "-translate-x-full",
        )}
      >
        <div className="flex h-14 shrink-0 items-center border-b border-border px-4 md:justify-center md:px-0 lg:justify-start lg:px-4">
          <Link to="/" className="flex items-center gap-2 font-display text-lg font-bold tracking-tight">
            <ContainerMark />
            <span className="md:hidden lg:inline">Cargo</span>
          </Link>
        </div>
        <div className="flex-1 overflow-y-auto py-3">
          {isAppScope ? <AppScopeNav /> : <OrgScopeNav />}
          {user?.is_instance_admin && !isAppScope && (
            <div className="mt-4 border-t border-border px-2 pt-3">
              <div className="px-3 pb-1 font-mono text-[0.68rem] uppercase tracking-[0.14em] text-amber md:hidden lg:block">
                Instance
              </div>
              <Link
                to="/admin"
                className="flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm text-muted transition-colors duration-150 hover:bg-raised hover:text-text md:justify-center lg:justify-start"
              >
                <Icon name="shield" size={16} />
                <span className="md:hidden lg:inline">Admin</span>
              </Link>
            </div>
          )}
        </div>
        <UserMenu />
      </aside>
    </>
  );
}
```

`Shell.tsx`:
```tsx
import { Outlet } from "react-router-dom";
import { Sidebar } from "./Sidebar";

export function Shell() {
  return (
    <div className="min-h-screen bg-bg text-text">
      <Sidebar />
      <div className="pt-14 md:pl-16 md:pt-0 lg:pl-60">
        <main className="mx-auto max-w-6xl px-4 py-6 sm:px-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
```

- [ ] **Step 5: Rewrite `OrgSwitcher.tsx` as dropdown**

```tsx
import { useLocation, useNavigate } from "react-router-dom";
import { useOrgs } from "../lib/hooks";
import { Dropdown, DropdownItem } from "./ui/Dropdown";
import { Icon } from "./ui/Icon";

export function OrgSwitcher() {
  const { data: orgs } = useOrgs();
  const navigate = useNavigate();
  const location = useLocation();
  if (!orgs || orgs.length === 0 || location.pathname.startsWith("/apps/")) return null;
  const current = orgs.find((o) => location.pathname.startsWith(`/orgs/${o.id}`));
  return (
    <div className="px-2 pb-3">
      <Dropdown
        label="switch organization"
        trigger={
          <span className="flex w-full items-center gap-2.5 px-2 py-1.5">
            <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-raised font-display text-xs font-bold text-amber">
              {(current?.name ?? "?")[0]?.toUpperCase()}
            </span>
            <span className="flex-1 truncate text-left font-display text-sm font-semibold md:hidden lg:inline">
              {current?.name ?? "Select org"}
            </span>
            <Icon name="chevron-down" size={14} className="text-muted md:hidden lg:inline" />
          </span>
        }
      >
        {orgs.map((o) => (
          <DropdownItem key={o.id} icon="box" onClick={() => navigate(`/orgs/${o.id}`)}>
            {o.name}
          </DropdownItem>
        ))}
        <DropdownItem icon="plus" onClick={() => navigate("/orgs/new")}>
          New organization
        </DropdownItem>
      </Dropdown>
    </div>
  );
}
```
Then render `<OrgSwitcher />` inside `Sidebar` between the brand header and the nav area (only in org scope): in `Sidebar.tsx`, change the nav container to:
```tsx
<div className="flex-1 overflow-y-auto py-3">
  {!isAppScope && <OrgSwitcher />}
  {isAppScope ? <AppScopeNav /> : <OrgScopeNav />}
  …
```
(import `OrgSwitcher` from `../OrgSwitcher`.)

- [ ] **Step 6: Update `App.tsx` routes**

Replace the old `Shell` function and routes:
```tsx
import { Route, Routes } from "react-router-dom";
import { RequireAuth } from "./auth";
import { Shell } from "./components/layout/Shell";
import Login from "./pages/Login";
import Register from "./pages/Register";
import Home from "./pages/Home";
import NewOrg from "./pages/NewOrg";
import OrgApps from "./pages/OrgApps";
import OrgSettings from "./pages/OrgSettings";
import NewApp from "./pages/NewApp";
import AppDetail from "./pages/AppDetail";
import DeploymentLogs from "./pages/DeploymentLogs";
import AcceptInvite from "./pages/AcceptInvite";
import Admin from "./pages/Admin";

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      <Route element={<RequireAuth><Shell /></RequireAuth>}>
        <Route path="/" element={<Home />} />
        <Route path="/orgs/new" element={<NewOrg />} />
        <Route path="/orgs/:orgId" element={<OrgApps />} />
        <Route path="/orgs/:orgId/settings" element={<OrgSettings />} />
        <Route path="/orgs/:orgId/apps/new" element={<NewApp />} />
        <Route path="/apps/:appId" element={<AppDetail />} />
        <Route path="/deployments/:deploymentId" element={<DeploymentLogs />} />
        <Route path="/invite/:token" element={<AcceptInvite />} />
        <Route path="/admin" element={<Admin />} />
      </Route>
    </Routes>
  );
}
```
Create a minimal `web/src/pages/OrgApps.tsx` placeholder for now (Task 6 replaces it):
```tsx
export default function OrgApps() {
  return <div />;
}
```
`Home.tsx`: change import to `import { useOrgs } from "../lib/hooks";`. `main.tsx`: wrap `<App />` with `<ToastProvider>` (import from `./components/ui`). Old `OrgDashboard.tsx` stays on disk until Task 6 (it is no longer routed; delete then). `OrgSettings` import of `useOrg` — switch to `../lib/hooks` NOW since `OrgDashboard` no longer exports it in Task 6; do it in this task to keep the build green: in `OrgSettings.tsx` replace `import { useOrg } from "./OrgDashboard";` with `import { useOrg } from "../lib/hooks";`.

- [ ] **Step 7: Run test to verify it passes, then gates**

Run: `npx vitest run src/components/layout && npm run lint && npx tsc -b`
Expected: shell tests pass; lint/tsc clean.

- [ ] **Step 8: Commit**

```bash
git add -A src
git commit -m "feat(web): context-switching sidebar shell and route skeleton"
```

---

### Task 5: Auth pages

**Files:**
- Create: `web/src/components/AuthShell.tsx`
- Rewrite: `web/src/pages/Login.tsx`, `web/src/pages/Register.tsx`
- Test: `web/src/pages/Login.test.tsx` (adapt existing)

**Interfaces:**
- Consumes: `Card`, `Button`, `Input`, `Label`, `FieldError` from barrel; `ContainerMark`; `useAuth`; `api`.
- Produces: `AuthShell({ title, children })` used by both pages.

- [ ] **Step 1: Adapt the test** — the existing `Login.test.tsx` behavior (login submit, error display, SSO link, oidc error) must keep passing unchanged. Run it first to confirm the baseline: `npx vitest run src/pages/Login.test.tsx`. It may rely on `AuthShell` being exported from `Login.tsx` — keep a re-export `export { AuthShell } from "../components/AuthShell";` in `Login.tsx` for compatibility.

- [ ] **Step 2: Implement `AuthShell.tsx`**

```tsx
import type { ReactNode } from "react";
import { ContainerMark } from "./layout/ContainerMark";

export function AuthShell({ title, children }: { title: string; children: ReactNode }) {
  return (
    <main className="corrugated glow-top flex min-h-screen items-center justify-center bg-bg px-4 text-text">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex flex-col items-center gap-2">
          <ContainerMark size={28} />
          <h1 className="font-display text-2xl font-bold tracking-tight">Cargo</h1>
        </div>
        <div className="rounded-lg border border-border bg-surface p-5">
          <div className="hazard mb-4 h-1.5 w-[72px] rounded-sm" />
          <h2 className="mb-4 font-display text-lg font-semibold">{title}</h2>
          {children}
        </div>
      </div>
    </main>
  );
}
```

- [ ] **Step 3: Rewrite `Login.tsx`** — same logic, new skin:

```tsx
import { useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAuth } from "../auth";
import { AuthShell } from "../components/AuthShell";
import { Button, FieldError, Input, Label } from "../components/ui";

export { AuthShell } from "../components/AuthShell";

export default function Login() {
  const { login } = useAuth();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [searchParams] = useSearchParams();
  const ssoError = searchParams.get("error") === "oidc";
  const { data: providers } = useQuery({
    queryKey: ["auth", "providers"],
    queryFn: () => api<{ password: boolean; oidc: boolean }>("/auth/providers"),
  });

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await login(email, password);
      navigate("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "login failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthShell title="Log in">
      {ssoError && (
        <p className="mb-4 rounded-lg border border-danger/40 bg-danger-tint px-3 py-2 text-sm text-danger">
          SSO sign-in failed. Try again or use your password.
        </p>
      )}
      <form onSubmit={submit} className="space-y-4">
        <div>
          <Label htmlFor="email">Email</Label>
          <Input id="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </div>
        <div>
          <Label htmlFor="password">Password</Label>
          <Input id="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
          <FieldError message={error} />
        </div>
        <Button type="submit" className="w-full" disabled={busy}>
          Log in
        </Button>
        {providers?.oidc && (
          <a
            href="/api/v1/auth/oidc/start"
            className="block w-full rounded-lg border border-border bg-raised px-3 py-1.5 text-center text-sm font-medium text-text transition-colors duration-150 hover:border-amber-dim"
          >
            Sign in with SSO
          </a>
        )}
        <p className="text-center text-sm text-muted">
          No account?{" "}
          <Link to="/register" className="text-amber hover:underline">
            Register
          </Link>
        </p>
      </form>
    </AuthShell>
  );
}
```

- [ ] **Step 4: Rewrite `Register.tsx`** — same shape: keep all logic (email/password state, min-10-chars validation, `register()` call, navigate `/`), render inside `AuthShell` with the same field/button/error styling as Login, link to `/login` ("Have an account? Log in" in amber).

- [ ] **Step 5: Run tests + gates**

Run: `npx vitest run src/pages/Login.test.tsx && npm run lint && npx tsc -b`
Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add -A src
git commit -m "style(web): freight auth pages"
```

---

### Task 6: Org Apps dashboard

**Files:**
- Rewrite: `web/src/pages/OrgApps.tsx` (replaces placeholder; deletes `OrgDashboard.tsx`)
- Delete: `web/src/pages/OrgDashboard.tsx`
- Test: `web/src/pages/OrgApps.test.tsx` (new)

**Interfaces:**
- Consumes: `useOrg`, `useOrgs` from `lib/hooks`; `PageHeader`, `Card`, `Badge`, `Button`, `Skeleton`, `EmptyState`, `StatusDot`, `Icon`; `api`, `isActive`, `relativeTime`.
- Produces: default export `OrgApps`; `AppCard({ app }: { app: App })` (also used by nothing else — keep local).

- [ ] **Step 1: Write the failing test** — `web/src/pages/OrgApps.test.tsx`

```tsx
import { screen } from "@testing-library/react";
import { mockApi, renderPage } from "../test/utils";
import OrgApps from "./OrgApps";

const APP = {
  id: "a1", org_id: "o1", name: "web", slug: "web", source_type: "git",
  git_repo_url: "https://github.com/acme/web", git_branch: "main",
  auto_deploy: true, created_at: "2026-07-01T00:00:00Z",
};

function mock() {
  return mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: false } },
    "GET /orgs/o1": { status: 200, body: { organization: { id: "o1", name: "Acme", slug: "acme" }, role: "owner" } },
    "GET /orgs/o1/apps": { status: 200, body: [APP] },
    "GET /apps/a1/deployments": {
      status: 200,
      body: [{ id: "d1", app_id: "a1", trigger: "manual", status: "live", commit_sha: "abc123", image_tag: "img", created_at: "2026-07-19T00:00:00Z" }],
    },
  });
}

describe("OrgApps", () => {
  it("renders app cards with live status and links to the app", async () => {
    mock();
    renderPage(<OrgApps />, { path: "/orgs/:orgId", route: "/orgs/o1" });
    const link = await screen.findByRole("link", { name: /web/i });
    expect(link).toHaveAttribute("href", "/apps/a1");
    expect(await screen.findByText("live")).toBeInTheDocument();
  });

  it("shows empty state when no apps", async () => {
    mockApi({
      "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: false } },
      "GET /orgs/o1": { status: 200, body: { organization: { id: "o1", name: "Acme", slug: "acme" }, role: "owner" } },
      "GET /orgs/o1/apps": { status: 200, body: [] },
    });
    renderPage(<OrgApps />, { path: "/orgs/:orgId", route: "/orgs/o1" });
    expect(await screen.findByText(/no applications yet/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/pages/OrgApps.test.tsx`
Expected: FAIL — placeholder renders nothing.

- [ ] **Step 3: Implement `OrgApps.tsx`**

```tsx
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { api } from "../lib/api";
import { useOrg } from "../lib/hooks";
import { relativeTime } from "../lib/time";
import { isActive, type App, type Deployment } from "../lib/types";
import { Badge, Button, Card, EmptyState, Icon, PageHeader, Skeleton, StatusDot } from "../components/ui";

function AppCard({ app }: { app: App }) {
  const { data: deployments } = useQuery({
    queryKey: ["deployments", app.id],
    queryFn: () => api<Deployment[]>(`/apps/${app.id}/deployments`),
    refetchInterval: (q) => (q.state.data?.some((d) => isActive(d.status)) ? 3000 : false),
  });
  const latest = deployments?.[0];
  return (
    <Link to={`/apps/${app.id}`}>
      <Card className="h-full transition-all duration-150 hover:-translate-y-0.5 hover:border-amber-dim">
        <div className="flex items-center justify-between gap-2">
          <h3 className="truncate font-display font-semibold">{app.name}</h3>
          <Badge tone={app.source_type === "git" ? "amber" : "neutral"}>{app.source_type}</Badge>
        </div>
        <p className="mt-1 truncate font-mono text-xs text-muted">{app.slug}</p>
        <div className="mt-3 flex items-center gap-2 text-xs text-muted">
          {latest ? (
            <>
              <StatusDot status={latest.status} />
              <span>{latest.status}</span>
              <span aria-hidden="true">·</span>
              <span>{relativeTime(latest.created_at)}</span>
            </>
          ) : (
            <span>not deployed yet</span>
          )}
        </div>
        <p className="mt-2 truncate font-mono text-xs text-muted">
          {app.source_type === "git" ? `${app.git_repo_url} @ ${app.git_branch}` : app.image_ref}
        </p>
      </Card>
    </Link>
  );
}

export default function OrgApps() {
  const { orgId } = useParams();
  const { data: orgData, error } = useOrg(orgId);
  const { data: apps, isLoading } = useQuery({
    queryKey: ["apps", orgId],
    queryFn: () => api<App[]>(`/orgs/${orgId}/apps`),
    enabled: !!orgId,
  });

  if (error) {
    return <EmptyState title="Organization not found" hint="You may not be a member of this organization." />;
  }
  const role = orgData?.role ?? "viewer";
  const canCreate = role !== "viewer";

  return (
    <div>
      <PageHeader
        eyebrow="org"
        title={orgData?.organization.name ?? "…"}
        actions={
          canCreate && (
            <Link to={`/orgs/${orgId}/apps/new`}>
              <Button>
                <Icon name="plus" size={14} /> New App
              </Button>
            </Link>
          )
        }
      />
      {isLoading ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-28" />
          ))}
        </div>
      ) : !apps || apps.length === 0 ? (
        <EmptyState
          title="No applications yet"
          hint="Deploy your first app from a git repository or a container image."
          action={
            canCreate ? (
              <Link to={`/orgs/${orgId}/apps/new`}>
                <Button>New App</Button>
              </Link>
            ) : undefined
          }
        />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {apps.map((app) => (
            <AppCard key={app.id} app={app} />
          ))}
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Delete `OrgDashboard.tsx`, run tests + gates**

```bash
rm src/pages/OrgDashboard.tsx
npx vitest run src/pages/OrgApps.test.tsx src/components/layout && npm run lint && npx tsc -b
```
Expected: pass. (`DatabasesTab` is still imported by nothing after this — it gets re-mounted in Task 7; a lint unused-file warning is NOT emitted for unreferenced files, so this is fine.)

- [ ] **Step 5: Commit**

```bash
git add -A src
git commit -m "feat(web): org apps dashboard with live status cards"
```

---

### Task 7: Org Databases page

**Files:**
- Create: `web/src/pages/OrgDatabases.tsx`
- Rewrite in place: `web/src/components/DatabasesTab.tsx` (restyle + polish, keeps its exported API)
- Test: `web/src/components/DatabasesTab.test.tsx` (adapt existing — mostly behavior-level)
- Modify: `web/src/App.tsx` (add route)

**Interfaces:**
- Consumes: full ui barrel, `ConfirmModal`, `Modal`, `useToast`; `useOrg` from `lib/hooks`.
- Produces: route `/orgs/:orgId/databases` → `OrgDatabases`; `DatabasesTab({ orgId }: { orgId: string })` (unchanged signature).

- [ ] **Step 1: Run the existing test as baseline**

Run: `npx vitest run src/components/DatabasesTab.test.tsx`
Note which assertions are structural (class-based) vs behavioral (text/role) — keep behavioral ones passing; update any that query removed structure.

- [ ] **Step 2: Create the page**

```tsx
import { useParams } from "react-router-dom";
import { PageHeader } from "../components/ui";
import { DatabasesTab } from "../components/DatabasesTab";

export default function OrgDatabases() {
  const { orgId } = useParams();
  if (!orgId) return null;
  return (
    <div>
      <PageHeader eyebrow="org" title="Databases" />
      <DatabasesTab orgId={orgId} />
    </div>
  );
}
```
Add to `App.tsx` inside the shell route: `<Route path="/orgs/:orgId/databases" element={<OrgDatabases />} />` (+ import).

- [ ] **Step 3: Restyle `DatabasesTab.tsx`**

Apply the Global Restyle Mapping throughout (slate→freight classes, `indigo`→amber links, `Badge color=`→`tone=`). Keep ALL logic, queries, and exports identical. Additionally:

1. Replace every `window.confirm(...)` (delete instance) with `ConfirmModal` using `requireText={instance.name}` (type-to-confirm):
```tsx
const [deleting, setDeleting] = useState<DatabaseInstance | null>(null);
…
{deleting && (
  <ConfirmModal
    title={`Delete ${deleting.name}?`}
    body="This destroys the instance, its volume, and all snapshots. Attached apps lose their credentials on next deploy."
    requireText={deleting.name}
    busy={deleteMutation.isPending}
    onConfirm={() => deleteMutation.mutate(deleting.id, { onSuccess: () => setDeleting(null) })}
    onClose={() => setDeleting(null)}
  />
)}
```
2. Replace the existing one-off connection-URL modal markup with the shared `Modal` (`wide`), same content.
3. Replace inline "Copied"/"Saved" texts with `useToast()`: on copy → `toast("Copied to clipboard")`; provision success → `toast("Database provisioning started")`; snapshot taken → `toast("Snapshot created")`; errors from mutations → `toast(err.message, "error")` in addition to any inline field errors.
4. Loading states: replace `<Spinner />`-only areas with `Skeleton` cards (`<Skeleton className="h-32" />` ×2).
5. Engine/status badges: postgres/redis → `Badge tone="amber"`; status running → `live`, error → `danger`, provisioning → `amber` (pulsing `StatusDot status="building"` is also acceptable).

- [ ] **Step 4: Run tests + gates**

Run: `npx vitest run src/components/DatabasesTab.test.tsx && npm run lint && npx tsc -b`
Expected: pass (adapt test file imports/selectors only if structurally necessary).

- [ ] **Step 5: Commit**

```bash
git add -A src
git commit -m "feat(web): databases page with freight styling and confirm modals"
```

---

### Task 8: Members page + Org Settings page

**Files:**
- Create: `web/src/pages/OrgMembers.tsx`, `web/src/pages/OrgMembers.test.tsx`
- Rewrite: `web/src/pages/OrgSettings.tsx` (GitHub card + danger zone only)
- Delete: `web/src/pages/OrgSettings.test.tsx` (content moves to `OrgMembers.test.tsx`; danger-zone coverage moves too or is dropped if it only tested member behavior — keep any delete-org test in a new `OrgSettings.test.tsx`)
- Modify: `web/src/App.tsx` (route)

**Interfaces:**
- Consumes: `useOrg` from `lib/hooks`; barrel; `ConfirmModal`; `useToast`.
- Produces: routes `/orgs/:orgId/members` and updated `/orgs/:orgId/settings`.

- [ ] **Step 1: Move member/invite behavior tests**

Rename `OrgSettings.test.tsx` → `OrgMembers.test.tsx`; inside, change the component import from `./OrgSettings` to `./OrgMembers` and the route from `/orgs/:orgId/settings` to `/orgs/:orgId/members`. Run to see them fail (component doesn't exist).

- [ ] **Step 2: Create `OrgMembers.tsx`**

Move the members table, role-change `Select`, remove-member button, invites card (create link, copy-once, revoke) from the current `OrgSettings.tsx`. Restyle per mapping. Wrap in:
```tsx
<PageHeader eyebrow="org" title="Members" actions={…invite button…} />
```
Replace `window.confirm` (remove member / revoke invite) with `ConfirmModal` (no `requireText`; confirmLabel "Remove"/"Revoke"). Replace inline "copied" feedback with `toast("Invite link copied")`.

- [ ] **Step 3: Rewrite `OrgSettings.tsx`** (GitHub connection card + owner danger zone)

`GithubCard` is a local function inside the current `OrgSettings.tsx` (line 19) — keep it as a local component in the rewritten file, restyled per the mapping. Skeleton of the new file:

```tsx
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { del } from "../lib/api";
import { useOrg } from "../lib/hooks";
import { Button, Card, ConfirmModal, Icon, PageHeader, useToast } from "../components/ui";

function GithubCard({ orgId, isAdmin }: { orgId: string; isAdmin: boolean }) {
  // move the existing GithubCard implementation here unchanged in logic, restyled
}
```
Danger zone:
```tsx
<Card className="border-danger/40">
  <h2 className="font-display text-lg font-semibold text-danger">Danger zone</h2>
  <p className="mt-1 text-sm text-muted">Deleting an organization removes all its apps, databases, and deployments.</p>
  <Button variant="danger" className="mt-4" onClick={() => setConfirming(true)}>
    <Icon name="trash" size={14} /> Delete organization
  </Button>
</Card>
{confirming && (
  <ConfirmModal
    title={`Delete ${org.name}?`}
    body="All apps, databases, and deployments in this organization will be destroyed."
    requireText={org.name}
    busy={deleteOrg.isPending}
    onConfirm={() => deleteOrg.mutate()}
    onClose={() => setConfirming(false)}
  />
)}
```
Delete mutation: `del(`/orgs/${orgId}`)` → invalidate `["orgs"]`, navigate `/`, `toast("Organization deleted")`. Render only for `role === "owner"`. Keep `PageHeader eyebrow="org" title="Org Settings"`.

- [ ] **Step 4: Route + tests + gates**

Add `<Route path="/orgs/:orgId/members" element={<OrgMembers />} />`. Run: `npx vitest run src/pages/OrgMembers.test.tsx src/pages/OrgSettings.test.tsx 2>/dev/null; npx vitest run src/pages && npm run lint && npx tsc -b`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add -A src
git commit -m "feat(web): members page and slimmed org settings with danger zone"
```

---

### Task 9: New App + New Org pages

**Files:**
- Rewrite: `web/src/pages/NewApp.tsx`, `web/src/pages/NewOrg.tsx`
- Test: `web/src/pages/NewApp.test.tsx` (adapt existing)

**Interfaces:**
- Consumes: barrel, `useToast`, `useOrg`, `useOrgs`.
- Produces: unchanged routes `/orgs/:orgId/apps/new`, `/orgs/new`.

- [ ] **Step 1: Baseline test run** — `npx vitest run src/pages/NewApp.test.tsx`; keep behavior assertions green (repo picker, branch picker, builder select, submit chain `POST app → PUT env → POST deploy`).

- [ ] **Step 2: Restyle `NewApp.tsx`**

Apply the mapping. `PageHeader eyebrow={org name} title="New App"`. Source toggle (git/image) becomes a segmented control:
```tsx
<div className="flex rounded-lg border border-border bg-raised p-0.5">
  {(["git", "image"] as const).map((s) => (
    <button
      key={s}
      type="button"
      onClick={() => setSourceType(s)}
      className={cn(
        "flex-1 rounded-md px-3 py-1.5 text-sm capitalize transition-colors duration-150",
        sourceType === s ? "bg-amber-tint text-amber" : "text-muted hover:text-text",
      )}
    >
      {s === "git" ? "Git repository" : "Container image"}
    </button>
  ))}
</div>
```
Keep ALL logic (GitHub repo/branch pickers, builder select, env rows, submit chain) identical. Env var rows: key/value `Input`s with a `Button variant="ghost"` trash icon per row. Errors: keep field-scoped inline errors; on final submit failure also `toast(message, "error")`.

- [ ] **Step 3: Restyle `NewOrg.tsx`** — centered narrow card (`max-w-md mx-auto mt-16`): `PageHeader`-less; hazard bar + Space Grotesk title "New organization", one `Input`, submit `Button` full-width; on success (unchanged logic) invalidate + navigate; `toast("Organization created")`.

- [ ] **Step 4: Tests + gates + commit**

Run: `npx vitest run src/pages/NewApp.test.tsx && npm run lint && npx tsc -b`
Commit: `style(web): freight new-app and new-org flows`

---

### Task 10: App pages — Overview, Deployments, Environment, Domains, Settings

**Files:**
- Create: `web/src/pages/AppOverview.tsx`, `AppDeployments.tsx`, `AppEnv.tsx`, `AppDomains.tsx`, `AppSettings.tsx`
- Delete: `web/src/pages/AppDetail.tsx`
- Restyle: `web/src/components/DeploymentsTab.tsx`, `EnvTab.tsx`, `DomainsTab.tsx`, `SettingsTab.tsx` (logic unchanged)
- Modify: `web/src/App.tsx` (routes), `web/src/components/DomainsTab.tsx` (use `useInstanceInfo` from `lib/hooks` instead of its local `/instance/info` query)
- Test: `web/src/components/DomainsTab.test.tsx` (adapt), new `web/src/pages/AppOverview.test.tsx`

**Interfaces:**
- Consumes: `useApp`, `useInstanceInfo`, `relativeTime`, barrel, `StatusDot`, `StatusBadge`, `ConfirmModal`, `useToast`.
- Produces: routes `/apps/:appId[/deployments|/env|/domains|/settings]`. Tab components keep their `{ app }` prop signatures.

- [ ] **Step 1: Write `AppOverview.test.tsx`**

```tsx
import { screen } from "@testing-library/react";
import { mockApi, renderPage } from "../test/utils";
import AppOverview from "./AppOverview";

const APP = {
  id: "a1", org_id: "o1", name: "web", slug: "web", source_type: "git",
  git_repo_url: "https://github.com/acme/web", git_branch: "main", builder: "auto",
  exposed_port: 3000, healthcheck_path: "/", auto_deploy: true,
};

it("shows app summary, status, and quick actions", async () => {
  mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: false } },
    "GET /apps/a1": { status: 200, body: APP },
    "GET /apps/a1/deployments": {
      status: 200,
      body: [{ id: "d1", app_id: "a1", trigger: "manual", status: "live", commit_sha: "abc123def", image_tag: "img", created_at: "2026-07-19T00:00:00Z" }],
    },
    "GET /instance/info": { status: 200, body: { apps_domain_suffix: "apps.example.com" } },
  });
  renderPage(<AppOverview />, { path: "/apps/:appId", route: "/apps/a1" });
  expect(await screen.findByRole("heading", { name: "web" })).toBeInTheDocument();
  expect(await screen.findByText("web.apps.example.com")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /deploy/i })).toBeInTheDocument();
  expect(screen.getByText("live")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails** — `npx vitest run src/pages/AppOverview.test.tsx` → FAIL.

- [ ] **Step 3: Implement `AppOverview.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, post } from "../lib/api";
import { useApp, useInstanceInfo } from "../lib/hooks";
import { relativeTime } from "../lib/time";
import { isActive, type Deployment } from "../lib/types";
import { Badge, Button, Card, EmptyState, Icon, PageHeader, Skeleton, StatusDot, useToast } from "../components/ui";
import { StatusBadge } from "../components/StatusBadge";

export default function AppOverview() {
  const { appId } = useParams();
  const navigate = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();
  const { data: app, isLoading, error } = useApp(appId);
  const { data: info } = useInstanceInfo();
  const { data: deployments } = useQuery({
    queryKey: ["deployments", appId],
    queryFn: () => api<Deployment[]>(`/apps/${appId}/deployments`),
    enabled: !!appId,
    refetchInterval: (q) => (q.state.data?.some((d) => isActive(d.status)) ? 3000 : false),
  });
  const deploy = useMutation({
    mutationFn: () => post<Deployment>(`/apps/${appId}/deploy`),
    onSuccess: (dep) => {
      void qc.invalidateQueries({ queryKey: ["deployments", appId] });
      navigate(`/deployments/${dep.id}`);
    },
    onError: (e) => toast(e instanceof Error ? e.message : "deploy failed", "error"),
  });

  if (isLoading) {
    return (
      <div>
        <Skeleton className="mb-6 h-14" />
        <Skeleton className="h-40" />
      </div>
    );
  }
  if (error || !app) return <EmptyState title="Application not found" />;

  const latest = deployments?.[0];
  const url = info ? `${app.slug}.${info.apps_domain_suffix}` : null;

  return (
    <div>
      <PageHeader
        eyebrow="app"
        title={app.name}
        actions={
          <Button onClick={() => deploy.mutate()} disabled={deploy.isPending}>
            <Icon name="rocket" size={14} /> Deploy
          </Button>
        }
      />
      <Card className="corrugated">
        <div className="flex flex-wrap items-center gap-3">
          {latest ? <StatusDot status={latest.status} /> : null}
          <span className="font-display text-lg font-semibold">{latest ? latest.status : "not deployed"}</span>
          <Badge tone={app.source_type === "git" ? "amber" : "neutral"}>{app.source_type}</Badge>
          {latest && <span className="text-xs text-muted">deployed {relativeTime(latest.created_at)}</span>}
        </div>
        {url && (
          <a
            href={`https://${url}`}
            target="_blank"
            rel="noreferrer"
            className="mt-3 inline-flex items-center gap-1.5 font-mono text-sm text-terminal-blue hover:underline"
          >
            {url} <Icon name="external" size={13} />
          </a>
        )}
        <dl className="mt-4 grid grid-cols-1 gap-3 border-t border-border pt-4 sm:grid-cols-3">
          <div>
            <dt className="font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">Source</dt>
            <dd className="mt-1 truncate font-mono text-sm">
              {app.source_type === "git" ? `${app.git_repo_url} @ ${app.git_branch}` : app.image_ref}
            </dd>
          </div>
          <div>
            <dt className="font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">Port</dt>
            <dd className="mt-1 font-mono text-sm">{app.exposed_port}</dd>
          </div>
          <div>
            <dt className="font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">Auto-deploy</dt>
            <dd className="mt-1 font-mono text-sm">{app.auto_deploy ? "on" : "off"}</dd>
          </div>
        </dl>
      </Card>
      <div className="mt-6 flex items-center justify-between">
        <h2 className="font-display text-lg font-semibold">Recent deployments</h2>
        <Link to={`/apps/${app.id}/deployments`} className="inline-flex items-center gap-1 text-sm text-amber hover:underline">
          View all <Icon name="arrow-right" size={13} />
        </Link>
      </div>
      <div className="mt-3">
        {!deployments || deployments.length === 0 ? (
          <EmptyState title="No deployments yet" hint="Hit Deploy to ship the current configuration." />
        ) : (
          <div className="flex flex-col gap-2">
            {deployments.slice(0, 5).map((d) => (
              <Link key={d.id} to={`/deployments/${d.id}`}>
                <Card className="flex items-center gap-3 !p-3 transition-colors duration-150 hover:border-amber-dim">
                  <StatusDot status={d.status} />
                  <StatusBadge status={d.status} />
                  <span className="font-mono text-xs text-muted">{d.commit_sha.slice(0, 8) || "—"}</span>
                  <span className="ml-auto text-xs text-muted">{relativeTime(d.created_at)}</span>
                </Card>
              </Link>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Create the four thin page wrappers**

`AppDeployments.tsx`:
```tsx
import { useParams } from "react-router-dom";
import { useApp } from "../lib/hooks";
import { PageHeader, Skeleton, EmptyState } from "../components/ui";
import { DeploymentsTab } from "../components/DeploymentsTab";

export default function AppDeployments() {
  const { appId } = useParams();
  const { data: app, isLoading, error } = useApp(appId);
  if (isLoading) return <Skeleton className="h-64" />;
  if (error || !app) return <EmptyState title="Application not found" />;
  return (
    <div>
      <PageHeader eyebrow="app" title="Deployments" />
      <DeploymentsTab app={app} />
    </div>
  );
}
```
`AppEnv.tsx`, `AppDomains.tsx`, `AppSettings.tsx`: identical shape, titles "Environment"/"Domains"/"Settings", rendering `<EnvTab app={app} />`, `<DomainsTab app={app} />`, `<SettingsTab app={app} />`.

- [ ] **Step 5: Restyle the four tab components** (logic untouched)

Apply the mapping to `DeploymentsTab.tsx`, `EnvTab.tsx`, `DomainsTab.tsx`, `SettingsTab.tsx`:
- `DeploymentsTab`: keep the top-right Deploy button row (it is the tab's primary action — restyle with the new `Button`), replace rollback `window.confirm` with `ConfirmModal` (confirmLabel "Rollback", no requireText, body "Roll back to this deployment's image?"), links `text-indigo-400` → `text-amber`, table headers mono uppercase (`font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted`), skeleton loading instead of spinner.
- `DomainsTab`: replace local instance-info query with `useInstanceInfo()` from `lib/hooks`; status badges → tones (active=live, pending=amber, misconfigured=danger); auto-subdomain shown in mono with a copy button (`Icon name="copy"`, `toast("Copied")` on click).
- `EnvTab`: masked values stay; delete-key `window.confirm` if any → `ConfirmModal` (confirmLabel "Delete", no requireText); saved → `toast("Environment updated")`.
- `SettingsTab`: delete app `window.confirm` → `ConfirmModal requireText={app.name}`; saved → `toast("Settings saved")`.

- [ ] **Step 6: Delete `AppDetail.tsx`, wire routes, tests + gates**

`App.tsx`: replace the `/apps/:appId` route element with `AppOverview` and add the four sub-routes (imports included). Delete `src/pages/AppDetail.tsx`. Fix any lingering imports (`OrgSettings` used `useOrg` already fixed in Task 4).
Run: `npx vitest run src/pages src/components/DomainsTab.test.tsx src/components/layout && npm run lint && npx tsc -b`
Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add -A src
git commit -m "feat(web): app-scoped pages with overview hero and freight tabs"
```

---

### Task 11: Deployment logs terminal

**Files:**
- Rewrite: `web/src/pages/DeploymentLogs.tsx`
- Test: `web/src/pages/DeploymentLogs.test.tsx` (adapt existing)

**Interfaces:**
- Consumes: `StatusBadge`, `Button`, `Icon`, barrel; existing SSE logic.
- Produces: unchanged route `/deployments/:deploymentId`.

- [ ] **Step 1: Baseline** — `npx vitest run src/pages/DeploymentLogs.test.tsx`. Preserve: `EventSource` on `/api/v1/deployments/:id/logs`, line appending, auto-scroll, 3s status poll while active, failure banner. Tests use a mocked EventSource — keep constructor usage identical.

- [ ] **Step 2: Restyle as a terminal window**

Keep the component's state/effects identical; change the render to:
```tsx
<div>
  <PageHeader eyebrow="deployment" title={app?.name ?? "Logs"} actions={<StatusBadge status={deployment.status} />} />
  {deployment.status === "failed" && deployment.error && (
    <p className="mb-3 rounded-lg border border-danger/40 bg-danger-tint px-3 py-2 text-sm text-danger">{deployment.error}</p>
  )}
  <div className="overflow-hidden rounded-lg border border-border bg-terminal shadow-[0_24px_60px_-24px_rgba(0,0,0,0.7)]">
    <div className="flex items-center gap-1.5 border-b border-border px-3 py-2">
      <span className="h-2.5 w-2.5 rounded-full bg-danger/70" />
      <span className="h-2.5 w-2.5 rounded-full bg-amber/70" />
      <span className="h-2.5 w-2.5 rounded-full bg-live/70" />
      <span className="ml-2 font-mono text-xs text-muted">deploy logs</span>
      <Button variant="ghost" className="ml-auto !px-2 !py-1 text-xs" onClick={scrollToBottom}>
        Latest
      </Button>
    </div>
    <pre ref={logRef} className="h-[60vh] overflow-y-auto p-4 font-mono text-xs leading-relaxed text-text">
      {lines.join("")}
    </pre>
  </div>
</div>
```
(`scrollToBottom` = existing auto-scroll behavior extracted; auto-scroll still happens on new lines.)

- [ ] **Step 3: Tests + gates + commit**

Run: `npx vitest run src/pages/DeploymentLogs.test.tsx && npm run lint && npx tsc -b`
Commit: `style(web): terminal-style deployment logs`

---

### Task 12: Invite + Admin pages

**Files:**
- Rewrite: `web/src/pages/AcceptInvite.tsx`, `web/src/pages/Admin.tsx`
- Test: `web/src/pages/Admin.test.tsx` (adapt existing)

**Interfaces:**
- Consumes: barrel, `useToast`, `ContainerMark`.
- Produces: unchanged routes.

- [ ] **Step 1: `AcceptInvite.tsx`** — same auto-accept effect; render inside `AuthShell` ("Joining organization…" with `Spinner`, error state with danger text + link to `/`).

- [ ] **Step 2: Restyle `Admin.tsx`** — keep all forms/lists logic (instance settings, SMTP, GitHub App, OIDC, users, orgs). `PageHeader eyebrow="instance" title="Admin"`. Each section becomes a `Card` with a mono amber eyebrow heading (`font-mono text-[0.68rem] uppercase tracking-[0.14em] text-amber`) + Space Grotesk card title. All saves → `toast("Saved")`; deletes → `ConfirmModal`. Users/orgs tables: mono uppercase headers per mapping.

- [ ] **Step 3: Tests + gates + commit**

Run: `npx vitest run src/pages/Admin.test.tsx && npm run lint && npx tsc -b`
Commit: `style(web): freight admin and invite pages`

---

### Task 13: Cleanup, compat removal, full gates

**Files:**
- Modify: `web/src/components/ui/index.ts` (remove `PageTitle` shim), any remaining old-class usages.

- [ ] **Step 1: Remove compat shims**

Delete the `PageTitle` shim from `ui/index.ts` and the deprecated `color` prop (`LegacyColor`/`LEGACY`) from `Badge.tsx`. Grep for remaining usages and migrate to `PageHeader` / `tone=`:
```bash
grep -rn "PageTitle" src/ ; grep -rn "color=" src/components src/pages | grep -i badge ; grep -rn "slate-\|indigo-" src/ | grep -v test
```
Expected after fixes: no `PageTitle`, no `Badge color=`, no `slate-`/`indigo-` classes anywhere. Also grep for `window.confirm` — expected: zero hits.

- [ ] **Step 2: Full suite**

Run: `npm run lint && npx tsc -b && npx vitest run && npm run build`
Expected: lint clean, tsc clean, ALL tests pass, build emits to `../internal/webui/dist`.

- [ ] **Step 3: Commit**

```bash
git add -A src ../internal/webui/dist
git commit -m "feat(web): freight redesign complete — remove compat shims, rebuild dist"
```

---

### Task 14: Live verification

**Files:** none (verification only).

- [ ] **Step 1: Rebuild the controlplane image and restart the local stack**

```bash
cd /home/bograh/Code/cargo/deploy && docker compose up -d --build controlplane
```
Expected: controlplane rebuilt with new embedded UI and restarted.

- [ ] **Step 2: Verify**

```bash
curl -s http://localhost/ | grep -o "assets/index-[^\"]*\.js" | head -1
docker logs cargo-controlplane-1 --since 1m 2>&1 | tail -3
```
Expected: new asset hash served (matches the fresh `dist/index.html`), no server errors. Open http://localhost in a browser: freight theme, sidebar, login works.

- [ ] **Step 3: Commit remaining + push**

```bash
cd /home/bograh/Code/cargo && git status --short
git add -A && git commit -m "chore: refresh embedded webui dist" || true
```
(Only if dist artifacts changed outside Task 13.)

---

## Self-Review Notes (already applied)

- Spec coverage: theme (T1), primitives (T2–3), shell/sidebar (T4), auth (T5), all 12 routes (T5–T12), polish layer (toasts/skeletons/dots/modals/focus/responsive — distributed per task + T13 sweep), tests (T13), live verify (T14).
- Type consistency: `useOrg`/`useApp`/`useOrgs`/`useInstanceInfo` live in `lib/hooks.ts` from Task 4 on; every consumer task references that path. `Badge` uses `tone`; `StatusBadge` maps deployment statuses. `ConfirmModal` signature identical across all usage sites. Query keys unchanged (`["orgs"]`, `["apps", orgId]`, `["app", appId]`, `["deployments", appId]`, `["instance-info"]`) so caches stay coherent.
- `AppScopeNav` back-link is labeled "All apps" (test asserts this).
