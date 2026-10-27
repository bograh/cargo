import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { openStream } from "../lib/sse";
import { useApp } from "../lib/hooks";
import { isActive, type Deployment } from "../lib/types";
import { StatusBadge } from "../components/StatusBadge";
import { Button, EmptyState, PageHeader, Spinner } from "../components/ui";

// The server ends a finished deployment's stream with this marker.
const END_MARKER = /^\[deployment (live|failed|cancelled|superseded)\]$/;

export default function DeploymentLogs() {
  const { deploymentId } = useParams();
  const [lines, setLines] = useState<string[]>([]);
  const logRef = useRef<HTMLPreElement>(null);

  const { data: dep, error } = useQuery({
    queryKey: ["deployment", deploymentId],
    queryFn: () => api<Deployment>(`/deployments/${deploymentId}`),
    // Poll while the deployment is in flight; stop once it settles or the
    // request fails (a 404 never turns into a deployment).
    refetchInterval: (query) => {
      if (query.state.error) return false;
      const data = query.state.data;
      return data && !isActive(data.status) ? false : 3000;
    },
    enabled: !!deploymentId,
  });
  // Read by the stream's reconnect check without reopening the stream.
  const settledRef = useRef(false);
  settledRef.current = !!dep && !isActive(dep.status);
  const { data: app } = useApp(dep?.app_id);

  const scrollToBottom = useCallback(() => {
    const el = logRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, []);

  useEffect(() => {
    if (!deploymentId) return;
    setLines([]);
    let ended = false;
    return openStream(`/api/v1/deployments/${deploymentId}/logs`, {
      // Every connection replays the log from the start.
      onOpen: () => setLines([]),
      onMessage: (line) => {
        if (END_MARKER.test(line)) ended = true;
        setLines((prev) => [...prev, line]);
      },
      // Keep following an in-flight deploy across drops (session refresh,
      // network blips); stop once it has finished.
      shouldReconnect: () => !ended && !settledRef.current,
    });
  }, [deploymentId]);

  useEffect(() => {
    scrollToBottom();
  }, [lines.length, scrollToBottom]);

  if (error) {
    return (
      <EmptyState
        title={error instanceof ApiError && error.status === 404 ? "Deployment not found" : "Could not load deployment"}
      />
    );
  }

  return (
    <div>
      <PageHeader
        eyebrow="deployment"
        title={app?.name ?? "Logs"}
        back={dep ? { to: `/apps/${dep.app_id}`, label: app?.name ?? "App" } : undefined}
        actions={dep ? <StatusBadge status={dep.status} /> : <Spinner />}
      />
      {dep?.status === "failed" && dep.error && (
        <p className="mb-3 rounded-lg border border-danger/40 bg-danger-tint px-3 py-2 text-sm text-danger">{dep.error}</p>
      )}
      <div className="overflow-hidden rounded-lg border border-border bg-terminal shadow-[0_24px_60px_-24px_rgba(0,0,0,0.7)]">
        <div className="flex items-center gap-1.5 border-b border-border px-3 py-2">
          <span className="h-2.5 w-2.5 rounded-full bg-danger/70" />
          <span className="h-2.5 w-2.5 rounded-full bg-amber/70" />
          <span className="h-2.5 w-2.5 rounded-full bg-live/70" />
          <span className="ml-2 font-mono text-xs text-muted">deploy logs</span>
          <Button variant="ghost" className="ml-auto !px-2 !py-1 text-xs" onClick={scrollToBottom}>
            Latest
          </Button>
        </div>
        <pre
          ref={logRef}
          data-testid="log-output"
          className="h-[60vh] overflow-y-auto p-4 font-mono text-xs leading-relaxed text-text"
        >
          {lines.length === 0 ? (
            <span className="text-muted">Waiting for logs…</span>
          ) : (
            lines.map((line, i) => <div key={i}>{line}</div>)
          )}
        </pre>
      </div>
    </div>
  );
}
