import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import NewApp from "./NewApp";
import { mockApi, renderPage } from "../test/utils";

afterEach(() => vi.restoreAllMocks());

test("image mode creates app then deploys", async () => {
  const calls = mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.co", is_instance_admin: false } },
    "POST /orgs/org-1/apps": {
      status: 201,
      body: { id: "app-1", org_id: "org-1", name: "api", slug: "api", source_type: "image" },
    },
    "POST /apps/app-1/deploy": {
      status: 202,
      body: { id: "dep-1", app_id: "app-1", status: "queued", trigger: "manual" },
    },
  });
  renderPage(<NewApp />, { path: "/orgs/:orgId/apps/new", route: "/orgs/org-1/apps/new" });

  await userEvent.type(screen.getByLabelText(/name/i), "api");
  await userEvent.click(screen.getByRole("button", { name: /container image/i }));
  await userEvent.type(screen.getByLabelText(/image reference/i), "nginx:alpine");
  await userEvent.click(screen.getByRole("button", { name: /create & deploy/i }));

  await waitFor(() => expect(screen.getByTestId("navigated")).toBeInTheDocument());
  const create = calls.find((c) => c.path === "/orgs/org-1/apps");
  expect(create?.body).toMatchObject({ name: "api", source_type: "image", image_ref: "nginx:alpine" });
  expect(calls.some((c) => c.path === "/apps/app-1/deploy")).toBe(true);
});

test("git mode shows repo picker when org is connected", async () => {
  mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.co", is_instance_admin: false } },
    "GET /orgs/org-1/github": { status: 200, body: { configured: true, connected: true, account_login: "acme" } },
    "GET /orgs/org-1/github/repos": {
      status: 200,
      body: [{ full_name: "acme/api", clone_url: "https://github.com/acme/api.git", default_branch: "main" }],
    },
  });
  renderPage(<NewApp />, { path: "/orgs/:orgId/apps/new", route: "/orgs/org-1/apps/new" });

  expect(await screen.findByLabelText(/github repository/i)).toBeInTheDocument();
  await screen.findByRole("option", { name: "acme/api" });
  await userEvent.selectOptions(screen.getByLabelText(/github repository/i), "acme/api");
  expect((screen.getByLabelText(/or repository url/i) as HTMLInputElement).value).toBe(
    "https://github.com/acme/api.git",
  );
});

test("compose source collects the file path and web service", async () => {
  const user = userEvent.setup();
  const calls = mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.co", is_instance_admin: false } },
    "GET /orgs/o1/github/status": { status: 200, body: { connected: false } },
    "POST /orgs/o1/apps": { status: 201, body: { id: "a1", org_id: "o1" } },
  });
  renderPage(<NewApp />, { path: "/orgs/:orgId/apps/new", route: "/orgs/o1/apps/new" });

  await user.click(await screen.findByRole("button", { name: /compose file/i }));

  await user.type(screen.getByLabelText(/^name$/i), "stack");
  await user.type(screen.getByLabelText(/repository url/i), "https://github.com/acme/stack.git");
  await user.type(screen.getByLabelText(/web service/i), "frontend");
  await user.click(screen.getByRole("button", { name: /create & deploy/i }));

  await waitFor(() => {
    const post = calls.find((c) => c.method === "POST" && c.path === "/orgs/o1/apps");
    expect(post).toBeTruthy();
    const body = post!.body as Record<string, unknown>;
    expect(body.source_type).toBe("compose");
    expect(body.compose_service).toBe("frontend");
    // Defaulted rather than left blank, matching the placeholder.
    expect(body.compose_path).toBe("docker-compose.yml");
    expect(body.git_repo_url).toBe("https://github.com/acme/stack.git");
  });
});

test("host selector lists org workers and sends host_id", async () => {
  const calls = mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.co", is_instance_admin: false } },
    "GET /orgs/org-1/hosts": {
      status: 200,
      body: [
        { id: "h-online", name: "worker-a", status: "online" },
        { id: "h-down", name: "worker-b", status: "unreachable" },
      ],
    },
    "POST /orgs/org-1/apps": {
      status: 201,
      body: { id: "app-h", org_id: "org-1", name: "api", slug: "api", source_type: "image" },
    },
    "POST /apps/app-h/deploy": {
      status: 202,
      body: { id: "dep-h", app_id: "app-h", status: "queued", trigger: "manual" },
    },
  });
  renderPage(<NewApp />, { path: "/orgs/:orgId/apps/new", route: "/orgs/org-1/apps/new" });

  const sel = await screen.findByLabelText(/worker host/i);
  await screen.findByRole("option", { name: /worker-a \(online\)/i });
  // Unreachable workers are not selectable.
  expect(screen.queryByRole("option", { name: /worker-b/i })).not.toBeInTheDocument();
  await userEvent.selectOptions(sel, "h-online");

  await userEvent.type(screen.getByLabelText(/name/i), "api");
  await userEvent.click(screen.getByRole("button", { name: /container image/i }));
  await userEvent.type(screen.getByLabelText(/image reference/i), "nginx:alpine");
  await userEvent.click(screen.getByRole("button", { name: /create & deploy/i }));

  await waitFor(() => expect(screen.getByTestId("navigated")).toBeInTheDocument());
  const create = calls.find((c) => c.path === "/orgs/org-1/apps");
  expect(create?.body).toMatchObject({ host_id: "h-online" });
});
