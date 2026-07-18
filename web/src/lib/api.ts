export class ApiError extends Error {
  code: string;
  status: number;
  fields?: Record<string, string>;

  constructor(status: number, code: string, message: string, fields?: Record<string, string>) {
    super(message);
    this.status = status;
    this.code = code;
    this.fields = fields;
  }
}

const BASE = "/api/v1";

async function rawRequest(path: string, opts: RequestInit): Promise<Response> {
  return fetch(BASE + path, {
    credentials: "same-origin",
    headers: opts.body ? { "Content-Type": "application/json" } : undefined,
    ...opts,
  });
}

async function toError(res: Response): Promise<ApiError> {
  try {
    const body = await res.json();
    if (body?.error?.code) {
      return new ApiError(res.status, body.error.code, body.error.message, body.error.fields);
    }
  } catch {
    // fall through
  }
  return new ApiError(res.status, "unknown", `request failed (${res.status})`);
}

/**
 * JSON API call with the standard envelope. On a 401 from a non-auth
 * endpoint it attempts one silent refresh and retries.
 */
export async function api<T>(path: string, opts: RequestInit = {}): Promise<T> {
  let res = await rawRequest(path, opts);
  if (res.status === 401 && !path.startsWith("/auth/")) {
    const refreshed = await rawRequest("/auth/refresh", { method: "POST" });
    if (refreshed.ok) {
      res = await rawRequest(path, opts);
    }
  }
  if (!res.ok) {
    throw await toError(res);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

export function post<T>(path: string, body?: unknown): Promise<T> {
  return api<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) });
}

export function put<T>(path: string, body: unknown): Promise<T> {
  return api<T>(path, { method: "PUT", body: JSON.stringify(body) });
}

export function patch<T>(path: string, body: unknown): Promise<T> {
  return api<T>(path, { method: "PATCH", body: JSON.stringify(body) });
}

export function del<T>(path: string): Promise<T> {
  return api<T>(path, { method: "DELETE" });
}
