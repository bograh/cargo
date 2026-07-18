import { useQuery } from "@tanstack/react-query";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { api } from "../lib/api";
import type { Org } from "../lib/types";
import { Select } from "./ui";

interface OrgRow {
  ID: string;
  Name: string;
  Slug: string;
  Role: string;
}

// The list endpoint returns sqlc rows (uppercase fields); normalize here.
export function normalizeOrgs(rows: OrgRow[]): Org[] {
  return rows.map((r) => ({ id: r.ID, name: r.Name, slug: r.Slug, role: r.Role }));
}

export function useOrgs() {
  return useQuery({
    queryKey: ["orgs"],
    queryFn: async () => normalizeOrgs(await api<OrgRow[]>("/orgs")),
  });
}

export function OrgSwitcher() {
  const { data: orgs } = useOrgs();
  const navigate = useNavigate();
  const { orgId } = useParams();
  const location = useLocation();
  if (!orgs || orgs.length === 0) return null;
  const current = orgId && orgs.some((o) => o.id === orgId) ? orgId : "";
  const hidden = !location.pathname.startsWith("/orgs/") && location.pathname !== "/";
  if (hidden) return null;
  return (
    <Select
      aria-label="organization"
      value={current}
      onChange={(e) => {
        if (e.target.value === "__new__") navigate("/orgs/new");
        else if (e.target.value) navigate(`/orgs/${e.target.value}`);
      }}
    >
      {current === "" && <option value="">Select org…</option>}
      {orgs.map((o) => (
        <option key={o.id} value={o.id}>
          {o.name}
        </option>
      ))}
      <option value="__new__">+ New organization</option>
    </Select>
  );
}
