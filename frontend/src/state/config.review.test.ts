// WP-38F: frontend review fixes in state/config.ts, state/save.ts, state/standup.ts, state/ui.ts, state/route.ts.
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Api, SaveAck, SpeakerConfig, StandupConfig, StandupTab, StandupView } from "../api/index";

type Fake = Record<string, Record<string, Mock>>;

async function boot() {
  vi.resetModules();
  const idx = await import("../api/index");
  const events = await import("../api/events");
  const errors = await import("../api/errors");
  const fake: Fake = {};
  for (const [svc, methods] of Object.entries(idx.wailsApi)) {
    const f: Record<string, Mock> = (fake[svc] = {});
    for (const m of Object.keys(methods)) f[m] = vi.fn(async () => (svc === "standup" && m === "list" ? [] : svc === "sync" && m === "start" ? 7 : undefined));
  }
  fake["standup"]!["view"]!.mockImplementation(async (id: number) => view(id));
  idx.setApi(fake as unknown as Api);
  events.setEventBus(events.createMemoryBus());
  return {
    fake, AppError: errors.AppError,
    C: await import("./config"), A: await import("./app"), R: await import("./route"), St: await import("./standup"),
    Sv: await import("./save"), U: await import("./ui"),
  };
}

function speaker(accountId: string, position: number, over: Partial<SpeakerConfig> = {}): SpeakerConfig {
  return { accountId, name: accountId.toUpperCase(), initials: accountId.toUpperCase(), position, included: true, manual: false, active: 1, note: "", ...over };
}
function cfg(id = 3, over: Partial<StandupConfig> = {}): StandupConfig {
  return {
    id, rev: 5, position: 1, name: "Saffron Mobile", space: { id: "s1", key: "MOB", name: "S" }, board: { id: "b1", name: "B", type: "scrum" },
    scope: "sprint", extraFilter: "", filterCheck: null, columns: [], lanes: [], unmappedStatuses: [], rowsMode: "fit",
    speakers: [speaker("a", 0), speaker("b", 1), speaker("c", 2), speaker("d", 3)], unassignedPosition: "last", reshuffleDaily: false,
    pulseDays: 21, showCommits: true, showPRs: true, showComments: true, shadeWeekends: true, doneWindow: "sprint", backlogRows: 12,
    needsSync: false, draft: false, ...over,
  } as unknown as StandupConfig;
}
const ack = (rev = 6, needsSync = false): SaveAck => ({ rev, savedAt: 1, needsSync });
function tab(id: number, over: Partial<StandupTab> = {}): StandupTab {
  return { id, rev: 1, name: `Standup ${id}`, spaceKey: `K${id}`, position: id, activeCount: 0, speakerCount: 0, configured: true, needsSync: false, lastSyncedAt: 0, ...over };
}
function view(id: number, rev = 1): StandupView {
  return { standupId: id, rev, name: `S${id}`, speakers: [], tickets: [], lanes: [], axis: [], backlog: [], warnings: [] } as unknown as StandupView;
}

let T: Awaited<ReturnType<typeof boot>>;
const settle = () => vi.advanceTimersByTimeAsync(0);
const order = () => T.C.config.value!.speakers.map((s) => s.accountId).join("");
beforeEach(async () => {
  vi.useFakeTimers();
  T = await boot();
});
afterEach(() => vi.useRealTimers());

