// WP-14 tests: the in-browser mock facade (createMockApi) and its fixtures (architecture §4.4, testing §5).
//
// SPEC-NOTE: api/fixtures/ is empty until WP-14 generates it from design/reference/sample-data.json, so the
//   expectations below are hardcoded from that sample (tab order MOB, WEB, DATA, GRO, PLAT; MOB's seven people; a
//   handful of MOB ticket keys). Active-ticket expectations are computed from the sample file itself.
// SPEC-NOTE: speaker ids may be remapped by the fixture generator, so people are matched by display name as a set,
//   never by id or order. An "Unassigned" SpeakerView (unassigned: true) may or may not be present in
//   view.speakers; the 7 counts exclude it.
// SPEC-NOTE: the done window may filter done tickets, so only the NON-done sample tickets (and a few known active
//   keys) are required to be present in the MOB view.
// SPEC-NOTE: `failures[..].times` is required in every failure test here; its default when omitted is not asserted.
// SPEC-NOTE: `calls` are asserted only after a call has settled (not at call time), so a mock that records either
//   at invocation or after the latency passes.
// SPEC-NOTE: pinned behaviours beyond the brief (confirm or relax): a zero-latency mock settles on microtasks alone
//   (no timers); standup.view of an unknown id rejects AppError "not_found"; a source-affecting update (extraFilter)
//   acks needsSync: true; config.create adds a DRAFT tab (configured false) and discardDraft removes it; choosing
//   a space then a board (ids taken from searchSpaces/searchBoards) turns the draft into a configured tab;
//   config.delete hides the tab and config.restore brings it back; searches with no match return [] (never null);
//   each config mutation (update, setSpeakerIncluded, setLaneLimit, setColumnLane) bumps rev by exactly 1; null
//   patch fields mean "unchanged"; separate createMockApi instances share no state; a failing call is still
//   recorded in `calls` and leaves state untouched.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError, setApi, wailsApi, type Api } from "./index";
import { createMemoryBus, on, setEventBus } from "./events";
import { createMockApi, type MockApi } from "./mock";

interface SampleTicket {
  k: string;
  a: string | null;
  s: string;
}
interface SampleStandup {
  id: string;
  name: string;
  key: string;
  people: { id: string; name: string }[];
  tickets: SampleTicket[];
}
const sample = JSON.parse(readFileSync(resolve(process.cwd(), "../design/reference/sample-data.json"), "utf8")) as {
  standups: SampleStandup[];
};
const MOB = sample.standups[0]!;
const TAB_NAMES = ["Saffron Mobile", "Web & Checkout", "Recipe Data", "Growth", "Platform"];
const TAB_KEYS = ["MOB", "WEB", "DATA", "GRO", "PLAT"];

afterEach(() => {
  vi.useRealTimers();
});

async function svcTab(mock: MockApi) {
  const tabs = await mock.standup.list();
  return tabs[0]!;
}

describe("createMockApi: surface", () => {
  it("implements_every_method_of_the_Api_interface", () => {
    const mock = createMockApi();
    for (const [svc, methods] of Object.entries(wailsApi)) {
      const mockSvc = (mock as unknown as Record<string, Record<string, unknown>>)[svc];
      expect(mockSvc, `service ${svc}`).toBeDefined();
      for (const m of Object.keys(methods)) {
        expect(typeof mockSvc![m], `${svc}.${m}`).toBe("function");
      }
    }
  });

  it("every_method_returns_a_promise_rather_than_throwing_synchronously", () => {
    const mock = createMockApi();
    const p = mock.standup.list();
    expect(p).toBeInstanceOf(Promise);
  });
});

