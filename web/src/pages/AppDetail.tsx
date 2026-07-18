import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { api } from "../lib/api";
import type { App } from "../lib/types";
import { Badge, EmptyState, PageTitle, Spinner } from "../components/ui";
import { DeploymentsTab } from "../components/DeploymentsTab";
import { EnvTab } from "../components/EnvTab";
import { SettingsTab } from "../components/SettingsTab";

const TABS = ["Overview", "Deployments", "Environment", "Settings"] as const;
type Tab = (typeof TABS)[number];

export function useApp(appId: string | undefined) {
  return useQuery({
    queryKey: ["app", appId],
    queryFn: () => api<App>(`/apps/${appId}`),
    enabled: !!appId,
  });
}

function Overview({ app }: { app: App }) {
  const rows: Array<[string, React.ReactNode]> = [
    ["Slug", app.slug],
    ["Source", app.source_type === "git" ? `${app.git_repo_url} @ ${app.git_branch}` : app.image_ref],
    ["Builder", app.builder],
    ["Port", app.exposed_port],
    ["Healthcheck", app.healthcheck_path],
    ["Auto-deploy", app.auto_deploy ? "on" : "off"],
  ];
  return (
    <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
      {rows.map(([k, v]) => (
        <div key={k} className="rounded-md border border-slate-800 bg-slate-900/60 p-3">
          <dt className="text-xs text-slate-500">{k}</dt>
          <dd className="mt-1 text-sm text-slate-200 break-all">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

export default function AppDetail() {
  const { appId } = useParams();
  const { data: app, isLoading, error } = useApp(appId);
  const [tab, setTab] = useState<Tab>("Deployments");

  if (isLoading) {
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  }
  if (error || !app) {
    return <EmptyState title="Application not found" />;
  }

  return (
    <div>
      <div className="mb-1 text-sm">
        <Link to={`/orgs/${app.org_id}`} className="text-slate-500 hover:text-slate-300">
          ← Back to org
        </Link>
      </div>
      <PageTitle actions={<Badge color={app.source_type === "git" ? "indigo" : "gray"}>{app.source_type}</Badge>}>
        {app.name}
      </PageTitle>

      <nav className="mb-6 flex gap-1 border-b border-slate-800">
        {TABS.map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-3 py-2 text-sm ${
              tab === t
                ? "border-b-2 border-indigo-500 text-slate-100"
                : "text-slate-400 hover:text-slate-200"
            }`}
          >
            {t}
          </button>
        ))}
      </nav>

      {tab === "Overview" && <Overview app={app} />}
      {tab === "Deployments" && <DeploymentsTab app={app} />}
      {tab === "Environment" && <EnvTab app={app} />}
      {tab === "Settings" && <SettingsTab app={app} />}
    </div>
  );
}
