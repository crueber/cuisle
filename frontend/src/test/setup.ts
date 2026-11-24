// Vitest setup (orchestrator-owned): every test starts with the mock facade and an in-memory event bus where tests
// install them explicitly via setApi/setEventBus. Nothing here performs network I/O.
import { cleanup } from "@testing-library/preact";
import { afterAll, afterEach, vi } from "vitest";

afterEach(() => {
  cleanup();
});

// @wailsio/runtime's drag module starts a 50 ms window.setInterval when it is imported; under jsdom it clears itself on
// its first tick. A test file that finishes within 50 ms would tear down jsdom first, so that tick throws
// "window is not defined" (an unhandled error in CI). Waiting one tick after each file lets it clear while window exists.
afterAll(async () => {
  vi.useRealTimers();
  await new Promise((resolve) => setTimeout(resolve, 60));
});
