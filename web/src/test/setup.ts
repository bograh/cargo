import "@testing-library/jest-dom/vitest";

class MockEventSource {
  url: string;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(url: string) {
    this.url = url;
  }
  close() {}
}
globalThis.EventSource = globalThis.EventSource ?? (MockEventSource as unknown as typeof EventSource);
