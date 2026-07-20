import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router-dom";
import { api } from "../lib/api";
import { useApp } from "../lib/hooks";
import { isActive, type Deployment } from "../lib/types";
import { StatusBadge } from "../components/StatusBadge";
import { Button, PageHeader, Spinner } from "../components/ui";

export default function DeploymentLogs() {
  const { deploymentId } = useParams();
  const [lines, setLines] = useState<string[]>([]);
  const logRef = useRef<HTMLPreElement>(null);

  const { data: dep } = useQuery({
    queryKey: ["deployment", deploymentId],
    queryFn: () => api<Deployment>(`/deployments/${deploymentId}`),
    refetchInterval: (query) => (query.state.data && !isActive(query.state.data.status) ? false : 3000),
    enabled: !!deploymentId,
  });
  const { data: app } = useApp(dep?.app_id);

  const scrollToBottom = useCallback(() => {
    const el = logRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, []);

  useEffect(() => {
    if (!deploymentId) return;
    setLines([]);
    const es = new EventSource(`/api/v1/deployments/${deploymentId}/logs`);
    es.onmessage = (e) => setLines((prev) => [...prev, e.data]);
    es.onerror = () => es.close();
    return () => es.close();
  }, [deploymentId]);

  useEffect(() => {
    scrollToBottom();
  }, [lines.length, scrollToBottom]);

  return (
    <div>
      <PageHeader
        eyebrow="deployment"
        title={app?.name ?? "Logs"}
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
