// WP-T9 tests: sync:progress that is not the current run's running progress is ignored. The media phase keeps ticking after
// sync:finished (jira-sync §2.14), so a late tick must not turn an idle standup into a running one.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { setEventBus, createMemoryBus } from "../api/events";
import type { SyncFinished } from "../api/types";
import { installSyncEvents, syncStatus } from "./sync";

let bus: ReturnType<typeof createMemoryBus>;
let off: () => void;
beforeEach(() => {
  bus = createMemoryBus();
  setEventBus(bus);
  syncStatus.value = new Map();
  off = installSyncEvents();
});
afterEach(() => off());

const progress = (runId: number, phase: string, done: number, total: number) => bus.emit("sync:progress", { runId, standupId: 1, phase, done, total });
const finished = (runId: number): SyncFinished =>
  ({ runId, standupId: 1, status: "ok", warnings: [], finishedAt: 5000, stats: { issues: 1, requests: 1, ms: 1 } }) as unknown as SyncFinished;

describe("sync:progress filtering", () => {
  it("moves the status of the run that is running", () => {
    bus.emit("sync:started", { runId: 7, standupId: 1, mode: "incremental", trigger: "manual" });
    progress(7, "issues", 3, 10);
    expect(syncStatus.value.get(1)).toMatchObject({ state: "running", runId: 7, phase: "issues", done: 3, total: 10 });
  });

  it("ignores a late media tick after the run finished: the standup stays idle", () => {
    bus.emit("sync:started", { runId: 7, standupId: 1, mode: "incremental", trigger: "manual" });
    bus.emit("sync:finished", finished(7));
    progress(7, "media", 2, 3);
    expect(syncStatus.value.get(1)?.state).toBe("idle");
    expect(syncStatus.value.get(1)?.phase).not.toBe("media");
    expect(syncStatus.value.get(1)?.done).not.toBe(2);
  });

  it("ignores progress of another run id", () => {
    bus.emit("sync:started", { runId: 8, standupId: 1, mode: "incremental", trigger: "manual" });
    progress(7, "media", 2, 3);
    expect(syncStatus.value.get(1)).toMatchObject({ state: "running", runId: 8, phase: "", done: 0 });
  });

  it("ignores progress for a standup with no known run", () => {
    progress(7, "scan", 1, 2);
    expect(syncStatus.value.get(1)).toBeUndefined();
  });
});
