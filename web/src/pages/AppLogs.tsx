import { useCallback, useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { useApp } from "../lib/hooks";
import { Button, EmptyState, PageHeader, Skeleton, StatusDot } from "../components/ui";

export default function AppLogs() {
  const { appId } = useParams();
  const { data: app, isLoading, error } = useApp(appId);
  const [lines, setLines] = useState<string[]>([]);
  const [connected, setConnected] = useState(false);
  const logRef = useRef<HTMLPreElement>(null);

  const scrollToBottom = useCallback(() => {
    const el = logRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, []);

  useEffect(() => {
    if (!appId) return;
    setLines([]);
    setConnected(false);
    const es = new EventSource(`/api/v1/apps/${appId}/logs`);
    es.onopen = () => setConnected(true);
    es.onmessage = (e) => setLines((prev) => [...prev, e.data]);
    es.onerror = () => setConnected(false);
    return () => es.close();
  }, [appId]);

  useEffect(() => {
    scrollToBottom();
  }, [lines.length, scrollToBottom]);

  if (isLoading) return <Skeleton className="h-64" />;
  if (error || !app) return <EmptyState title="Application not found" />;

  return (
    <div>
      <PageHeader
        eyebrow="app"
        title="Logs"
        back={{ to: `/apps/${app.id}`, label: app.name }}
        actions={
          <span className="flex items-center gap-2 text-xs text-muted">
            <StatusDot status={connected ? "live" : "failed"} />
            {connected ? "streaming" : "disconnected"}
          </span>
        }
      />
      <div className="overflow-hidden rounded-lg border border-border bg-terminal shadow-[0_24px_60px_-24px_rgba(0,0,0,0.7)]">
        <div className="flex items-center gap-1.5 border-b border-border px-3 py-2">
          <span className="h-2.5 w-2.5 rounded-full bg-danger/70" />
          <span className="h-2.5 w-2.5 rounded-full bg-amber/70" />
          <span className="h-2.5 w-2.5 rounded-full bg-live/70" />
          <span className="ml-2 font-mono text-xs text-muted">application &amp; request logs</span>
          <Button variant="ghost" className="ml-auto !px-2 !py-1 text-xs" onClick={scrollToBottom}>
            Latest
          </Button>
        </div>
        <pre
          ref={logRef}
          data-testid="app-log-output"
          className="h-[60vh] overflow-y-auto p-4 font-mono text-xs leading-relaxed text-text"
        >
          {lines.length === 0 ? (
            <span className="text-muted">
              {connected ? "Waiting for output…" : "Connecting… (the app must have a running container)"}
            </span>
          ) : (
            lines.map((line, i) => <div key={i}>{line}</div>)
          )}
        </pre>
      </div>
    </div>
  );
}
