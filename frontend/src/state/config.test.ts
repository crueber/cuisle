// WP-15 tests: Configure state (state/config.ts; architecture §5.3, ux-spec §9.5, §4 drafts).
//
// SPEC-NOTE: the autosave key is `${standupId}:${field}` for saveField; it is proven by behaviour (a validation
//   rejection lands in fieldErrors under "3:name"), never by spying on enqueue.
// SPEC-NOTE: saveField sends ConfigService.Update(id, { [field]: value }) (a patch carrying ONLY that field with its
//   full new value). A failed save keeps the optimistic value in `config` (the user's text stays).
// SPEC-NOTE: after a save ack, config.rev = max(config.rev, ack.rev) and config.needsSync becomes true when
//   ack.needsSync is true.
// SPEC-NOTE: view-affecting fields (everything except the source fields scope and extraFilter) re-request
//   StandupService.View for that standup after the ack (architecture §5.3) and store it in `views`; scope and
//   extraFilter do not (they need a sync first).
// SPEC-NOTE: setSpace/setBoard pass the option's `id` (SpaceOption.id, BoardOption.id), and replace `config` with the
//   server response. setBoard (a draft leaves DRAFT) also re-reads StandupService.List into `standups`, as does
//   create/duplicate/delete/restore, because the server emits no data:changed for the receiving call itself.
// SPEC-NOTE: createStandup and duplicateStandup replace `config` with the response AND open Configure on it
//   (route { view: "configure", standupId: <new id> }). copySettingsFrom(from) calls
//   ConfigService.CopySettingsFrom(current id, from) and replaces `config`.
// SPEC-NOTE: removeSpeaker's Undo re-adds the speaker (AddSpeaker) and then MoveSpeaker to the removed speaker's
//   original SpeakerConfig.position; fixtures use position === index so index and position readings agree. The
//   toast timeout is 10 000 ms.
// SPEC-NOTE: moveSpeaker/moveStandup are only asserted at the facade-call level (their position base and optimistic
//   ordering are not specified; reorders share a per-list key and are deltas, so no coalescing is asserted).
// SPEC-NOTE: deleteStandup's toast message contains the standup's name (taken from `standups`) and its action is
//   "Undo" (ConfigService.Restore).
// SPEC-NOTE: leaveConfigure: flush saves, DiscardDraft for every draft tab with an empty spaceKey (a draft with a
//   space chosen is NOT discarded; Go's DiscardDraft would no-op anyway), SyncService.Start(id, "full") for every
//   configured tab with needsSync (fixtures set needsSync on the tab, and on `config` for the open standup, so
//   either source passes), then returns to the standup route (id and person) that was active before Configure
//   opened. A sync start failure does not block navigation. Failed saves do not block either: they raise a toast
//   "1 change not saved" with a "Retry" action that calls retryFailed.
// SPEC-NOTE: pinned beyond the brief: validateFilter stores the FilterCheck (ok or not) into config.filterCheck; the
//   optimistic shapes are: setColumnLane sets that column's lane, setWalkOrder reorders config.lanes with "done"
//   kept LAST, setLaneLimit changes only that lane's limitDays, setSpeakerIncluded changes only that speaker,
//   removeSpeaker drops the speaker; saveField is a silent no-op when no config is loaded; two saves of the same
//   field are serialised and the pending one is coalesced; different fields never wait on each other; a retryable
//   failure retries through the save queue (0.5 s) while the optimistic value stays.
// SPEC-NOTE: the fake Api's standup.view answers a minimal view for any id and standup.list answers the tabs the
//   test sets, so a correct implementation can read their results.
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Api, SaveAck, SpaceOption, BoardOption, SpeakerConfig, StandupConfig, StandupTab, StandupView, UserOption } from "../api/index";

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
    C: await import("./config"), A: await import("./app"), R: await import("./route"), St: await import("./standup"),
    Sv: await import("./save"), U: await import("./ui"),
  };
}

