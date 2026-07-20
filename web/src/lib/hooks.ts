import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import type { App, Org } from "./types";

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
    queryFn: () => api<{ apps_domain_suffix: string }>("/instance/info"),
  });
}
