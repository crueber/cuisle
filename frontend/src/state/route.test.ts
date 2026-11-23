// WP-15 tests: routing state (state/route.ts; architecture §4.3 last bullet, §4.5).
//
// SPEC-NOTE: initialRoute for "only drafts exist" returns { view: "configure", standupId: <the first draft's id> };
//   the tests use a single draft so "first" is unambiguous.
// SPEC-NOTE: initialRoute's "last standup" is Bootstrap.ui.lastStandupId when it is a CONFIGURED standup in
//   Bootstrap.standups; 0, a draft or an unknown id falls back to the first configured standup in list order.
//   Bootstrap.ui.lastView is deliberately not asserted (the doc comment lists only three rules).
// SPEC-NOTE: ui.lastPerson[id] "" means whole board (person null); a missing entry also means null.
// SPEC-NOTE: a missing connection with configured standups present still opens the last standup (a banner offers
//   Reconnect, architecture §4.5); only "missing AND no standups" forces connect.
// SPEC-NOTE: navigate's persisted patch uses the UIStatePatch fields: lastStandupId + standupId + lastPerson ("" =
//   whole board) + lastView "standup" for a standup route; lastView "configure" for a configure route. Other
//   configure/connect patch fields are not asserted.
// SPEC-NOTE: the 500 ms persistence is a trailing debounce (each navigate restarts it) and sends ONE patch, for the
//   latest route.
// SPEC-NOTE: goStandup(id) without a person means the whole board (person null); goConfigure(null) means "the
//   standup of the current route" when that route is a standup (or configure) route.
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Api, Bootstrap, StandupTab } from "../api/index";

type Fake = Record<string, Record<string, Mock>>;
const DEFAULTS: Record<string, unknown> = { "standup.list": [], "sync.status": [], "sync.start": 1 };

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
  return { fake, bus, AppError: errors.AppError, R: await import("./route") };
}
const totalCalls = (fake: Fake) => Object.values(fake).reduce((n, s) => n + Object.values(s).reduce((k, m) => k + m.mock.calls.length, 0), 0);

function tab(id: number, over: Partial<StandupTab> = {}): StandupTab {
  return { id, rev: 1, name: `Standup ${id}`, spaceKey: `K${id}`, position: id, activeCount: 0, speakerCount: 0, configured: true, needsSync: false, lastSyncedAt: 0, ...over };
}
function bootstrap(over: { state?: string; standups?: StandupTab[]; ui?: Partial<Bootstrap["ui"]> } = {}): Bootstrap {
  return {
    build: { version: "dev", commit: "", date: "", goVersion: "", os: "", arch: "", dev: true },
    settings: {},
    connection: { state: over.state ?? "ok", source: "jira", baseUrl: "", account: "", displayName: "", devActivity: "unknown", capabilities: {} },
    standups: over.standups ?? [],
    ui: { lastStandupId: 0, lastView: "standup", lastPerson: {}, window: null, ...over.ui },
    view: null,
    sync: [],
    recovered: false,
  } as unknown as Bootstrap;
}

