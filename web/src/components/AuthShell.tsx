import type { ReactNode } from "react";
import { ContainerMark } from "./layout/ContainerMark";

export function AuthShell({ title, children }: { title: string; children: ReactNode }) {
  return (
    <main className="corrugated glow-top flex min-h-screen items-center justify-center bg-bg px-4 text-text">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex flex-col items-center gap-2">
          <ContainerMark size={28} />
          <h1 className="font-display text-2xl font-bold tracking-tight">Cargo</h1>
        </div>
        <div className="rounded-lg border border-border bg-surface p-5">
          <div className="hazard mb-4 h-1.5 w-[72px] rounded-sm" />
          <h2 className="mb-4 font-display text-lg font-semibold">{title}</h2>
          {children}
        </div>
      </div>
    </main>
  );
}
