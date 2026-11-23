// WP-15 tests: standup view state (state/standup.ts; architecture §4.3, ux-spec §2.2, §4 last bullet, §5.3, §6.2, §8).
//
// SPEC-NOTE: "person switch makes zero facade calls" (testing §5, perf budget) conflicts literally with route
//   persistence, which calls AppService.SetUIState debounced 500 ms. Resolution used here: NO call at all happens
//   synchronously or within the first 499 ms; afterwards the ONLY permitted call is app.setUIState; calls to
//   standup.*, config.*, sync.*, connection.* never happen.
// SPEC-NOTE: currentView / currentPerson / personGroups / boardLanes derive from `route` and `views` when
//   route.view === "standup" (what Configure shows is not asserted). currentPerson resolves person "unassigned" to
//   the SpeakerView with unassigned: true, and an unknown id to null.
// SPEC-NOTE: personGroups is [] for the whole board (person null); with person "unassigned" it groups the tickets
//   whose assignee is "".
// SPEC-NOTE: nextSpeaker/prevSpeaker cycle over view.speakers with included === true, in view order, treating the
//   Unassigned SpeakerView like any other included speaker at its position (architecture §3.2: it is
//   included === false when position is "skip"). Whole board sits between the last and the first speaker.
// SPEC-NOTE: nextSpeaker/prevSpeaker/selectPerson are no-ops unless route.view === "standup".
// SPEC-NOTE: "speaks there" (switchStandup keeps the person) means: a SpeakerView with that id AND included === true
//   exists in the target view. An excluded speaker therefore falls back to the whole board. The edge is the
//   least certain reading; the clear cases (present+included, absent) are the load-bearing ones.
// SPEC-NOTE: switchStandup's cached view is "stale" when its standup is in staleViews OR when the cached view.rev is
//   lower than the tab's rev (architecture §4.3). When a view is missing, the final state (route, cache, loadingView
//   false) is asserted after the promise resolves; whether the route changes before the fetch completes is not.
// SPEC-NOTE: data:changed for the CURRENTLY DISPLAYED standup is deliberately not asserted (refresh at once vs
//   stale); only non-current standups (marked stale, no fetch) and day:rolled (current re-requested, others stale)
//   are, per architecture §3.3/§4.3.
// SPEC-NOTE: boardLanes fixtures give every speaker at least one active ticket, because whether a speaker with zero
//   active tickets still gets a lane is not specified.
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Api, SpeakerView, StandupTab, StandupView, TicketView, WalkLane } from "../api/index";

type Fake = Record<string, Record<string, Mock>>;
const DEFAULTS: Record<string, unknown> = { "standup.list": [], "sync.status": [], "sync.start": 1 };

async function boot() {
  vi.resetModules();
  const idx = await import("../api/index");
  const events = await import("../api/events");
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
  return { fake, bus, A: await import("./app"), R: await import("./route"), S: await import("./standup") };
}
/** Names of every facade method that was called, except the allowed ones. */
function callsOtherThan(fake: Fake, allowed: string[] = []): string[] {
  const out: string[] = [];
  for (const [svc, ms] of Object.entries(fake)) for (const [m, fn] of Object.entries(ms)) if (fn.mock.calls.length && !allowed.includes(`${svc}.${m}`)) out.push(`${svc}.${m}`);
  return out;
}

