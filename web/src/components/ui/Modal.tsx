import { useEffect, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { cn } from "../../lib/cn";

export function Modal({
  title,
  onClose,
  wide,
  children,
}: {
  title: string;
  onClose: () => void;
  wide?: boolean;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  // Callers pass inline closures, so onClose changes on every parent render.
  // Read it through a ref so the effect below runs once: re-running it would
  // pull focus back to the first field each time a polling parent re-renders.
  const onCloseRef = useRef(onClose);
  useEffect(() => {
    onCloseRef.current = onClose;
  }, [onClose]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCloseRef.current();
    };
    document.addEventListener("keydown", onKey);
    ref.current?.querySelector<HTMLElement>("input, select, textarea, button, a")?.focus();
    return () => document.removeEventListener("keydown", onKey);
  }, []);
  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/60" onClick={onClose} />
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={cn(
          "relative w-full rounded-lg border border-border bg-surface p-5 shadow-[0_24px_60px_-24px_rgba(0,0,0,0.7)]",
          wide ? "max-w-2xl" : "max-w-md",
        )}
      >
        <h2 className="font-display text-lg font-semibold">{title}</h2>
        <div className="mt-3">{children}</div>
      </div>
    </div>,
    document.body,
  );
}
