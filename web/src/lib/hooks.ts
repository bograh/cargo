import { useEffect } from "react";
import { useMatch } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import type { App, Org, RegistrationMode } from "./types";

const ACTIVE_ORG_KEY = "cargo.activeOrgId";

function readStoredOrgId(): string | undefined {
  try {
    return localStorage.getItem(ACTIVE_ORG_KEY) ?? undefined;
  } catch {
    return undefined;
  }
}

interface OrgRow {
  ID: string;
  Name: string;
  Slug: string;
  Role: string;
}

// The /orgs list endpoint returns sqlc rows (uppercase fields); normalize here.
export function normalizeOrgs(rows: OrgRow[]): Org[] {
  return rows.map((r) => ({ id: r.ID, name: r.Name, slug: r.Slug, role: r.Role }));
}

export function useOrgs() {
  return useQuery({
    queryKey: ["orgs"],
    queryFn: async () => normalizeOrgs(await api<OrgRow[]>("/orgs")),
  });
}

export function useOrg(orgId: string | undefined) {
  return useQuery({
    queryKey: ["org", orgId],
    queryFn: () => api<{ organization: Org; role: string }>(`/orgs/${orgId}`),
    enabled: !!orgId,
  });
}

export function useApp(appId: string | undefined) {
  return useQuery({
    queryKey: ["app", appId],
    queryFn: () => api<App>(`/apps/${appId}`),
    enabled: !!appId,
  });
}

export function useInstanceInfo() {
  return useQuery({
    queryKey: ["instance-info"],
    queryFn: () =>
      api<{ apps_domain_suffix: string; registration_mode: RegistrationMode }>("/instance/info"),
  });
}

// useActiveOrgId resolves the org the sidebar should stay scoped to. It reads
// the org from the current route (an /orgs/:orgId page, or the org owning an
// /apps/:appId page), persists it, and falls back to the last-seen org on
// routes with no org context (/, /deployments/:id, /admin) so the selection
// does not reset as the user navigates.
export function useActiveOrgId(): string | undefined {
  const orgMatch = useMatch("/orgs/:orgId/*");
  const appMatch = useMatch("/apps/:appId/*");
  const { data: app } = useApp(appMatch?.params.appId);
  const routeOrgId = orgMatch?.params.orgId ?? app?.org_id;

  useEffect(() => {
    if (!routeOrgId) return;
    try {
      localStorage.setItem(ACTIVE_ORG_KEY, routeOrgId);
    } catch {
      // ignore storage failures (private mode, quota)
    }
  }, [routeOrgId]);

  return routeOrgId ?? readStoredOrgId();
}
