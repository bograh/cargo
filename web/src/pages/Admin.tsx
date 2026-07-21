import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate, useSearchParams } from "react-router-dom";
import { api, del, put } from "../lib/api";
import { useAuth } from "../auth";
import type { User } from "../lib/types";
import { Badge, Button, Card, FieldError, Icon, Input, Label, PageHeader, Spinner, Textarea, useToast } from "../components/ui";

function SectionHeading({ eyebrow, title, aside }: { eyebrow: string; title: string; aside?: ReactNode }) {
  return (
    <div className="mb-3 flex items-start justify-between gap-2">
      <div>
        <div className="font-mono text-[0.68rem] uppercase tracking-[0.14em] text-amber">{eyebrow}</div>
        <h2 className="mt-0.5 font-display font-semibold">{title}</h2>
      </div>
      {aside}
    </div>
  );
}

interface InstanceSettings {
  apps_domain_suffix: string;
  smtp: { configured: boolean; host?: string; port?: number; username?: string; from?: string };
}

function InstanceSettingsCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const { data } = useQuery({
    queryKey: ["admin", "settings"],
    queryFn: () => api<InstanceSettings>("/admin/settings"),
  });
  const [suffix, setSuffix] = useState("");
  const [smtpHost, setSmtpHost] = useState("");
  const [smtpPort, setSmtpPort] = useState("587");
  const [smtpUser, setSmtpUser] = useState("");
  const [smtpPass, setSmtpPass] = useState("");
  const [smtpFrom, setSmtpFrom] = useState("");
  const [error, setError] = useState("");

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["admin", "settings"] });
    void qc.invalidateQueries({ queryKey: ["instance-info"] });
  };
  const onError = (err: unknown) => {
    const message = err instanceof Error ? err.message : "save failed";
    setError(message);
    toast(message, "error");
  };

  const saveSuffix = useMutation({
    mutationFn: () => put("/admin/settings/apps-domain-suffix", { suffix }),
    onSuccess: () => {
      setSuffix("");
      setError("");
      invalidate();
      toast("Saved");
    },
    onError,
  });
  const saveSmtp = useMutation({
    mutationFn: () =>
      put("/admin/settings/smtp", {
        host: smtpHost,
        port: Number(smtpPort),
        username: smtpUser,
        password: smtpPass,
        from: smtpFrom,
      }),
    onSuccess: () => {
      setSmtpPass("");
      setError("");
      invalidate();
      toast("Saved");
    },
    onError,
  });
  const clearSmtp = useMutation({
    mutationFn: () => del("/admin/settings/smtp"),
    onSuccess: () => {
      invalidate();
      toast("SMTP cleared");
    },
    onError,
  });

  return (
    <Card>
      <SectionHeading eyebrow="instance" title="Instance settings" />
      <div className="space-y-6">
        <form
          className="flex items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (suffix.trim()) saveSuffix.mutate();
          }}
        >
          <div className="flex-1">
            <Label htmlFor="suffix">
              Apps domain suffix — currently <code className="font-mono text-text">{data?.apps_domain_suffix}</code>
            </Label>
            <Input id="suffix" placeholder="apps.example.com" value={suffix} onChange={(e) => setSuffix(e.target.value)} />
          </div>
          <Button type="submit" disabled={saveSuffix.isPending || !suffix.trim()}>
            Save suffix
          </Button>
        </form>
        <p className="-mt-4 text-xs text-muted">Applies to new deployments; existing app URLs are unchanged.</p>

        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            saveSmtp.mutate();
          }}
        >
          <div className="flex items-center justify-between">
            <Label className="mb-0">SMTP (optional)</Label>
            {data?.smtp.configured ? (
              <span className="flex items-center gap-2">
                <Badge tone="live">configured — {data.smtp.host}:{data.smtp.port}</Badge>
                <Button type="button" variant="secondary" onClick={() => clearSmtp.mutate()}>
                  Clear
                </Button>
              </span>
            ) : (
              <Badge tone="neutral">not configured</Badge>
            )}
          </div>
          <div className="grid grid-cols-2 gap-3">
            <Input aria-label="smtp host" placeholder="smtp.example.com" value={smtpHost} onChange={(e) => setSmtpHost(e.target.value)} />
            <Input aria-label="smtp port" type="number" placeholder="587" value={smtpPort} onChange={(e) => setSmtpPort(e.target.value)} />
            <Input aria-label="smtp username" placeholder="username" value={smtpUser} onChange={(e) => setSmtpUser(e.target.value)} />
            <Input aria-label="smtp password" type="password" placeholder="password (write-only)" value={smtpPass} onChange={(e) => setSmtpPass(e.target.value)} />
            <Input aria-label="smtp from" placeholder="cargo@example.com" value={smtpFrom} onChange={(e) => setSmtpFrom(e.target.value)} className="col-span-2" />
          </div>
          <Button type="submit" disabled={saveSmtp.isPending || !smtpHost.trim()}>
            Save SMTP
          </Button>
        </form>
        <FieldError message={error} />
      </div>
    </Card>
  );
}

