import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { del, patch } from "../lib/api";
import type { App } from "../lib/types";
import { Button, Card, Checkbox, ConfirmModal, FieldError, Icon, Input, Label, Select, useToast } from "./ui";

export function SettingsTab({ app }: { app: App }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const [name, setName] = useState(app.name);
  const [branch, setBranch] = useState(app.git_branch);
  const [imageRef, setImageRef] = useState(app.image_ref);
  const [builder, setBuilder] = useState(app.builder);
  const [port, setPort] = useState(app.exposed_port);
  const [healthPath, setHealthPath] = useState(app.healthcheck_path);
  const [autoDeploy, setAutoDeploy] = useState(app.auto_deploy);
  const [memLimit, setMemLimit] = useState(app.mem_limit ?? "");
  const [cpuLimit, setCpuLimit] = useState(app.cpu_limit ?? "");
  const [pidsLimit, setPidsLimit] = useState(app.pids_limit != null ? String(app.pids_limit) : "");
  const [notifyOnSuccess, setNotifyOnSuccess] = useState(app.notify_on_success);
  const [deployStrategy, setDeployStrategy] = useState<string>(app.deploy_strategy ?? "");
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState(false);

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
        mem_limit: memLimit.trim(),
        cpu_limit: cpuLimit.trim(),
        pids_limit: pidsLimit.trim() === "" ? 0 : Number(pidsLimit),
        notify_on_success: notifyOnSuccess,
        deploy_strategy: deployStrategy,
      }),
    onSuccess: () => {
      toast("Settings saved");
      void qc.invalidateQueries({ queryKey: ["app", app.id] });
    },
    onError: (err) => {
      const message = err instanceof Error ? err.message : "save failed";
      setError(message);
      toast(message, "error");
    },
  });
  const remove = useMutation({
    mutationFn: () => del(`/apps/${app.id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["apps", app.org_id] });
      toast("Application deleted");
      navigate(`/orgs/${app.org_id}`);
    },
    onError: (err) => {
      const message = err instanceof Error ? err.message : "delete failed";
      setError(message);
      toast(message, "error");
    },
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
              <Input id="s-health" value={healthPath} onChange={(e) => setHealthPath(e.target.value)} placeholder="/health (optional)" />
              <p className="mt-1 text-xs text-muted">Blank = live once the container is up; set a path to also require an HTTP check.</p>
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm text-muted">
            <Checkbox checked={autoDeploy} onChange={(e) => setAutoDeploy(e.target.checked)} />
            Auto-deploy on push
          </label>
          <label className="flex items-center gap-2 text-sm text-muted">
            <Checkbox checked={notifyOnSuccess} onChange={(e) => setNotifyOnSuccess(e.target.checked)} />
            Notify on successful deploy (failures always notify)
          </label>
          <div className="border-t border-border pt-4">
            <h3 className="text-sm font-semibold">Deploy strategy</h3>
            <p className="mt-1 text-xs text-muted">
              Zero-downtime starts the new version alongside the running one and only switches
              traffic once it passes its healthcheck; a version that never gets healthy leaves the
              current one serving. Recreate replaces the container in place — pick it if the app
              can't tolerate two instances running at once.
            </p>
            <Select
              id="s-strategy"
              aria-label="Deploy strategy"
              className="mt-3 w-full"
              value={deployStrategy}
              onChange={(e) => setDeployStrategy(e.target.value)}
            >
              <option value="">Instance default</option>
              <option value="bluegreen">Zero-downtime (blue/green)</option>
              <option value="recreate">Recreate (brief downtime)</option>
            </Select>
            {deployStrategy === "bluegreen" && healthPath.trim() === "" && (
              <p className="mt-2 text-xs text-amber">
                Set a healthcheck path so traffic only reaches the new version once it's ready.
              </p>
            )}
          </div>
          <div className="border-t border-border pt-4">
            <h3 className="text-sm font-semibold">Resource limits</h3>
            <p className="mt-1 text-xs text-muted">
              Caps per app container. Leave blank to use the instance defaults. Applied on the next deploy.
            </p>
            <div className="mt-3 grid grid-cols-3 gap-4">
              <div>
                <Label htmlFor="s-mem">Memory</Label>
                <Input id="s-mem" value={memLimit} onChange={(e) => setMemLimit(e.target.value)} placeholder="512m" />
              </div>
              <div>
                <Label htmlFor="s-cpu">CPUs</Label>
                <Input id="s-cpu" value={cpuLimit} onChange={(e) => setCpuLimit(e.target.value)} placeholder="1" />
              </div>
              <div>
                <Label htmlFor="s-pids">Max processes</Label>
                <Input
                  id="s-pids"
                  type="number"
                  min={1}
                  value={pidsLimit}
                  onChange={(e) => setPidsLimit(e.target.value)}
                  placeholder="512"
                />
              </div>
            </div>
          </div>
          <FieldError message={error} />
          <Button type="submit" disabled={update.isPending}>
            Save changes
          </Button>
        </form>
      </Card>

      <Card className="border-danger/40">
        <h2 className="font-display text-lg font-semibold text-danger">Danger zone</h2>
        <p className="mt-1 text-sm text-muted">
          Deleting the app stops its containers and removes its deployments and logs.
        </p>
        <Button variant="danger" className="mt-4" onClick={() => setConfirming(true)}>
          <Icon name="trash" size={14} /> Delete application
        </Button>
      </Card>

      {confirming && (
        <ConfirmModal
          title={`Delete ${app.name}?`}
          body="This stops the app's containers and removes its deployments and logs."
          requireText={app.name}
          busy={remove.isPending}
          onConfirm={() => remove.mutate()}
          onClose={() => setConfirming(false)}
        />
      )}
    </div>
  );
}
