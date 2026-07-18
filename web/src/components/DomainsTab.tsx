import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, del, post } from "../lib/api";
import type { App } from "../lib/types";
import { Badge, Button, Card, Input, Spinner } from "./ui";

interface Domain {
  id: string;
  hostname: string;
  status: "active" | "pending" | "misconfigured";
}

const STATUS_COLORS = { active: "green", pending: "amber", misconfigured: "red" } as const;

export function DomainsTab({ app }: { app: App }) {
  const qc = useQueryClient();
  const [hostname, setHostname] = useState("");
  const [error, setError] = useState("");

  const { data: info } = useQuery({
    queryKey: ["instance-info"],
    queryFn: () => api<{ apps_domain_suffix: string }>("/instance/info"),
  });
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
    onError: (err) => setError(err instanceof Error ? err.message : "attach failed"),
  });
  const remove = useMutation({
    mutationFn: (id: string) => del(`/apps/${app.id}/domains/${id}`),
    onSuccess: invalidate,
    onError: (err) => setError(err instanceof Error ? err.message : "remove failed"),
  });

  if (isLoading) {
    return (
      <div className="flex justify-center py-10">
        <Spinner />
      </div>
    );
  }
  const autoDomain = `${app.slug}.${info?.apps_domain_suffix ?? "apps.localhost"}`;

  return (
    <div className="space-y-6">
      <Card>
        <h2 className="mb-3 font-semibold">Auto subdomain</h2>
        <div className="flex items-center justify-between text-sm">
          <a
            href={`https://${autoDomain}`}
            target="_blank"
            rel="noreferrer"
            className="text-indigo-400 hover:underline"
          >
            {autoDomain}
          </a>
          <Badge color="indigo">automatic</Badge>
        </div>
      </Card>

      <Card>
        <h2 className="mb-3 font-semibold">Custom domains</h2>
        <p className="mb-3 text-xs text-slate-500">
          Point the domain's DNS at this server, attach it here, then redeploy — the router picks it up on
          the next deploy. SSL is issued automatically.
        </p>
        {domains && domains.length > 0 ? (
          <ul className="mb-4 space-y-2">
            {domains.map((d) => (
              <li key={d.id} className="flex items-center justify-between text-sm">
                <span className="flex items-center gap-2">
                  <code className="text-slate-300">{d.hostname}</code>
                  <Badge color={STATUS_COLORS[d.status] ?? "gray"}>{d.status}</Badge>
                </span>
                <Button variant="secondary" onClick={() => remove.mutate(d.id)}>
                  Remove
                </Button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="mb-4 text-sm text-slate-500">No custom domains attached.</p>
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
        {error && <p className="mt-2 text-sm text-red-400">{error}</p>}
      </Card>
    </div>
  );
}