// ── builders ────────────────────────────────────────────────────────────────────────────────────
function sp(id: string, over: Partial<SpeakerView> = {}): SpeakerView {
  return { id, name: id.toUpperCase(), first: id, initials: id.slice(0, 2).toUpperCase(), unassigned: id === "unassigned", included: true, active: 0, done: 0, ...over };
}
let nextTicketId = 1;
function tk(key: string, assignee: string, lane: string, over: Partial<TicketView> = {}): TicketView {
  return {
    id: nextTicketId++, sourceId: key, key, summary: `Summary ${key}`, url: "", type: "story", assignee, lane, statusName: lane,
    daysInLane: 1, overLimit: false, blocked: false, doneAt: 0, pulse: { segs: [], marks: [], blocked: [], label: "" }, ...over,
  };
}
const LABELS: Record<string, string> = { todo: "To do", prog: "In progress", review: "Review", qa: "QA", done: "Done" };
function lanes(order: WalkLane[] = ["qa", "review", "prog", "todo"]) {
  return [...order, "done"].map((lane) => ({ lane, label: LABELS[lane]!, limitDays: 0, columns: [] as string[] }));
}
function mkView(standupId: number, speakers: SpeakerView[], tickets: TicketView[], over: Partial<StandupView> = {}): StandupView {
  return {
    standupId, rev: 1, generatedAt: 0, today: "2026-11-23", asOf: 0, live: true, name: `Standup ${standupId}`, spaceKey: `K${standupId}`,
    boardName: "Board", boardType: "scrum", scope: "sprint", sprint: null, pulseDays: 21, axis: [], lanes: lanes(), speakers, tickets,
    backlog: [], backlogTotal: 0, counts: { active: 0, unassigned: 0, done: 0, backlog: 0 }, options: {}, sync: {}, warnings: [],
    previewKey: "", lastSyncedAt: 0, ...over,
  } as unknown as StandupView;
}
function tab(id: number, over: Partial<StandupTab> = {}): StandupTab {
  return { id, rev: 1, name: `Standup ${id}`, spaceKey: `K${id}`, position: id, activeCount: 0, speakerCount: 0, configured: true, needsSync: false, lastSyncedAt: 0, ...over };
}

let T: Awaited<ReturnType<typeof boot>>;
/** Installs views/tabs/route directly into the signals. */
function seed(opts: { views?: StandupView[]; tabs?: StandupTab[]; route?: Parameters<typeof T.R.navigate>[0] }) {
  T.S.views.value = new Map((opts.views ?? []).map((v) => [v.standupId, v]));
  T.A.standups.value = opts.tabs ?? (opts.views ?? []).map((v) => tab(v.standupId));
  T.R.route.value = opts.route ?? { view: "connect" };
}
const person = () => (T.R.route.value.view === "standup" ? T.R.route.value.person : "<not standup>");

beforeEach(async () => {
  vi.useFakeTimers();
  nextTicketId = 1;
  T = await boot();
});
afterEach(() => {
  vi.useRealTimers();
});

// ── derived signals ─────────────────────────────────────────────────────────────────────────────
describe("currentView and currentPerson", () => {
  it("currentView_is_the_cached_view_of_the_routed_standup", () => {
    const v1 = mkView(1, [sp("a")], []);
    const v2 = mkView(2, [sp("a")], []);
    seed({ views: [v1, v2], route: { view: "standup", standupId: 2, person: null } });
    expect(T.S.currentView.value).toBe(v2);
    T.R.route.value = { view: "standup", standupId: 1, person: null };
    expect(T.S.currentView.value).toBe(v1);
  });

  it("currentView_is_null_when_nothing_is_cached_for_the_routed_standup", () => {
    seed({ views: [mkView(1, [], [])], route: { view: "standup", standupId: 9, person: null } });
    expect(T.S.currentView.value).toBeNull();
  });

  it("currentPerson_is_the_routed_speaker_and_null_for_the_whole_board", () => {
    const v = mkView(1, [sp("iris"), sp("amara")], []);
    seed({ views: [v], route: { view: "standup", standupId: 1, person: "amara" } });
    expect(T.S.currentPerson.value?.id).toBe("amara");
    T.R.route.value = { view: "standup", standupId: 1, person: null };
    expect(T.S.currentPerson.value).toBeNull();
  });

  it("currentPerson_resolves_unassigned_to_the_unassigned_speaker", () => {
    const v = mkView(1, [sp("iris"), sp("unassigned")], []);
    seed({ views: [v], route: { view: "standup", standupId: 1, person: "unassigned" } });
    expect(T.S.currentPerson.value?.unassigned).toBe(true);
  });

  it("currentPerson_is_null_for_a_person_who_does_not_speak_in_the_view", () => {
    seed({ views: [mkView(1, [sp("iris")], [])], route: { view: "standup", standupId: 1, person: "ghost" } });
    expect(T.S.currentPerson.value).toBeNull();
  });
});