function speaker(accountId: string, position: number, over: Partial<SpeakerConfig> = {}): SpeakerConfig {
  return { accountId, name: accountId.toUpperCase(), initials: accountId.slice(0, 2).toUpperCase(), position, included: true, manual: false, active: 1, note: "", ...over };
}
function cfg(id = 3, over: Partial<StandupConfig> = {}): StandupConfig {
  return {
    id, rev: 5, position: 1, name: "Saffron Mobile",
    space: { id: "s1", key: "MOB", name: "Saffron Mobile" }, board: { id: "b1", name: "MOB board", type: "scrum" },
    scope: "sprint", extraFilter: "", filterCheck: null,
    columns: [
      { columnName: "To Do", statusNames: ["To Do"], lane: "todo", auto: true },
      { columnName: "In Progress", statusNames: ["In Progress"], lane: "prog", auto: true },
      { columnName: "Done", statusNames: ["Done"], lane: "done", auto: true },
    ],
    lanes: ["qa", "review", "prog", "todo", "done"].map((lane) => ({ lane, label: lane, limitDays: 5, columns: [] })),
    unmappedStatuses: [], rowsMode: "fit", speakers: [speaker("a", 0), speaker("b", 1), speaker("c", 2)],
    unassignedPosition: "last", reshuffleDaily: false, pulseDays: 21, showCommits: true, showPRs: true, showComments: true,
    shadeWeekends: true, doneWindow: "sprint", backlogRows: 12, needsSync: false, draft: false, ...over,
  } as unknown as StandupConfig;
}
const ack = (rev: number, needsSync = false): SaveAck => ({ rev, savedAt: 1_700_000_000, needsSync });
function tab(id: number, over: Partial<StandupTab> = {}): StandupTab {
  return { id, rev: 1, name: `Standup ${id}`, spaceKey: `K${id}`, position: id, activeCount: 0, speakerCount: 0, configured: true, needsSync: false, lastSyncedAt: 0, ...over };
}
function view(id: number, rev = 1): StandupView {
  return { standupId: id, rev, name: `S${id}`, speakers: [], tickets: [], lanes: [], axis: [], backlog: [], warnings: [] } as unknown as StandupView;
}

let T: Awaited<ReturnType<typeof boot>>;
const settle = () => vi.advanceTimersByTimeAsync(0);
beforeEach(async () => {
  vi.useFakeTimers();
  T = await boot();
});
afterEach(() => {
  vi.useRealTimers();
});
/** Loads a config straight into the signal (loadConfig is tested separately). */
function open(c = cfg()) {
  T.C.config.value = c;
  T.fake.config!.update!.mockResolvedValue(ack(6));
}

describe("loadConfig", () => {
  it("reads_ConfigService_Get_into_the_config_signal_with_a_loading_flag", async () => {
    const c = cfg(3);
    let release!: (v: StandupConfig) => void;
    T.fake.config!.get!.mockImplementation(() => new Promise<StandupConfig>((r) => (release = r)));
    const p = T.C.loadConfig(3);
    p.catch(() => undefined);
    await settle();
    expect(T.C.configLoading.value).toBe(true);
    release(c);
    await p;
    expect(T.fake.config!.get).toHaveBeenCalledWith(3);
    expect(T.C.config.value).toEqual(c);
    expect(T.C.configLoading.value).toBe(false);
  });
});

