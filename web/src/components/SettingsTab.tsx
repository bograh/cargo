import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { del, patch } from "../lib/api";
import type { App } from "../lib/types";
import { Button, Card, FieldError, Input, Label, Select } from "./ui";

export function SettingsTab({ app }: { app: App }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [name, setName] = useState(app.name);
  const [branch, setBranch] = useState(app.git_branch);
  const [imageRef, setImageRef] = useState(app.image_ref);
  const [builder, setBuilder] = useState(app.builder);
  const [port, setPort] = useState(app.exposed_port);
  const [healthPath, setHealthPath] = useState(app.healthcheck_path);
  const [autoDeploy, setAutoDeploy] = useState(app.auto_deploy);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  const update = useMutation({
    mutationFn: () =>
      patch(`/apps/${app.id}`, {
        name,
        git_branch: branch,
        image_ref: imageRef,
        builder,
        exposed_port: port,
        healthcheck_path: healthPath,
        auto_deploy: autoDeploy,
      }),
    onSuccess: () => {
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
      void qc.invalidateQueries({ queryKey: ["app", app.id] });
    },
    onError: (err) => setError(err instanceof Error ? err.message : "save failed"),
  });
  const remove = useMutation({
    mutationFn: () => del(`/apps/${app.id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["apps", app.org_id] });
      navigate(`/orgs/${app.org_id}`);
    },
    onError: (err) => setError(err instanceof Error ? err.message : "delete failed"),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    update.mutate();
  }

  return (
    <div className="space-y-6">
      <Card>
        <form onSubmit={submit} className="space-y-4">
          <div>
            <Label htmlFor="s-name">Name</Label>
            <Input id="s-name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          {app.source_type === "git" ? (
            <div className="grid grid-cols-2 gap-4">
              <div>
                <Label htmlFor="s-branch">Branch</Label>
                <Input id="s-branch" value={branch} onChange={(e) => setBranch(e.target.value)} />
              </div>
              <div>
                <Label htmlFor="s-builder">Builder</Label>
                <Select id="s-builder" className="w-full" value={builder} onChange={(e) => setBuilder(e.target.value)}>
                  <option value="auto">Auto-detect</option>
                  <option value="dockerfile">Dockerfile</option>
                  <option value="nixpacks">Nixpacks</option>
                </Select>
              </div>
            </div>
          ) : (
            <div>
              <Label htmlFor="s-image">Image reference</Label>
              <Input id="s-image" value={imageRef} onChange={(e) => setImageRef(e.target.value)} />
            </div>
          )}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <Label htmlFor="s-port">Exposed port</Label>
              <Input
                id="s-port"
                type="number"
                min={1}
                max={65535}
                value={port}
                onChange={(e) => setPort(Number(e.target.value))}
              />
            </div>
            <div>
              <Label htmlFor="s-health">Healthcheck path</Label>
              <Input id="s-health" value={healthPath} onChange={(e) => setHealthPath(e.target.value)} />
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm text-slate-300">
            <input type="checkbox" checked={autoDeploy} onChange={(e) => setAutoDeploy(e.target.checked)} />
            Auto-deploy on push
          </label>
          <FieldError message={error} />
          <div className="flex items-center gap-3">
            <Button type="submit" disabled={update.isPending}>
              Save changes
            </Button>
            {saved && <span className="text-sm text-green-400">Saved — applies on next deploy</span>}
          </div>
        </form>
      </Card>

      <Card className="border-red-900">
        <h2 className="mb-2 font-semibold text-red-400">Danger zone</h2>
        <p className="mb-3 text-sm text-slate-400">
          Deleting the app stops its containers and removes its deployments and logs.
        </p>
        <Button
          variant="danger"
          onClick={() => {
            if (window.confirm(`Delete ${app.name}? This cannot be undone.`)) {
              remove.mutate();
            }
          }}
        >
          Delete application
        </Button>
      </Card>
    </div>
  );
}
