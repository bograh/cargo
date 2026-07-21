import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { Icon } from "./Icon";

export function PageHeader({
  eyebrow,
  title,
  actions,
  back,
}: {
  eyebrow?: string;
  title: ReactNode;
  actions?: ReactNode;
  back?: { to: string; label: string };
}) {
  return (
    <div className="glass sticky top-0 z-20 -mx-4 mb-6 border-b border-border px-4 py-3 sm:-mx-6 sm:px-6">
      {back && (
        <Link
          to={back.to}
          className="mb-1 inline-flex items-center gap-1 text-xs text-muted transition-colors duration-150 hover:text-text"
        >
          <Icon name="arrow-left" size={13} /> {back.label}
        </Link>
      )}
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
