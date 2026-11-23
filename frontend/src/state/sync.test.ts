// WP-15 tests: sync state and events (state/sync.ts; architecture §3.3, §5.4; jira-sync §4.7; ux-spec §2.1).
//
// SPEC-NOTE: "user-started" is decided by sync:started.trigger: "manual" is user-started and gets banners; "open"
//   (sync-on-open) never gets a banner. "config" (leaving Configure) is deliberately NOT asserted either way.
//   A startSync() call followed by sync:started{trigger:"manual"} and a standalone sync:started{trigger:"manual"}
//   (no startSync call) must both raise the banner.
// SPEC-NOTE: only an `offline` failure of a user-started sync is asserted to raise a banner (kind "offline",
//   dismissible, message mentions "offline"); other failure codes (forbidden, filter_invalid, ...) show a tab
//   warning dot in the spec, so no banner assertion is made for them.
// SPEC-NOTE: the header reads syncStatus, so an offline failure of an "open" sync still records state "failed",
//   errorCode "offline" and leaves lastSuccessAt untouched, even though no banner appears.
// SPEC-NOTE: syncStatus after events: started => state "running" (+runId, mode); progress => phase/done/total;
//   finished ok|partial|cancelled => state "idle" with lastStatus; failed => state "failed" with errorCode and
//   errorMessage; lastSuccessAt = finishedAt only for ok|partial.
// SPEC-NOTE: sync:finished ok|partial re-requests the finished standup's view (StandupService.View) and replaces
//   the cache entry; failed|cancelled do not.
// SPEC-NOTE: connection:auth{missing} raises ONE banner kind "auth_missing" and {invalid} ONE banner kind
//   "auth_invalid" (repeat events do not duplicate); each has an action that opens the Connect screen; {ok}
//   removes both. The connection signal's `state` follows the event.
// SPEC-NOTE: startSync resolves (never rejects) when the facade call fails, because callers are key handlers and
//   buttons; an auth_missing / auth_invalid rejection raises the matching banner.
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Api, Connection, StandupView, SyncFinished } from "../api/index";

type Fake = Record<string, Record<string, Mock>>;
const DEFAULTS: Record<string, unknown> = { "standup.list": [], "sync.status": [], "sync.start": 7 };

async function boot() {
  vi.resetModules();
  const idx = await import("../api/index");
  const events = await import("../api/events");
  const errors = await import("../api/errors");
  const fake: Fake = {};
  for (const [svc, methods] of Object.entries(idx.wailsApi)) {
    const f: Record<string, Mock> = (fake[svc] = {});
    for (const m of Object.keys(methods)) f[m] = vi.fn(async () => structuredClone(DEFAULTS[`${svc}.${m}`]));
  }
  // A realistic default: StandupService.View always answers with a minimal view of the requested standup, so code that
  // reads the result of an un-mocked call does not throw. Tests override it where the content matters.
  fake["standup"]!["view"]!.mockImplementation(async (id: number) => ({ standupId: id, rev: 1, name: `S${id}`, speakers: [], tickets: [], lanes: [], axis: [], backlog: [], warnings: [] }));
  idx.setApi(fake as unknown as Api);
  const bus = events.createMemoryBus();
  events.setEventBus(bus);
  return {
    fake, bus, AppError: errors.AppError,
    A: await import("./app"), R: await import("./route"), St: await import("./standup"), Sy: await import("./sync"), U: await import("./ui"),
  };
}

function finished(over: Partial<SyncFinished> = {}): SyncFinished {
  return { runId: 7, standupId: 1, status: "ok", warnings: [], finishedAt: 1_700_000_500, stats: { issues: 3, requests: 4, ms: 50 }, ...over } as SyncFinished;
}
function view(id: number, rev = 1): StandupView {
  return { standupId: id, rev, name: `S${id}`, speakers: [], tickets: [], lanes: [], axis: [], backlog: [], warnings: [] } as unknown as StandupView;
}

