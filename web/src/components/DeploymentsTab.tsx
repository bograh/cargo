import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { api, post } from "../lib/api";
import { isActive, type App, type Deployment } from "../lib/types";
import { StatusBadge } from "./StatusBadge";
import { Button, ConfirmModal, EmptyState, Icon, Skeleton, StatusDot, useToast } from "./ui";

export function DeploymentsTab({ app }: { app: App }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const [rollbackTarget, setRollbackTarget] = useState<Deployment | null>(null);
  const { data: deployments, isLoading } = useQuery({
    queryKey: ["deployments", app.id],
    queryFn: () => api<Deployment[]>(`/apps/${app.id}/deployments`),
    refetchInterval: (query) =>
      query.state.data?.some((d) => isActive(d.status)) ? 3000 : false,
  });

  const deploy = useMutation({
    mutationFn: () => post<Deployment>(`/apps/${app.id}/deploy`),
    onSuccess: (dep) => {
      void qc.invalidateQueries({ queryKey: ["deployments", app.id] });
      navigate(`/deployments/${dep.id}`);
    },
    onError: (err) => toast(err instanceof Error ? err.message : "deploy failed", "error"),
  });
  const rollback = useMutation({
    mutationFn: (targetID: string) =>
      post<Deployment>(`/apps/${app.id}/rollback`, { deployment_id: targetID }),
    onSuccess: (dep) => {
      setRollbackTarget(null);
      void qc.invalidateQueries({ queryKey: ["deployments", app.id] });
      navigate(`/deployments/${dep.id}`);
    },
    onError: (err) => toast(err instanceof Error ? err.message : "rollback failed", "error"),
  });

  if (isLoading) {
    return (
      <div className="space-y-2">
        <Skeleton className="h-10" />
        <Skeleton className="h-10" />
        <Skeleton className="h-10" />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-4 flex justify-end">
        <Button onClick={() => deploy.mutate()} disabled={deploy.isPending}>
          <Icon name="rocket" size={14} /> Deploy
        </Button>
      </div>
      {!deployments || deployments.length === 0 ? (
        <EmptyState title="No deployments yet" hint="Hit Deploy to ship the current configuration." />
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">
              <th className="pb-2">Status</th>
              <th className="pb-2">Trigger</th>
              <th className="pb-2">Commit</th>
              <th className="pb-2">Image</th>
              <th className="pb-2" />
            </tr>
          </thead>
          <tbody>
            {deployments.map((d) => (
              <tr key={d.id} className="border-t border-border">
                <td className="py-2">
                  <span className="flex items-center gap-2">
                    <StatusDot status={d.status} />
                    <StatusBadge status={d.status} />
                  </span>
                  {d.status === "failed" && d.error && (
                    <p className="mt-1 max-w-xs truncate text-xs text-danger">{d.error}</p>
                  )}
                </td>
                <td className="py-2 text-muted">{d.trigger}</td>
                <td className="py-2 font-mono text-xs text-muted">{d.commit_sha.slice(0, 8) || "—"}</td>
                <td className="max-w-40 truncate py-2 font-mono text-xs text-muted">{d.image_tag || "—"}</td>
                <td className="space-x-2 whitespace-nowrap py-2 text-right">
                  <Link to={`/deployments/${d.id}`} className="text-amber hover:underline">
                    Logs
                  </Link>
                  {(d.status === "live" || d.status === "superseded") && d.image_tag && (
                    <Button variant="secondary" onClick={() => setRollbackTarget(d)}>
                      Rollback
                    </Button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {rollbackTarget && (
        <ConfirmModal
          title="Roll back deployment?"
          body="Roll back to this deployment's image?"
          confirmLabel="Rollback"
          busy={rollback.isPending}
          onConfirm={() => rollback.mutate(rollbackTarget.id)}
          onClose={() => setRollbackTarget(null)}
        />
      )}
    </div>
  );
}
