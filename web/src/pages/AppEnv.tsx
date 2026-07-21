import { useParams } from "react-router-dom";
import { useApp } from "../lib/hooks";
import { PageHeader, Skeleton, EmptyState } from "../components/ui";
import { EnvTab } from "../components/EnvTab";

export default function AppEnv() {
  const { appId } = useParams();
  const { data: app, isLoading, error } = useApp(appId);
  if (isLoading) return <Skeleton className="h-64" />;
  if (error || !app) return <EmptyState title="Application not found" />;
  return (
    <div>
      <PageHeader eyebrow="app" title="Environment" back={{ to: `/apps/${app.id}`, label: app.name }} />
      <EnvTab app={app} />
    </div>
  );
}
