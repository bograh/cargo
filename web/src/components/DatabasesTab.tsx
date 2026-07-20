import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, del, post } from "../lib/api";
import type { App, DatabaseAttachment, DatabaseDetail, DatabaseInstance, Snapshot } from "../lib/types";
import {
  Badge,
  Button,
  Card,
  ConfirmModal,
  FieldError,
  Icon,
  Input,
  Label,
  Modal,
  Select,
  Skeleton,
  StatusDot,
  useToast,
  type BadgeTone,
} from "./ui";

const STATUS_TONES: Record<string, BadgeTone> = {
  running: "live",
  provisioning: "amber",
  error: "danger",
  stopped: "neutral",
};

const POSTGRES_VERSIONS = ["16", "17"];
const REDIS_VERSIONS = ["7"];

function formatSize(bytes?: number) {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let n = bytes;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(1)} ${units[i]}`;
}

export function DatabasesTab({ orgId }: { orgId: string }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState("");
  const [engine, setEngine] = useState<"postgres" | "redis">("postgres");
  const [version, setVersion] = useState("16");
  const [redisMode, setRedisMode] = useState("acl");
  const [exposePort, setExposePort] = useState(false);
  const [formError, setFormError] = useState("");
  const [oneTimeUrl, setOneTimeUrl] = useState<{ url: string; envKey: string } | null>(null);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const { data: instances, isLoading } = useQuery({
    queryKey: ["databases", orgId],
    queryFn: () => api<DatabaseInstance[]>(`/orgs/${orgId}/databases`),
    enabled: !!orgId,
  });

  const invalidateList = () => void qc.invalidateQueries({ queryKey: ["databases", orgId] });

  const create = useMutation({
    mutationFn: () =>
      post(`/orgs/${orgId}/databases`, {
        name,
        engine,
        version,
        ...(engine === "redis" ? { redis_mode: redisMode } : {}),
        expose_port: exposePort,
      }),
    onSuccess: () => {
      setName("");
      setExposePort(false);
      setFormError("");
      invalidateList();
      toast("Database provisioning started");
    },
    onError: (err) => {
      const message = err instanceof Error ? err.message : "provision failed";
      setFormError(message);
      toast(message, "error");
    },
  });

  function onEngineChange(next: "postgres" | "redis") {
    setEngine(next);
    setVersion(next === "postgres" ? POSTGRES_VERSIONS[0] : REDIS_VERSIONS[0]);
  }

  return (
    <div className="space-y-6">
      <Card>
        <h2 className="mb-3 font-display font-semibold">Provision a database</h2>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (name.trim()) create.mutate();
          }}
        >
          <div>
            <Label htmlFor="db-name">Name</Label>
            <Input
              id="db-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-database"
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label htmlFor="db-engine">Engine</Label>
              <Select
                id="db-engine"
                className="w-full"
                value={engine}
                onChange={(e) => onEngineChange(e.target.value as "postgres" | "redis")}
              >
                <option value="postgres">postgres</option>
                <option value="redis">redis</option>
              </Select>
            </div>
            <div>
              <Label htmlFor="db-version">Version</Label>
              <Select id="db-version" className="w-full" value={version} onChange={(e) => setVersion(e.target.value)}>
                {(engine === "postgres" ? POSTGRES_VERSIONS : REDIS_VERSIONS).map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </Select>
            </div>
          </div>
          {engine === "redis" && (
            <div>
              <Label htmlFor="db-redis-mode">Redis mode</Label>
              <Select id="db-redis-mode" className="w-full" value={redisMode} onChange={(e) => setRedisMode(e.target.value)}>
                <option value="acl">acl (per-app users, isolated)</option>
                <option value="shared">shared (one password, indexes only)</option>
              </Select>
            </div>
          )}
          <div>
            <label className="flex items-center gap-2 text-sm text-muted">
              <input
                type="checkbox"
                checked={exposePort}
                onChange={(e) => setExposePort(e.target.checked)}
              />
              Expose a host port
            </label>
            {exposePort && (
              <p className="mt-1 text-xs text-amber">
                Warning: exposing a host port makes this database reachable outside the deploy network.
                Only enable this if you understand the security implications.
              </p>
            )}
          </div>
          <Button type="submit" disabled={create.isPending || !name.trim()}>
            Provision
          </Button>
          <FieldError message={formError} />
        </form>
      </Card>

      {isLoading ? (
        <div className="space-y-4">
          <Skeleton className="h-32" />
          <Skeleton className="h-32" />
        </div>
      ) : !instances || instances.length === 0 ? (
        <p className="text-sm text-muted">No databases provisioned yet.</p>
      ) : (
        <div className="space-y-4">
          {instances.map((inst) => (
            <InstanceCard
              key={inst.id}
              orgId={orgId}
              instance={inst}
              expanded={expandedId === inst.id}
              onToggle={() => setExpandedId(expandedId === inst.id ? null : inst.id)}
              onOneTimeUrl={setOneTimeUrl}
              invalidateList={invalidateList}
            />
          ))}
        </div>
      )}

      {oneTimeUrl && <OneTimeUrlModal {...oneTimeUrl} onClose={() => setOneTimeUrl(null)} />}
    </div>
  );
}

function OneTimeUrlModal({
  url,
  envKey,
  onClose,
}: {
  url: string;
  envKey: string;
  onClose: () => void;
}) {
  const toast = useToast();
  return (
    <Modal title="Connection URL" onClose={onClose} wide>
      <p className="mb-3 text-sm text-muted">
        Injected into the attached app as <code className="font-mono text-text">{envKey}</code>.
      </p>
      <div className="mb-3 flex items-center gap-2">
        <code className="flex-1 truncate rounded-md bg-terminal px-2 py-1.5 font-mono text-xs text-text">{url}</code>
        <Button
          variant="secondary"
          onClick={() => {
            void navigator.clipboard?.writeText(url);
            toast("Copied to clipboard");
          }}
        >
          <Icon name="copy" size={14} /> Copy
        </Button>
      </div>
      <p className="mb-4 text-sm font-medium text-amber">
        Save this now — it will not be shown again.
      </p>
      <div className="flex justify-end">
        <Button onClick={onClose}>Close</Button>
      </div>
    </Modal>
  );
}

function InstanceCard({
  orgId,
  instance,
  expanded,
  onToggle,
  onOneTimeUrl,
  invalidateList,
}: {
  orgId: string;
  instance: DatabaseInstance;
  expanded: boolean;
  onToggle: () => void;
  onOneTimeUrl: (v: { url: string; envKey: string }) => void;
  invalidateList: () => void;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const [showDelete, setShowDelete] = useState(false);
  const [selectedAppId, setSelectedAppId] = useState("");
  const [error, setError] = useState("");

  const { data: detail } = useQuery({
    queryKey: ["database", instance.id],
    queryFn: () => api<DatabaseDetail>(`/databases/${instance.id}`),
    enabled: expanded,
  });
  const { data: apps } = useQuery({
    queryKey: ["apps", orgId],
    queryFn: () => api<App[]>(`/orgs/${orgId}/apps`),
    enabled: expanded,
  });
  const { data: snapshots } = useQuery({
    queryKey: ["database-snapshots", instance.id],
    queryFn: () => api<Snapshot[]>(`/databases/${instance.id}/snapshots`),
    enabled: expanded,
  });

  const invalidateDetail = () => void qc.invalidateQueries({ queryKey: ["database", instance.id] });
  const invalidateSnapshots = () =>
    void qc.invalidateQueries({ queryKey: ["database-snapshots", instance.id] });

  function reportError(err: unknown, fallback: string) {
    const message = err instanceof Error ? err.message : fallback;
    setError(message);
    toast(message, "error");
  }

  const attach = useMutation({
    mutationFn: (appId: string) =>
      post<{ url: string; env_key: string }>(`/databases/${instance.id}/attachments`, { app_id: appId }),
    onSuccess: (res) => {
      setSelectedAppId("");
      setError("");
      onOneTimeUrl({ url: res.url, envKey: res.env_key });
      invalidateDetail();
      invalidateList();
    },
    onError: (err) => reportError(err, "attach failed"),
  });
  const detach = useMutation({
    mutationFn: (appId: string) => del(`/databases/${instance.id}/attachments/${appId}`),
    onSuccess: () => {
      invalidateDetail();
      invalidateList();
    },
    onError: (err) => reportError(err, "detach failed"),
  });
  const takeSnapshot = useMutation({
    mutationFn: () => post(`/databases/${instance.id}/snapshots`),
    onSuccess: () => {
      invalidateSnapshots();
      toast("Snapshot created");
    },
    onError: (err) => reportError(err, "snapshot failed"),
  });
  const deleteSnapshot = useMutation({
    mutationFn: (snapshotName: string) => del(`/databases/${instance.id}/snapshots/${snapshotName}`),
    onSuccess: invalidateSnapshots,
    onError: (err) => reportError(err, "delete snapshot failed"),
  });
  const deleteInstance = useMutation({
    mutationFn: () => del(`/databases/${instance.id}`),
    onSuccess: () => {
      setShowDelete(false);
      invalidateList();
      toast("Database deleted");
    },
    onError: (err) => reportError(err, "delete failed"),
  });

  const attachedAppIds = new Set((detail?.attachments ?? []).map((a: DatabaseAttachment) => a.app_id));
  const availableApps = (apps ?? []).filter((a) => !attachedAppIds.has(a.id));
  const appNameById = new Map((apps ?? []).map((a) => [a.id, a.name]));

  const dotStatus =
    instance.status === "running" ? "live" : instance.status === "error" ? "failed" : "building";

  return (
    <Card>
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <StatusDot status={dotStatus} />
          <h3 className="font-display font-semibold text-text">{instance.name}</h3>
          <Badge tone="amber">{instance.engine}</Badge>
          <span className="text-xs text-muted">v{instance.version}</span>
          <Badge tone={STATUS_TONES[instance.status] ?? "neutral"}>{instance.status}</Badge>
        </div>
        <Button variant="secondary" onClick={onToggle}>
          {expanded ? "Hide" : "Manage"}
        </Button>
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted">
        {detail && <span>{formatSize(detail.size_bytes)}</span>}
        {instance.host_port && <span>host port {instance.host_port}</span>}
        {(detail?.attachments ?? []).map((a) => (
          <Badge key={a.app_id} tone="neutral">
            {appNameById.get(a.app_id) ?? a.app_id}
          </Badge>
        ))}
      </div>

      {expanded && (
        <div className="mt-4 space-y-4 border-t border-border pt-4">
          <div>
            <h4 className="mb-2 text-sm font-medium text-text">Attach to an app</h4>
            <div className="flex gap-2">
              <Select
                aria-label="attach app"
                value={selectedAppId}
                onChange={(e) => setSelectedAppId(e.target.value)}
              >
                <option value="">Select an app…</option>
                {availableApps.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
              </Select>
              <Button
                onClick={() => selectedAppId && attach.mutate(selectedAppId)}
                disabled={!selectedAppId || attach.isPending}
              >
                Attach
              </Button>
            </div>
            {(detail?.attachments ?? []).length > 0 && (
              <ul className="mt-2 space-y-1">
                {(detail?.attachments ?? []).map((a) => (
                  <li key={a.app_id} className="flex items-center justify-between text-sm">
                    <span>{appNameById.get(a.app_id) ?? a.app_id}</span>
                    <Button variant="secondary" onClick={() => detach.mutate(a.app_id)}>
                      Detach
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div>
            <div className="mb-2 flex items-center justify-between">
              <h4 className="text-sm font-medium text-text">Snapshots</h4>
              <Button
                variant="secondary"
                onClick={() => takeSnapshot.mutate()}
                disabled={takeSnapshot.isPending}
              >
                Take snapshot
              </Button>
            </div>
            {snapshots && snapshots.length > 0 ? (
              <ul className="space-y-1">
                {snapshots.map((s) => (
                  <li key={s.name} className="flex items-center justify-between text-sm">
                    <a
                      href={`/api/v1/databases/${instance.id}/snapshots/${s.name}`}
                      className="font-mono text-amber hover:underline"
                    >
                      {s.name}
                    </a>
                    <span className="text-xs text-muted">{formatSize(s.size)}</span>
                    <Button variant="danger" onClick={() => deleteSnapshot.mutate(s.name)}>
                      Delete
                    </Button>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-sm text-muted">No snapshots yet.</p>
            )}
          </div>

          <FieldError message={error} />

          <div className="border-t border-border pt-4">
            <Button variant="danger" onClick={() => setShowDelete(true)}>
              <Icon name="trash" size={14} /> Delete database
            </Button>
          </div>
        </div>
      )}

      {showDelete && (
        <ConfirmModal
          title={`Delete ${instance.name}?`}
          body="This destroys the instance, its volume, and all snapshots. Attached apps lose their credentials on next deploy."
          requireText={instance.name}
          busy={deleteInstance.isPending}
          onConfirm={() => deleteInstance.mutate()}
          onClose={() => setShowDelete(false)}
        />
      )}
    </Card>
  );
}
