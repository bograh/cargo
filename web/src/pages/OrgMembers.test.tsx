import { screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import OrgMembers from "./OrgMembers";
import { mockApi, renderPage } from "../test/utils";

afterEach(() => vi.restoreAllMocks());

function routes(role: string) {
  return {
    "GET /auth/me": { status: 200, body: { id: "u1", email: "me@x.co", is_instance_admin: false } },
    "GET /orgs/org-1": {
      status: 200,
      body: { organization: { id: "org-1", name: "Acme", slug: "acme" }, role },
    },
    "GET /orgs/org-1/members": {
      status: 200,
      body: [
        { UserID: "u1", Role: role, Email: "me@x.co" },
        { UserID: "u2", Role: "member", Email: "teammate@x.co" },
      ],
    },
    "GET /orgs/org-1/invites": { status: 200, body: [] },
  };
}

test("owner sees role controls and invite section", async () => {
  mockApi(routes("owner"));
  renderPage(<OrgMembers />, { path: "/orgs/:orgId/members", route: "/orgs/org-1/members" });

  expect(await screen.findByText("teammate@x.co")).toBeInTheDocument();
  expect(screen.getByLabelText("role for teammate@x.co")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /create invite link/i })).toBeInTheDocument();
});

test("viewer sees read-only members and no invite controls", async () => {
  mockApi(routes("viewer"));
  renderPage(<OrgMembers />, { path: "/orgs/:orgId/members", route: "/orgs/org-1/members" });

  expect(await screen.findByText("teammate@x.co")).toBeInTheDocument();
  expect(screen.queryByLabelText("role for teammate@x.co")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /create invite link/i })).not.toBeInTheDocument();
});
