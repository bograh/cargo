import { screen } from "@testing-library/react";
import { mockApi, renderPage } from "../test/utils";
import AppOverview from "./AppOverview";

const APP = {
  id: "a1", org_id: "o1", name: "web", slug: "web", source_type: "git",
  git_repo_url: "https://github.com/acme/web", git_branch: "main", builder: "auto",
  exposed_port: 3000, healthcheck_path: "/", auto_deploy: true,
};

it("shows app summary, status, and quick actions", async () => {
  mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: false } },
    "GET /apps/a1": { status: 200, body: APP },
    "GET /apps/a1/deployments": {
      status: 200,
      body: [{ id: "d1", app_id: "a1", trigger: "manual", status: "live", commit_sha: "abc123def", image_tag: "img", created_at: "2026-07-19T00:00:00Z" }],
    },
    "GET /instance/info": { status: 200, body: { apps_domain_suffix: "apps.example.com" } },
  });
  renderPage(<AppOverview />, { path: "/apps/:appId", route: "/apps/a1" });
  expect(await screen.findByRole("heading", { name: "web" })).toBeInTheDocument();
  expect(await screen.findByText("web.apps.example.com")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /deploy/i })).toBeInTheDocument();
  expect(screen.getAllByText("live").length).toBeGreaterThan(0);
});