describe("speakers: optimistic order (fix 2)", () => {
  beforeEach(() => {
    T.C.config.value = cfg();
    T.fake.config!.moveSpeaker!.mockResolvedValue(ack());
  });

  it("moveSpeaker reorders the list at once and keeps positions dense", async () => {
    T.C.moveSpeaker("d", 1);
    expect(order()).toBe("adbc");
    expect(T.C.config.value!.speakers.map((s) => s.position)).toEqual([0, 1, 2, 3]);
    await settle();
    expect(T.fake.config!.moveSpeaker).toHaveBeenCalledWith(3, "d", 1);
  });

  it("moving down lands on the target index", () => {
    T.C.moveSpeaker("a", 2);
    expect(order()).toBe("bcad");
  });

  it("Undo of a removal uses the captured standup id and restores the original place", async () => {
    T.C.config.value = cfg(3, { speakers: [speaker("a", 0), speaker("b", 1, { manual: true }), speaker("c", 2)] });
    T.fake.config!.removeSpeaker!.mockResolvedValue(ack());
    T.fake.config!.addSpeaker!.mockResolvedValue([speaker("a", 0), speaker("c", 1), speaker("b", 2, { manual: true })]);
    T.C.removeSpeaker("b");
    expect(order()).toBe("ac");
    await settle();
    // Another standup is open when Undo is pressed.
    T.C.config.value = cfg(9);
    T.U.undoLatestToast();
    await settle();
    expect(T.fake.config!.addSpeaker).toHaveBeenCalledWith(3, "b");
    expect(T.fake.config!.moveSpeaker).toHaveBeenCalledWith(3, "b", 1);
  });

  it("Undo puts the speaker back at the original index in the visible list", async () => {
    T.C.config.value = cfg(3, { speakers: [speaker("a", 0), speaker("b", 1, { manual: true }), speaker("c", 2)] });
    T.fake.config!.removeSpeaker!.mockResolvedValue(ack());
    T.fake.config!.addSpeaker!.mockResolvedValue([speaker("a", 0), speaker("c", 1), speaker("b", 2, { manual: true })]);
    T.C.removeSpeaker("b");
    await settle();
    T.U.undoLatestToast();
    await settle();
    expect(order()).toBe("abc");
  });
});

describe("leaveConfigure (fixes 3, 7)", () => {
  beforeEach(() => {
    T.A.standups.value = [tab(1), tab(2)];
    T.fake.standup!.list!.mockResolvedValue([tab(1), tab(2)]);
    T.R.route.value = { view: "standup", standupId: 1, person: null };
    T.R.route.value = { view: "configure", standupId: 1 };
  });

  it("clears the loaded config so the next visit loads fresh", async () => {
    T.C.config.value = cfg(1);
    await T.C.leaveConfigure();
    expect(T.C.config.value).toBeNull();
    expect(T.R.route.value.view).toBe("standup");
  });

  it("never starts a full sync for a standup whose refreshed needsSync is false, even if the open config says so", async () => {
    T.C.config.value = cfg(1, { needsSync: true });
    await T.C.leaveConfigure();
    expect(T.fake.sync!.start).not.toHaveBeenCalled();
  });

  it("starts a full sync only for tabs the refreshed list marks needsSync", async () => {
    T.fake.standup!.list!.mockResolvedValue([tab(1), tab(2, { needsSync: true })]);
    await T.C.leaveConfigure();
    expect(T.fake.sync!.start).toHaveBeenCalledTimes(1);
    expect(T.fake.sync!.start).toHaveBeenCalledWith(2, "full");
  });

  it("ignores a second call while one is running", async () => {
    const a = T.C.leaveConfigure();
    const b = T.C.leaveConfigure();
    await Promise.all([a, b]);
    expect(T.fake.standup!.list).toHaveBeenCalledTimes(1);
  });

  it("with nothing configured left after discarding drafts, opens Configure with no selection and no config", async () => {
    const draft = tab(1, { configured: false, spaceKey: "" });
    T.fake.standup!.list!.mockResolvedValueOnce([draft]).mockResolvedValue([]);
    T.A.standups.value = [draft];
    T.C.config.value = cfg(1, { draft: true });
    await T.C.leaveConfigure();
    expect(T.fake.config!.discardDraft).toHaveBeenCalledWith(1);
    expect(T.R.route.value).toEqual({ view: "configure", standupId: null });
    expect(T.C.config.value).toBeNull();
  });

  it("counts validation rejections in the not-saved toast", async () => {
    T.C.config.value = cfg(1);
    T.fake.config!.update!.mockRejectedValue(new T.AppError("validation", "Too long.", "", false, "name"));
    T.C.saveField("name", "x", { text: true });
    await vi.advanceTimersByTimeAsync(500);
    await T.C.leaveConfigure();
    expect(T.U.toasts.value.map((t) => t.message)).toContain("1 change not saved");
  });

  it("the standup tab flags win: needsSync on the tab list is the only source", async () => {
    T.C.config.value = cfg(1, { needsSync: false });
    T.fake.standup!.list!.mockResolvedValue([tab(1, { needsSync: true }), tab(2)]);
    await T.C.leaveConfigure();
    expect(T.fake.sync!.start).toHaveBeenCalledWith(1, "full");
  });
});

