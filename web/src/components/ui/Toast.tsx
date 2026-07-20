import { createContext, useCallback, useContext, useState, type ReactNode } from "react";
import { cn } from "../../lib/cn";

type Tone = "success" | "error" | "info";
type ToastItem = { id: number; tone: Tone; text: string };

const ToastCtx = createContext<(text: string, tone?: Tone) => void>(() => {});

export function useToast() {
  return useContext(ToastCtx);
}

const TONE_STYLES: Record<Tone, string> = {
  success: "border-[color-mix(in_srgb,var(--color-live)_35%,transparent)] text-live",
  error: "border-[color-mix(in_srgb,var(--color-danger)_35%,transparent)] text-danger",
  info: "border-border text-text",
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const push = useCallback((text: string, tone: Tone = "success") => {
    const id = Date.now() + Math.random();
    setToasts((t) => [...t, { id, tone, text }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 4000);
  }, []);
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="fixed bottom-4 right-4 z-[60] flex w-72 flex-col gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            role="status"
            className={cn(
              "rounded-lg border bg-surface px-3 py-2 text-sm shadow-[0_24px_60px_-24px_rgba(0,0,0,0.7)]",
              TONE_STYLES[t.tone],
            )}
          >
            {t.text}
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}
