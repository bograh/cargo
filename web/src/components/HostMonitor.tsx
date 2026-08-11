import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../lib/api";
import type { AppMetricSummary, HostMetric } from "../lib/types";
import { Badge, Card, EmptyState, Icon, StatusDot } from "./ui";
import { Sparkline } from "./ui/Sparkline";

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

function pct(part: number, whole: number) {
  return whole > 0 ? (part / whole) * 100 : 0;
}

/** Whole-server resource charts, streamed live alongside 24h of history. */
export function HostMonitor() {
  const [live, setLive] = useState<HostMetric[]>([]);
  const [connected, setConnected] = useState(false);
  const seeded = useRef(false);

  const { data: history } = useQuery({
    queryKey: ["admin", "host-metrics"],
    queryFn: () => api<HostMetric[]>("/admin/host/metrics?window=24h"),
  });

  useEffect(() => {
    const es = new EventSource("/api/v1/admin/host/metrics/stream");
    es.onopen = () => setConnected(true);
    es.onmessage = (e) => {
      try {
        setLive((prev) => [...prev, JSON.parse(e.data) as HostMetric].slice(-MAX_POINTS));
      } catch {
        /* ignore malformed frame */
      }
    };
    es.onerror = () => setConnected(false);
    return () => es.close();
  }, []);

  useEffect(() => {
    if (history && !seeded.current) {
      setLive(history.slice(-MAX_POINTS));
      seeded.current = true;
    }
  }, [history]);

  const latest = live[live.length - 1];
  const series = (sel: (m: HostMetric) => number) => live.map(sel);

  const tiles = latest
    ? [
        {
          label: "Host CPU",
          value: `${latest.cpu_pct.toFixed(1)} %`,
          points: series((m) => m.cpu_pct),
        },
        {
          label: "Host memory",
          value: `${fmtBytes(latest.mem_used_bytes)} / ${fmtBytes(latest.mem_total_bytes)}`,
          points: series((m) => pct(m.mem_used_bytes, m.mem_total_bytes)),
        },
        {
          label: "Disk used",
          value: `${fmtBytes(latest.disk_total_bytes - latest.disk_free_bytes)} / ${fmtBytes(latest.disk_total_bytes)}`,
          points: series((m) => pct(m.disk_total_bytes - m.disk_free_bytes, m.disk_total_bytes)),
        },
        {
          label: "Containers",
          value: `${latest.containers}`,
          points: series((m) => m.containers),
        },
      ]
    : [];

  return (
    <Card>
      <div className="mb-4 flex items-center justify-between">
        <div>
          <span className="font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">operations</span>
          <h2 className="font-display text-lg font-semibold">Server</h2>
        </div>
        <span className="flex items-center gap-2 text-xs text-muted">
          <StatusDot status={connected ? "live" : "failed"} />
          {connected ? "streaming" : "disconnected"}
        </span>
      </div>
      {!latest ? (
        <EmptyState title="No host samples yet" hint="The first sample lands within ~15s." />
      ) : (
        <>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {tiles.map((t) => (
              <div key={t.label} className="rounded-lg border border-border p-3">
                <div className="flex items-baseline justify-between gap-2">
                  <span className="font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">{t.label}</span>
                </div>
                <div className="mt-1 font-display text-base font-semibold">{t.value}</div>
                <Sparkline points={t.points} className="mt-2 w-full text-amber" height={40} />
              </div>
            ))}
          </div>
          <p className="mt-3 text-xs text-muted">
            {latest.running_apps} app{latest.running_apps === 1 ? "" : "s"} running of {latest.containers} container
            {latest.containers === 1 ? "" : "s"} on this host (the rest are the platform's own and any managed databases).
          </p>
        </>
      )}
    </Card>
  );
}

/** Latest sample for every app on the instance, across all organizations. */
export function AllAppsMonitor() {
  const { data, isLoading } = useQuery({
    queryKey: ["admin", "apps-metrics"],
    queryFn: () => api<AppMetricSummary[]>("/admin/apps/metrics"),
    refetchInterval: 15_000,
  });

  return (
    <Card>
      <div className="mb-4">
        <span className="font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">operations</span>
        <h2 className="font-display text-lg font-semibold">All applications</h2>
      </div>
      {isLoading ? null : !data || data.length === 0 ? (
        <EmptyState title="No app metrics yet" hint="Apps appear here once they've been running for a tick." />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="text-muted">
              <tr className="border-b border-border">
                <th className="pb-2 font-medium">App</th>
                <th className="pb-2 font-medium">CPU</th>
                <th className="pb-2 font-medium">Memory</th>
                <th className="pb-2 font-medium">Req/s</th>
                <th className="pb-2 font-medium">Errors</th>
                <th className="pb-2 font-medium">p95</th>
              </tr>
            </thead>
            <tbody>
              {data.map((a) => (
                <tr key={a.app_id} className="border-b border-border last:border-0">
                  <td className="py-2">
                    <Link to={`/apps/${a.app_id}`} className="inline-flex items-center gap-2 hover:text-amber">
                      <StatusDot status={a.desired_state === "running" ? "live" : "failed"} />
                      <span>{a.name}</span>
                      <Icon name="arrow-right" size={12} />
                    </Link>
                  </td>
                  <td className="py-2 font-mono">{a.cpu_pct.toFixed(1)}%</td>
                  <td className="py-2 font-mono">
                    {fmtBytes(a.mem_bytes)}
                    {a.mem_limit_bytes > 0 && (
                      <span className="text-muted"> / {fmtBytes(a.mem_limit_bytes)}</span>
                    )}
                  </td>
                  <td className="py-2 font-mono">{a.req_rate.toFixed(2)}</td>
                  <td className="py-2">
                    {a.err_rate > 0.05 ? (
                      <Badge tone="danger">{(a.err_rate * 100).toFixed(1)}%</Badge>
                    ) : (
                      <span className="font-mono">{(a.err_rate * 100).toFixed(1)}%</span>
                    )}
                  </td>
                  <td className="py-2 font-mono">{a.p95_ms.toFixed(0)} ms</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}
