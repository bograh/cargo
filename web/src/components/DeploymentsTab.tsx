import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { api, post } from "../lib/api";
import { isActive, type App, type Deployment } from "../lib/types";
import { StatusBadge } from "./StatusBadge";
import { Button, EmptyState, Spinner } from "./ui";

export function DeploymentsTab({ app }: { app: App }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
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
  });
  const rollback = useMutation({
    mutationFn: (targetID: string) =>
      post<Deployment>(`/apps/${app.id}/rollback`, { deployment_id: targetID }),
    onSuccess: (dep) => {
      void qc.invalidateQueries({ queryKey: ["deployments", app.id] });
      navigate(`/deployments/${dep.id}`);
    },
  });

  if (isLoading) {
    return (
      <div className="flex justify-center py-10">
        <Spinner />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-4 flex justify-end">
        <Button onClick={() => deploy.mutate()} disabled={deploy.isPending}>
          Deploy
        </Button>
      </div>
      {(deploy.error || rollback.error) && (
        <p className="mb-2 text-sm text-red-400">
          {(deploy.error ?? rollback.error) instanceof Error
            ? (deploy.error ?? rollback.error)?.message
            : "operation failed"}
        </p>
      )}
      {!deployments || deployments.length === 0 ? (
        <EmptyState title="No deployments yet" hint="Hit Deploy to ship the current configuration." />
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-slate-500">
              <th className="pb-2">Status</th>
              <th className="pb-2">Trigger</th>
              <th className="pb-2">Commit</th>
              <th className="pb-2">Image</th>
              <th className="pb-2" />
            </tr>
          </thead>
          <tbody>
            {deployments.map((d) => (
              <tr key={d.id} className="border-t border-slate-800">
                <td className="py-2">
                  <StatusBadge status={d.status} />
                  {d.status === "failed" && d.error && (
                    <p className="mt-1 max-w-xs truncate text-xs text-red-400">{d.error}</p>
                  )}
                </td>
                <td className="py-2 text-slate-400">{d.trigger}</td>
                <td className="py-2 font-mono text-xs text-slate-400">{d.commit_sha.slice(0, 8) || "—"}</td>
                <td className="py-2 font-mono text-xs text-slate-400 truncate max-w-40">{d.image_tag || "—"}</td>
                <td className="py-2 text-right space-x-2 whitespace-nowrap">
                  <Link to={`/deployments/${d.id}`} className="text-indigo-400 hover:underline">
                    Logs
                  </Link>
                  {d.status === "live" && d.image_tag && (
                    <Button
                      variant="secondary"
                      onClick={() => {
                        if (window.confirm("Roll back to this deployment's image?")) {
                          rollback.mutate(d.id);
                        }
                      }}
                    >
                      Rollback
                    </Button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
