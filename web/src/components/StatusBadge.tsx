import type { Deployment } from "../lib/types";
import { Badge, type BadgeTone } from "./ui/Badge";

const TONES: Record<Deployment["status"], BadgeTone> = {
  queued: "amber",
  building: "amber",
  deploying: "amber",
  live: "live",
  superseded: "neutral",
  failed: "danger",
  cancelled: "neutral",
};

export function StatusBadge({ status }: { status: Deployment["status"] }) {
  return <Badge tone={TONES[status] ?? "neutral"}>{status}</Badge>;
}
