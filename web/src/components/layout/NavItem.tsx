import { NavLink } from "react-router-dom";
import { cn } from "../../lib/cn";
import { Icon, type IconName } from "../ui/Icon";
import { Tooltip } from "../ui/Tooltip";

export function NavItem({ to, icon, label, end }: { to: string; icon: IconName; label: string; end?: boolean }) {
  return (
    <Tooltip label={label}>
      <NavLink
        to={to}
        end={end}
        className={({ isActive }) =>
          cn(
            "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors duration-150 md:justify-center lg:justify-start",
            isActive ? "bg-amber-tint text-amber" : "text-muted hover:bg-raised hover:text-text",
          )
        }
      >
        <Icon name={icon} size={16} className="shrink-0" />
        <span className="truncate md:hidden lg:inline">{label}</span>
      </NavLink>
    </Tooltip>
  );
}
