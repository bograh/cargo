import { useMatch } from "react-router-dom";
import { NavItem } from "./NavItem";

export function OrgScopeNav() {
  const match = useMatch("/orgs/:orgId/*");
  const orgId = match?.params.orgId;
  if (!orgId) return null;
  return (
    <nav className="flex flex-col gap-0.5 px-2">
      <NavItem to={`/orgs/${orgId}`} icon="apps" label="Apps" end />
      <NavItem to={`/orgs/${orgId}/databases`} icon="database" label="Databases" />
      <NavItem to={`/orgs/${orgId}/members`} icon="users" label="Members" />
      <NavItem to={`/orgs/${orgId}/settings`} icon="settings" label="Org Settings" />
    </nav>
  );
}
