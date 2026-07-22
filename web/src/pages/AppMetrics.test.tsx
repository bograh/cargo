import { screen } from "@testing-library/react";
import { mockApi, renderPage } from "../test/utils";
import AppMetrics from "./AppMetrics";

const APP = {
  id: "a1", org_id: "o1", name: "web", slug: "web", source_type: "image",
  image_ref: "nginx", exposed_port: 80, healthcheck_path: "/", auto_deploy: true,
  builder: "auto", git_repo_url: "", git_branch: "", build_context: ".", dockerfile_path: "Dockerfile",
  has_registry_credentials: false, desired_state: "running",
};

it("shows metric tiles from history", async () => {
  mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: false } },
    "GET /apps/a1": { status: 200, body: APP },
    "GET /apps/a1/metrics": {
      status: 200,
      body: [
        { ts: "2026-07-22T00:00:00Z", cpu_pct: 12.5, mem_bytes: 67108864, mem_limit_bytes: 536870912, net_rx_bytes: 0, net_tx_bytes: 0, req_rate: 3, err_rate: 0.1, p50_ms: 20, p95_ms: 80 },
      ],
    },
  });
  renderPage(<AppMetrics />, { path: "/apps/:appId/metrics", route: "/apps/a1/metrics" });
  expect(await screen.findByRole("heading", { name: "Metrics" })).toBeInTheDocument();
  expect(await screen.findByText(/CPU/i)).toBeInTheDocument();
});
