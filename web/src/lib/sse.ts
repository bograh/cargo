import { refreshSession } from "./api";

export interface StreamHandlers {
  onOpen?: () => void;
  onMessage: (data: string) => void;
  onDisconnect?: () => void;
  /** Return false to stop reconnecting (e.g. the stream reached its end). */
  shouldReconnect?: () => boolean;
}

const MIN_BACKOFF_MS = 1000;
const MAX_BACKOFF_MS = 15000;

/**
 * Opens an SSE stream that survives an expired session. The server re-checks
 * the access cookie while a stream runs and closes it once that cookie lapses;
 * a plain EventSource would then retry with the same stale cookie, get a 401,
 * and give up for good. Here every drop refreshes the session (single-flighted
 * with the rest of the API) and reopens the stream with backoff.
 *
 * Each (re)connect replays the server's backlog, so callers should reset their
 * buffer in onOpen. Returns a function that closes the stream for good.
 */
export function openStream(url: string, handlers: StreamHandlers): () => void {
  let es: EventSource | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let closed = false;
  let backoff = MIN_BACKOFF_MS;

  const connect = () => {
    if (closed) return;
    es = new EventSource(url);
    es.onopen = () => {
      backoff = MIN_BACKOFF_MS;
      handlers.onOpen?.();
    };
    es.onmessage = (e) => handlers.onMessage(e.data);
    es.onerror = () => {
      // Take over from EventSource's own retry, which would reuse the cookie.
      es?.close();
      es = null;
      handlers.onDisconnect?.();
      if (closed || handlers.shouldReconnect?.() === false) return;
      timer = setTimeout(async () => {
        if (closed) return;
        // A failed refresh means the session is gone; auth handles the
        // redirect, so stop here rather than retrying forever.
        if (!(await refreshSession())) return;
        connect();
      }, backoff);
      backoff = Math.min(backoff * 2, MAX_BACKOFF_MS);
    };
  };

  connect();
  return () => {
    closed = true;
    clearTimeout(timer);
    es?.close();
  };
}
