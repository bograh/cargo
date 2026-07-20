import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { AuthProvider } from "../../auth";
import NotFound from "../../pages/NotFound";
import { mockApi, renderPage } from "../../test/utils";
import { Shell } from "./Shell";

const ME = { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: false } };
const ORGS = { status: 200, body: [{ ID: "o1", Name: "Acme", Slug: "acme", Role: "owner" }] };
const ORG = { status: 200, body: { organization: { id: "o1", name: "Acme", slug: "acme" }, role: "owner" } };
const APP = {
  status: 200,
  body: { id: "a1", org_id: "o1", name: "web", slug: "web", source_type: "git", auto_deploy: true },
};

describe("Shell", () => {
  it("shows org-scope nav on org pages", async () => {
    mockApi({
      "GET /auth/me": ME,
      "GET /orgs": ORGS,
      "GET /orgs/o1": ORG,
      "GET /orgs/o1/apps": { status: 200, body: [] },
    });
    renderPage(<Shell />, { path: "/orgs/:orgId", route: "/orgs/o1" });
    expect(await screen.findByRole("link", { name: /apps/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /databases/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /members/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /settings/i })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /deployments/i })).not.toBeInTheDocument();
  });

  it("switches to app-scope nav on app pages", async () => {
    mockApi({
      "GET /auth/me": ME,
      "GET /orgs": ORGS,
      "GET /apps/a1": APP,
      "GET /apps/a1/deployments": { status: 200, body: [] },
    });
    renderPage(<Shell />, { path: "/apps/:appId/*", route: "/apps/a1/deployments" });
    expect(await screen.findByRole("link", { name: /overview/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /deployments/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /environment/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /domains/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /all apps/i })).toBeInTheDocument();
  });

  it("renders not-found inside the shell for unmatched routes", async () => {
    mockApi({
      "GET /auth/me": ME,
      "GET /orgs": ORGS,
    });
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter initialEntries={["/orgs/o1/databases"]}>
          <AuthProvider>
            <Routes>
              <Route element={<Shell />}>
                <Route path="*" element={<NotFound />} />
              </Route>
            </Routes>
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(await screen.findByText("Page not found")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /apps/i })).toBeInTheDocument();
  });
});