describe("deleteStandup (fix 8)", () => {
  it("opens Configure with no selection when nothing remains", async () => {
    T.A.standups.value = [tab(1)];
    T.fake.standup!.list!.mockResolvedValue([]);
    T.R.route.value = { view: "configure", standupId: 1 };
    T.C.config.value = cfg(1);
    await T.C.deleteStandup(1);
    expect(T.R.route.value).toEqual({ view: "configure", standupId: null });
    expect(T.C.config.value).toBeNull();
  });
});

describe("loadConfig races (fix 6)", () => {
  it("drops a reply that was overtaken by a later load", async () => {
    const resolvers: Record<number, (c: StandupConfig) => void> = {};
    T.fake.config!.get!.mockImplementation((id: number) => new Promise<StandupConfig>((r) => (resolvers[id] = r)));
    const first = T.C.loadConfig(1);
    const second = T.C.loadConfig(2);
    resolvers[2]!(cfg(2));
    await second;
    resolvers[1]!(cfg(1));
    await first;
    expect(T.C.config.value!.id).toBe(2);
    expect(T.C.configLoading.value).toBe(false);
  });

  it("a late reply after leaving does not repopulate the config", async () => {
    T.A.standups.value = [tab(1)];
    T.fake.standup!.list!.mockResolvedValue([tab(1)]);
    T.R.route.value = { view: "configure", standupId: 1 };
    let release!: (c: StandupConfig) => void;
    T.fake.config!.get!.mockImplementation(() => new Promise<StandupConfig>((r) => (release = r)));
    const p = T.C.loadConfig(1);
    await T.C.leaveConfigure();
    release(cfg(1));
    await p;
    expect(T.C.config.value).toBeNull();
  });
});

describe("text kept across server replies (fix 14)", () => {
  it("a SetSpace reply does not overwrite a name that is still waiting to save", async () => {
    T.C.config.value = cfg(3);
    T.fake.config!.setSpace!.mockResolvedValue(cfg(3, { name: "Server name" }));
    T.C.saveField("name", "Typing…", { text: true });
    T.C.setSpace({ id: "s2", key: "NEW", name: "New" });
    await settle();
    expect(T.C.config.value!.name).toBe("Typing…");
  });
});

describe("filter validation races (fix 17)", () => {
  it("only the newest answer for the current text counts", async () => {
    T.C.config.value = cfg(3, { extraFilter: "a" });
    const answers: Array<(v: unknown) => void> = [];
    T.fake.connection!.validateFilter!.mockImplementation(() => new Promise((r) => answers.push(r)));
    T.C.validateFilter(); // Enter
    T.C.validateFilter(); // blur
    answers[1]!({ ok: true, errors: [] });
    await settle();
    answers[0]!({ ok: false, errors: ["old"] });
    await settle();
    expect(T.C.config.value!.filterCheck).toEqual({ ok: true, errors: [] });
  });

  it("drops an answer for text that has since changed", async () => {
    T.C.config.value = cfg(3, { extraFilter: "a" });
    let answer!: (v: unknown) => void;
    T.fake.connection!.validateFilter!.mockImplementation(() => new Promise((r) => (answer = r)));
    T.C.validateFilter();
    T.C.config.value = { ...T.C.config.value!, extraFilter: "ab" };
    answer({ ok: false, errors: ["old"] });
    await settle();
    expect(T.C.config.value!.filterCheck).toBeNull();
  });
});

