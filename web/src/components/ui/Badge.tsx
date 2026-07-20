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