describe("saveField", () => {
  it("updates_the_signal_optimistically_and_saves_only_that_field", async () => {
    open();
    T.C.saveField("pulseDays", 28);
    expect(T.C.config.value?.pulseDays).toBe(28);
    await settle();
    expect(T.fake.config!.update).toHaveBeenCalledTimes(1);
    expect(T.fake.config!.update).toHaveBeenCalledWith(3, { pulseDays: 28 });
  });

  it("is_a_no_op_when_no_config_is_loaded", async () => {
    T.C.config.value = null;
    expect(() => T.C.saveField("pulseDays", 28)).not.toThrow();
    await settle();
    expect(T.fake.config!.update).not.toHaveBeenCalled();
  });

  it("debounces_text_fields_400ms_and_coalesces_keystrokes_into_one_save_of_the_full_value", async () => {
    open();
    T.C.saveField("name", "F", { text: true });
    expect(T.C.config.value?.name).toBe("F");
    await vi.advanceTimersByTimeAsync(200);
    T.C.saveField("name", "Fo", { text: true });
    await vi.advanceTimersByTimeAsync(200);
    T.C.saveField("name", "Foo", { text: true });
    expect(T.C.config.value?.name).toBe("Foo");
    await vi.advanceTimersByTimeAsync(399);
    expect(T.fake.config!.update).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(T.fake.config!.update).toHaveBeenCalledTimes(1);
    expect(T.fake.config!.update).toHaveBeenCalledWith(3, { name: "Foo" });
  });

  it("saves_non_text_fields_immediately", async () => {
    open();
    T.C.saveField("showPRs", false);
    await settle();
    expect(T.fake.config!.update).toHaveBeenCalledWith(3, { showPRs: false });
  });

  it("saves_different_fields_independently_without_waiting_on_each_other", async () => {
    open();
    T.fake.config!.update!.mockImplementationOnce(() => new Promise<SaveAck>(() => undefined)); // first call never ends
    T.C.saveField("rowsMode", "roomy");
    T.C.saveField("shadeWeekends", false);
    await settle();
    expect(T.fake.config!.update!.mock.calls).toEqual([
      [3, { rowsMode: "roomy" }],
      [3, { shadeWeekends: false }],
    ]);
  });

  it("serialises_two_saves_of_the_same_field", async () => {
    open();
    let release!: (a: SaveAck) => void;
    T.fake.config!.update!.mockImplementationOnce(() => new Promise<SaveAck>((r) => (release = r)));
    T.C.saveField("pulseDays", 14);
    await settle();
    T.C.saveField("pulseDays", 28);
    T.C.saveField("pulseDays", 7);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(T.fake.config!.update).toHaveBeenCalledTimes(1);
    release(ack(6));
    await settle();
    expect(T.fake.config!.update!.mock.calls).toEqual([
      [3, { pulseDays: 14 }],
      [3, { pulseDays: 7 }],
    ]);
  });

  it("applies_the_ack_rev_and_needsSync_to_the_config", async () => {
    open(cfg(3, { rev: 5, needsSync: false }));
    T.fake.config!.update!.mockResolvedValue(ack(9, true));
    T.C.saveField("scope", "board");
    await settle();
    expect(T.C.config.value?.rev).toBe(9);
    expect(T.C.config.value?.needsSync).toBe(true);
  });

  it("never_lowers_the_rev_when_acks_arrive_out_of_order", async () => {
    open(cfg(3, { rev: 5 }));
    T.fake.config!.update!.mockResolvedValueOnce(ack(9)).mockResolvedValueOnce(ack(8));
    T.C.saveField("rowsMode", "roomy");
    await settle();
    T.C.saveField("showPRs", false);
    await settle();
    expect(T.C.config.value?.rev).toBe(9);
  });

  it("re_requests_the_view_after_a_view_affecting_save", async () => {
    open();
    T.fake.standup!.view!.mockResolvedValue(view(3, 9));
    T.C.saveField("pulseDays", 14);
    await settle();
    expect(T.fake.standup!.view).toHaveBeenCalledWith(3);
    expect(T.St.views.value.get(3)?.rev).toBe(9);
  });

  it("does_not_re_request_the_view_after_a_source_field_save", async () => {
    open();
    T.fake.config!.update!.mockResolvedValue(ack(6, true));
    T.C.saveField("extraFilter", "labels = x", { text: true });
    T.C.saveField("scope", "board");
    await vi.advanceTimersByTimeAsync(500);
    expect(T.fake.config!.update).toHaveBeenCalledTimes(2);
    expect(T.fake.standup!.view).not.toHaveBeenCalled();
  });

  it("puts_a_validation_error_on_the_field_by_key_and_keeps_the_typed_text", async () => {
    open();
    const err = new T.AppError("validation", "Name is too long.", "", false, "name");
    T.fake.config!.update!.mockRejectedValue(err);
    T.C.saveField("name", "x".repeat(300), { text: true });
    await vi.advanceTimersByTimeAsync(400);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(T.fake.config!.update).toHaveBeenCalledTimes(1); // no retry
    expect(T.Sv.fieldErrors.value.get("3:name")).toBe(err);
    expect(T.C.config.value?.name).toBe("x".repeat(300));
  });

  it("retries_a_retryable_failure_through_the_save_queue", async () => {
    open();
    T.fake.config!.update!.mockRejectedValueOnce(new T.AppError("db", "Busy.", "", true)).mockResolvedValue(ack(6));
    T.C.saveField("pulseDays", 14);
    await settle();
    expect(T.fake.config!.update).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(500);
    expect(T.fake.config!.update).toHaveBeenCalledTimes(2);
    expect(T.C.config.value?.pulseDays).toBe(14);
    expect(T.Sv.failedKeys.value.size).toBe(0);
  });
});