describe("createMockApi: bootstrap", () => {
  it("returns_at_least_one_configured_standup_with_a_view", async () => {
    const mock = createMockApi();
    const boot = await mock.app.bootstrap();
    expect(boot.standups.length).toBeGreaterThanOrEqual(1);
    expect(boot.standups.some((t) => t.configured)).toBe(true);
    expect(boot.view).not.toBeNull();
    expect(boot.standups.map((t) => t.id)).toContain(boot.view!.standupId);
  });

  it("never_sends_null_lists_or_maps_in_the_boot_object", async () => {
    const boot = await createMockApi().app.bootstrap();
    expect(boot.settings).not.toBeNull();
    expect(Array.isArray(boot.standups)).toBe(true);
    expect(Array.isArray(boot.sync)).toBe(true);
    expect(boot.ui.lastPerson).not.toBeNull();
    expect(Array.isArray(boot.view!.speakers)).toBe(true);
    expect(Array.isArray(boot.view!.tickets)).toBe(true);
  });

  it("reports_a_usable_connection_and_build_info", async () => {
    const boot = await createMockApi().app.bootstrap();
    expect(boot.connection.state).toBe("ok");
    expect(typeof boot.build.version).toBe("string");
    expect(boot.recovered).toBe(false);
  });
});

describe("createMockApi: standup.list", () => {
  it("returns_five_tabs_in_order_with_service_platform_first", async () => {
    const tabs = await createMockApi().standup.list();
    expect(tabs).toHaveLength(5);
    expect(tabs.map((t) => t.name)).toEqual(TAB_NAMES);
    expect(tabs.map((t) => t.spaceKey)).toEqual(TAB_KEYS);
  });

  it("has_ascending_positions_and_every_tab_configured", async () => {
    const tabs = await createMockApi().standup.list();
    const positions = tabs.map((t) => t.position);
    expect(positions).toEqual([...positions].sort((a, b) => a - b));
    expect(new Set(tabs.map((t) => t.id)).size).toBe(5);
    expect(tabs.every((t) => t.configured)).toBe(true);
  });
});

describe("createMockApi: standup.view", () => {
  it("returns_the_SVC_view_with_the_seven_people_from_the_sample", async () => {
    const mock = createMockApi();
    const tab = await svcTab(mock);
    const view = await mock.standup.view(tab.id);
    expect(view.standupId).toBe(tab.id);
    expect(view.name).toBe("Saffron Mobile");
    expect(view.spaceKey).toBe("MOB");
    const people = view.speakers.filter((s) => !s.unassigned).map((s) => s.name).sort();
    expect(people).toEqual(MOB.people.map((p) => p.name).sort());
    expect(people).toHaveLength(7);
  });

  it("contains_every_active_ticket_key_of_the_sample_SVC_standup", async () => {
    const mock = createMockApi();
    const view = await mock.standup.view((await svcTab(mock)).id);
    const keys = view.tickets.map((t) => t.key);
    const active = MOB.tickets.filter((t) => t.s !== "done").map((t) => t.k);
    expect(active).toHaveLength(40);
    for (const k of active) expect(keys, k).toContain(k);
    for (const k of ["MOB-1390", "MOB-1399", "MOB-1407", "MOB-1411", "MOB-1440"]) expect(keys).toContain(k);
    expect(new Set(keys).size).toBe(keys.length);
    expect(keys.every((k) => k.startsWith("MOB-"))).toBe(true);
  });

  it("assigns_tickets_to_known_speakers_or_to_nobody", async () => {
    const mock = createMockApi();
    const view = await mock.standup.view((await svcTab(mock)).id);
    const ids = new Set(view.speakers.map((s) => s.id));
    for (const t of view.tickets) {
      expect(t.assignee === "" || ids.has(t.assignee), `${t.key} assignee ${t.assignee}`).toBe(true);
    }
    expect(view.tickets.some((t) => t.assignee === "")).toBe(true);
    const lanes = new Set(["todo", "prog", "review", "qa", "done"]);
    expect(view.tickets.every((t) => lanes.has(t.lane))).toBe(true);
  });

  it("never_returns_null_lists_even_for_empty_collections", async () => {
    const mock = createMockApi();
    for (const tab of await mock.standup.list()) {
      const v = await mock.standup.view(tab.id);
      for (const list of [v.axis, v.lanes, v.speakers, v.tickets, v.backlog, v.warnings]) {
        expect(Array.isArray(list)).toBe(true);
      }
      for (const t of v.tickets) {
        expect(Array.isArray(t.pulse.segs)).toBe(true);
        expect(Array.isArray(t.pulse.marks)).toBe(true);
        expect(Array.isArray(t.pulse.blocked)).toBe(true);
      }
      expect(v.lanes.map((l) => l.lane).at(-1)).toBe("done");
    }
  });

  it("serves_a_view_for_every_tab_whose_standupId_matches_the_tab", async () => {
    const mock = createMockApi();
    const tabs = await mock.standup.list();
    const names: string[] = [];
    for (const tab of tabs) {
      const v = await mock.standup.view(tab.id);
      expect(v.standupId).toBe(tab.id);
      names.push(v.name);
    }
    expect(names).toEqual(TAB_NAMES);
  });

  it("rejects_with_AppError_not_found_for_an_unknown_standup", async () => {
    const mock = createMockApi();
    const err = await mock.standup.view(987654).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AppError);
    expect((err as AppError).code).toBe("not_found");
  });
});