describe("personGroups", () => {
  const tickets = [
    tk("T-1", "iris", "todo"),
    tk("T-2", "iris", "qa"),
    tk("T-3", "amara", "qa"),
    tk("T-4", "iris", "done"),
    tk("T-5", "iris", "prog"),
    tk("T-6", "iris", "qa"),
    tk("T-7", "", "review"),
    tk("T-8", "iris", "done"),
  ];
  const v = () => mkView(1, [sp("iris"), sp("amara"), sp("unassigned")], tickets);

  it("groups_the_selected_persons_tickets_in_walk_order_with_done_last_and_empty_groups_dropped", () => {
    seed({ views: [v()], route: { view: "standup", standupId: 1, person: "iris" } });
    const groups = T.S.personGroups.value;
    expect(groups.map((g) => g.lane.lane)).toEqual(["qa", "prog", "todo", "done"]); // no review: iris has none
    expect(groups.map((g) => g.tickets.map((t) => t.key))).toEqual([["T-2", "T-6"], ["T-5"], ["T-1"], ["T-4", "T-8"]]);
  });

  it("carries_the_LaneInfo_of_each_group", () => {
    seed({ views: [v()], route: { view: "standup", standupId: 1, person: "amara" } });
    const groups = T.S.personGroups.value;
    expect(groups).toHaveLength(1);
    expect(groups[0]!.lane).toEqual(v().lanes[0]);
    expect(groups[0]!.lane.label).toBe("QA");
  });

  it("follows_a_configured_walk_order_other_than_the_default", () => {
    const custom = mkView(1, [sp("iris")], tickets, { lanes: lanes(["todo", "prog", "review", "qa"]) });
    seed({ views: [custom], route: { view: "standup", standupId: 1, person: "iris" } });
    expect(T.S.personGroups.value.map((g) => g.lane.lane)).toEqual(["todo", "prog", "qa", "done"]);
  });

  it("is_empty_for_the_whole_board", () => {
    seed({ views: [v()], route: { view: "standup", standupId: 1, person: null } });
    expect(T.S.personGroups.value).toEqual([]);
  });

  it("groups_unowned_tickets_for_the_unassigned_person", () => {
    seed({ views: [v()], route: { view: "standup", standupId: 1, person: "unassigned" } });
    expect(T.S.personGroups.value.map((g) => g.tickets.map((t) => t.key))).toEqual([["T-7"]]);
  });

  it("recomputes_when_the_person_changes", () => {
    seed({ views: [v()], route: { view: "standup", standupId: 1, person: "iris" } });
    expect(T.S.personGroups.value).toHaveLength(4);
    T.R.route.value = { view: "standup", standupId: 1, person: "amara" };
    expect(T.S.personGroups.value).toHaveLength(1);
  });
});

