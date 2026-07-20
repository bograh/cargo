import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "../../lib/cn";
import { Icon, type IconName } from "./Icon";

export function Dropdown({
  trigger,
  label,
  align = "left",
  children,
}: {
  trigger: ReactNode;
  label: string;
  align?: "left" | "right";
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={label}
        onClick={() => setOpen((o) => !o)}
        className="w-full rounded-lg transition-colors duration-150 hover:bg-raised"
      >
        {trigger}
      </button>
      {open && (
        <div
          role="menu"
          onClick={() => setOpen(false)}
          className={cn(
            "absolute z-40 mt-1 min-w-52 rounded-lg border border-border bg-raised p-1 shadow-[0_24px_60px_-24px_rgba(0,0,0,0.7)]",
            align === "right" ? "right-0" : "left-0",
          )}
        >
          {children}
        </div>
      )}
    </div>
  );
}

export function DropdownItem({
  icon,
  danger,
  onClick,
  children,
}: {
  icon?: IconName;
  danger?: boolean;
  onClick?: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-sm transition-colors duration-150",
        danger ? "text-danger hover:bg-danger-tint" : "text-text hover:bg-surface",
      )}
    >
      {icon && <Icon name={icon} size={14} />}
      <span className="truncate">{children}</span>
    </button>
  );
}
