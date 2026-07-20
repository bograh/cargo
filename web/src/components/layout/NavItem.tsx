import { NavLink } from "react-router-dom";
import { cn } from "../../lib/cn";
import { Icon, type IconName } from "../ui/Icon";
import { Tooltip } from "../ui/Tooltip";

export function NavItem({ to, icon, label, end }: { to: string; icon: IconName; label: string; end?: boolean }) {
  return (
    <Tooltip label={label} block>
      <NavLink
        to={to}
        end={end}
        className={({ isActive }) =>
          cn(
            // -mx-2 cancels the nav container's px-2 so the active fill bleeds
            // to both sidebar edges; the left border reads as an active rail.
            "-mx-2 flex w-full items-center gap-2.5 border-l-2 px-5 py-2 text-sm transition-colors duration-150 md:justify-center md:px-0 lg:justify-start lg:px-5",
            isActive
              ? "border-amber bg-amber-tint text-amber"
              : "border-transparent text-muted hover:bg-raised hover:text-text",
          )
        }
      >
        <Icon name={icon} size={16} className="shrink-0" />
        <span className="truncate md:hidden lg:inline">{label}</span>
      </NavLink>
    </Tooltip>
  );
}
