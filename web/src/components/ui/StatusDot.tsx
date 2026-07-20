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
