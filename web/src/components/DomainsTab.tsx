import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, del, post } from "../lib/api";
import { useInstanceInfo } from "../lib/hooks";
import type { App } from "../lib/types";
import { Badge, Button, Card, Icon, Input, Skeleton, useToast, type BadgeTone } from "./ui";

interface Domain {
  id: string;
  hostname: string;
  status: "active" | "pending" | "misconfigured";
}

const STATUS_TONES: Record<Domain["status"], BadgeTone> = {
  active: "live",
  pending: "amber",
  misconfigured: "danger",
};

export function DomainsTab({ app }: { app: App }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [hostname, setHostname] = useState("");
  const [error, setError] = useState("");

  const { data: info } = useInstanceInfo();
  const { data: domains, isLoading } = useQuery({
    queryKey: ["domains", app.id],
    queryFn: () => api<Domain[]>(`/apps/${app.id}/domains`),
  });

  const invalidate = () => void qc.invalidateQueries({ queryKey: ["domains", app.id] });
  const add = useMutation({
    mutationFn: () => post(`/apps/${app.id}/domains`, { hostname }),
    onSuccess: () => {
      setHostname("");
      setError("");
      invalidate();
    },
    onError: (err) => {
      const message = err instanceof Error ? err.message : "attach failed";
      setError(message);
      toast(message, "error");
    },
  });
  const remove = useMutation({
    mutationFn: (id: string) => del(`/apps/${app.id}/domains/${id}`),
    onSuccess: invalidate,
    onError: (err) => {
      const message = err instanceof Error ? err.message : "remove failed";
      setError(message);
      toast(message, "error");
    },
  });

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-24" />
        <Skeleton className="h-40" />
      </div>
    );
  }
  const autoDomain = `${app.slug}.${info?.apps_domain_suffix ?? "apps.localhost"}`;

  return (
    <div className="space-y-6">
      <Card>
        <h2 className="mb-3 font-display font-semibold">Auto subdomain</h2>
        <div className="flex items-center justify-between gap-2 text-sm">
          <a
            href={`https://${autoDomain}`}
            target="_blank"
            rel="noreferrer"
            className="truncate font-mono text-terminal-blue hover:underline"
          >
            {autoDomain}
          </a>
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              aria-label="copy subdomain"
              onClick={() => {
                void navigator.clipboard?.writeText(autoDomain);
                toast("Copied");
              }}
            >
              <Icon name="copy" size={14} />
            </Button>
            <Badge tone="amber">automatic</Badge>
          </div>
        </div>
      </Card>

      <Card>
        <h2 className="mb-3 font-display font-semibold">Custom domains</h2>
        <p className="mb-3 text-xs text-muted">
          Point the domain's DNS at this server, attach it here, then redeploy — the router picks it up on
          the next deploy. SSL is issued automatically.
        </p>
        {domains && domains.length > 0 ? (
          <ul className="mb-4 space-y-2">
            {domains.map((d) => (
              <li key={d.id} className="flex items-center justify-between text-sm">
                <span className="flex items-center gap-2">
                  <code className="font-mono text-text">{d.hostname}</code>
                  <Badge tone={STATUS_TONES[d.status] ?? "neutral"}>{d.status}</Badge>
                </span>
                <Button variant="secondary" onClick={() => remove.mutate(d.id)}>
                  Remove
                </Button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="mb-4 text-sm text-muted">No custom domains attached.</p>
        )}
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (hostname.trim()) add.mutate();
          }}
        >
          <Input
            aria-label="custom domain"
            placeholder="app.yourdomain.com"
            value={hostname}
            onChange={(e) => setHostname(e.target.value)}
          />
          <Button type="submit" disabled={add.isPending || !hostname.trim()}>
            Attach
          </Button>
        </form>
        {error && <p className="mt-2 text-sm text-danger">{error}</p>}
      </Card>
    </div>
  );
}
