// Vitest setup (orchestrator-owned): every test starts with the mock facade and an in-memory event bus where tests
// install them explicitly via setApi/setEventBus. Nothing here performs network I/O.
import { cleanup } from "@testing-library/preact";
import { afterEach } from "vitest";

afterEach(() => {
  cleanup();
});
