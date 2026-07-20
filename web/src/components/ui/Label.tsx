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