describe("createMockApi: call recording", () => {
  it("records_calls_as_service_dot_method_with_their_args_in_order", async () => {
    const mock = createMockApi();
    const tab = await svcTab(mock);
    mock.resetCalls();
    await mock.standup.list();
    await mock.standup.view(tab.id);
    await mock.sync.status();
    expect(mock.calls.map((c) => c.method)).toEqual(["standup.list", "standup.view", "sync.status"]);
    expect(mock.calls[1]!.args).toEqual([tab.id]);
  });

  it("resetCalls_empties_the_log", async () => {
    const mock = createMockApi();
    await mock.standup.list();
    expect(mock.calls.length).toBeGreaterThan(0);
    mock.resetCalls();
    expect(mock.calls).toHaveLength(0);
    await mock.standup.list();
    expect(mock.calls.map((c) => c.method)).toEqual(["standup.list"]);
  });

  it("records_failed_calls_too", async () => {
    const mock = createMockApi({ failures: { "standup.list": { code: "db", times: 1 } } });
    await mock.standup.list().catch(() => undefined);
    expect(mock.calls.map((c) => c.method)).toEqual(["standup.list"]);
  });
});

describe("createMockApi: options", () => {
  it("delays_each_call_by_the_latency_option", async () => {
    vi.useFakeTimers();
    const mock = createMockApi({ latency: 50 });
    let settled = false;
    const p = mock.standup.list().then((v) => {
      settled = true;
      return v;
    });
    await vi.advanceTimersByTimeAsync(49);
    expect(settled).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(settled).toBe(true);
    expect(await p).toHaveLength(5);
  });

  it("resolves_without_timers_when_latency_is_zero", async () => {
    vi.useFakeTimers();
    const mock = createMockApi();
    // no timer advance: a zero-latency mock must settle on microtasks alone
    await expect(mock.standup.list()).resolves.toHaveLength(5);
  });

  it("rejects_the_configured_method_with_an_AppError_times_times_then_succeeds", async () => {
    const mock = createMockApi({ failures: { "config.update": { code: "db", message: "disk on fire", times: 2 } } });
    const id = (await svcTab(mock)).id;
    for (let i = 0; i < 2; i++) {
      const err = await mock.config.update(id, { name: "X" }).catch((e: unknown) => e);
      expect(err).toBeInstanceOf(AppError);
      expect((err as AppError).code).toBe("db");
      expect((err as AppError).message).toBe("disk on fire");
    }
    const ack = await mock.config.update(id, { name: "X" });
    expect(typeof ack.rev).toBe("number");
    expect((await mock.config.get(id)).name).toBe("X");
  });

  it("carries_the_field_of_a_validation_failure_and_leaves_state_untouched", async () => {
    const mock = createMockApi({
      failures: { "config.update": { code: "validation", message: "Name is too long.", field: "name", times: 1 } },
    });
    const id = (await svcTab(mock)).id;
    const before = await mock.config.get(id);
    const err = (await mock.config.update(id, { name: "Y" }).catch((e: unknown) => e)) as AppError;
    expect(err.code).toBe("validation");
    expect(err.field).toBe("name");
    const after = await mock.config.get(id);
    expect(after.name).toBe(before.name);
    expect(after.rev).toBe(before.rev);
  });

  it("only_fails_the_named_method", async () => {
    const mock = createMockApi({ failures: { "config.update": { code: "db", times: 5 } } });
    await expect(mock.standup.list()).resolves.toHaveLength(5);
    await expect(mock.config.get((await svcTab(mock)).id)).resolves.toBeDefined();
  });
});

