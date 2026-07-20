import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { DatabasesTab } from "./DatabasesTab";
import { mockApi, renderPage } from "../test/utils";

afterEach(() => vi.restoreAllMocks());

const baseRoutes = {
  "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.co", is_instance_admin: false } },
};

test("provisioning submits the expected payload", async () => {
  const calls = mockApi({
    ...baseRoutes,
    "GET /orgs/org-1/databases": { status: 200, body: [] },
    "POST /orgs/org-1/databases": { status: 202, body: { id: "db-1", status: "provisioning" } },
  });
  const user = userEvent.setup();
  renderPage(<DatabasesTab orgId="org-1" />);

  await user.type(await screen.findByLabelText("Name"), "my-db");
  await user.click(screen.getByRole("button", { name: "Provision" }));

  await waitFor(() => {
    const call = calls.find((c) => c.method === "POST" && c.path === "/orgs/org-1/databases");
    expect(call).toBeTruthy();
    expect(call?.body).toEqual({
      name: "my-db",
      engine: "postgres",
      version: "16",
      expose_port: false,
    });
  });
});

test("provisioning a redis instance defaults redis_mode to acl", async () => {
  const calls = mockApi({
    ...baseRoutes,
    "GET /orgs/org-1/databases": { status: 200, body: [] },
    "POST /orgs/org-1/databases": { status: 202, body: { id: "db-2", status: "provisioning" } },
  });
  const user = userEvent.setup();
  renderPage(<DatabasesTab orgId="org-1" />);

  await user.type(await screen.findByLabelText("Name"), "my-redis");
  await user.selectOptions(screen.getByLabelText("Engine"), "redis");
  await user.click(screen.getByRole("button", { name: "Provision" }));

  await waitFor(() => {
    const call = calls.find((c) => c.method === "POST" && c.path === "/orgs/org-1/databases");
    expect(call).toBeTruthy();
    expect(call?.body).toEqual({
      name: "my-redis",
      engine: "redis",
      version: "7",
      redis_mode: "acl",
      expose_port: false,
    });
  });
});

test("attach shows a one-time URL modal with the exact response url", async () => {
  mockApi({
    ...baseRoutes,
    "GET /orgs/org-1/databases": {
      status: 200,
      body: [
        {
          id: "db-1",
          org_id: "org-1",
          name: "my-db",
          engine: "postgres",
          version: "16",
          redis_mode: "",
          host_port: null,
          status: "running",
          attachment_count: 0,
        },
      ],
    },
    "GET /databases/db-1": {
      status: 200,
      body: {
        id: "db-1",
        org_id: "org-1",
        name: "my-db",
        engine: "postgres",
        version: "16",
        redis_mode: "",
        host_port: null,
        status: "running",
        attachment_count: 0,
        size_bytes: 1024,
        attachments: [],
      },
    },
    "GET /orgs/org-1/apps": {
      status: 200,
      body: [{ id: "app-1", org_id: "org-1", name: "my-app", slug: "my-app", source_type: "git" }],
    },
    "GET /databases/db-1/snapshots": { status: 200, body: [] },
    "POST /databases/db-1/attachments": {
      status: 201,
      body: { url: "postgres://user:pass@host:5432/db1", env_key: "DATABASE_URL" },
    },
  });
  const user = userEvent.setup();
  renderPage(<DatabasesTab orgId="org-1" />);

  await user.click(await screen.findByRole("button", { name: "Manage" }));
  await user.selectOptions(await screen.findByLabelText("attach app"), "app-1");
  await user.click(screen.getByRole("button", { name: "Attach" }));

  expect(await screen.findByText("postgres://user:pass@host:5432/db1")).toBeInTheDocument();
  expect(screen.getByText("Save this now — it will not be shown again.")).toBeInTheDocument();
});

test("delete confirm button is disabled until the typed name matches", async () => {
  mockApi({
    ...baseRoutes,
    "GET /orgs/org-1/databases": {
      status: 200,
      body: [
        {
          id: "db-1",
          org_id: "org-1",
          name: "my-db",
          engine: "postgres",
          version: "16",
          redis_mode: "",
          host_port: null,
          status: "running",
          attachment_count: 0,
        },
      ],
    },
    "GET /databases/db-1": {
      status: 200,
      body: {
        id: "db-1",
        org_id: "org-1",
        name: "my-db",
        engine: "postgres",
        version: "16",
        redis_mode: "",
        host_port: null,
        status: "running",
        attachment_count: 0,
        size_bytes: 0,
        attachments: [],
      },
    },
    "GET /orgs/org-1/apps": { status: 200, body: [] },
    "GET /databases/db-1/snapshots": { status: 200, body: [] },
  });
  const user = userEvent.setup();
  renderPage(<DatabasesTab orgId="org-1" />);

  await user.click(await screen.findByRole("button", { name: "Manage" }));
  await user.click(await screen.findByRole("button", { name: "Delete database" }));

  const confirmButton = screen.getByRole("button", { name: "Delete" });
  expect(confirmButton).toBeDisabled();

  await user.type(screen.getByLabelText(/type my-db to confirm/i), "my-db");

  expect(confirmButton).not.toBeDisabled();
});

test("detach calls DELETE on the attachment", async () => {
  const calls = mockApi({
    ...baseRoutes,
    "GET /orgs/org-1/databases": {
      status: 200,
      body: [
        {
          id: "db-1",
          org_id: "org-1",
          name: "my-db",
          engine: "postgres",
          version: "16",
          redis_mode: "",
          host_port: null,
          status: "running",
          attachment_count: 1,
        },
      ],
    },
    "GET /databases/db-1": {
      status: 200,
      body: {
        id: "db-1",
        org_id: "org-1",
        name: "my-db",
        engine: "postgres",
        version: "16",
        redis_mode: "",
        host_port: null,
        status: "running",
        attachment_count: 1,
        size_bytes: 0,
        attachments: [{ id: "att-1", app_id: "app-1", db_name: "db1" }],
      },
    },
    "GET /orgs/org-1/apps": {
      status: 200,
      body: [{ id: "app-1", org_id: "org-1", name: "my-app", slug: "my-app", source_type: "git" }],
    },
    "GET /databases/db-1/snapshots": { status: 200, body: [] },
    "DELETE /databases/db-1/attachments/app-1": { status: 200, body: { status: "detached" } },
  });
  const user = userEvent.setup();
  renderPage(<DatabasesTab orgId="org-1" />);

  await user.click(await screen.findByRole("button", { name: "Manage" }));
  await user.click(await screen.findByRole("button", { name: "Detach" }));

  await waitFor(() => {
    expect(
      calls.some((c) => c.method === "DELETE" && c.path === "/databases/db-1/attachments/app-1"),
    ).toBe(true);
  });
});