describe("boardLanes and doneTickets", () => {
  const tickets = [
    tk("U-1", "", "prog"),
    tk("A-1", "a", "todo"),
    tk("B-1", "b", "qa"),
    tk("A-2", "a", "qa"),
    tk("A-3", "a", "prog"),
    tk("A-4", "a", "qa"),
    tk("A-D", "a", "done"),
    tk("B-D", "b", "done"),
    tk("U-D", "", "done"),
    tk("C-1", "c", "review"),
  ];

  it("lists_unassigned_first_even_when_it_is_last_in_the_speaking_order", () => {
    const v = mkView(1, [sp("a"), sp("b"), sp("c"), sp("unassigned")], tickets);
    seed({ views: [v], route: { view: "standup", standupId: 1, person: null } });
    expect(T.S.boardLanes.value.map((l) => l.speaker.id)).toEqual(["unassigned", "a", "b", "c"]);
  });

  it("keeps_speakers_in_view_order_after_unassigned", () => {
    const v = mkView(1, [sp("c"), sp("unassigned"), sp("a"), sp("b")], tickets);
    seed({ views: [v], route: { view: "standup", standupId: 1, person: null } });
    expect(T.S.boardLanes.value.map((l) => l.speaker.id)).toEqual(["unassigned", "c", "a", "b"]);
  });

  it("omits_unassigned_when_it_has_no_tickets", () => {
    const own = tickets.filter((t) => t.assignee !== "");
    const v = mkView(1, [sp("a"), sp("b"), sp("c"), sp("unassigned")], own);
    seed({ views: [v], route: { view: "standup", standupId: 1, person: null } });
    expect(T.S.boardLanes.value.map((l) => l.speaker.id)).toEqual(["a", "b", "c"]);
  });

  it("holds_each_speakers_active_tickets_in_walk_lane_order_then_rank_without_done", () => {
    const v = mkView(1, [sp("a"), sp("b"), sp("c"), sp("unassigned")], tickets); // walk order: qa, review, prog, todo
    seed({ views: [v], route: { view: "standup", standupId: 1, person: null } });
    const byId = Object.fromEntries(T.S.boardLanes.value.map((l) => [l.speaker.id, l.tickets.map((t) => t.key)]));
    expect(byId["a"]).toEqual(["A-2", "A-4", "A-3", "A-1"]);
    expect(byId["b"]).toEqual(["B-1"]);
    expect(byId["c"]).toEqual(["C-1"]);
    expect(byId["unassigned"]).toEqual(["U-1"]);
  });

  it("lists_each_speaker_once", () => {
    const v = mkView(1, [sp("a"), sp("unassigned"), sp("b"), sp("c")], tickets);
    seed({ views: [v], route: { view: "standup", standupId: 1, person: null } });
    const ids = T.S.boardLanes.value.map((l) => l.speaker.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it("doneTickets_is_every_done_ticket_in_view_order", () => {
    const v = mkView(1, [sp("a"), sp("b"), sp("c"), sp("unassigned")], tickets);
    seed({ views: [v], route: { view: "standup", standupId: 1, person: null } });
    expect(T.S.doneTickets.value.map((t) => t.key)).toEqual(["A-D", "B-D", "U-D"]);
  });
});

// ── person selection ────────────────────────────────────────────────────────────────────────────
describe("selectPerson", () => {
  const v = () => mkView(1, [sp("iris"), sp("amara")], [tk("T-1", "iris", "prog")]);

  it("changes_the_person_and_keeps_the_standup", () => {
    seed({ views: [v()], route: { view: "standup", standupId: 1, person: null } });
    T.S.selectPerson("amara");
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 1, person: "amara" });
    T.S.selectPerson(null);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 1, person: null });
  });

  it("makes_zero_facade_calls_when_switching_person", async () => {
    seed({ views: [v()], route: { view: "standup", standupId: 1, person: null } });
    T.S.selectPerson("iris");
    T.S.selectPerson("amara");
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(499);
    expect(callsOtherThan(T.fake)).toEqual([]);
  });

  it("only_ever_calls_SetUIState_and_only_after_the_500ms_debounce", async () => {
    seed({ views: [v()], route: { view: "standup", standupId: 1, person: null } });
    T.S.selectPerson("iris");
    T.S.selectPerson("amara");
    await vi.advanceTimersByTimeAsync(500);
    expect(T.fake.app!.setUIState).toHaveBeenCalledTimes(1);
    expect(T.fake.app!.setUIState).toHaveBeenCalledWith(expect.objectContaining({ standupId: 1, lastPerson: "amara" }));
    await vi.advanceTimersByTimeAsync(10_000);
    expect(callsOtherThan(T.fake, ["app.setUIState"])).toEqual([]);
  });

  it("is_a_no_op_outside_the_standup_route", () => {
    seed({ views: [v()], route: { view: "configure", standupId: 1 } });
    T.S.selectPerson("amara");
    expect(T.R.route.value).toEqual({ view: "configure", standupId: 1 });
  });
});

