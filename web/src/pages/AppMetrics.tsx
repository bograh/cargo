import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useApp } from "../lib/hooks";
import type { AppMetric } from "../lib/types";
import { Card, EmptyState, PageHeader, Skeleton, StatusDot } from "../components/ui";
import { Sparkline } from "../components/ui/Sparkline";

const MAX_POINTS = 240; // ~1h at 15s

function fmtBytes(n: number) {
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1)} ${u[i]}`;
}

export default function AppMetrics() {
  const { appId } = useParams();
  const { data: app, isLoading, error } = useApp(appId);
  const [live, setLive] = useState<AppMetric[]>([]);
  const [connected, setConnected] = useState(false);
  const seeded = useRef(false);

  const { data: history } = useQuery({
    queryKey: ["metrics", appId],
    queryFn: () => api<AppMetric[]>(`/apps/${appId}/metrics?window=24h`),
    enabled: !!appId,
  });

  useEffect(() => {
    if (!appId) return;
    setLive([]);
    seeded.current = false;
    const es = new EventSource(`/api/v1/apps/${appId}/metrics/stream`);
    es.onopen = () => setConnected(true);
    es.onmessage = (e) => {
      try {
        const m = JSON.parse(e.data) as AppMetric;
        setLive((prev) => [...prev, m].slice(-MAX_POINTS));
      } catch {
        /* ignore malformed frame */
      }
    };
    es.onerror = () => setConnected(false);
    return () => es.close();
  }, [appId]);

  // Seed the live buffer from history once.
  useEffect(() => {
    if (history && !seeded.current) {
      setLive(history.slice(-MAX_POINTS));
      seeded.current = true;
    }
  }, [history]);

  if (isLoading) return <Skeleton className="h-64" />;
  if (error || !app) return <EmptyState title="Application not found" />;
  if (history === undefined) return <Skeleton className="h-64" />;

  const latest = live[live.length - 1];
  const series = (sel: (m: AppMetric) => number) => live.map(sel);

  const tiles = [
    { label: "CPU", value: latest ? `${latest.cpu_pct.toFixed(1)} %` : "—", points: series((m) => m.cpu_pct) },
    { label: "Memory", value: latest ? fmtBytes(latest.mem_bytes) : "—", points: series((m) => m.mem_bytes) },
    { label: "Requests/s", value: latest ? latest.req_rate.toFixed(2) : "—", points: series((m) => m.req_rate) },
    { label: "Errors", value: latest ? `${(latest.err_rate * 100).toFixed(1)} %` : "—", points: series((m) => m.err_rate) },
    { label: "Latency p50", value: latest ? `${latest.p50_ms.toFixed(0)} ms` : "—", points: series((m) => m.p50_ms) },
    { label: "Latency p95", value: latest ? `${latest.p95_ms.toFixed(0)} ms` : "—", points: series((m) => m.p95_ms) },
  ];

  return (
    <div>
      <PageHeader
        eyebrow="app"
        title="Metrics"
        back={{ to: `/apps/${app.id}`, label: app.name }}
        actions={
          <span className="flex items-center gap-2 text-xs text-muted">
            <StatusDot status={connected ? "live" : "failed"} />
            {connected ? "streaming" : "disconnected"}
          </span>
        }
      />
      {live.length === 0 ? (
        <EmptyState title="No metrics yet" hint="Metrics appear ~15s after the app is running." />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {tiles.map((t) => (
            <Card key={t.label}>
              <div className="flex items-baseline justify-between">
                <span className="font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">{t.label}</span>
                <span className="font-display text-lg font-semibold">{t.value}</span>
              </div>
              <Sparkline points={t.points} className="mt-3 w-full text-amber" height={48} />
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
