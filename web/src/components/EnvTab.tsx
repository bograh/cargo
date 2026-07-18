import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, del, put } from "../lib/api";
import type { App } from "../lib/types";
import { Button, Card, Input, Spinner } from "./ui";

interface EnvRow {
  key: string;
  value: string;
}

export function EnvTab({ app }: { app: App }) {
  const qc = useQueryClient();
  const [rows, setRows] = useState<EnvRow[]>([]);
  const [error, setError] = useState("");
  const { data, isLoading } = useQuery({
    queryKey: ["env", app.id],
    queryFn: () => api<{ keys: string[] }>(`/apps/${app.id}/env`),
  });

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["env", app.id] });
  };
  const save = useMutation({
    mutationFn: (vars: Record<string, string>) => put(`/apps/${app.id}/env`, { vars }),
    onSuccess: () => {
      setRows([]);
      invalidate();
    },
    onError: (err) => setError(err instanceof Error ? err.message : "save failed"),
  });
  const remove = useMutation({
    mutationFn: (key: string) => del(`/apps/${app.id}/env/${encodeURIComponent(key)}`),
    onSuccess: invalidate,
    onError: (err) => setError(err instanceof Error ? err.message : "delete failed"),
  });

  if (isLoading) {
    return (
      <div className="flex justify-center py-10">
        <Spinner />
      </div>
    );
  }

  const vars = Object.fromEntries(rows.filter((r) => r.key.trim()).map((r) => [r.key.trim(), r.value]));

  return (
    <div className="space-y-6">
      <Card>
        <h2 className="mb-3 font-semibold">Saved variables</h2>
        <p className="mb-3 text-xs text-slate-500">
          Values are write-only: they are encrypted at rest and never displayed after saving. Setting an
          existing key overwrites its value. Changes apply on the next deploy.
        </p>
        {!data || data.keys.length === 0 ? (
          <p className="text-sm text-slate-500">No variables saved.</p>
        ) : (
          <ul className="space-y-2">
            {data.keys.map((k) => (
              <li key={k} className="flex items-center justify-between text-sm">
                <code className="text-slate-300">
                  {k}=<span className="text-slate-600">••••••••</span>
                </code>
                <Button variant="secondary" onClick={() => remove.mutate(k)}>
                  Delete
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Card>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="font-semibold">Add / update variables</h2>
          <Button variant="secondary" onClick={() => setRows((r) => [...r, { key: "", value: "" }])}>
            Add row
          </Button>
        </div>
        <div className="space-y-2">
          {rows.map((row, i) => (
            <div key={i} className="flex gap-2">
              <Input
                aria-label={`new env key ${i}`}
                placeholder="KEY"
                value={row.key}
                onChange={(e) => setRows((r) => r.map((x, j) => (j === i ? { ...x, key: e.target.value } : x)))}
              />
              <Input
                aria-label={`new env value ${i}`}
                placeholder="value"
                value={row.value}
                onChange={(e) => setRows((r) => r.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))}
              />
              <Button variant="secondary" onClick={() => setRows((r) => r.filter((_, j) => j !== i))}>
                ✕
              </Button>
            </div>
          ))}
        </div>
        {error && <p className="mt-2 text-sm text-red-400">{error}</p>}
        {rows.length > 0 && (
          <Button
            className="mt-3"
            onClick={() => save.mutate(vars)}
            disabled={save.isPending || Object.keys(vars).length === 0}
          >
            Save variables
          </Button>
        )}
      </Card>
    </div>
  );
}
