import { screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { AllAppsMonitor, HostMonitor } from "./HostMonitor";
import { mockApi, renderPage } from "../test/utils";

const HOST_SAMPLE = {
  ts: "2026-08-10T00:00:00Z",
  cpu_pct: 42.5,
  mem_used_bytes: 8 * 1024 ** 3,
  mem_total_bytes: 16 * 1024 ** 3,
  disk_free_bytes: 50 * 1024 ** 3,
  disk_total_bytes: 200 * 1024 ** 3,
  containers: 9,
  running_apps: 4,
};

test("renders whole-server tiles from history", async () => {
  mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: true } },
    "GET /admin/host/metrics": { status: 200, body: [HOST_SAMPLE] },
  });
  renderPage(<HostMonitor />);

  expect(await screen.findByText("Host CPU")).toBeInTheDocument();
  expect(screen.getByText("42.5 %")).toBeInTheDocument();
  // Memory and disk read as used-of-total, not a bare percentage.
  expect(screen.getByText("8.0 GB / 16.0 GB")).toBeInTheDocument();
  expect(screen.getByText("150.0 GB / 200.0 GB")).toBeInTheDocument();
  expect(screen.getByText("9")).toBeInTheDocument();
  // The container count includes the platform's own, so say so rather than
  // letting an admin read it as "4 apps means 4 containers".
  expect(screen.getByText(/4 apps running of 9 containers/)).toBeInTheDocument();
});

test("host monitor shows an empty state before the first sample", async () => {
  mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: true } },
    "GET /admin/host/metrics": { status: 200, body: [] },
  });
  renderPage(<HostMonitor />);
  expect(await screen.findByText(/no host samples yet/i)).toBeInTheDocument();
});

test("all-apps table lists every app across organizations", async () => {
  mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: true } },
    "GET /admin/apps/metrics": {
      status: 200,
      body: [
        {
          app_id: "a1", org_id: "o1", name: "web", slug: "web", desired_state: "running",
          ts: "2026-08-10T00:00:00Z", cpu_pct: 3.25, mem_bytes: 64 * 1024 ** 2,
          mem_limit_bytes: 512 * 1024 ** 2, req_rate: 1.5, err_rate: 0.01, p95_ms: 80,
        },
        {
          app_id: "a2", org_id: "o2", name: "api", slug: "api", desired_state: "running",
          ts: "2026-08-10T00:00:00Z", cpu_pct: 90, mem_bytes: 0,
          mem_limit_bytes: 0, req_rate: 0, err_rate: 0.25, p95_ms: 1200,
        },
      ],
    },
  });
  renderPage(<AllAppsMonitor />);

  expect(await screen.findByText("web")).toBeInTheDocument();
  expect(screen.getByText("api")).toBeInTheDocument();
  expect(screen.getByText("3.3%")).toBeInTheDocument();
  // A high error rate is badged so it stands out in a long table.
  expect(screen.getByText("25.0%")).toBeInTheDocument();
  expect(screen.getByText("1.0%")).toBeInTheDocument();
  // Apps link through to their detail page.
  expect(screen.getByRole("link", { name: /web/ })).toHaveAttribute("href", "/apps/a1");
});
