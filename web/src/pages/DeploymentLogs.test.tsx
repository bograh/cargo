import { act, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import DeploymentLogs from "./DeploymentLogs";
import { mockApi, renderPage } from "../test/utils";

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  url: string;
  closed = false;

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  emit(data: string) {
    this.onmessage?.({ data } as MessageEvent);
  }

  close() {
    this.closed = true;
  }
}

afterEach(() => {
  vi.restoreAllMocks();
  FakeEventSource.instances = [];
});

test("renders streamed log lines", async () => {
  vi.stubGlobal("EventSource", FakeEventSource);
  mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.co", is_instance_admin: false } },
    "GET /deployments/dep-1": {
      status: 200,
      body: { id: "dep-1", app_id: "app-1", status: "building", trigger: "manual", commit_sha: "", image_tag: "", error: "" },
    },
  });
  renderPage(<DeploymentLogs />, { path: "/deployments/:deploymentId", route: "/deployments/dep-1" });

  expect(await screen.findByText(/waiting for logs/i)).toBeInTheDocument();
  const es = FakeEventSource.instances.find((e) => e.url.includes("/deployments/dep-1/logs"));
  expect(es).toBeDefined();

  act(() => {
    es!.emit("==> cloning repo");
    es!.emit("==> building with dockerfile");
  });

  expect(await screen.findByText("==> cloning repo")).toBeInTheDocument();
  expect(screen.getByText("==> building with dockerfile")).toBeInTheDocument();
  expect(await screen.findByText("building")).toBeInTheDocument();
});
