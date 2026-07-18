import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { api } from "../lib/api";
import { isActive, type Deployment } from "../lib/types";
import { StatusBadge } from "../components/StatusBadge";
import { PageTitle, Spinner } from "../components/ui";

export default function DeploymentLogs() {
  const { deploymentId } = useParams();
  const [lines, setLines] = useState<string[]>([]);
  const bottomRef = useRef<HTMLDivElement>(null);

  const { data: dep } = useQuery({
    queryKey: ["deployment", deploymentId],
    queryFn: () => api<Deployment>(`/deployments/${deploymentId}`),
    refetchInterval: (query) => (query.state.data && !isActive(query.state.data.status) ? false : 3000),
    enabled: !!deploymentId,
  });

  useEffect(() => {
    if (!deploymentId) return;
    setLines([]);
    const es = new EventSource(`/api/v1/deployments/${deploymentId}/logs`);
    es.onmessage = (e) => setLines((prev) => [...prev, e.data]);
    es.onerror = () => es.close();
    return () => es.close();
  }, [deploymentId]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView?.({ behavior: "smooth" });
  }, [lines.length]);

  return (
    <div>
      {dep && (
        <div className="mb-1 text-sm">
          <Link to={`/apps/${dep.app_id}`} className="text-slate-500 hover:text-slate-300">
            ← Back to app
          </Link>
        </div>
      )}
      <PageTitle actions={dep ? <StatusBadge status={dep.status} /> : <Spinner />}>Deployment logs</PageTitle>
      {dep?.status === "failed" && dep.error && (
        <p className="mb-3 rounded-md border border-red-900 bg-red-950/50 p-3 text-sm text-red-300">{dep.error}</p>
      )}
      <div
        data-testid="log-output"
        className="h-[32rem] overflow-y-auto rounded-lg border border-slate-800 bg-black p-4 font-mono text-xs leading-5 text-slate-300"
      >
        {lines.length === 0 ? (
          <p className="text-slate-600">Waiting for logs…</p>
        ) : (
          lines.map((line, i) => <div key={i}>{line}</div>)
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}
