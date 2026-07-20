import { useLocation, useNavigate } from "react-router-dom";
import { useActiveOrgId, useOrgs } from "../lib/hooks";
import { Dropdown, DropdownItem } from "./ui/Dropdown";
import { Icon } from "./ui/Icon";

export function OrgSwitcher() {
  const { data: orgs } = useOrgs();
  const navigate = useNavigate();
  const location = useLocation();
  const activeOrgId = useActiveOrgId();
  if (!orgs || orgs.length === 0 || location.pathname.startsWith("/apps/")) return null;
  const current = orgs.find((o) => o.id === activeOrgId);
  return (
    <div className="px-2 pb-3">
      <Dropdown
        label="switch organization"
        trigger={
          <span className="flex w-full items-center gap-2.5 px-2 py-1.5">
            <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-raised font-display text-xs font-bold text-amber">
              {(current?.name ?? "?")[0]?.toUpperCase()}
            </span>
            <span className="flex-1 truncate text-left font-display text-sm font-semibold md:hidden lg:inline">
              {current?.name ?? "Select org"}
            </span>
            <Icon name="chevron-down" size={14} className="text-muted md:hidden lg:inline" />
          </span>
        }
      >
        {orgs.map((o) => (
          <DropdownItem key={o.id} icon="box" onClick={() => navigate(`/orgs/${o.id}`)}>
            {o.name}
          </DropdownItem>
        ))}
        <DropdownItem icon="plus" onClick={() => navigate("/orgs/new")}>
          New organization
        </DropdownItem>
      </Dropdown>
    </div>
  );
}
