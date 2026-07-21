import { useParams } from "react-router-dom";
import { useApp } from "../lib/hooks";
import { PageHeader, Skeleton, EmptyState } from "../components/ui";
import { SettingsTab } from "../components/SettingsTab";

export default function AppSettings() {
  const { appId } = useParams();
  const { data: app, isLoading, error } = useApp(appId);
  if (isLoading) return <Skeleton className="h-64" />;
  if (error || !app) return <EmptyState title="Application not found" />;
  return (
    <div>
      <PageHeader eyebrow="app" title="Settings" back={{ to: `/apps/${app.id}`, label: app.name }} />
      <SettingsTab app={app} />
    </div>
  );
}