let T: Awaited<ReturnType<typeof boot>>;
let off: (() => void) | undefined;
beforeEach(async () => {
  vi.useFakeTimers();
  T = await boot();
  T.R.route.value = { view: "standup", standupId: 1, person: null };
  T.St.views.value = new Map([[1, view(1)]]);
  off = T.Sy.installSyncEvents();
});
afterEach(() => {
  off?.();
  off = undefined;
  vi.useRealTimers();
});
const settle = () => vi.advanceTimersByTimeAsync(0);
const kinds = () => T.U.banners.value.map((b) => b.kind);

describe("startSync and cancelSync", () => {
  it("starts_an_incremental_sync_through_SyncService_Start", async () => {
    await T.Sy.startSync(1, "incremental");
    expect(T.fake.sync!.start).toHaveBeenCalledTimes(1);
    expect(T.fake.sync!.start).toHaveBeenCalledWith(1, "incremental");
  });

  it("starts_a_full_sync", async () => {
    await T.Sy.startSync(2, "full");
    expect(T.fake.sync!.start).toHaveBeenCalledWith(2, "full");
  });

  it("cancels_through_SyncService_Cancel", async () => {
    await T.Sy.cancelSync(1);
    expect(T.fake.sync!.cancel).toHaveBeenCalledWith(1);
  });

  it("resolves_even_when_the_start_call_fails", async () => {
    T.fake.sync!.start!.mockRejectedValue(new T.AppError("internal", "boom", "", false));
    await expect(T.Sy.startSync(1, "incremental")).resolves.toBeUndefined();
  });

  it("raises_the_auth_missing_banner_when_start_is_rejected_for_a_missing_token", async () => {
    T.fake.sync!.start!.mockRejectedValue(new T.AppError("auth_missing", "No token.", "", false));
    await T.Sy.startSync(1, "incremental");
    expect(kinds()).toContain("auth_missing");
  });

  it("raises_the_auth_invalid_banner_when_start_is_rejected_for_a_rejected_token", async () => {
    T.fake.sync!.start!.mockRejectedValue(new T.AppError("auth_invalid", "Rejected.", "", false));
    await T.Sy.startSync(1, "incremental");
    expect(kinds()).toContain("auth_invalid");
  });
});

describe("syncStatus from events", () => {
  it("marks_a_standup_running_on_sync_started", async () => {
    T.bus.emit("sync:started", { runId: 7, standupId: 1, mode: "full", trigger: "manual" });
    await settle();
    const s = T.Sy.syncStatus.value.get(1)!;
    expect(s.state).toBe("running");
    expect(s.runId).toBe(7);
    expect(s.mode).toBe("full");
  });

  it("tracks_phase_and_counts_on_sync_progress", async () => {
    T.bus.emit("sync:started", { runId: 7, standupId: 1, mode: "incremental", trigger: "manual" });
    T.bus.emit("sync:progress", { runId: 7, standupId: 1, phase: "issues", done: 120, total: 300 });
    await settle();
    const s = T.Sy.syncStatus.value.get(1)!;
    expect(s.state).toBe("running");
    expect(s.phase).toBe("issues");
    expect(s.done).toBe(120);
    expect(s.total).toBe(300);
  });

  it("records_success_on_sync_finished_ok", async () => {
    T.bus.emit("sync:started", { runId: 7, standupId: 1, mode: "incremental", trigger: "manual" });
    T.bus.emit("sync:finished", finished({ status: "ok", finishedAt: 1_700_000_500 }));
    await settle();
    const s = T.Sy.syncStatus.value.get(1)!;
    expect(s.state).toBe("idle");
    expect(s.lastStatus).toBe("ok");
    expect(s.lastSuccessAt).toBe(1_700_000_500);
  });

  it("records_a_partial_run_with_its_warnings", async () => {
    T.bus.emit("sync:started", { runId: 7, standupId: 1, mode: "incremental", trigger: "manual" });
    T.bus.emit("sync:finished", finished({ status: "partial", warnings: ["dev activity unavailable"] }));
    await settle();
    const s = T.Sy.syncStatus.value.get(1)!;
    expect(s.state).toBe("idle");
    expect(s.lastStatus).toBe("partial");
    expect(s.warnings).toEqual(["dev activity unavailable"]);
    expect(s.lastSuccessAt).toBe(1_700_000_500);
  });

  it("records_a_failure_without_moving_lastSuccessAt", async () => {
    T.Sy.syncStatus.value = new Map([[1, { standupId: 1, lastSuccessAt: 111, state: "idle" } as never]]);
    T.bus.emit("sync:started", { runId: 7, standupId: 1, mode: "incremental", trigger: "open" });
    T.bus.emit("sync:finished", finished({ status: "failed", errorCode: "offline", errorMessage: "You appear to be offline." }));
    await settle();
    const s = T.Sy.syncStatus.value.get(1)!;
    expect(s.state).toBe("failed");
    expect(s.errorCode).toBe("offline");
    expect(s.errorMessage).toBe("You appear to be offline.");
    expect(s.lastSuccessAt).toBe(111);
  });

  it("keeps_each_standups_status_separate", async () => {
    T.bus.emit("sync:started", { runId: 7, standupId: 1, mode: "incremental", trigger: "manual" });
    T.bus.emit("sync:started", { runId: 8, standupId: 2, mode: "full", trigger: "manual" });
    T.bus.emit("sync:finished", finished({ standupId: 2, runId: 8, status: "ok" }));
    await settle();
    expect(T.Sy.syncStatus.value.get(1)!.state).toBe("running");
    expect(T.Sy.syncStatus.value.get(2)!.state).toBe("idle");
  });
});