describe("retries (fixes 16, 21)", () => {
  const dbError = () => new T.AppError("db", "Could not save.", "", true);

  it("list operations never retry", async () => {
    T.C.config.value = cfg(3);
    T.fake.config!.moveSpeaker!.mockRejectedValue(dbError());
    T.C.moveSpeaker("b", 2);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(T.fake.config!.moveSpeaker).toHaveBeenCalledTimes(1);
  });

  it("calls that reach Jira (SetSpace, SetBoard) never retry", async () => {
    T.C.config.value = cfg(3);
    T.fake.config!.setSpace!.mockRejectedValue(dbError());
    T.fake.config!.setBoard!.mockRejectedValue(dbError());
    T.C.setSpace({ id: "s2", key: "N", name: "N" });
    T.C.setBoard({ id: "b2", name: "B2", type: "scrum" });
    await vi.advanceTimersByTimeAsync(10_000);
    expect(T.fake.config!.setSpace).toHaveBeenCalledTimes(1);
    expect(T.fake.config!.setBoard).toHaveBeenCalledTimes(1);
  });

  it("only db and timeout errors retry a pure database save", async () => {
    T.C.config.value = cfg(3);
    T.fake.config!.update!.mockRejectedValue(new T.AppError("offline", "Offline.", "", true));
    T.C.saveField("pulseDays", 14);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(T.fake.config!.update).toHaveBeenCalledTimes(1);

    T.fake.config!.update!.mockReset();
    T.fake.config!.update!.mockRejectedValue(dbError());
    T.C.saveField("pulseDays", 21);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(T.fake.config!.update).toHaveBeenCalledTimes(4);
  });
});

describe("refreshView ordering (fix 15)", () => {
  it("ignores a reply whose rev is below the cached view's", async () => {
    T.St.views.value = new Map([[3, view(3, 9)]]);
    T.fake.standup!.view!.mockResolvedValue(view(3, 4));
    const v = await T.St.refreshView(3);
    expect(v.rev).toBe(9);
    expect(T.St.views.value.get(3)!.rev).toBe(9);
  });

  it("accepts an equal or newer rev", async () => {
    T.St.views.value = new Map([[3, view(3, 9)]]);
    T.fake.standup!.view!.mockResolvedValue(view(3, 10));
    await T.St.refreshView(3);
    expect(T.St.views.value.get(3)!.rev).toBe(10);
  });
});

describe("toasts (fix 11)", () => {
  it("running an action removes the toast first, so it cannot run twice", () => {
    const run = vi.fn();
    const id = T.U.showToast({ message: "Removed X", action: { label: "Undo", run }, timeoutMs: 10_000 });
    T.U.runToastAction(id);
    T.U.runToastAction(id);
    expect(run).toHaveBeenCalledTimes(1);
    expect(T.U.toasts.value).toHaveLength(0);
  });

  it("undoLatestToast runs the newest Undo toast and reports whether one existed", () => {
    const a = vi.fn();
    const b = vi.fn();
    expect(T.U.undoLatestToast()).toBe(false);
    T.U.showToast({ message: "a", action: { label: "Undo", run: a }, timeoutMs: 10_000 });
    T.U.showToast({ message: "b", action: { label: "Retry", run: vi.fn() }, timeoutMs: 10_000 });
    T.U.showToast({ message: "c", action: { label: "Undo", run: b }, timeoutMs: 10_000 });
    expect(T.U.undoLatestToast()).toBe(true);
    expect(b).toHaveBeenCalledTimes(1);
    expect(a).not.toHaveBeenCalled();
  });
});

describe("connect detour (fix 12)", () => {
  it("leaveConnect returns to the route the Connect banner was pressed on", () => {
    T.R.route.value = { view: "standup", standupId: 2, person: "p1" };
    T.R.goConnect();
    expect(T.R.route.value.view).toBe("connect");
    T.R.leaveConnect();
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 2, person: "p1" });
  });
});