let T: Awaited<ReturnType<typeof boot>>;
beforeEach(async () => {
  vi.useFakeTimers();
  T = await boot();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("initialRoute", () => {
  it("is_connect_when_the_connection_is_missing_and_there_are_no_standups", () => {
    expect(T.R.initialRoute(bootstrap({ state: "missing", standups: [] }))).toEqual({ view: "connect" });
  });

  it("is_configure_on_the_draft_when_only_drafts_exist", () => {
    const draft = tab(7, { configured: false, spaceKey: "" });
    expect(T.R.initialRoute(bootstrap({ standups: [draft] }))).toEqual({ view: "configure", standupId: 7 });
  });

  it("is_the_last_standup_and_last_person", () => {
    const boot = bootstrap({ standups: [tab(1), tab(2), tab(3)], ui: { lastStandupId: 2, lastPerson: { 2: "iris", 1: "amara" } } });
    expect(T.R.initialRoute(boot)).toEqual({ view: "standup", standupId: 2, person: "iris" });
  });

  it("keeps_the_unassigned_pseudo_person", () => {
    const boot = bootstrap({ standups: [tab(1)], ui: { lastStandupId: 1, lastPerson: { 1: "unassigned" } } });
    expect(T.R.initialRoute(boot)).toEqual({ view: "standup", standupId: 1, person: "unassigned" });
  });

  it("treats_an_empty_last_person_as_the_whole_board", () => {
    const boot = bootstrap({ standups: [tab(1), tab(2)], ui: { lastStandupId: 2, lastPerson: { 2: "" } } });
    expect(T.R.initialRoute(boot)).toEqual({ view: "standup", standupId: 2, person: null });
  });

  it("uses_the_whole_board_when_no_person_was_remembered_for_that_standup", () => {
    const boot = bootstrap({ standups: [tab(1), tab(2)], ui: { lastStandupId: 2, lastPerson: { 1: "amara" } } });
    expect(T.R.initialRoute(boot)).toEqual({ view: "standup", standupId: 2, person: null });
  });

  it("falls_back_to_the_first_configured_standup_when_none_was_remembered", () => {
    const boot = bootstrap({ standups: [tab(4), tab(5)], ui: { lastStandupId: 0 } });
    expect(T.R.initialRoute(boot)).toEqual({ view: "standup", standupId: 4, person: null });
  });

  it("falls_back_when_the_remembered_standup_no_longer_exists", () => {
    const boot = bootstrap({ standups: [tab(4), tab(5)], ui: { lastStandupId: 99, lastPerson: { 99: "iris" } } });
    expect(T.R.initialRoute(boot)).toEqual({ view: "standup", standupId: 4, person: null });
  });

  it("never_picks_a_draft_as_the_standup_even_when_it_is_first_or_remembered", () => {
    const draft = tab(1, { configured: false, spaceKey: "" });
    const boot = bootstrap({ standups: [draft, tab(2)], ui: { lastStandupId: 1 } });
    expect(T.R.initialRoute(boot)).toEqual({ view: "standup", standupId: 2, person: null });
  });

  it("opens_the_last_standup_even_when_the_connection_is_missing_if_standups_exist", () => {
    const boot = bootstrap({ state: "missing", standups: [tab(1)], ui: { lastStandupId: 1 } });
    expect(T.R.initialRoute(boot)).toEqual({ view: "standup", standupId: 1, person: null });
  });
});

describe("navigate and the route signal", () => {
  it("updates_the_route_signal_immediately", () => {
    T.R.navigate({ view: "standup", standupId: 3, person: "iris" });
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 3, person: "iris" });
  });

  it("persists_ui_state_through_SetUIState_only_after_500ms", async () => {
    T.R.navigate({ view: "standup", standupId: 3, person: "iris" });
    await vi.advanceTimersByTimeAsync(499);
    expect(T.fake.app!.setUIState).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(T.fake.app!.setUIState).toHaveBeenCalledTimes(1);
    expect(T.fake.app!.setUIState).toHaveBeenCalledWith(
      expect.objectContaining({ lastStandupId: 3, lastView: "standup", standupId: 3, lastPerson: "iris" }),
    );
  });

  it("sends_an_empty_last_person_for_the_whole_board", async () => {
    T.R.navigate({ view: "standup", standupId: 3, person: null });
    await vi.advanceTimersByTimeAsync(500);
    expect(T.fake.app!.setUIState).toHaveBeenCalledWith(expect.objectContaining({ standupId: 3, lastPerson: "" }));
  });

  it("restarts_the_debounce_and_sends_one_patch_for_the_latest_route", async () => {
    T.R.navigate({ view: "standup", standupId: 3, person: "iris" });
    await vi.advanceTimersByTimeAsync(400);
    T.R.navigate({ view: "standup", standupId: 3, person: "amara" });
    await vi.advanceTimersByTimeAsync(400); // t = 800: 400 ms after the 2nd navigate
    expect(T.fake.app!.setUIState).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(100); // t = 900
    expect(T.fake.app!.setUIState).toHaveBeenCalledTimes(1);
    expect(T.fake.app!.setUIState).toHaveBeenCalledWith(expect.objectContaining({ lastPerson: "amara" }));
  });

  it("persists_the_configure_view", async () => {
    T.R.navigate({ view: "configure", standupId: 3 });
    expect(T.R.route.value).toEqual({ view: "configure", standupId: 3 });
    await vi.advanceTimersByTimeAsync(500);
    expect(T.fake.app!.setUIState).toHaveBeenCalledWith(expect.objectContaining({ lastView: "configure" }));
  });

  it("navigates_to_connect", () => {
    T.R.navigate({ view: "connect" });
    expect(T.R.route.value).toEqual({ view: "connect" });
  });

  it("ignores_a_failure_of_the_fire_and_forget_SetUIState_call", async () => {
    T.fake.app!.setUIState!.mockRejectedValue(new T.AppError("db", "no", "", true));
    T.R.navigate({ view: "standup", standupId: 3, person: null });
    await vi.advanceTimersByTimeAsync(500);
    await vi.advanceTimersByTimeAsync(5_000);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 3, person: null });
    expect(T.fake.app!.setUIState).toHaveBeenCalledTimes(1);
  });

  it("makes_no_facade_call_other_than_SetUIState", async () => {
    T.R.navigate({ view: "standup", standupId: 3, person: "iris" });
    T.R.navigate({ view: "standup", standupId: 3, person: "amara" });
    await vi.advanceTimersByTimeAsync(2_000);
    expect(totalCalls(T.fake)).toBe(T.fake.app!.setUIState!.mock.calls.length);
  });
});

describe("route helpers", () => {
  it("goStandup_selects_a_person", () => {
    T.R.goStandup(3, "iris");
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 3, person: "iris" });
  });

  it("goStandup_without_a_person_shows_the_whole_board", () => {
    T.R.goStandup(3, "iris");
    T.R.goStandup(4);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 4, person: null });
    T.R.goStandup(3, null);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 3, person: null });
  });

  it("goStandup_persists_the_standup_and_person", async () => {
    T.R.goStandup(3, "iris");
    await vi.advanceTimersByTimeAsync(500);
    expect(T.fake.app!.setUIState).toHaveBeenCalledWith(expect.objectContaining({ lastStandupId: 3, lastPerson: "iris" }));
  });

  it("goConfigure_opens_a_given_standup", () => {
    T.R.goConfigure(5);
    expect(T.R.route.value).toEqual({ view: "configure", standupId: 5 });
  });

  it("goConfigure_null_opens_the_current_standup", () => {
    T.R.goStandup(3, "iris");
    T.R.goConfigure(null);
    expect(T.R.route.value).toEqual({ view: "configure", standupId: 3 });
  });

  it("goConnect_opens_the_connect_screen", () => {
    T.R.goStandup(3);
    T.R.goConnect();
    expect(T.R.route.value).toEqual({ view: "connect" });
  });
});