describe("refreshing the view after a sync", () => {
  it("re_requests_the_view_when_a_sync_finishes_ok", async () => {
    T.fake.standup!.view!.mockResolvedValue(view(1, 2));
    T.bus.emit("sync:finished", finished({ status: "ok", standupId: 1 }));
    await settle();
    expect(T.fake.standup!.view).toHaveBeenCalledWith(1);
    expect(T.St.views.value.get(1)?.rev).toBe(2);
  });

  it("re_requests_the_view_when_a_sync_finishes_partial", async () => {
    T.fake.standup!.view!.mockResolvedValue(view(1, 2));
    T.bus.emit("sync:finished", finished({ status: "partial" }));
    await settle();
    expect(T.fake.standup!.view).toHaveBeenCalledWith(1);
  });

  it.each(["failed", "cancelled"] as const)("does_not_re_request_the_view_when_a_sync_%s", async (status) => {
    T.bus.emit("sync:finished", finished({ status, errorCode: status === "failed" ? "offline" : undefined }));
    await settle();
    expect(T.fake.standup!.view).not.toHaveBeenCalled();
  });
});

describe("banners: only for syncs the user started", () => {
  it("raises_a_dismissible_offline_banner_when_a_manual_resync_fails_offline", async () => {
    await T.Sy.startSync(1, "incremental");
    T.bus.emit("sync:started", { runId: 7, standupId: 1, mode: "incremental", trigger: "manual" });
    T.bus.emit("sync:finished", finished({ status: "failed", errorCode: "offline", errorMessage: "Offline." }));
    await settle();
    expect(T.U.banners.value).toHaveLength(1);
    const b = T.U.banners.value[0]!;
    expect(b.kind).toBe("offline");
    expect(b.dismissible).toBe(true);
    expect(b.message).toMatch(/offline/i);
  });

  it("raises_the_banner_for_a_manual_trigger_even_without_a_startSync_call", async () => {
    T.bus.emit("sync:started", { runId: 7, standupId: 1, mode: "incremental", trigger: "manual" });
    T.bus.emit("sync:finished", finished({ status: "failed", errorCode: "offline" }));
    await settle();
    expect(kinds()).toEqual(["offline"]);
  });

  it("raises_no_banner_when_the_sync_on_open_attempt_fails_offline", async () => {
    T.bus.emit("sync:started", { runId: 7, standupId: 1, mode: "incremental", trigger: "open" });
    T.bus.emit("sync:finished", finished({ status: "failed", errorCode: "offline", errorMessage: "Offline." }));
    await settle();
    expect(T.U.banners.value).toEqual([]);
    expect(T.Sy.syncStatus.value.get(1)!.errorCode).toBe("offline"); // the header still says "offline · data from …"
  });

  it("raises_no_banner_for_a_successful_manual_sync", async () => {
    T.bus.emit("sync:started", { runId: 7, standupId: 1, mode: "incremental", trigger: "manual" });
    T.bus.emit("sync:finished", finished({ status: "ok" }));
    await settle();
    expect(T.U.banners.value).toEqual([]);
  });
});