describe("nextSpeaker and prevSpeaker", () => {
  const speakers = [sp("a"), sp("b", { included: false }), sp("c"), sp("d")];
  const withRoute = (p: string | null, spk = speakers) =>
    seed({ views: [mkView(1, spk, [])], route: { view: "standup", standupId: 1, person: p } });

  it("next_walks_included_speakers_then_the_whole_board_then_the_first_speaker", () => {
    withRoute(null);
    const seen: (string | null)[] = [];
    for (let i = 0; i < 5; i++) {
      T.S.nextSpeaker();
      seen.push(person());
    }
    expect(seen).toEqual(["a", "c", "d", null, "a"]);
  });

  it("prev_walks_backwards_from_the_whole_board_to_the_last_speaker_and_back_to_the_whole_board", () => {
    withRoute(null);
    const seen: (string | null)[] = [];
    for (let i = 0; i < 5; i++) {
      T.S.prevSpeaker();
      seen.push(person());
    }
    expect(seen).toEqual(["d", "c", "a", null, "d"]);
  });

  it("skips_excluded_speakers_in_both_directions", () => {
    withRoute("a");
    T.S.nextSpeaker();
    expect(person()).toBe("c");
    T.S.prevSpeaker();
    expect(person()).toBe("a");
  });

  it("includes_the_unassigned_speaker_at_its_position", () => {
    const spk = [sp("a"), sp("c"), sp("unassigned")];
    withRoute("c", spk);
    T.S.nextSpeaker();
    expect(person()).toBe("unassigned");
    T.S.nextSpeaker();
    expect(person()).toBeNull();
  });

  it("skips_unassigned_when_it_is_not_included_position_skip", () => {
    const spk = [sp("a"), sp("unassigned", { included: false })];
    withRoute("a", spk);
    T.S.nextSpeaker();
    expect(person()).toBeNull();
  });

  it("stays_on_the_whole_board_when_nobody_is_included", () => {
    withRoute(null, [sp("a", { included: false })]);
    T.S.nextSpeaker();
    expect(person()).toBeNull();
    T.S.prevSpeaker();
    expect(person()).toBeNull();
  });

  it("keeps_the_standup_while_cycling_and_makes_no_facade_calls", async () => {
    withRoute(null);
    T.S.nextSpeaker();
    T.S.nextSpeaker();
    T.S.prevSpeaker();
    await vi.advanceTimersByTimeAsync(499);
    expect(T.R.route.value.view === "standup" && T.R.route.value.standupId).toBe(1);
    expect(callsOtherThan(T.fake)).toEqual([]);
  });

  it("is_a_no_op_outside_the_standup_route", () => {
    seed({ views: [mkView(1, speakers, [])], route: { view: "configure", standupId: 1 } });
    T.S.nextSpeaker();
    T.S.prevSpeaker();
    expect(T.R.route.value).toEqual({ view: "configure", standupId: 1 });
  });
});

