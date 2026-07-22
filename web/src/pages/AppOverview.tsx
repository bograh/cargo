import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, post } from "../lib/api";
import { useApp, useInstanceInfo } from "../lib/hooks";
import { relativeTime } from "../lib/time";
import { isActive, type App, type Deployment } from "../lib/types";
import { Badge, Button, Card, EmptyState, Icon, PageHeader, Skeleton, StatusDot, useToast } from "../components/ui";
import { StatusBadge } from "../components/StatusBadge";

export default function AppOverview() {
  const { appId } = useParams();
  const navigate = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();
  const { data: app, isLoading, error } = useApp(appId);
  const { data: info } = useInstanceInfo();
  const { data: deployments } = useQuery({
    queryKey: ["deployments", appId],
    queryFn: () => api<Deployment[]>(`/apps/${appId}/deployments`),
    enabled: !!appId,
    refetchInterval: (q) => (q.state.data?.some((d) => isActive(d.status)) ? 3000 : false),
  });
  const deploy = useMutation({
    mutationFn: () => post<Deployment>(`/apps/${appId}/deploy`),
    onSuccess: (dep) => {
      void qc.invalidateQueries({ queryKey: ["deployments", appId] });
      navigate(`/deployments/${dep.id}`);
    },
    onError: (e) => toast(e instanceof Error ? e.message : "deploy failed", "error"),
  });
  const setRunState = (action: "stop" | "start") => ({
    mutationFn: () => post<App>(`/apps/${appId}/${action}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["app", appId] });
      toast(action === "stop" ? "Application stopped" : "Application started");
    },
    onError: (e: unknown) => toast(e instanceof Error ? e.message : `${action} failed`, "error"),
  });
  const stop = useMutation(setRunState("stop"));
  const start = useMutation(setRunState("start"));

  if (isLoading) {
    return (
      <div>
        <Skeleton className="mb-6 h-14" />
        <Skeleton className="h-40" />
      </div>
    );
  }
  if (error || !app) return <EmptyState title="Application not found" />;

  const latest = deployments?.[0];
  const url = info ? `${app.slug}.${info.apps_domain_suffix}` : null;
  const stopped = app.desired_state === "stopped";
  const deployed = !!latest;

  return (
    <div>
      <PageHeader
        eyebrow="app"
        title={app.name}
        back={{ to: `/orgs/${app.org_id}`, label: "All apps" }}
        actions={
          <>
            {stopped ? (
              <Button variant="secondary" onClick={() => start.mutate()} disabled={start.isPending || !deployed}>
                <Icon name="play" size={14} /> Start
              </Button>
            ) : (
              <Button variant="secondary" onClick={() => stop.mutate()} disabled={stop.isPending || !deployed}>
                <Icon name="stop" size={14} /> Stop
              </Button>
            )}
            <Button onClick={() => deploy.mutate()} disabled={deploy.isPending}>
              <Icon name="rocket" size={14} /> Deploy
            </Button>
          </>
        }
      />
      <Card className="corrugated">
        <div className="flex flex-wrap items-center gap-3">
          {stopped ? (
            <>
              <span className="inline-flex h-2 w-2 shrink-0 rounded-full bg-muted" />
              <span className="font-display text-lg font-semibold text-muted">stopped</span>
            </>
          ) : (
            <>
              {latest ? <StatusDot status={latest.status} /> : null}
              <span className="font-display text-lg font-semibold">{latest ? latest.status : "not deployed"}</span>
            </>
          )}
          <Badge tone={app.source_type === "git" ? "amber" : "neutral"}>{app.source_type}</Badge>
          {latest && !stopped && <span className="text-xs text-muted">deployed {relativeTime(latest.created_at)}</span>}
        </div>
        {url && (
          <a
            href={`https://${url}`}
            target="_blank"
            rel="noreferrer"
            className="mt-3 inline-flex items-center gap-1.5 font-mono text-sm text-terminal-blue hover:underline"
          >
            {url} <Icon name="external" size={13} />
          </a>
        )}
        <dl className="mt-4 grid grid-cols-1 gap-3 border-t border-border pt-4 sm:grid-cols-3">
          <div>
            <dt className="font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">Source</dt>
            <dd className="mt-1 truncate font-mono text-sm">
              {app.source_type === "git" ? `${app.git_repo_url} @ ${app.git_branch}` : app.image_ref}
            </dd>
          </div>
          <div>
            <dt className="font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">Port</dt>
            <dd className="mt-1 font-mono text-sm">{app.exposed_port}</dd>
          </div>
          <div>
            <dt className="font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">Auto-deploy</dt>
            <dd className="mt-1 font-mono text-sm">{app.auto_deploy ? "on" : "off"}</dd>
          </div>
        </dl>
      </Card>
      <div className="mt-6 flex items-center justify-between">
        <h2 className="font-display text-lg font-semibold">Recent deployments</h2>
        <Link to={`/apps/${app.id}/deployments`} className="inline-flex items-center gap-1 text-sm text-amber hover:underline">
          View all <Icon name="arrow-right" size={13} />
        </Link>
      </div>
      <div className="mt-3">
        {!deployments || deployments.length === 0 ? (
          <EmptyState title="No deployments yet" hint="Hit Deploy to ship the current configuration." />
        ) : (
          <div className="flex flex-col gap-2">
            {deployments.slice(0, 5).map((d) => (
              <Link key={d.id} to={`/deployments/${d.id}`}>
                <Card className="flex items-center gap-3 !p-3 transition-colors duration-150 hover:border-amber-dim">
                  <StatusDot status={d.status} />
                  <StatusBadge status={d.status} />
                  <span className="font-mono text-xs text-muted">{d.commit_sha.slice(0, 8) || "—"}</span>
                  <span className="ml-auto text-xs text-muted">{relativeTime(d.created_at)}</span>
                </Card>
              </Link>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
