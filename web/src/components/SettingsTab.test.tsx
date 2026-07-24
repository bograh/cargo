import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { SettingsTab } from "./SettingsTab";
import { mockApi, renderPage } from "../test/utils";
import type { App } from "../lib/types";

afterEach(() => vi.restoreAllMocks());

const app: App = {
  id: "a1", org_id: "o1", name: "web", slug: "web", source_type: "image",
  builder: "auto", git_repo_url: "", git_branch: "", image_ref: "nginx:alpine",
  exposed_port: 3000, healthcheck_path: "/", auto_deploy: true,
  build_context: ".", dockerfile_path: "Dockerfile", has_registry_credentials: false,
  desired_state: "running", mem_limit: "256m", cpu_limit: "0.5", pids_limit: 128,
};

test("prefills resource limits and sends them in the PATCH payload", async () => {
  const user = userEvent.setup();
  const calls = mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.co", is_instance_admin: false } },
    "PATCH /apps/a1": { status: 200, body: { ...app } },
  });
  renderPage(<SettingsTab app={app} />);

  // Current overrides are prefilled.
  expect(await screen.findByLabelText(/memory/i)).toHaveValue("256m");
  expect(screen.getByLabelText(/cpus/i)).toHaveValue("0.5");
  expect(screen.getByLabelText(/max processes/i)).toHaveValue(128);

  // Change memory, save, and assert the payload carries all three limits.
  const mem = screen.getByLabelText(/memory/i);
  await user.clear(mem);
  await user.type(mem, "1g");
  await user.click(screen.getByRole("button", { name: /save changes/i }));

  await waitFor(() => {
    const patch = calls.find((c) => c.method === "PATCH" && c.path === "/apps/a1");
    expect(patch).toBeTruthy();
    const body = patch!.body as Record<string, unknown>;
    expect(body.mem_limit).toBe("1g");
    expect(body.cpu_limit).toBe("0.5");
    expect(body.pids_limit).toBe(128);
  });
});

test("blank limits are sent as cleared (default) values", async () => {
  const user = userEvent.setup();
  const cleared: App = { ...app, mem_limit: null, cpu_limit: null, pids_limit: null };
  const calls = mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.co", is_instance_admin: false } },
    "PATCH /apps/a1": { status: 200, body: { ...cleared } },
  });
  renderPage(<SettingsTab app={cleared} />);

  await user.click(await screen.findByRole("button", { name: /save changes/i }));

  await waitFor(() => {
    const patch = calls.find((c) => c.method === "PATCH" && c.path === "/apps/a1");
    expect(patch).toBeTruthy();
    const body = patch!.body as Record<string, unknown>;
    expect(body.mem_limit).toBe("");
    expect(body.cpu_limit).toBe("");
    expect(body.pids_limit).toBe(0);
  });
});
