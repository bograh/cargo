import { useNavigate } from "react-router-dom";
import { useAuth } from "../../auth";
import { Dropdown, DropdownItem } from "../ui/Dropdown";

export function UserMenu() {
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  if (!user) return null;
  return (
    <div className="border-t border-border p-2">
      <Dropdown
        label="account menu"
        trigger={
          <span className="flex items-center gap-2.5 px-2 py-1.5">
            <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-amber-tint font-mono text-xs font-semibold text-amber">
              {user.email[0]?.toUpperCase()}
            </span>
            <span className="truncate text-sm text-text md:hidden lg:inline">{user.email}</span>
          </span>
        }
      >
        {user.is_instance_admin && (
          <DropdownItem icon="shield" onClick={() => navigate("/admin")}>
            Instance admin
          </DropdownItem>
        )}
        <DropdownItem icon="logout" danger onClick={() => logout().then(() => navigate("/login"))}>
          Log out
        </DropdownItem>
      </Dropdown>
    </div>
  );
}
