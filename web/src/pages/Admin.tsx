import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate } from "react-router-dom";
import { api, put } from "../lib/api";
import { useAuth } from "../auth";
import type { User } from "../lib/types";
import { Badge, Button, Card, FieldError, Input, Label, PageTitle, Spinner } from "../components/ui";

interface GithubAppStatus {
  configured: boolean;
  app_slug: string;
  app_id: number;
}

function GithubAppForm() {
  const qc = useQueryClient();
  const { data: status } = useQuery({
    queryKey: ["admin", "github-app"],
    queryFn: () => api<GithubAppStatus>("/admin/settings/github-app"),
  });
  const [appId, setAppId] = useState("");
  const [appSlug, setAppSlug] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [webhookSecret, setWebhookSecret] = useState("");
  const [error, setError] = useState("");
  const save = useMutation({
    mutationFn: () =>
      put("/admin/settings/github-app", {
        app_id: Number(appId),
        app_slug: appSlug,
        private_key: privateKey,
        webhook_secret: webhookSecret,
      }),
    onSuccess: () => {
      setPrivateKey("");
      setWebhookSecret("");
      void qc.invalidateQueries({ queryKey: ["admin", "github-app"] });
    },
    onError: (err) => setError(err instanceof Error ? err.message : "save failed"),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    save.mutate();
  }

  return (
    <Card>
      <div className="mb-3 flex items-center justify-between">
        <h2 className="font-semibold">GitHub App</h2>
        {status?.configured ? (
          <Badge color="green">configured — {status.app_slug} (#{status.app_id})</Badge>
        ) : (
          <Badge color="gray">not configured</Badge>
        )}
      </div>
      <p className="mb-4 text-xs text-slate-500">
        Create a GitHub App with repository read access and push webhooks pointed at{" "}
        <code>{window.location.origin}/api/v1/webhooks/github</code>, then paste its credentials here.
        They are stored encrypted; the key and secret are never shown again.
      </p>
      <form onSubmit={submit} className="space-y-3">
        <div className="grid grid-cols-2 gap-4">
          <div>
            <Label htmlFor="gh-id">App ID</Label>
            <Input id="gh-id" type="number" value={appId} onChange={(e) => setAppId(e.target.value)} required />
          </div>
          <div>
            <Label htmlFor="gh-slug">App slug</Label>
            <Input id="gh-slug" value={appSlug} onChange={(e) => setAppSlug(e.target.value)} required />
          </div>
        </div>
        <div>
          <Label htmlFor="gh-key">Private key (PEM)</Label>
          <textarea
            id="gh-key"
            value={privateKey}
            onChange={(e) => setPrivateKey(e.target.value)}
            required
            rows={4}
            className="w-full rounded-md bg-slate-900 border border-slate-700 px-3 py-1.5 font-mono text-xs text-slate-100 focus:outline-none focus:border-indigo-500"
          />
        </div>
        <div>
          <Label htmlFor="gh-secret">Webhook secret</Label>
          <Input
            id="gh-secret"
            type="password"
            value={webhookSecret}
            onChange={(e) => setWebhookSecret(e.target.value)}
            required
          />
        </div>
        <FieldError message={error} />
        <Button type="submit" disabled={save.isPending}>
          Save GitHub App
        </Button>
      </form>
    </Card>
  );
}

interface AdminOrg {
  id: string;
  name: string;
  slug: string;
}

export default function Admin() {
  const { user } = useAuth();
  const isAdmin = !!user?.is_instance_admin;
  const { data: users, isLoading } = useQuery({
    queryKey: ["admin", "users"],
    queryFn: () => api<User[]>("/admin/users"),
    enabled: isAdmin,
  });
  const { data: orgs } = useQuery({
    queryKey: ["admin", "orgs"],
    queryFn: () => api<AdminOrg[]>("/admin/orgs"),
    enabled: isAdmin,
  });

  if (!isAdmin) {
    return <Navigate to="/" replace />;
  }
  if (isLoading) {
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  }

  return (
    <div className="space-y-8">
      <PageTitle>Instance administration</PageTitle>
      <GithubAppForm />
      <Card>
        <h2 className="mb-3 font-semibold">Users</h2>
        <ul className="space-y-2 text-sm">
          {users?.map((u) => (
            <li key={u.id} className="flex items-center justify-between border-t border-slate-800 pt-2 first:border-0 first:pt-0">
              <span>{u.email}</span>
              {u.is_instance_admin && <Badge color="indigo">instance admin</Badge>}
            </li>
          ))}
        </ul>
      </Card>
      <Card>
        <h2 className="mb-3 font-semibold">Organizations</h2>
        <ul className="space-y-2 text-sm">
          {orgs?.map((o) => (
            <li key={o.id} className="flex items-center justify-between border-t border-slate-800 pt-2 first:border-0 first:pt-0">
              <span>{o.name}</span>
              <span className="text-slate-500">{o.slug}</span>
            </li>
          ))}
        </ul>
      </Card>
    </div>
  );
}