describe("server-shaped responses replace local state", () => {
  const space: SpaceOption = { id: "10001", key: "WEB", name: "Web & Checkout" };
  const board: BoardOption = { id: "42", name: "WEB board", type: "scrum" };

  it("setSpace_sends_the_space_id_and_replaces_the_config", async () => {
    open();
    const response = cfg(3, { rev: 11, space, board: null, columns: [] });
    T.fake.config!.setSpace!.mockResolvedValue(response);
    T.C.setSpace(space);
    await settle();
    expect(T.fake.config!.setSpace).toHaveBeenCalledWith(3, "10001");
    expect(T.C.config.value).toEqual(response);
  });

  it("setBoard_sends_the_board_id_replaces_the_config_and_refreshes_the_tabs", async () => {
    open(cfg(3, { draft: true, board: null }));
    const response = cfg(3, { rev: 12, board, draft: false });
    const tabs = [tab(3)];
    T.fake.config!.setBoard!.mockResolvedValue(response);
    T.fake.standup!.list!.mockResolvedValue(tabs);
    T.C.setBoard(board);
    await settle();
    expect(T.fake.config!.setBoard).toHaveBeenCalledWith(3, "42");
    expect(T.C.config.value).toEqual(response);
    expect(T.fake.standup!.list).toHaveBeenCalled();
    expect(T.A.standups.value).toEqual(tabs);
  });

  it("sortSpeakers_replaces_the_speaker_list_with_the_response", async () => {
    open();
    const sorted = [speaker("c", 0), speaker("a", 1), speaker("b", 2)];
    T.fake.config!.sortSpeakers!.mockResolvedValue(sorted);
    T.C.sortSpeakers("az");
    await settle();
    expect(T.fake.config!.sortSpeakers).toHaveBeenCalledWith(3, "az");
    expect(T.C.config.value?.speakers).toEqual(sorted);
  });

  it("addSpeaker_sends_the_account_id_and_replaces_the_speaker_list", async () => {
    open();
    const user: UserOption = { accountId: "zed", displayName: "Zed Zeta", initials: "ZZ", onBoard: false };
    const withZed = [...cfg().speakers, speaker("zed", 3, { manual: true })];
    T.fake.config!.addSpeaker!.mockResolvedValue(withZed);
    T.C.addSpeaker(user);
    await settle();
    expect(T.fake.config!.addSpeaker).toHaveBeenCalledWith(3, "zed");
    expect(T.C.config.value?.speakers).toEqual(withZed);
  });
});

