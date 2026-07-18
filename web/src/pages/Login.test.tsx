import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import Login from "./Login";
import { mockApi, renderPage } from "../test/utils";

afterEach(() => vi.restoreAllMocks());

const noSession = {
  "GET /auth/me": { status: 401, body: { error: { code: "unauthenticated", message: "x" } } },
};

test("shows API error message on failed login", async () => {
  mockApi({
    ...noSession,
    "POST /auth/login": {
      status: 401,
      body: { error: { code: "invalid_credentials", message: "invalid email or password" } },
    },
  });
  renderPage(<Login />, { path: "/login", route: "/login" });

  await userEvent.type(screen.getByLabelText(/email/i), "a@b.co");
  await userEvent.type(screen.getByLabelText(/password/i), "wrong-password");
  await userEvent.click(screen.getByRole("button", { name: /log in/i }));

  expect(await screen.findByText("invalid email or password")).toBeInTheDocument();
});

test("successful login navigates away", async () => {
  mockApi({
    ...noSession,
    "POST /auth/login": {
      status: 200,
      body: { id: "u1", email: "a@b.co", is_instance_admin: false },
    },
  });
  renderPage(<Login />, { path: "/login", route: "/login" });

  await userEvent.type(screen.getByLabelText(/email/i), "a@b.co");
  await userEvent.type(screen.getByLabelText(/password/i), "password-123");
  await userEvent.click(screen.getByRole("button", { name: /log in/i }));

  await waitFor(() => expect(screen.getByTestId("navigated")).toBeInTheDocument());
});
