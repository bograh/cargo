import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { api } from "../lib/api";
import { useOrg } from "../lib/hooks";
import { relativeTime } from "../lib/time";
import { isActive, type App, type Deployment } from "../lib/types";
import { Badge, Button, Card, EmptyState, Icon, PageHeader, Skeleton, StatusDot } from "../components/ui";

function AppCard({ app }: { app: App }) {
  const { data: deployments } = useQuery({
    queryKey: ["deployments", app.id],
    queryFn: () => api<Deployment[]>(`/apps/${app.id}/deployments`),
    refetchInterval: (q) => (q.state.data?.some((d) => isActive(d.status)) ? 3000 : false),
  });
  const latest = deployments?.[0];
  return (
    <Link to={`/apps/${app.id}`}>
      <Card className="h-full transition-all duration-150 hover:-translate-y-0.5 hover:border-amber-dim">
        <div className="flex items-center justify-between gap-2">
          <h3 className="truncate font-display font-semibold">{app.name}</h3>
          <Badge tone={app.source_type === "git" ? "amber" : "neutral"}>{app.source_type}</Badge>
        </div>
        <p className="mt-1 truncate font-mono text-xs text-muted">{app.slug}</p>
        <div className="mt-3 flex items-center gap-2 text-xs text-muted">
          {latest ? (
            <>
              <StatusDot status={latest.status} />
              <span>{latest.status}</span>
              <span aria-hidden="true">·</span>
              <span>{relativeTime(latest.created_at)}</span>
            </>
          ) : (
            <span>not deployed yet</span>
          )}
        </div>
        <p className="mt-2 truncate font-mono text-xs text-muted">
          {app.source_type === "git" ? `${app.git_repo_url} @ ${app.git_branch}` : app.image_ref}
        </p>
      </Card>
    </Link>
  );
}

export default function OrgApps() {
  const { orgId } = useParams();
  const { data: orgData, error } = useOrg(orgId);
  const { data: apps, isLoading } = useQuery({
    queryKey: ["apps", orgId],
    queryFn: () => api<App[]>(`/orgs/${orgId}/apps`),
    enabled: !!orgId,
  });

  if (error) {
    return <EmptyState title="Organization not found" hint="You may not be a member of this organization." />;
  }
  const role = orgData?.role ?? "viewer";
  const canCreate = role !== "viewer";

  return (
    <div>
      <PageHeader
        eyebrow="org"
        title={orgData?.organization.name ?? "…"}
        actions={
          canCreate && (
            <Link to={`/orgs/${orgId}/apps/new`}>
              <Button>
                <Icon name="plus" size={14} /> New App
              </Button>
            </Link>
          )
        }
      />
      {isLoading ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-28" />
          ))}
        </div>
      ) : !apps || apps.length === 0 ? (
        <EmptyState
          title="No applications yet"
          hint="Deploy your first app from a git repository or a container image."
          action={
            canCreate ? (
              <Link to={`/orgs/${orgId}/apps/new`}>
                <Button>New App</Button>
              </Link>
            ) : undefined
          }
        />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {apps.map((app) => (
            <AppCard key={app.id} app={app} />
          ))}
        </div>
      )}
    </div>
  );
}