describe("column, lane and speaker settings", () => {
  it("setColumnLane_updates_the_mapping_optimistically_and_saves_it", async () => {
    open();
    T.fake.config!.setColumnLane!.mockResolvedValue(ack(7, true));
    T.C.setColumnLane("In Progress", "review");
    expect(T.C.config.value?.columns.find((c) => c.columnName === "In Progress")?.lane).toBe("review");
    await settle();
    expect(T.fake.config!.setColumnLane).toHaveBeenCalledWith(3, "In Progress", "review");
    expect(T.C.config.value?.needsSync).toBe(true);
  });

  it("setWalkOrder_reorders_the_walk_lanes_keeping_done_last_and_saves", async () => {
    open();
    T.fake.config!.setWalkOrder!.mockResolvedValue(ack(7));
    T.C.setWalkOrder(["todo", "prog", "review", "qa"]);
    expect(T.C.config.value?.lanes.map((l) => l.lane)).toEqual(["todo", "prog", "review", "qa", "done"]);
    await settle();
    expect(T.fake.config!.setWalkOrder).toHaveBeenCalledWith(3, ["todo", "prog", "review", "qa"]);
  });

  it("setLaneLimit_updates_that_lane_and_saves", async () => {
    open();
    T.fake.config!.setLaneLimit!.mockResolvedValue(ack(7));
    T.C.setLaneLimit("review", 9);
    expect(T.C.config.value?.lanes.find((l) => l.lane === "review")?.limitDays).toBe(9);
    expect(T.C.config.value?.lanes.find((l) => l.lane === "qa")?.limitDays).toBe(5);
    await settle();
    expect(T.fake.config!.setLaneLimit).toHaveBeenCalledWith(3, "review", 9);
  });

  it("setSpeakerIncluded_updates_that_speaker_and_saves", async () => {
    open();
    T.fake.config!.setSpeakerIncluded!.mockResolvedValue(ack(7));
    T.C.setSpeakerIncluded("b", false);
    const s = T.C.config.value!.speakers;
    expect(s.find((x) => x.accountId === "b")?.included).toBe(false);
    expect(s.find((x) => x.accountId === "a")?.included).toBe(true);
    await settle();
    expect(T.fake.config!.setSpeakerIncluded).toHaveBeenCalledWith(3, "b", false);
  });

  it("moveSpeaker_calls_MoveSpeaker_with_the_target_position", async () => {
    open();
    T.fake.config!.moveSpeaker!.mockResolvedValue(ack(7));
    T.C.moveSpeaker("c", 0);
    await settle();
    expect(T.fake.config!.moveSpeaker).toHaveBeenCalledWith(3, "c", 0);
  });
});

describe("removeSpeaker with Undo", () => {
  it("removes_the_speaker_optimistically_and_calls_RemoveSpeaker", async () => {
    open();
    T.fake.config!.removeSpeaker!.mockResolvedValue(ack(7));
    T.C.removeSpeaker("b");
    expect(T.C.config.value?.speakers.map((s) => s.accountId)).toEqual(["a", "c"]);
    await settle();
    expect(T.fake.config!.removeSpeaker).toHaveBeenCalledWith(3, "b");
  });

  it("offers_a_10_second_Undo_toast_that_re_adds_and_moves_the_speaker_back", async () => {
    open();
    T.fake.config!.removeSpeaker!.mockResolvedValue(ack(7));
    T.fake.config!.addSpeaker!.mockResolvedValue(cfg().speakers);
    T.fake.config!.moveSpeaker!.mockResolvedValue(ack(8));
    T.C.removeSpeaker("b");
    await settle();
    expect(T.U.toasts.value).toHaveLength(1);
    const toast = T.U.toasts.value[0]!;
    expect(toast.timeoutMs).toBe(10_000);
    expect(toast.action?.label).toMatch(/undo/i);
    toast.action!.run();
    await settle();
    expect(T.fake.config!.addSpeaker).toHaveBeenCalledWith(3, "b");
    expect(T.fake.config!.moveSpeaker).toHaveBeenCalledWith(3, "b", 1);
    const order = [T.fake.config!.addSpeaker!.mock.invocationCallOrder[0]!, T.fake.config!.moveSpeaker!.mock.invocationCallOrder[0]!];
    expect(order[0]).toBeLessThan(order[1]!);
  });
});

describe("validateFilter", () => {
  it("validates_the_current_extra_filter_and_stores_the_result", async () => {
    open(cfg(3, { extraFilter: "labels = standup" }));
    const check = { ok: false, errors: ["Error in the JQL Query: unknown field."], checkedAt: 1_700_000_000 };
    T.fake.connection!.validateFilter!.mockResolvedValue(check);
    T.C.validateFilter();
    await settle();
    expect(T.fake.connection!.validateFilter).toHaveBeenCalledWith(3, "labels = standup");
    expect(T.C.config.value?.filterCheck).toEqual(check);
  });

  it("stores_an_ok_result_too", async () => {
    open(cfg(3, { extraFilter: "labels = standup" }));
    const check = { ok: true, errors: [], checkedAt: 1_700_000_000 };
    T.fake.connection!.validateFilter!.mockResolvedValue(check);
    T.C.validateFilter();
    await settle();
    expect(T.C.config.value?.filterCheck).toEqual(check);
  });
});

