import type { InputHTMLAttributes } from "react";
import { cn } from "../../lib/cn";

export function Checkbox({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return <input type="checkbox" className={cn("cargo-checkbox", className)} {...props} />;
}