// ── standup switching ───────────────────────────────────────────────────────────────────────────
describe("switchStandup", () => {
  const iris1 = () => mkView(1, [sp("iris"), sp("amara")], []);
  const iris2 = () => mkView(2, [sp("iris"), sp("silas")], []);
  const noIris3 = () => mkView(3, [sp("silas")], []);

  it("shows_a_fresh_cached_view_at_once_without_calling_StandupService_View", async () => {
    seed({ views: [iris1(), iris2()], route: { view: "standup", standupId: 1, person: null } });
    const p = T.S.switchStandup(2);
    p.catch(() => undefined); // a stub rejection must fail the assertions below, not the process
    expect(T.R.route.value.view === "standup" && T.R.route.value.standupId).toBe(2); // before awaiting
    expect(T.S.currentView.value?.standupId).toBe(2);
    await p;
    await vi.advanceTimersByTimeAsync(10_000);
    expect(callsOtherThan(T.fake, ["app.setUIState"])).toEqual([]);
    expect(T.fake.standup!.view).not.toHaveBeenCalled();
  });

  it("keeps_the_person_when_they_speak_in_the_target_standup", async () => {
    seed({ views: [iris1(), iris2()], route: { view: "standup", standupId: 1, person: "iris" } });
    await T.S.switchStandup(2);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 2, person: "iris" });
  });

  it("shows_the_whole_board_when_the_person_does_not_speak_in_the_target_standup", async () => {
    seed({ views: [iris1(), noIris3()], route: { view: "standup", standupId: 1, person: "iris" } });
    await T.S.switchStandup(3);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 3, person: null });
  });

  it("shows_the_whole_board_when_the_person_is_excluded_in_the_target_standup", async () => {
    const excluded = mkView(2, [sp("iris", { included: false }), sp("silas")], []);
    seed({ views: [iris1(), excluded], route: { view: "standup", standupId: 1, person: "iris" } });
    await T.S.switchStandup(2);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 2, person: null });
  });

  it("stays_on_the_whole_board_when_that_was_selected", async () => {
    seed({ views: [iris1(), iris2()], route: { view: "standup", standupId: 1, person: null } });
    await T.S.switchStandup(2);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 2, person: null });
  });

  it("fetches_a_view_that_is_not_cached_and_caches_it", async () => {
    const v3 = noIris3();
    seed({ views: [iris1()], tabs: [tab(1), tab(3)], route: { view: "standup", standupId: 1, person: null } });
    let release!: (v: StandupView) => void;
    T.fake.standup!.view!.mockImplementation(() => new Promise<StandupView>((r) => (release = r)));
    const p = T.S.switchStandup(3);
    p.catch(() => undefined);
    await vi.advanceTimersByTimeAsync(0);
    expect(T.S.loadingView.value).toBe(true);
    release(v3);
    await p;
    expect(T.S.loadingView.value).toBe(false);
    expect(T.fake.standup!.view).toHaveBeenCalledTimes(1);
    expect(T.fake.standup!.view).toHaveBeenCalledWith(3);
    expect(T.S.views.value.get(3)).toEqual(v3);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 3, person: null });
  });

  it("keeps_the_person_after_fetching_a_view_where_they_speak", async () => {
    seed({ views: [iris1()], tabs: [tab(1), tab(2)], route: { view: "standup", standupId: 1, person: "iris" } });
    T.fake.standup!.view!.mockResolvedValue(iris2());
    await T.S.switchStandup(2);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 2, person: "iris" });
  });

  it("shows_the_cached_view_at_once_but_refreshes_it_when_the_view_is_marked_stale", async () => {
    const old2 = iris2();
    const fresh2 = { ...iris2(), rev: 2 };
    seed({ views: [iris1(), old2], tabs: [tab(1), tab(2, { rev: 1 })], route: { view: "standup", standupId: 1, person: null } });
    T.S.staleViews.value = new Set([2]);
    let release!: (v: StandupView) => void;
    T.fake.standup!.view!.mockImplementation(() => new Promise<StandupView>((r) => (release = r)));
    const p = T.S.switchStandup(2);
    p.catch(() => undefined); // a stub rejection must fail the assertions below, not the process
    expect(T.R.route.value.view === "standup" && T.R.route.value.standupId).toBe(2);
    expect(T.S.currentView.value).toBe(old2); // the stale cache is shown while refreshing
    await vi.advanceTimersByTimeAsync(0);
    release(fresh2);
    await p;
    expect(T.fake.standup!.view).toHaveBeenCalledTimes(1);
    expect(T.fake.standup!.view).toHaveBeenCalledWith(2);
    expect(T.S.currentView.value?.rev).toBe(2);
    expect(T.S.staleViews.value.has(2)).toBe(false);
  });

  it("refreshes_when_the_cached_rev_is_older_than_the_tabs_rev", async () => {
    seed({ views: [iris1(), iris2()], tabs: [tab(1), tab(2, { rev: 5 })], route: { view: "standup", standupId: 1, person: null } });
    T.fake.standup!.view!.mockResolvedValue({ ...iris2(), rev: 5 });
    await T.S.switchStandup(2);
    expect(T.fake.standup!.view).toHaveBeenCalledTimes(1);
    expect(T.S.views.value.get(2)?.rev).toBe(5);
  });

  it("does_not_refetch_when_the_cached_rev_equals_the_tabs_rev", async () => {
    seed({ views: [iris1(), { ...iris2(), rev: 5 }], tabs: [tab(1), tab(2, { rev: 5 })], route: { view: "standup", standupId: 1, person: null } });
    await T.S.switchStandup(2);
    expect(T.fake.standup!.view).not.toHaveBeenCalled();
  });
});

describe("switchByNumber", () => {
  const tabs = () => [
    tab(9, { position: 0, configured: false, spaceKey: "" }), // a draft is not a tab and takes no number
    tab(20, { position: 1 }),
    tab(30, { position: 2 }),
    tab(40, { position: 3 }),
  ];
  const views = () => [20, 30, 40].map((id) => mkView(id, [sp("a")], []));

  it("numbers_configured_standups_only_so_drafts_are_excluded", async () => {
    seed({ views: views(), tabs: tabs(), route: { view: "standup", standupId: 40, person: null } });
    await T.S.switchByNumber(1);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 20, person: null });
    await T.S.switchByNumber(2);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 30, person: null });
    await T.S.switchByNumber(3);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 40, person: null });
  });

  it("orders_by_position_not_by_array_order", async () => {
    const scrambled = [tab(40, { position: 3 }), tab(20, { position: 1 }), tab(30, { position: 2 })];
    seed({ views: views(), tabs: scrambled, route: { view: "standup", standupId: 40, person: null } });
    await T.S.switchByNumber(1);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 20, person: null });
  });

  it("does_nothing_for_a_number_beyond_the_configured_standups", async () => {
    seed({ views: views(), tabs: tabs(), route: { view: "standup", standupId: 20, person: "a" } });
    await T.S.switchByNumber(4);
    await T.S.switchByNumber(9);
    await T.S.switchByNumber(0);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 20, person: "a" });
    expect(callsOtherThan(T.fake)).toEqual([]);
  });

  it("keeps_the_person_when_they_speak_in_the_numbered_standup", async () => {
    seed({ views: views(), tabs: tabs(), route: { view: "standup", standupId: 20, person: "a" } });
    await T.S.switchByNumber(2);
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 30, person: "a" });
  });
});

