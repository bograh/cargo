import type { TextareaHTMLAttributes } from "react";
import { cn } from "../../lib/cn";

export function Textarea({ className, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      className={cn(
        "w-full rounded-lg border border-border bg-raised px-3 py-1.5 text-sm text-text placeholder:text-muted/60 focus:border-amber-dim focus:outline-none",
        className,
      )}
      {...props}
    />
  );
}
