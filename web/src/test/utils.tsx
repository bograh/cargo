import type { ReactNode } from "react";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { vi } from "vitest";
import { AuthProvider } from "../auth";

type RouteHandler = (init?: RequestInit) => { status: number; body: unknown };

/** Installs a fetch mock keyed by "METHOD /path" (path without /api/v1). */
export function mockApi(routes: Record<string, RouteHandler | { status: number; body: unknown }>) {
  const calls: Array<{ method: string; path: string; body?: unknown }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      const path = url.replace("/api/v1", "");
      const pathNoQuery = path.split("?")[0];
      calls.push({
        method,
        path,
        body: init?.body ? JSON.parse(init.body as string) : undefined,
      });
      // Fall back to matching without the query string so callers can mock
      // "/foo" and still handle requests like "/foo?window=24h".
      const handler = routes[`${method} ${path}`] ?? routes[`${method} ${pathNoQuery}`];
      const result =
        typeof handler === "function" ? handler(init) : handler ?? {
          status: 404,
          body: { error: { code: "not_found", message: `no mock for ${method} ${path}` } },
        };
      return new Response(JSON.stringify(result.body), {
        status: result.status,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
  return calls;
}

export function renderPage(ui: ReactNode, { path = "/", route = "/" }: { path?: string; route?: string } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[route]}>
        <AuthProvider>
          <Routes>
            <Route path={path} element={ui} />
            <Route path="*" element={<div data-testid="navigated" />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