interface GithubAppStatus {
  configured: boolean;
  app_slug: string;
  app_id: number;
}

function GithubAppForm() {
  const qc = useQueryClient();
  const toast = useToast();
  const { data: status } = useQuery({
    queryKey: ["admin", "github-app"],
    queryFn: () => api<GithubAppStatus>("/admin/settings/github-app"),
  });
  const [appId, setAppId] = useState("");
  const [appSlug, setAppSlug] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [webhookSecret, setWebhookSecret] = useState("");
  const [error, setError] = useState("");
  const [manual, setManual] = useState(false);
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
      toast("Saved");
    },
    onError: (err) => {
      const message = err instanceof Error ? err.message : "save failed";
      setError(message);
      toast(message, "error");
    },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    save.mutate();
  }

  return (
    <Card>
      <SectionHeading
        eyebrow="integration"
        title="GitHub App"
        aside={
          status?.configured ? (
            <Badge tone="live">configured — {status.app_slug} (#{status.app_id})</Badge>
          ) : (
            <Badge tone="neutral">not configured</Badge>
          )
        }
      />
      <p className="mb-4 text-xs text-muted">
        Create a GitHub App for this instance. The one-click option sends a
        prefilled manifest to GitHub and captures the credentials automatically —
        no copy-pasting. They are stored encrypted; the key and secret are never
        shown again.
      </p>

      <a href="/api/v1/admin/settings/github-app/manifest">
        <Button>
          <Icon name="git-branch" size={14} /> Create GitHub App automatically
        </Button>
      </a>

      <button
        type="button"
        onClick={() => setManual((m) => !m)}
        className="mt-3 block text-xs text-muted hover:text-text"
      >
        {manual ? "Hide manual setup" : "or enter existing credentials manually"}
      </button>

      {manual && (
        <form onSubmit={submit} className="mt-4 space-y-3 border-t border-border pt-4">
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
            <Textarea
              id="gh-key"
              value={privateKey}
              onChange={(e) => setPrivateKey(e.target.value)}
              required
              rows={4}
              className="font-mono text-xs"
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
      )}
    </Card>
  );
}

interface OIDCStatus {
  configured: boolean;
  issuer_url: string;
  client_id: string;
}

