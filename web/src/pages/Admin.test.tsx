import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import Admin from "./Admin";
import { mockApi, renderPage } from "../test/utils";

afterEach(() => vi.restoreAllMocks());

function adminRoutes() {
  return {
    "GET /auth/me": { status: 200, body: { id: "u1", email: "root@x.co", is_instance_admin: true } },
    "GET /admin/settings": {
      status: 200,
      body: { apps_domain_suffix: "apps.example.com", smtp: { configured: false } },
    },
    "GET /admin/settings/github-app": { status: 200, body: { configured: false, app_slug: "", app_id: 0 } },
    "GET /admin/settings/oidc": { status: 200, body: { configured: false, issuer_url: "", client_id: "" } },
    "GET /admin/users": { status: 200, body: [{ id: "u1", email: "root@x.co", is_instance_admin: true }] },
    "GET /admin/orgs": { status: 200, body: [] },
    "PUT /admin/settings/apps-domain-suffix": { status: 200, body: { status: "saved" } },
  };
}

test("shows current suffix and saves a new one", async () => {
  const calls = mockApi(adminRoutes());
  renderPage(<Admin />, { path: "/admin", route: "/admin" });

  expect(await screen.findByText("apps.example.com")).toBeInTheDocument();
  await userEvent.type(screen.getByLabelText(/apps domain suffix/i), "apps.new.example.com");
  await userEvent.click(screen.getByRole("button", { name: /save suffix/i }));

  await waitFor(() => {
    const call = calls.find((c) => c.path === "/admin/settings/apps-domain-suffix");
    expect(call?.body).toEqual({ suffix: "apps.new.example.com" });
  });
});

test("saves oidc config and never displays the secret", async () => {
  const calls = mockApi({
    ...adminRoutes(),
    "PUT /admin/settings/oidc": { status: 200, body: { status: "saved" } },
  });
  renderPage(<Admin />, { path: "/admin", route: "/admin" });

  await userEvent.type(await screen.findByLabelText(/issuer url/i), "https://idp.example.com/realms/cargo");
  await userEvent.type(screen.getByLabelText(/client id/i), "cargo-web");
  const secret = screen.getByLabelText(/client secret/i);
  await userEvent.type(secret, "super-secret");
  await userEvent.click(screen.getByRole("button", { name: /save oidc/i }));

  await waitFor(() => {
    const call = calls.find((c) => c.path === "/admin/settings/oidc" && c.method === "PUT");
    expect(call?.body).toEqual({
      issuer_url: "https://idp.example.com/realms/cargo",
      client_id: "cargo-web",
      client_secret: "super-secret",
    });
  });
  await waitFor(() => expect(secret).toHaveValue(""));
  expect(secret).toHaveAttribute("type", "password");
});

test("clear oidc calls DELETE", async () => {
  const calls = mockApi({
    ...adminRoutes(),
    "GET /admin/settings/oidc": {
      status: 200,
      body: { configured: true, issuer_url: "https://idp.example.com", client_id: "cargo-web" },
    },
    "DELETE /admin/settings/oidc": { status: 200, body: { status: "cleared" } },
  });
  renderPage(<Admin />, { path: "/admin", route: "/admin" });

  await userEvent.click(await screen.findByRole("button", { name: /^clear$/i }));

  await waitFor(() => {
    expect(calls.some((c) => c.path === "/admin/settings/oidc" && c.method === "DELETE")).toBe(true);
  });
});