describe("createMockApi: configuration persists within the instance", () => {
  it("config_get_returns_the_SVC_configuration", async () => {
    const mock = createMockApi();
    const tab = await svcTab(mock);
    const cfg = await mock.config.get(tab.id);
    expect(cfg.id).toBe(tab.id);
    expect(cfg.name).toBe("Saffron Mobile");
    expect(cfg.draft).toBe(false);
    expect(cfg.speakers.length).toBeGreaterThanOrEqual(7);
    for (const list of [cfg.columns, cfg.lanes, cfg.speakers, cfg.unmappedStatuses]) expect(Array.isArray(list)).toBe(true);
  });

  it("update_then_get_reflects_the_patch_and_rev_increments_by_one", async () => {
    const mock = createMockApi();
    const id = (await svcTab(mock)).id;
    const before = await mock.config.get(id);
    const ack = await mock.config.update(id, { name: "Platform Services", pulseDays: 14 });
    expect(ack.rev).toBe(before.rev + 1);
    const after = await mock.config.get(id);
    expect(after.name).toBe("Platform Services");
    expect(after.pulseDays).toBe(14);
    expect(after.rev).toBe(ack.rev);
    const ack2 = await mock.config.update(id, { showPRs: !before.showPRs });
    expect(ack2.rev).toBe(ack.rev + 1);
    expect((await mock.config.get(id)).showPRs).toBe(!before.showPRs);
  });

  it("treats_null_patch_fields_as_unchanged", async () => {
    const mock = createMockApi();
    const id = (await svcTab(mock)).id;
    const before = await mock.config.get(id);
    await mock.config.update(id, { name: null, pulseDays: 7 });
    const after = await mock.config.get(id);
    expect(after.name).toBe(before.name);
    expect(after.pulseDays).toBe(7);
  });

  it("does_not_share_state_between_mock_instances", async () => {
    const a = createMockApi();
    const b = createMockApi();
    const id = (await svcTab(a)).id;
    await a.config.update(id, { name: "Only in A" });
    expect((await b.config.get((await svcTab(b)).id)).name).toBe("Saffron Mobile");
  });

  it("source_affecting_changes_report_needsSync_in_the_ack", async () => {
    const mock = createMockApi();
    const id = (await svcTab(mock)).id;
    const ack = await mock.config.update(id, { extraFilter: "labels = standup" });
    expect(ack.needsSync).toBe(true);
    const viewOnly = await mock.config.update(id, { pulseDays: 28 });
    expect(typeof viewOnly.needsSync).toBe("boolean");
  });

  it("speaker_inclusion_persists_and_bumps_rev", async () => {
    const mock = createMockApi();
    const id = (await svcTab(mock)).id;
    const cfg = await mock.config.get(id);
    const target = cfg.speakers[0]!;
    const ack = await mock.config.setSpeakerIncluded(id, target.accountId, false);
    expect(ack.rev).toBe(cfg.rev + 1);
    const after = await mock.config.get(id);
    expect(after.speakers.find((s) => s.accountId === target.accountId)!.included).toBe(false);
  });

  it("lane_limit_and_column_mapping_persist", async () => {
    const mock = createMockApi();
    const id = (await svcTab(mock)).id;
    const cfg = await mock.config.get(id);
    await mock.config.setLaneLimit(id, "review", 9);
    const column = cfg.columns[0]!;
    await mock.config.setColumnLane(id, column.columnName, "hidden");
    const after = await mock.config.get(id);
    expect(after.lanes.find((l) => l.lane === "review")!.limitDays).toBe(9);
    expect(after.columns.find((c) => c.columnName === column.columnName)!.lane).toBe("hidden");
    expect(after.rev).toBe(cfg.rev + 2);
  });

  it("create_adds_a_draft_tab_and_discardDraft_removes_it", async () => {
    const mock = createMockApi();
    const draft = await mock.config.create();
    expect(draft.draft).toBe(true);
    expect(draft.board).toBeNull();
    const tabs = await mock.standup.list();
    expect(tabs).toHaveLength(6);
    const tab = tabs.find((t) => t.id === draft.id)!;
    expect(tab.configured).toBe(false);
    await mock.config.discardDraft(draft.id);
    expect(await mock.standup.list()).toHaveLength(5);
  });

  it("choosing_a_space_and_board_turns_a_draft_into_a_configured_tab", async () => {
    const mock = createMockApi();
    const draft = await mock.config.create();
    const spaces = await mock.connection.searchSpaces("");
    expect(spaces.length).toBeGreaterThan(0);
    const space = spaces[0]!;
    const withSpace = await mock.config.setSpace(draft.id, space.id);
    expect(withSpace.space?.id).toBe(space.id);
    const boards = await mock.connection.searchBoards(space.key, "");
    expect(boards.length).toBeGreaterThan(0);
    const withBoard = await mock.config.setBoard(draft.id, boards[0]!.id);
    expect(withBoard.board?.id).toBe(boards[0]!.id);
    expect(withBoard.draft).toBe(false);
    expect((await mock.standup.list()).find((t) => t.id === draft.id)!.configured).toBe(true);
  });

  it("delete_archives_a_standup_and_restore_brings_it_back", async () => {
    const mock = createMockApi();
    const tabs = await mock.standup.list();
    const victim = tabs[1]!;
    await mock.config.delete(victim.id);
    expect((await mock.standup.list()).map((t) => t.id)).not.toContain(victim.id);
    await mock.config.restore(victim.id);
    expect((await mock.standup.list()).map((t) => t.id)).toContain(victim.id);
  });
});

