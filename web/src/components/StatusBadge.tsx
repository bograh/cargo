import type { Deployment } from "../lib/types";
import { Badge } from "./ui";

const COLORS: Record<Deployment["status"], "green" | "amber" | "red" | "gray"> = {
  queued: "amber",
  building: "amber",
  deploying: "amber",
  live: "green",
  failed: "red",
  cancelled: "gray",
};

export function StatusBadge({ status }: { status: Deployment["status"] }) {
  return <Badge color={COLORS[status] ?? "gray"}>{status}</Badge>;
}
