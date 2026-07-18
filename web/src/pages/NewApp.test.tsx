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
