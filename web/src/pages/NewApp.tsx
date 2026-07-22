import { useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, post, put } from "../lib/api";
import { cn } from "../lib/cn";
import { useOrg } from "../lib/hooks";
import type { App, Deployment } from "../lib/types";
import { Button, Card, Checkbox, FieldError, Icon, Input, Label, PageHeader, Select, useToast } from "../components/ui";

interface GithubRepo {
  full_name: string;
  clone_url: string;
  default_branch: string;
}

interface EnvRow {
  key: string;
  value: string;
}

export default function NewApp() {
  const { orgId } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const { data: orgData } = useOrg(orgId);

  const [name, setName] = useState("");
  const [sourceType, setSourceType] = useState<"git" | "image">("git");
  const [gitRepoURL, setGitRepoURL] = useState("");
  const [gitBranch, setGitBranch] = useState("main");
  const [imageRef, setImageRef] = useState("");
  const [builder, setBuilder] = useState("auto");
  const [port, setPort] = useState(8080);
  const [healthPath, setHealthPath] = useState("");
  const [autoDeploy, setAutoDeploy] = useState(true);
  const [envRows, setEnvRows] = useState<EnvRow[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [selectedRepo, setSelectedRepo] = useState("");

  const { data: gh } = useQuery({
    queryKey: ["github", orgId],
    queryFn: () => api<{ connected: boolean }>(`/orgs/${orgId}/github`),
  });
  const connected = !!gh?.connected;
  const { data: repos } = useQuery({
    queryKey: ["github-repos", orgId],
    queryFn: () => api<GithubRepo[]>(`/orgs/${orgId}/github/repos`),
    enabled: connected && sourceType === "git",
  });
  const { data: branches } = useQuery({
    queryKey: ["github-branches", orgId, selectedRepo],
    queryFn: () => api<string[]>(`/orgs/${orgId}/github/repos/${selectedRepo}/branches`),
    enabled: connected && !!selectedRepo,
  });

  function pickRepo(fullName: string) {
    setSelectedRepo(fullName);
    const repo = repos?.find((r) => r.full_name === fullName);
    if (repo) {
      setGitRepoURL(repo.clone_url);
      setGitBranch(repo.default_branch);
      if (!name) setName(fullName.split("/")[1] ?? "");
    }
  }

  function setEnvRow(i: number, patch: Partial<EnvRow>) {
    setEnvRows((rows) => rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const app = await post<App>(`/orgs/${orgId}/apps`, {
        name,
        source_type: sourceType,
        git_repo_url: sourceType === "git" ? gitRepoURL : "",
        git_branch: sourceType === "git" ? gitBranch : "",
        image_ref: sourceType === "image" ? imageRef : "",
        builder: sourceType === "git" ? builder : "auto",
        exposed_port: port,
        healthcheck_path: healthPath,
        auto_deploy: autoDeploy,
      });
      const vars = Object.fromEntries(
        envRows.filter((r) => r.key.trim()).map((r) => [r.key.trim(), r.value]),
      );
      if (Object.keys(vars).length > 0) {
        await put(`/apps/${app.id}/env`, { vars });
      }
      const dep = await post<Deployment>(`/apps/${app.id}/deploy`);
      void qc.invalidateQueries({ queryKey: ["apps", orgId] });
      navigate(`/deployments/${dep.id}`);
    } catch (err) {
      const message = err instanceof Error ? err.message : "failed to create app";
      setError(message);
      toast(message, "error");
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto max-w-2xl">
      <PageHeader eyebrow={orgData?.organization.name ?? "org"} title="New App" />
      <form onSubmit={submit} className="space-y-6">
        <Card className="space-y-4">
          <div>
            <Label htmlFor="app-name">Name</Label>
            <Input id="app-name" value={name} onChange={(e) => setName(e.target.value)} required placeholder="my-api" />
          </div>
          <div>
            <Label>Source</Label>
            <div className="flex rounded-lg border border-border bg-raised p-0.5">
              {(["git", "image"] as const).map((s) => (
                <button
                  key={s}
                  type="button"
                  onClick={() => setSourceType(s)}
                  className={cn(
                    "flex-1 rounded-md px-3 py-1.5 text-sm capitalize transition-colors duration-150",
                    sourceType === s ? "bg-amber-tint text-amber" : "text-muted hover:text-text",
                  )}
                >
                  {s === "git" ? "Git repository" : "Container image"}
                </button>
              ))}
            </div>
          </div>
          {sourceType === "git" ? (
            <>
              {connected && (
                <div>
                  <Label htmlFor="gh-repo">GitHub repository</Label>
                  <Select
                    id="gh-repo"
                    className="w-full"
                    value={selectedRepo}
                    onChange={(e) => pickRepo(e.target.value)}
                  >
                    <option value="">Choose from connected account…</option>
                    {repos?.map((r) => (
                      <option key={r.full_name} value={r.full_name}>
                        {r.full_name}
                      </option>
                    ))}
                  </Select>
                </div>
              )}
              <div>
                <Label htmlFor="repo">{connected ? "…or repository URL" : "Repository URL (public)"}</Label>
                <Input
                  id="repo"
                  value={gitRepoURL}
                  onChange={(e) => setGitRepoURL(e.target.value)}
                  placeholder="https://github.com/acme/api.git"
                  required
                />
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <Label htmlFor="branch">Branch</Label>
                  {connected && selectedRepo && branches ? (
                    <Select
                      id="branch"
                      className="w-full"
                      value={gitBranch}
                      onChange={(e) => setGitBranch(e.target.value)}
                    >
                      {branches.map((b) => (
                        <option key={b} value={b}>
                          {b}
                        </option>
                      ))}
                    </Select>
                  ) : (
                    <Input id="branch" value={gitBranch} onChange={(e) => setGitBranch(e.target.value)} required />
                  )}
                </div>
                <div>
                  <Label htmlFor="builder">Builder</Label>
                  <Select id="builder" className="w-full" value={builder} onChange={(e) => setBuilder(e.target.value)}>
                    <option value="auto">Auto-detect</option>
                    <option value="dockerfile">Dockerfile</option>
                    <option value="nixpacks">Nixpacks</option>
                  </Select>
                </div>
              </div>
            </>
          ) : (
            <div>
              <Label htmlFor="image">Image reference</Label>
              <Input
                id="image"
                value={imageRef}
                onChange={(e) => setImageRef(e.target.value)}
                placeholder="ghcr.io/acme/api:latest"
                required
              />
            </div>
          )}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <Label htmlFor="port">Exposed port</Label>
              <Input
                id="port"
                type="number"
                min={1}
                max={65535}
                value={port}
                onChange={(e) => setPort(Number(e.target.value))}
                required
              />
            </div>
            <div>
              <Label htmlFor="health">Healthcheck path</Label>
              <Input id="health" value={healthPath} onChange={(e) => setHealthPath(e.target.value)} placeholder="/health (optional)" />
              <p className="mt-1 text-xs text-muted">Optional. Leave blank to mark live once the container is up; set a path to also require an HTTP 2xx–4xx.</p>
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm text-muted">
            <Checkbox checked={autoDeploy} onChange={(e) => setAutoDeploy(e.target.checked)} />
            Auto-deploy on push (takes effect once GitHub integration is connected)
          </label>
        </Card>

        <Card>
          <div className="mb-3 flex items-center justify-between">
            <h2 className="font-display font-semibold">Environment variables</h2>
            <Button type="button" variant="secondary" onClick={() => setEnvRows((r) => [...r, { key: "", value: "" }])}>
              <Icon name="plus" size={14} /> Add variable
            </Button>
          </div>
          {envRows.length === 0 && <p className="text-sm text-muted">No variables yet.</p>}
          <div className="space-y-2">
            {envRows.map((row, i) => (
              <div key={i} className="flex gap-2">
                <Input
                  aria-label={`env key ${i}`}
                  placeholder="KEY"
                  value={row.key}
                  onChange={(e) => setEnvRow(i, { key: e.target.value })}
                />
                <Input
                  aria-label={`env value ${i}`}
                  placeholder="value"
                  value={row.value}
                  onChange={(e) => setEnvRow(i, { value: e.target.value })}
                />
                <Button
                  type="button"
                  variant="ghost"
                  aria-label={`remove env ${i}`}
                  onClick={() => setEnvRows((r) => r.filter((_, j) => j !== i))}
                >
                  <Icon name="trash" size={14} />
                </Button>
              </div>
            ))}
          </div>
        </Card>

        <FieldError message={error} />
        <Button type="submit" disabled={busy || !name.trim()}>
          {busy ? "Creating…" : "Create & Deploy"}
        </Button>
      </form>
    </div>
  );
}
