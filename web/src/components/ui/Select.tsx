import type { SelectHTMLAttributes } from "react";
import { cn } from "../../lib/cn";

export function Select({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        "cargo-select rounded-lg border border-border bg-raised px-2 py-1.5 text-sm text-text focus:border-amber-dim focus:outline-none",
        className,
      )}
      {...props}
    />
  );
}