describe("createMockApi: list-returning methods never return null", () => {
  it("returns_arrays_for_searches_status_and_sinks", async () => {
    const mock = createMockApi();
    const id = (await svcTab(mock)).id;
    expect(await mock.sync.status()).toBeInstanceOf(Array);
    expect(await mock.sink.kinds()).toBeInstanceOf(Array);
    expect(await mock.sink.list()).toBeInstanceOf(Array);
    expect(await mock.connection.searchSpaces("zzzzqqqq-no-such-space")).toEqual([]);
    expect(await mock.connection.searchBoards("MOB", "zzzzqqqq")).toEqual([]);
    expect(await mock.connection.searchUsers(id, "zzzzqqqq")).toEqual([]);
  });

  it("sync_start_returns_a_run_id", async () => {
    const mock = createMockApi();
    const id = (await svcTab(mock)).id;
    const runId = await mock.sync.start(id, "incremental");
    expect(typeof runId).toBe("number");
  });
});

describe("createMockApi: event bus", () => {
  it("delivers_emitted_events_to_subscribers_until_they_unsubscribe", () => {
    const mock = createMockApi();
    const got: string[] = [];
    const off = mock.bus.on("day:rolled", (p) => got.push(p.today));
    mock.bus.emit("day:rolled", { today: "2026-11-23" });
    off();
    mock.bus.emit("day:rolled", { today: "2026-11-24" });
    expect(got).toEqual(["2026-11-23"]);
  });

  it("works_through_setEventBus_and_the_typed_on_helper", () => {
    const mock = createMockApi();
    setEventBus(mock.bus);
    const seen: number[] = [];
    const off = on("data:changed", (p) => seen.push(p.standupId));
    mock.bus.emit("data:changed", { standupId: 3, what: "view" });
    off();
    expect(seen).toEqual([3]);
  });

  it("is_separate_per_mock_instance", () => {
    const a = createMockApi();
    const b = createMockApi();
    const got: string[] = [];
    b.bus.on("day:rolled", (p) => got.push(p.today));
    a.bus.emit("day:rolled", { today: "x" });
    expect(got).toEqual([]);
  });

  it("can_be_installed_as_the_facade_via_setApi", async () => {
    const mock = createMockApi();
    setApi(mock as Api);
    const { api } = await import("./index");
    expect(await api().standup.list()).toHaveLength(5);
    // keep the memory bus import used so a bus swap cannot be tree-shaken from this test
    expect(typeof createMemoryBus).toBe("function");
  });
});
