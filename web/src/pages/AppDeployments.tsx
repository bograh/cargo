import { useParams } from "react-router-dom";
import { useApp } from "../lib/hooks";
import { PageHeader, Skeleton, EmptyState } from "../components/ui";
import { DeploymentsTab } from "../components/DeploymentsTab";

export default function AppDeployments() {
  const { appId } = useParams();
  const { data: app, isLoading, error } = useApp(appId);
  if (isLoading) return <Skeleton className="h-64" />;
  if (error || !app) return <EmptyState title="Application not found" />;
  return (
    <div>
      <PageHeader eyebrow="app" title="Deployments" />
      <DeploymentsTab app={app} />
    </div>
  );
}
