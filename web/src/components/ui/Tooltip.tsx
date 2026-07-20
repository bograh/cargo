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