describe("standup lifecycle", () => {
  it("createStandup_replaces_the_config_opens_configure_on_the_draft_and_refreshes_the_tabs", async () => {
    const draft = cfg(11, { draft: true, board: null, space: null, name: "New standup" });
    const tabs = [tab(1), tab(11, { configured: false, spaceKey: "" })];
    T.fake.config!.create!.mockResolvedValue(draft);
    T.fake.standup!.list!.mockResolvedValue(tabs);
    await T.C.createStandup();
    expect(T.C.config.value).toEqual(draft);
    expect(T.R.route.value).toEqual({ view: "configure", standupId: 11 });
    expect(T.A.standups.value).toEqual(tabs);
  });

  it("duplicateStandup_replaces_the_config_with_the_copy_and_selects_it", async () => {
    const copy = cfg(12, { name: "Saffron Mobile (copy)" });
    T.fake.config!.duplicate!.mockResolvedValue(copy);
    T.fake.standup!.list!.mockResolvedValue([tab(3), tab(12)]);
    await T.C.duplicateStandup(3);
    expect(T.fake.config!.duplicate).toHaveBeenCalledWith(3);
    expect(T.C.config.value).toEqual(copy);
    expect(T.R.route.value).toEqual({ view: "configure", standupId: 12 });
    expect(T.A.standups.value.map((t) => t.id)).toEqual([3, 12]);
  });

  it("copySettingsFrom_copies_into_the_current_standup_and_replaces_the_config", async () => {
    open(cfg(3));
    const copied = cfg(3, { rev: 20, pulseDays: 14 });
    T.fake.config!.copySettingsFrom!.mockResolvedValue(copied);
    await T.C.copySettingsFrom(2);
    expect(T.fake.config!.copySettingsFrom).toHaveBeenCalledWith(3, 2);
    expect(T.C.config.value).toEqual(copied);
  });

  it("deleteStandup_archives_refreshes_the_tabs_and_offers_a_10_second_undo_naming_the_standup", async () => {
    T.A.standups.value = [tab(1), tab(3, { name: "Web & Checkout" })];
    T.fake.config!.delete!.mockResolvedValue(ack(2));
    T.fake.standup!.list!.mockResolvedValue([tab(1)]);
    await T.C.deleteStandup(3);
    expect(T.fake.config!.delete).toHaveBeenCalledWith(3);
    expect(T.A.standups.value.map((t) => t.id)).toEqual([1]);
    expect(T.U.toasts.value).toHaveLength(1);
    const toast = T.U.toasts.value[0]!;
    expect(toast.message).toContain("Web & Checkout");
    expect(toast.timeoutMs).toBe(10_000);
    expect(toast.action?.label).toMatch(/undo/i);
  });

  it("the_delete_undo_restores_the_standup_and_refreshes_the_tabs", async () => {
    T.A.standups.value = [tab(1), tab(3)];
    T.fake.config!.delete!.mockResolvedValue(ack(2));
    T.fake.config!.restore!.mockResolvedValue(ack(3));
    T.fake.standup!.list!.mockResolvedValueOnce([tab(1)]).mockResolvedValue([tab(1), tab(3)]);
    await T.C.deleteStandup(3);
    T.U.toasts.value[0]!.action!.run();
    await settle();
    expect(T.fake.config!.restore).toHaveBeenCalledWith(3);
    expect(T.A.standups.value.map((t) => t.id)).toEqual([1, 3]);
  });

  it("moveStandup_calls_ConfigService_Move_with_the_target_position", async () => {
    T.fake.config!.move!.mockResolvedValue(ack(4));
    await T.C.moveStandup(3, 1);
    await settle();
    expect(T.fake.config!.move).toHaveBeenCalledWith(3, 1);
  });
});

