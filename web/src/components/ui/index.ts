export { Button } from "./Button";
export { Input } from "./Input";
export { Select } from "./Select";
export { Textarea } from "./Textarea";
export { Label } from "./Label";
export { Card } from "./Card";
export { Badge } from "./Badge";
export type { BadgeTone } from "./Badge";
export { Spinner } from "./Spinner";
export { Skeleton } from "./Skeleton";
export { FieldError } from "./FieldError";
export { EmptyState } from "./EmptyState";
export { PageHeader } from "./PageHeader";
export { Icon } from "./Icon";
export type { IconName } from "./Icon";
export { StatusDot } from "./StatusDot";
export { Tooltip } from "./Tooltip";
export { Dropdown, DropdownItem } from "./Dropdown";
export { Modal } from "./Modal";
export { ConfirmModal } from "./ConfirmModal";
export { ToastProvider, useToast } from "./Toast";

// --- compat shims (deleted in the final cleanup task) ---
import { createElement, type ReactNode } from "react";
import { PageHeader } from "./PageHeader";

/** @deprecated use PageHeader */
export function PageTitle({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
  return createElement(PageHeader, { title: children, actions });
}
