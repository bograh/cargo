import { useQuery } from "@tanstack/react-query";
import { Link, useMatch } from "react-router-dom";
import { api } from "../../lib/api";
import { isActive, type Deployment } from "../../lib/types";
import { useApp } from "../../lib/hooks";
import { StatusDot } from "../ui/StatusDot";
import { Icon } from "../ui/Icon";
import { NavItem } from "./NavItem";

export function AppScopeNav() {
  const match = useMatch("/apps/:appId/*");
  const appId = match?.params.appId;
  const { data: app } = useApp(appId);
  const { data: deployments } = useQuery({
    queryKey: ["deployments", appId],
    queryFn: () => api<Deployment[]>(`/apps/${appId}/deployments`),
    enabled: !!appId,
    refetchInterval: (q) => (q.state.data?.some((d) => isActive(d.status)) ? 3000 : false),
  });
  if (!appId) return null;
  const latest = deployments?.[0];
  return (
    <div className="flex flex-col gap-3 px-2">
      {app && (
        <>
          <Link
            to={`/orgs/${app.org_id}`}
            className="flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs text-muted transition-colors duration-150 hover:text-text md:justify-center lg:justify-start"
          >
            <Icon name="arrow-left" size={14} />
            <span className="md:hidden lg:inline">All apps</span>
          </Link>
          <div className="flex items-center gap-2 px-3 md:justify-center lg:justify-start">
            {latest && <StatusDot status={latest.status} />}
            <span className="truncate font-display text-sm font-semibold md:hidden lg:inline">{app.name}</span>
          </div>
        </>
      )}
      <nav className="flex flex-col gap-0.5">
        <NavItem to={`/apps/${appId}`} icon="box" label="Overview" end />
        <NavItem to={`/apps/${appId}/deployments`} icon="rocket" label="Deployments" />
        <NavItem to={`/apps/${appId}/env`} icon="key" label="Environment" />
        <NavItem to={`/apps/${appId}/domains`} icon="globe" label="Domains" />
        <NavItem to={`/apps/${appId}/settings`} icon="settings" label="Settings" />
      </nav>
    </div>
  );
}
