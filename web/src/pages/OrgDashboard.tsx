import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { api } from "../lib/api";
import type { App, Org } from "../lib/types";
import { Badge, Button, Card, EmptyState, PageTitle, Spinner } from "../components/ui";

export function useOrg(orgId: string | undefined) {
  return useQuery({
    queryKey: ["org", orgId],
    queryFn: () => api<{ organization: Org; role: string }>(`/orgs/${orgId}`),
    enabled: !!orgId,
  });
}

export default function OrgDashboard() {
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
      <PageTitle
        actions={
          <div className="flex gap-2">
            <Link to={`/orgs/${orgId}/settings`}>
              <Button variant="secondary">Settings</Button>
            </Link>
            {canCreate && (
              <Link to={`/orgs/${orgId}/apps/new`}>
                <Button>New App</Button>
              </Link>
            )}
          </div>
        }
      >
        {orgData?.organization.name ?? "…"}
      </PageTitle>

      {isLoading ? (
        <div className="flex justify-center py-20">
          <Spinner />
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
            <Link key={app.id} to={`/apps/${app.id}`}>
              <Card className="hover:border-slate-600 transition-colors">
                <div className="flex items-center justify-between">
                  <h3 className="font-semibold text-slate-100">{app.name}</h3>
                  <Badge color={app.source_type === "git" ? "indigo" : "gray"}>{app.source_type}</Badge>
                </div>
                <p className="mt-1 text-sm text-slate-500">{app.slug}</p>
                <p className="mt-2 truncate text-xs text-slate-400">
                  {app.source_type === "git" ? `${app.git_repo_url} @ ${app.git_branch}` : app.image_ref}
                </p>
              </Card>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
