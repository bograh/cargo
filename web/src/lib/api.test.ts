import { afterEach, expect, test, vi } from "vitest";
import { api, ApiError } from "./api";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => vi.restoreAllMocks());

test("api parses the error envelope", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      jsonResponse(404, { error: { code: "not_found", message: "application not found" } }),
    ),
  );
  const err = (await api("/apps/x").catch((e) => e)) as ApiError;
  expect(err).toBeInstanceOf(ApiError);
  expect(err.code).toBe("not_found");
  expect(err.message).toBe("application not found");
});

test("api refreshes once on 401 then retries", async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(jsonResponse(401, { error: { code: "unauthenticated", message: "x" } }))
    .mockResolvedValueOnce(jsonResponse(200, { status: "refreshed" }))
    .mockResolvedValueOnce(jsonResponse(200, { ok: true }));
  vi.stubGlobal("fetch", fetchMock);

  const out = await api<{ ok: boolean }>("/orgs");
  expect(out.ok).toBe(true);
  expect(fetchMock).toHaveBeenCalledTimes(3);
  expect(fetchMock.mock.calls[1][0]).toBe("/api/v1/auth/refresh");
});

test("api rejects when refresh also fails", async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(jsonResponse(401, { error: { code: "unauthenticated", message: "x" } }))
    .mockResolvedValueOnce(jsonResponse(401, { error: { code: "unauthenticated", message: "x" } }));
  vi.stubGlobal("fetch", fetchMock);

  const err = (await api("/orgs").catch((e) => e)) as ApiError;
  expect(err.status).toBe(401);
  expect(fetchMock).toHaveBeenCalledTimes(2);
});

test("auth endpoints do not trigger refresh", async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValue(jsonResponse(401, { error: { code: "invalid_credentials", message: "no" } }));
  vi.stubGlobal("fetch", fetchMock);

  const err = (await api("/auth/login", { method: "POST" }).catch((e) => e)) as ApiError;
  expect(err.code).toBe("invalid_credentials");
  expect(fetchMock).toHaveBeenCalledTimes(1);
});
