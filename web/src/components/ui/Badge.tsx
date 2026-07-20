import type { HTMLAttributes } from "react";
import { cn } from "../../lib/cn";

export type BadgeTone = "amber" | "live" | "danger" | "neutral";

const TONES: Record<BadgeTone, string> = {
  amber: "bg-amber-tint text-amber border-[color-mix(in_srgb,var(--color-amber)_35%,transparent)]",
  live: "bg-live-tint text-live border-[color-mix(in_srgb,var(--color-live)_35%,transparent)]",
  danger: "bg-danger-tint text-danger border-[color-mix(in_srgb,var(--color-danger)_35%,transparent)]",
  neutral: "bg-raised text-muted border-border",
};

export function Badge({
  tone = "neutral",
  className,
  ...props
}: HTMLAttributes<HTMLSpanElement> & { tone?: BadgeTone }) {
  const resolved: BadgeTone = tone;
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