function OIDCForm() {
  const qc = useQueryClient();
  const toast = useToast();
  const { data: status } = useQuery({
    queryKey: ["admin", "oidc"],
    queryFn: () => api<OIDCStatus>("/admin/settings/oidc"),
  });
  const [issuerUrl, setIssuerUrl] = useState("");
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [error, setError] = useState("");
  const invalidate = () => void qc.invalidateQueries({ queryKey: ["admin", "oidc"] });
  const save = useMutation({
    mutationFn: () =>
      put("/admin/settings/oidc", {
        issuer_url: issuerUrl,
        client_id: clientId,
        client_secret: clientSecret,
      }),
    onSuccess: () => {
      setClientSecret("");
      setError("");
      invalidate();
      toast("Saved");
    },
    onError: (err) => {
      const message = err instanceof Error ? err.message : "save failed";
      setError(message);
      toast(message, "error");
    },
  });
  const clear = useMutation({
    mutationFn: () => del("/admin/settings/oidc"),
    onSuccess: () => {
      invalidate();
      toast("SSO cleared");
    },
    onError: (err) => {
      const message = err instanceof Error ? err.message : "clear failed";
      setError(message);
      toast(message, "error");
    },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    save.mutate();
  }

  return (
    <Card>
      <SectionHeading
        eyebrow="auth"
        title="SSO (OIDC)"
        aside={
          status?.configured ? (
            <span className="flex items-center gap-2">
              <Badge tone="live">configured — {status.issuer_url}</Badge>
              <Button type="button" variant="secondary" onClick={() => clear.mutate()}>
                Clear
              </Button>
            </span>
          ) : (
            <Badge tone="neutral">not configured</Badge>
          )
        }
      />
      <p className="mb-4 text-xs text-muted">
        Register a confidential OIDC client at your identity provider with redirect URI{" "}
        <code className="font-mono text-text">{window.location.origin}/api/v1/auth/oidc/callback</code>. The client secret is stored
        encrypted and never shown again.
      </p>
      <form onSubmit={submit} className="space-y-3">
        <div>
          <Label htmlFor="oidc-issuer">Issuer URL</Label>
          <Input
            id="oidc-issuer"
            placeholder="https://idp.example.com/realms/cargo"
            value={issuerUrl}
            onChange={(e) => setIssuerUrl(e.target.value)}
            required
          />
        </div>
        <div className="grid grid-cols-2 gap-4">
          <div>
            <Label htmlFor="oidc-client-id">Client ID</Label>
            <Input id="oidc-client-id" value={clientId} onChange={(e) => setClientId(e.target.value)} required />
          </div>
          <div>
            <Label htmlFor="oidc-client-secret">Client secret</Label>
            <Input
              id="oidc-client-secret"
              type="password"
              placeholder="write-only"
              value={clientSecret}
              onChange={(e) => setClientSecret(e.target.value)}
              required
            />
          </div>
        </div>
        <FieldError message={error} />
        <Button type="submit" disabled={save.isPending}>
          Save OIDC
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
  const { user, loading } = useAuth();
  const isAdmin = !!user?.is_instance_admin;
  const qc = useQueryClient();
  const toast = useToast();
  const [params, setParams] = useSearchParams();

  // Surface the outcome of the GitHub App manifest round-trip.
  useEffect(() => {
    const result = params.get("github");
    if (!result) return;
    if (result === "connected") {
      toast("GitHub App connected");
      void qc.invalidateQueries({ queryKey: ["admin", "github-app"] });
    } else if (result === "error") {
      toast("GitHub App setup failed", "error");
    }
    params.delete("github");
    setParams(params, { replace: true });
  }, [params, setParams, toast, qc]);

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

  if (loading) {
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  }
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
    <div className="space-y-6">
      <PageHeader eyebrow="instance" title="Admin" />
      <InstanceSettingsCard />
      <GithubAppForm />
      <OIDCForm />
      <Card>
        <SectionHeading eyebrow="people" title="Users" />
        <ul className="space-y-2 text-sm">
          {users?.map((u) => (
            <li key={u.id} className="flex items-center justify-between border-t border-border pt-2 first:border-0 first:pt-0">
              <span>{u.email}</span>
              {u.is_instance_admin && <Badge tone="amber">instance admin</Badge>}
            </li>
          ))}
        </ul>
      </Card>
      <Card>
        <SectionHeading eyebrow="tenants" title="Organizations" />
        <ul className="space-y-2 text-sm">
          {orgs?.map((o) => (
            <li key={o.id} className="flex items-center justify-between border-t border-border pt-2 first:border-0 first:pt-0">
              <span>{o.name}</span>
              <span className="font-mono text-muted">{o.slug}</span>
            </li>
          ))}
        </ul>
      </Card>
    </div>
  );
}
