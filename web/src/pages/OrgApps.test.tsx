import { screen } from "@testing-library/react";
import { mockApi, renderPage } from "../test/utils";
import OrgApps from "./OrgApps";

const APP = {
  id: "a1", org_id: "o1", name: "web", slug: "web", source_type: "git",
  git_repo_url: "https://github.com/acme/web", git_branch: "main",
  auto_deploy: true, created_at: "2026-07-01T00:00:00Z",
};

function mock() {
  return mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: false } },
    "GET /orgs/o1": { status: 200, body: { organization: { id: "o1", name: "Acme", slug: "acme" }, role: "owner" } },
    "GET /orgs/o1/apps": { status: 200, body: [APP] },
    "GET /apps/a1/deployments": {
      status: 200,
      body: [{ id: "d1", app_id: "a1", trigger: "manual", status: "live", commit_sha: "abc123", image_tag: "img", created_at: "2026-07-19T00:00:00Z" }],
    },
  });
}

describe("OrgApps", () => {
  it("renders app cards with live status and links to the app", async () => {
    mock();
    renderPage(<OrgApps />, { path: "/orgs/:orgId", route: "/orgs/o1" });
    const link = await screen.findByRole("link", { name: /web/i });
    expect(link).toHaveAttribute("href", "/apps/a1");
    expect(await screen.findByText("live")).toBeInTheDocument();
  });

  it("shows empty state when no apps", async () => {
    mockApi({
      "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: false } },
      "GET /orgs/o1": { status: 200, body: { organization: { id: "o1", name: "Acme", slug: "acme" }, role: "owner" } },
      "GET /orgs/o1/apps": { status: 200, body: [] },
    });
    renderPage(<OrgApps />, { path: "/orgs/:orgId", route: "/orgs/o1" });
    expect(await screen.findByText(/no applications yet/i)).toBeInTheDocument();
  });
});
