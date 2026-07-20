import { useEffect, useState } from "react";
import { Link, useLocation } from "react-router-dom";
import { cn } from "../../lib/cn";
import { useAuth } from "../../auth";
import { Icon } from "../ui/Icon";
import { OrgSwitcher } from "../OrgSwitcher";
import { AppScopeNav } from "./AppScopeNav";
import { ContainerMark } from "./ContainerMark";
import { OrgScopeNav } from "./OrgScopeNav";
import { UserMenu } from "./UserMenu";

export function Sidebar() {
  const { pathname } = useLocation();
  const { user } = useAuth();
  const [open, setOpen] = useState(false);
  const isAppScope = pathname.startsWith("/apps/");
  useEffect(() => setOpen(false), [pathname]);
  return (
    <>
      {/* mobile top bar */}
      <div className="glass fixed inset-x-0 top-0 z-30 flex h-14 items-center gap-3 border-b border-border px-4 md:hidden">
        <button aria-label="Open navigation" onClick={() => setOpen(true)} className="text-muted hover:text-text">
          <Icon name="menu" size={20} />
        </button>
        <Link to="/" className="flex items-center gap-2 font-display font-bold tracking-tight">
          <ContainerMark size={18} /> Cargo
        </Link>
      </div>
      {open && <div className="fixed inset-0 z-30 bg-black/60 md:hidden" onClick={() => setOpen(false)} />}
      <aside
        className={cn(
          "fixed inset-y-0 left-0 z-40 flex w-60 flex-col border-r border-border bg-surface transition-transform duration-150 md:w-16 md:translate-x-0 lg:w-60",
          open ? "translate-x-0" : "-translate-x-full",
        )}
      >
        <div className="flex h-14 shrink-0 items-center border-b border-border px-4 md:justify-center md:px-0 lg:justify-start lg:px-4">
          <Link to="/" className="flex items-center gap-2 font-display text-lg font-bold tracking-tight">
            <ContainerMark />
            <span className="md:hidden lg:inline">Cargo</span>
          </Link>
        </div>
        <div className="flex-1 overflow-y-auto py-3">
          {!isAppScope && <OrgSwitcher />}
          {isAppScope ? <AppScopeNav /> : <OrgScopeNav />}
          {user?.is_instance_admin && !isAppScope && (
            <div className="mt-4 border-t border-border px-2 pt-3">
              <div className="px-3 pb-1 font-mono text-[0.68rem] uppercase tracking-[0.14em] text-amber md:hidden lg:block">
                Instance
              </div>
              <Link
                to="/admin"
                className="flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm text-muted transition-colors duration-150 hover:bg-raised hover:text-text md:justify-center lg:justify-start"
              >
                <Icon name="shield" size={16} />
                <span className="md:hidden lg:inline">Admin</span>
              </Link>
            </div>
          )}
        </div>
        <UserMenu />
      </aside>
    </>
  );
}
