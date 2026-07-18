import { screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { DomainsTab } from "./DomainsTab";
import { mockApi, renderPage } from "../test/utils";
import type { App } from "../lib/types";

afterEach(() => vi.restoreAllMocks());

const app = { id: "app-1", slug: "my-api", org_id: "org-1" } as App;

test("renders auto subdomain and custom domain statuses", async () => {
  mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.co", is_instance_admin: false } },
    "GET /instance/info": { status: 200, body: { apps_domain_suffix: "apps.example.com" } },
    "GET /apps/app-1/domains": {
      status: 200,
      body: [
        { id: "d1", hostname: "api.example.com", status: "active" },
        { id: "d2", hostname: "new.example.com", status: "misconfigured" },
      ],
    },
  });
  renderPage(<DomainsTab app={app} />);

  expect(await screen.findByText("my-api.apps.example.com")).toBeInTheDocument();
  expect(await screen.findByText("api.example.com")).toBeInTheDocument();
  expect(screen.getByText("active")).toBeInTheDocument();
  expect(screen.getByText("misconfigured")).toBeInTheDocument();
});
