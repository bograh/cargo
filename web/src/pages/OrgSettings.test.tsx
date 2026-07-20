import { screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import OrgSettings from "./OrgSettings";
import { mockApi, renderPage } from "../test/utils";

afterEach(() => vi.restoreAllMocks());

function routes(role: string) {
  return {
    "GET /auth/me": { status: 200, body: { id: "u1", email: "me@x.co", is_instance_admin: false } },
    "GET /orgs/org-1": {
      status: 200,
      body: { organization: { id: "org-1", name: "Acme", slug: "acme" }, role },
    },
    "GET /orgs/org-1/github": {
      status: 200,
      body: { configured: false, connected: false, account_login: "" },
    },
  };
}

test("owner sees the danger zone delete control", async () => {
  mockApi(routes("owner"));
  renderPage(<OrgSettings />, { path: "/orgs/:orgId/settings", route: "/orgs/org-1/settings" });

  expect(await screen.findByRole("button", { name: /delete organization/i })).toBeInTheDocument();
});

test("non-owner does not see the danger zone", async () => {
  mockApi(routes("admin"));
  renderPage(<OrgSettings />, { path: "/orgs/:orgId/settings", route: "/orgs/org-1/settings" });

  expect(await screen.findByText("GitHub")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /delete organization/i })).not.toBeInTheDocument();
});