describe("connection:auth", () => {
  const conn = (state: string) => ({ state, source: "jira", baseUrl: "https://a.atlassian.net", account: "me", displayName: "Me", devActivity: "ok", capabilities: {} }) as unknown as Connection;

  it("raises_the_connect_to_jira_banner_when_the_token_is_missing_and_its_action_opens_connect", async () => {
    T.A.connection.value = conn("ok");
    T.bus.emit("connection:auth", { state: "missing" });
    await settle();
    expect(kinds()).toEqual(["auth_missing"]);
    const b = T.U.banners.value[0]!;
    expect(b.action).toBeDefined();
    b.action!.run();
    expect(T.R.route.value).toEqual({ view: "connect" });
  });

  it("raises_the_reconnect_banner_when_the_token_is_rejected_and_its_action_opens_connect", async () => {
    T.A.connection.value = conn("ok");
    T.bus.emit("connection:auth", { state: "invalid" });
    await settle();
    expect(kinds()).toEqual(["auth_invalid"]);
    expect(T.U.banners.value[0]!.message).toMatch(/token|reconnect/i);
    T.U.banners.value[0]!.action!.run();
    expect(T.R.route.value).toEqual({ view: "connect" });
  });

  it("does_not_duplicate_the_banner_on_repeated_events", async () => {
    T.bus.emit("connection:auth", { state: "invalid" });
    T.bus.emit("connection:auth", { state: "invalid" });
    await settle();
    expect(kinds()).toEqual(["auth_invalid"]);
  });

  it("removes_the_auth_banners_when_the_connection_is_ok_again", async () => {
    T.bus.emit("connection:auth", { state: "invalid" });
    await settle();
    expect(kinds()).toEqual(["auth_invalid"]);
    T.bus.emit("connection:auth", { state: "ok" });
    await settle();
    expect(T.U.banners.value).toEqual([]);
  });

  it("leaves_unrelated_banners_alone_when_the_connection_is_ok_again", async () => {
    T.U.showBanner({ kind: "recovered", message: "Settings were reset.", dismissible: true });
    T.bus.emit("connection:auth", { state: "invalid" });
    T.bus.emit("connection:auth", { state: "ok" });
    await settle();
    expect(kinds()).toEqual(["recovered"]);
  });

  it("updates_the_connection_state_signal", async () => {
    T.A.connection.value = conn("ok");
    T.bus.emit("connection:auth", { state: "invalid" });
    await settle();
    expect(T.A.connection.value?.state).toBe("invalid");
    expect(T.A.connection.value?.account).toBe("me");
  });
});

describe("installSyncEvents", () => {
  it("stops_reacting_after_unsubscribe", async () => {
    off!();
    off = undefined;
    T.bus.emit("sync:started", { runId: 7, standupId: 1, mode: "incremental", trigger: "manual" });
    T.bus.emit("sync:finished", finished({ status: "ok" }));
    T.bus.emit("connection:auth", { state: "invalid" });
    await settle();
    expect(T.Sy.syncStatus.value.size).toBe(0);
    expect(T.U.banners.value).toEqual([]);
    expect(T.fake.standup!.view).not.toHaveBeenCalled();
  });
});
