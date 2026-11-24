// Entry (orchestrator-owned): seed state from window.__CUISLE_BOOT__ (fallback AppService.Bootstrap()), mount, mark
// first-view (architecture §5.1). In `--mode mock` the mock facade and memory event bus are installed first.
import { render } from "preact";
import { api, setApi } from "./api";
import { setEventBus } from "./api/events";
import type { Bootstrap } from "./api/types";
import { initFromBoot } from "./state/app";
import { installStandupEvents } from "./state/standup";
import { installSyncEvents } from "./state/sync";
import { installUpdateEvents, loadUpdateStatus } from "./state/update";
import { installGlobalKeys } from "./keys";
import { flush } from "./state/save";
import { App } from "./App";
import "./styles/tokens.css";
import "./styles/base.css";
import "./components/components.css";

async function start() {
  if (import.meta.env.MODE === "mock") {
    const { createMockApi } = await import("./api/mock");
    const mock = createMockApi();
    setApi(mock);
    setEventBus(mock.bus);
  }
  const boot: Bootstrap = window.__CUISLE_BOOT__ ?? (await api().app.bootstrap());
  initFromBoot(boot);
  installStandupEvents();
  installSyncEvents();
  installUpdateEvents();
  void loadUpdateStatus(); // local only; Go runs the daily check itself (ADR 0016)
  installGlobalKeys();
  window.addEventListener("pagehide", () => void flush());
  window.addEventListener("error", (e) => void api().app.logClientError(String(e.message), String(e.error?.stack ?? "")).catch(() => {}));
  window.addEventListener("unhandledrejection", (e) => void api().app.logClientError(String(e.reason), String(e.reason?.stack ?? "")).catch(() => {}));
  render(<App />, document.getElementById("app")!);
  requestAnimationFrame(() => {
    performance.mark("first-view");
    const m = performance.getEntriesByName("first-view")[0];
    if (m) void api().app.reportPerf({ "first-view": m.startTime }).catch(() => {});
  });
}

void start();