// ── refresh and events ──────────────────────────────────────────────────────────────────────────
describe("refreshView", () => {
  it("re_requests_the_view_replaces_the_cache_entry_returns_it_and_clears_stale", async () => {
    const old = mkView(2, [sp("a")], []);
    const fresh = { ...mkView(2, [sp("a"), sp("b")], []), rev: 4 };
    seed({ views: [old], route: { view: "standup", standupId: 2, person: null } });
    T.S.staleViews.value = new Set([2]);
    T.fake.standup!.view!.mockResolvedValue(fresh);
    const got = await T.S.refreshView(2);
    expect(T.fake.standup!.view).toHaveBeenCalledWith(2);
    expect(got).toEqual(fresh);
    expect(T.S.views.value.get(2)).toEqual(fresh);
    expect(T.S.staleViews.value.has(2)).toBe(false);
  });

  it("does_not_touch_other_cached_views", async () => {
    const v1 = mkView(1, [], []);
    seed({ views: [v1, mkView(2, [], [])], route: { view: "standup", standupId: 2, person: null } });
    T.fake.standup!.view!.mockResolvedValue({ ...mkView(2, [], []), rev: 2 });
    await T.S.refreshView(2);
    expect(T.S.views.value.get(1)).toBe(v1);
  });
});

describe("installStandupEvents", () => {
  let off: () => void;
  beforeEach(() => {
    seed({
      views: [mkView(1, [], []), mkView(2, [], []), mkView(3, [], [])],
      route: { view: "standup", standupId: 1, person: null },
    });
    off = T.S.installStandupEvents();
  });
  afterEach(() => off?.());

  it("marks_a_non_current_standup_stale_on_data_changed_without_fetching", async () => {
    T.bus.emit("data:changed", { standupId: 2, what: "view" });
    await vi.advanceTimersByTimeAsync(0);
    expect(T.S.staleViews.value.has(2)).toBe(true);
    expect(T.S.staleViews.value.has(3)).toBe(false);
    expect(T.fake.standup!.view).not.toHaveBeenCalledWith(2);
  });

  it("marks_every_other_cached_standup_stale_when_standupId_is_0", async () => {
    T.bus.emit("data:changed", { standupId: 0, what: "view" });
    await vi.advanceTimersByTimeAsync(0);
    expect(T.S.staleViews.value.has(2)).toBe(true);
    expect(T.S.staleViews.value.has(3)).toBe(true);
  });

  it("re_requests_the_current_view_and_marks_the_others_stale_on_day_rolled", async () => {
    T.fake.standup!.view!.mockResolvedValue({ ...mkView(1, [], []), rev: 2, today: "2026-11-24" });
    T.bus.emit("day:rolled", { today: "2026-11-24" });
    await vi.advanceTimersByTimeAsync(0);
    expect(T.fake.standup!.view).toHaveBeenCalledWith(1);
    expect(T.S.views.value.get(1)?.today).toBe("2026-11-24");
    expect(T.S.staleViews.value.has(2)).toBe(true);
    expect(T.S.staleViews.value.has(3)).toBe(true);
  });

  it("stops_reacting_after_unsubscribe", async () => {
    off();
    T.bus.emit("data:changed", { standupId: 2, what: "view" });
    T.bus.emit("day:rolled", { today: "2026-11-24" });
    await vi.advanceTimersByTimeAsync(0);
    expect(T.S.staleViews.value.size).toBe(0);
    expect(T.fake.standup!.view).not.toHaveBeenCalled();
  });
});