describe("leaveConfigure", () => {
  /** Tabs as the server would report them after the saves. */
  function tabsFor(...tabs: StandupTab[]) {
    T.A.standups.value = tabs;
    T.fake.standup!.list!.mockResolvedValue(tabs);
  }
  /** Standup 1, person "amara", open in Configure. */
  function inConfigure() {
    T.R.goStandup(1, "amara");
    T.R.goConfigure(null);
    open(cfg(1));
  }

  it("flushes_pending_text_saves_before_starting_syncs", async () => {
    tabsFor(tab(1, { needsSync: true }));
    inConfigure();
    T.C.saveField("name", "Renamed", { text: true });
    await T.C.leaveConfigure(); // no timers advanced: the 400 ms debounce must be flushed, not awaited
    expect(T.fake.config!.update).toHaveBeenCalledWith(1, { name: "Renamed" });
    expect(T.fake.sync!.start).toHaveBeenCalledWith(1, "full");
    expect(T.fake.config!.update!.mock.invocationCallOrder[0]!).toBeLessThan(T.fake.sync!.start!.mock.invocationCallOrder[0]!);
  });

  it("starts_a_full_sync_for_every_configured_standup_that_needs_one_and_only_those", async () => {
    tabsFor(tab(1, { needsSync: true }), tab(2), tab(4, { needsSync: true }));
    inConfigure();
    T.C.config.value = cfg(1, { needsSync: true });
    await T.C.leaveConfigure();
    expect(T.fake.sync!.start!.mock.calls).toEqual([
      [1, "full"],
      [4, "full"],
    ]);
  });

  it("discards_drafts_with_no_space_but_keeps_a_draft_that_has_one", async () => {
    tabsFor(tab(1), tab(9, { configured: false, spaceKey: "" }), tab(10, { configured: false, spaceKey: "ABC" }));
    inConfigure();
    await T.C.leaveConfigure();
    expect(T.fake.config!.discardDraft).toHaveBeenCalledWith(9);
    expect(T.fake.config!.discardDraft).not.toHaveBeenCalledWith(10);
    expect(T.fake.config!.discardDraft).not.toHaveBeenCalledWith(1);
  });

  it("never_starts_a_sync_for_a_draft", async () => {
    tabsFor(tab(1), tab(9, { configured: false, spaceKey: "", needsSync: true }));
    inConfigure();
    await T.C.leaveConfigure();
    expect(T.fake.sync!.start).not.toHaveBeenCalled();
  });

  it("makes_no_sync_call_when_nothing_needs_one", async () => {
    tabsFor(tab(1), tab(2));
    inConfigure();
    await T.C.leaveConfigure();
    expect(T.fake.sync!.start).not.toHaveBeenCalled();
  });

  it("returns_to_the_standup_and_person_that_was_open_before_configure", async () => {
    tabsFor(tab(1), tab(2));
    inConfigure();
    expect(T.R.route.value).toEqual({ view: "configure", standupId: 1 });
    await T.C.leaveConfigure();
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 1, person: "amara" });
  });

  it("still_returns_when_a_sync_cannot_be_started", async () => {
    tabsFor(tab(1, { needsSync: true }));
    inConfigure();
    T.fake.sync!.start!.mockRejectedValue(new T.AppError("offline", "Offline.", "", true));
    await expect(T.C.leaveConfigure()).resolves.toBeUndefined();
    expect(T.R.route.value.view).toBe("standup");
  });

  it("shows_a_not_saved_toast_with_Retry_for_failed_saves_and_still_returns", async () => {
    tabsFor(tab(1));
    inConfigure();
    T.fake.config!.update!.mockRejectedValueOnce(new T.AppError("source_http", "Jira said no.", "", false)).mockResolvedValue(ack(7));
    T.C.saveField("scope", "board");
    await T.C.leaveConfigure();
    expect(T.R.route.value.view).toBe("standup");
    const toast = T.U.toasts.value.find((t) => /1 change not saved/i.test(t.message));
    expect(toast).toBeDefined();
    expect(toast!.action?.label).toMatch(/retry/i);
    toast!.action!.run();
    await settle();
    expect(T.fake.config!.update).toHaveBeenCalledTimes(2);
    expect(T.Sv.failedKeys.value.size).toBe(0);
  });

  it("shows_no_not_saved_toast_when_everything_saved", async () => {
    tabsFor(tab(1));
    inConfigure();
    T.C.saveField("pulseDays", 14);
    await T.C.leaveConfigure();
    expect(T.U.toasts.value.some((t) => /not saved/i.test(t.message))).toBe(false);
  });
});
