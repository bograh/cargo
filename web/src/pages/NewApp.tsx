import { useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { post, put } from "../lib/api";
import type { App, Deployment } from "../lib/types";
import { Button, Card, FieldError, Input, Label, PageTitle, Select } from "../components/ui";

interface EnvRow {
  key: string;
  value: string;
}

export default function NewApp() {
  const { orgId } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();

  const [name, setName] = useState("");
  const [sourceType, setSourceType] = useState<"git" | "image">("git");
  const [gitRepoURL, setGitRepoURL] = useState("");
  const [gitBranch, setGitBranch] = useState("main");
  const [imageRef, setImageRef] = useState("");
  const [builder, setBuilder] = useState("auto");
  const [port, setPort] = useState(8080);
  const [healthPath, setHealthPath] = useState("/");
  const [autoDeploy, setAutoDeploy] = useState(true);
  const [envRows, setEnvRows] = useState<EnvRow[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

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
      setError(err instanceof Error ? err.message : "failed to create app");
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto max-w-2xl">
      <PageTitle>New App</PageTitle>
      <form onSubmit={submit} className="space-y-6">
        <Card className="space-y-4">
          <div>
            <Label htmlFor="app-name">Name</Label>
            <Input id="app-name" value={name} onChange={(e) => setName(e.target.value)} required placeholder="my-api" />
          </div>
          <div>
            <Label>Source</Label>
            <div className="flex gap-2">
              {(["git", "image"] as const).map((t) => (
                <Button
                  key={t}
                  type="button"
                  variant={sourceType === t ? "primary" : "secondary"}
                  onClick={() => setSourceType(t)}
                >
                  {t === "git" ? "Git repository" : "Container image"}
                </Button>
              ))}
            </div>
          </div>
          {sourceType === "git" ? (
            <>
              <div>
                <Label htmlFor="repo">Repository URL (public)</Label>
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
                  <Input id="branch" value={gitBranch} onChange={(e) => setGitBranch(e.target.value)} required />
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
              <Input id="health" value={healthPath} onChange={(e) => setHealthPath(e.target.value)} />
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm text-slate-300">
            <input type="checkbox" checked={autoDeploy} onChange={(e) => setAutoDeploy(e.target.checked)} />
            Auto-deploy on push (takes effect once GitHub integration is connected)
          </label>
        </Card>

        <Card>
          <div className="mb-3 flex items-center justify-between">
            <h2 className="font-semibold">Environment variables</h2>
            <Button type="button" variant="secondary" onClick={() => setEnvRows((r) => [...r, { key: "", value: "" }])}>
              Add variable
            </Button>
          </div>
          {envRows.length === 0 && <p className="text-sm text-slate-500">No variables yet.</p>}
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
                <Button type="button" variant="secondary" onClick={() => setEnvRows((r) => r.filter((_, j) => j !== i))}>
                  ✕
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
