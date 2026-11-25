// WP-15 tests: global keyboard (keys.ts; ux-spec §8, testing §4 "Global keys are ignored while focus is in inputs or
// comboboxes").
//
// SPEC-NOTE: key mapping asserted (event.key based, route = standup unless stated): ArrowUp/ArrowDown =>
//   prevSpeaker/nextSpeaker; ArrowLeft/ArrowRight => previous/next standup tab (wraps); "b" or "B" => toggle whole board / back; "1".."9" => switchByNumber(n); "r" => startSync(current,
//   "incremental"); "R" with shiftKey => startSync(current, "full"); "," => open Configure on the current standup;
//   "?" => keyLegendOpen = true (a second press is not asserted); "/" => switcherOpen = true;
//   Escape => closes keyLegendOpen / switcherOpen (handled, true); with nothing open on the standup route it is NOT
//   handled (false); in Configure with nothing open it behaves like Done (leaveConfigure).
// SPEC-NOTE: keys combined with Ctrl, Meta or Alt are never handled (Cmd+R must not resync, Cmd+, is the OS's, and
//   Ctrl/Cmd+Z undo is handled elsewhere). Shift is allowed ("?" needs it, Shift+R means full).
// SPEC-NOTE: speaker/board/number/resync keys act only on the standup route: on configure/connect, ArrowLeft,
//   ArrowRight and "b" are not handled (return false) and change nothing. (The standup screen stays mounted under
//   Configure, so this gate is what keeps ← → from moving a hidden selection.)
// SPEC-NOTE: Escape from inside an editable target is ignored by the GLOBAL handler too (a Combobox restores the
//   previous value on Escape; Configure's Esc must not also leave the screen).
// SPEC-NOTE: isEditableTarget checks the `contenteditable` ATTRIBUTE ("", "true", "plaintext-only" are editable;
//   "false" is not) and ancestors via closest(), because jsdom has no isContentEditable.
// SPEC-NOTE: digit keys beyond the configured standups change nothing; their return value is not asserted.
// SPEC-NOTE: modules are re-imported fresh per test; the keydown listener under test is installed via
//   handleGlobalKey called from a test-owned listener (so the return value is observable) or via installGlobalKeys.
// SPEC-NOTE: the fake Api's standup.view answers a minimal view for any id, and standup.list answers the same three
//   tabs the `standups` signal holds, so Escape-as-Done (leaveConfigure re-reading the list) lands on the standup
//   that was open before Configure.
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Api, SpeakerView, StandupTab, StandupView } from "./api/index";

type Fake = Record<string, Record<string, Mock>>;
const DEFAULTS: Record<string, unknown> = { "standup.list": [], "sync.status": [], "sync.start": 7 };

async function boot() {
  vi.resetModules();
  const idx = await import("./api/index");
  const events = await import("./api/events");
  const fake: Fake = {};
  for (const [svc, methods] of Object.entries(idx.wailsApi)) {
    const f: Record<string, Mock> = (fake[svc] = {});
    for (const m of Object.keys(methods)) f[m] = vi.fn(async () => structuredClone(DEFAULTS[`${svc}.${m}`]));
  }
  // A realistic default: StandupService.View always answers with a minimal view of the requested standup, so code that
  // reads the result of an un-mocked call does not throw. Tests override it where the content matters.
  fake["standup"]!["view"]!.mockImplementation(async (id: number) => ({ standupId: id, rev: 1, name: `S${id}`, speakers: [], tickets: [], lanes: [], axis: [], backlog: [], warnings: [] }));
  idx.setApi(fake as unknown as Api);
  events.setEventBus(events.createMemoryBus());
  return {
    fake,
    K: await import("./keys"),
    A: await import("./state/app"),
    R: await import("./state/route"),
    St: await import("./state/standup"),
    U: await import("./state/ui"),
  };
}
function callsOtherThan(fake: Fake, allowed: string[] = []): string[] {
  const out: string[] = [];
  for (const [svc, ms] of Object.entries(fake)) for (const [m, fn] of Object.entries(ms)) if (fn.mock.calls.length && !allowed.includes(`${svc}.${m}`)) out.push(`${svc}.${m}`);
  return out;
}

function sp(id: string, over: Partial<SpeakerView> = {}): SpeakerView {
  return { id, name: id.toUpperCase(), first: id, initials: id.slice(0, 2).toUpperCase(), unassigned: false, included: true, active: 0, done: 0, ...over };
}
function view(standupId: number, speakers: SpeakerView[]): StandupView {
  return { standupId, rev: 1, name: `S${standupId}`, speakers, tickets: [], lanes: [], axis: [], backlog: [], warnings: [] } as unknown as StandupView;
}
function tab(id: number, over: Partial<StandupTab> = {}): StandupTab {
  return { id, rev: 1, name: `S${id}`, spaceKey: `K${id}`, position: id, activeCount: 0, speakerCount: 0, configured: true, needsSync: false, lastSyncedAt: 0, ...over };
}

let T: Awaited<ReturnType<typeof boot>>;
let uninstall: (() => void) | undefined;

beforeEach(async () => {
  vi.useFakeTimers();
  T = await boot();
  T.A.standups.value = [tab(1), tab(2), tab(3)];
  T.fake.standup!.list!.mockResolvedValue([tab(1), tab(2), tab(3)]); // what the server reports after saves
  T.St.views.value = new Map([1, 2, 3].map((id) => [id, view(id, [sp("a"), sp("b")])]));
  T.R.route.value = { view: "standup", standupId: 1, person: null };
  document.body.innerHTML = "";
});
afterEach(() => {
  uninstall?.();
  uninstall = undefined;
  vi.useRealTimers();
  document.body.innerHTML = "";
});

const routeNow = () => T.R.route.value;
const settle = () => vi.advanceTimersByTimeAsync(0);

/** Dispatches a real keydown on `target` (so event.target is set) and returns handleGlobalKey's verdict. */
function press(key: string, o: { target?: Element; shift?: boolean; ctrl?: boolean; meta?: boolean; alt?: boolean } = {}) {
  const el = o.target ?? document.body;
  let handled: boolean | undefined;
  let thrown: unknown;
  const listener = (e: Event) => {
    try {
      handled = T.K.handleGlobalKey(e as KeyboardEvent);
    } catch (err) {
      thrown = err;
    }
  };
  document.addEventListener("keydown", listener);
  try {
    el.dispatchEvent(new KeyboardEvent("keydown", { key, shiftKey: !!o.shift, ctrlKey: !!o.ctrl, metaKey: !!o.meta, altKey: !!o.alt, bubbles: true, cancelable: true }));
  } finally {
    document.removeEventListener("keydown", listener);
  }
  if (thrown) throw thrown;
  return handled;
}
function el(html: string): HTMLElement {
  const host = document.createElement("div");
  host.innerHTML = html;
  document.body.appendChild(host);
  return host.firstElementChild as HTMLElement;
}

describe("isEditableTarget", () => {
  it.each([
    ["an input", `<input type="text">`],
    ["a textarea", `<textarea></textarea>`],
    ["a select", `<select><option>a</option></select>`],
    ["a contenteditable=true div", `<div contenteditable="true">x</div>`],
    ["a contenteditable='' div", `<div contenteditable="">x</div>`],
    ["a contenteditable=plaintext-only div", `<div contenteditable="plaintext-only">x</div>`],
  ])("is_true_for_%s", (_n, html) => {
    expect(T.K.isEditableTarget(el(html))).toBe(true);
  });

  it("is_true_for_a_descendant_of_a_contenteditable_region", () => {
    const host = el(`<div contenteditable="true"><p><b id="deep">x</b></p></div>`);
    expect(T.K.isEditableTarget(host.querySelector("#deep"))).toBe(true);
  });

  it("is_true_for_a_role_combobox_element_and_everything_inside_it", () => {
    const box = el(`<div role="combobox"><span id="chip"><i id="icon"></i></span></div>`);
    expect(T.K.isEditableTarget(box)).toBe(true);
    expect(T.K.isEditableTarget(box.querySelector("#chip"))).toBe(true);
    expect(T.K.isEditableTarget(box.querySelector("#icon"))).toBe(true);
  });

  it.each([
    ["a plain div", `<div>x</div>`],
    ["a button", `<button>Go</button>`],
    ["a link", `<a href="#">x</a>`],
    ["contenteditable=false", `<div contenteditable="false">x</div>`],
    ["a listbox that is not a combobox", `<ul role="listbox"><li>x</li></ul>`],
  ])("is_false_for_%s", (_n, html) => {
    expect(T.K.isEditableTarget(el(html))).toBe(false);
  });

  it("is_false_for_null_and_for_non_element_targets", () => {
    expect(T.K.isEditableTarget(null)).toBe(false);
    expect(T.K.isEditableTarget(window)).toBe(false);
    expect(T.K.isEditableTarget(document)).toBe(false);
  });
});

describe("handleGlobalKey: speakers and board", () => {
  it("ArrowDown_selects_the_next_speaker", () => {
    expect(press("ArrowDown")).toBe(true);
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: "a" });
    expect(press("ArrowDown")).toBe(true);
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: "b" });
    press("ArrowDown");
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null }); // whole board after the last
  });

  it("ArrowUp_selects_the_previous_speaker_and_wraps_through_the_whole_board", () => {
    expect(press("ArrowUp")).toBe(true);
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: "b" });
    press("ArrowUp");
    press("ArrowUp");
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null });
  });

  it("ArrowRight_and_ArrowLeft_cycle_the_standup_tabs_and_wrap", async () => {
    expect(press("ArrowRight")).toBe(true);
    await settle();
    expect(routeNow()).toEqual({ view: "standup", standupId: 2, person: null });
    press("ArrowRight");
    press("ArrowRight");
    await settle();
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null }); // wrapped past the last
    expect(press("ArrowLeft")).toBe(true);
    await settle();
    expect(routeNow()).toEqual({ view: "standup", standupId: 3, person: null }); // wrapped before the first
  });

  it("ArrowRight_keeps_the_person_when_they_speak_in_the_next_standup", async () => {
    T.R.route.value = { view: "standup", standupId: 1, person: "a" };
    press("ArrowRight");
    await settle();
    expect(routeNow()).toEqual({ view: "standup", standupId: 2, person: "a" });
  });

  it("left_and_right_are_not_handled_with_a_single_standup", async () => {
    T.A.standups.value = [tab(1)];
    expect(press("ArrowRight")).toBe(false);
    expect(press("ArrowLeft")).toBe(false);
    await settle();
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null });
  });

  it.each(["b", "B"])("%s_shows_the_whole_board", (key) => {
    T.R.route.value = { view: "standup", standupId: 1, person: "a" };
    expect(press(key, { shift: key === "B" })).toBe(true);
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null });
  });

  it("b_a_second_time_returns_to_the_person_and_a_third_time_goes_back_to_the_board", () => {
    T.R.route.value = { view: "standup", standupId: 1, person: "b" };
    press("b");
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null });
    expect(press("b")).toBe(true);
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: "b" });
    press("b");
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null });
  });

  it("b_on_a_board_reached_another_way_stays_on_the_board_but_is_handled", () => {
    expect(press("b")).toBe(true);
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null });
  });

  it("speaker_keys_make_no_facade_call_before_the_500ms_persistence_debounce", async () => {
    press("ArrowDown");
    press("ArrowUp");
    press("b");
    await vi.advanceTimersByTimeAsync(499);
    expect(callsOtherThan(T.fake)).toEqual([]);
  });
});

describe("handleGlobalKey: G groups the whole board (F13)", () => {
  const group = () => T.St.boardGroupOf(1);

  it.each(["g", "G"])("%s_toggles_assignee_and_status_on_the_whole_board_and_persists_at_once", async (key) => {
    expect(group()).toBe("assignee");
    expect(press(key, { shift: key === "G" })).toBe(true);
    expect(group()).toBe("status");
    expect(T.fake.app!.setUIState).toHaveBeenCalledWith({ standupId: 1, boardGroup: "status" }); // no debounce
    expect(press(key, { shift: key === "G" })).toBe(true);
    expect(group()).toBe("assignee");
    expect(T.fake.app!.setUIState).toHaveBeenLastCalledWith({ standupId: 1, boardGroup: "assignee" });
  });

  it("does_nothing_on_a_person_view", () => {
    T.R.route.value = { view: "standup", standupId: 1, person: "b" };
    expect(press("g")).toBe(false);
    expect(group()).toBe("assignee");
    expect(T.fake.app!.setUIState).not.toHaveBeenCalled();
  });

  it("does_nothing_in_configure", () => {
    T.R.route.value = { view: "configure", standupId: 1 };
    expect(press("g")).toBe(false);
    expect(group()).toBe("assignee");
  });

  it("is_ignored_with_a_modifier", () => {
    expect(press("g", { ctrl: true })).toBe(false);
    expect(press("g", { meta: true })).toBe(false);
    expect(group()).toBe("assignee");
  });

  it("each_standup_keeps_its_own_grouping", () => {
    press("g");
    T.R.route.value = { view: "standup", standupId: 2, person: null };
    expect(group()).toBe("status"); // standup 1 unchanged
    expect(T.St.boardGroupOf(2)).toBe("assignee");
    press("g");
    expect(T.St.boardGroupOf(2)).toBe("status");
    T.R.route.value = { view: "standup", standupId: 1, person: null };
    press("g");
    expect(T.St.boardGroupOf(1)).toBe("assignee");
    expect(T.St.boardGroupOf(2)).toBe("status");
  });

  it("arrow_keys_and_G_still_work_with_the_board_switch_focused (a toggle group, not a radiogroup)", () => {
    const btn = el(`<div role="group"><button aria-pressed="true" id="r"></button></div>`).querySelector("#r")!;
    expect(press("ArrowDown", { target: btn })).toBe(true);
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: "a" });
    T.R.route.value = { view: "standup", standupId: 1, person: null };
    expect(press("g", { target: btn })).toBe(true);
    expect(T.St.boardGroupOf(1)).toBe("status");
  });
});

describe("handleGlobalKey: standup switching", () => {
  it.each([
    ["2", 2],
    ["3", 3],
    ["1", 1],
  ])("digit_%s_switches_to_standup_%i_by_position", async (key, id) => {
    T.R.route.value = { view: "standup", standupId: 2, person: null };
    expect(press(key)).toBe(true);
    await settle();
    expect(routeNow()).toMatchObject({ view: "standup", standupId: id });
  });

  it("a_digit_beyond_the_configured_standups_changes_nothing", async () => {
    press("9");
    press("4");
    press("0");
    await settle();
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null });
    expect(callsOtherThan(T.fake)).toEqual([]);
  });

  it("numbers_skip_drafts", async () => {
    T.A.standups.value = [tab(7, { position: 0, configured: false, spaceKey: "" }), tab(1, { position: 1 }), tab(2, { position: 2 })];
    press("2");
    await settle();
    expect(routeNow()).toMatchObject({ view: "standup", standupId: 2 });
  });
});

describe("handleGlobalKey: resync, configure, legend, switcher", () => {
  it("r_starts_an_incremental_sync_of_the_current_standup", async () => {
    T.R.route.value = { view: "standup", standupId: 2, person: null };
    expect(press("r")).toBe(true);
    await settle();
    expect(T.fake.sync!.start).toHaveBeenCalledTimes(1);
    expect(T.fake.sync!.start).toHaveBeenCalledWith(2, "incremental");
  });

  it("shift_R_starts_a_full_sync", async () => {
    expect(press("R", { shift: true })).toBe(true);
    await settle();
    expect(T.fake.sync!.start).toHaveBeenCalledWith(1, "full");
  });

  it("comma_opens_configure_on_the_current_standup", () => {
    T.R.route.value = { view: "standup", standupId: 2, person: "a" };
    expect(press(",")).toBe(true);
    expect(routeNow()).toEqual({ view: "configure", standupId: 2 });
  });

  it("question_mark_opens_the_key_legend", () => {
    expect(T.U.keyLegendOpen.value).toBe(false);
    expect(press("?", { shift: true })).toBe(true);
    expect(T.U.keyLegendOpen.value).toBe(true);
  });

  it("slash_focuses_the_standup_switcher", () => {
    expect(T.U.switcherOpen.value).toBe(false);
    expect(press("/")).toBe(true);
    expect(T.U.switcherOpen.value).toBe(true);
  });
});

describe("handleGlobalKey: Escape", () => {
  it("closes_the_key_legend", () => {
    T.U.keyLegendOpen.value = true;
    expect(press("Escape")).toBe(true);
    expect(T.U.keyLegendOpen.value).toBe(false);
  });

  it("closes_the_standup_switcher", () => {
    T.U.switcherOpen.value = true;
    expect(press("Escape")).toBe(true);
    expect(T.U.switcherOpen.value).toBe(false);
  });

  it("is_not_handled_on_the_standup_screen_when_nothing_is_open", () => {
    expect(press("Escape")).toBe(false);
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null });
  });

  it("in_configure_with_nothing_open_behaves_like_Done", async () => {
    T.R.route.value = { view: "standup", standupId: 2, person: "a" };
    T.R.goConfigure(null);
    expect(routeNow()).toEqual({ view: "configure", standupId: 2 });
    expect(press("Escape")).toBe(true);
    await settle();
    expect(routeNow()).toMatchObject({ view: "standup", standupId: 2 });
  });

  it("in_configure_with_a_popover_open_only_closes_the_popover", async () => {
    T.R.goConfigure(1);
    T.U.keyLegendOpen.value = true;
    expect(press("Escape")).toBe(true);
    await settle();
    expect(T.U.keyLegendOpen.value).toBe(false);
    expect(routeNow()).toEqual({ view: "configure", standupId: 1 });
  });
});

describe("handleGlobalKey: ignored cases", () => {
  const KEYS: [string, boolean][] = [
    ["ArrowRight", false],
    ["ArrowLeft", false],
    ["ArrowUp", false],
    ["ArrowDown", false],
    ["b", false],
    ["B", true],
    ["g", false],
    ["G", true],
    ["1", false],
    ["2", false],
    ["r", false],
    ["R", true],
    [",", false],
    ["/", false],
    ["?", true],
  ];

  it.each([
    ["an input", `<input type="text">`],
    ["a textarea", `<textarea></textarea>`],
    ["a contenteditable region", `<div contenteditable="true">x</div>`],
    ["a combobox", `<div role="combobox"><span id="inner" tabindex="0">x</span></div>`],
  ])("ignores_every_global_key_typed_in_%s", async (_n, html) => {
    const host = el(html);
    const target = host.querySelector("#inner") ?? host;
    for (const [key, shift] of KEYS) {
      expect(press(key, { target, shift }), key).toBe(false);
    }
    await vi.advanceTimersByTimeAsync(2_000);
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null });
    expect(T.U.keyLegendOpen.value).toBe(false);
    expect(T.U.switcherOpen.value).toBe(false);
    expect(callsOtherThan(T.fake)).toEqual([]);
  });

  it("ignores_Escape_typed_in_an_input_even_in_configure", async () => {
    T.R.goConfigure(1);
    const input = el(`<input type="text">`);
    expect(press("Escape", { target: input })).toBe(false);
    await settle();
    expect(routeNow()).toEqual({ view: "configure", standupId: 1 });
  });

  it("ignores_Escape_typed_in_a_combobox_while_its_popover_is_open", () => {
    T.U.keyLegendOpen.value = true;
    const box = el(`<div role="combobox"><input id="q"></div>`);
    expect(press("Escape", { target: box.querySelector("#q")! })).toBe(false);
    expect(T.U.keyLegendOpen.value).toBe(true);
  });

  it.each([
    ["ctrl", { ctrl: true }],
    ["meta", { meta: true }],
    ["alt", { alt: true }],
  ])("does_not_handle_keys_combined_with_%s", async (_n, mod) => {
    for (const key of ["r", ",", "1", "b", "ArrowRight", "ArrowDown", "/"]) expect(press(key, mod), key).toBe(false);
    await settle();
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null });
    expect(callsOtherThan(T.fake)).toEqual([]);
  });

  it.each(["x", "Enter", "Tab", "a", "F5", " "])("does_not_handle_%j", (key) => {
    expect(press(key)).toBe(false);
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null });
  });

  it.each([
    ["configure", { view: "configure", standupId: 1 } as const],
    ["connect", { view: "connect" } as const],
  ])("does_not_move_speakers_or_the_board_on_the_%s_route", (_n, route) => {
    T.R.route.value = route;
    for (const k of ["ArrowRight", "ArrowLeft", "ArrowUp", "ArrowDown", "b"]) expect(press(k), k).toBe(false);
    expect(T.R.route.value).toEqual(route);
  });
});

describe("installGlobalKeys", () => {
  it("registers_one_keydown_listener_on_the_target_and_removes_that_same_listener", () => {
    const add = vi.fn();
    const remove = vi.fn();
    uninstall = T.K.installGlobalKeys({ addEventListener: add, removeEventListener: remove } as never);
    expect(add).toHaveBeenCalledTimes(1);
    expect(add.mock.calls[0]![0]).toBe("keydown");
    expect(remove).not.toHaveBeenCalled();
    uninstall();
    uninstall = undefined;
    expect(remove).toHaveBeenCalledTimes(1);
    expect(remove.mock.calls[0]![0]).toBe("keydown");
    expect(remove.mock.calls[0]![1]).toBe(add.mock.calls[0]![1]);
  });

  it("the_listener_calls_preventDefault_only_for_handled_keys", () => {
    const add = vi.fn();
    uninstall = T.K.installGlobalKeys({ addEventListener: add, removeEventListener: vi.fn() } as never);
    const listener = add.mock.calls[0]![1] as (e: KeyboardEvent) => void;
    const handled = new KeyboardEvent("keydown", { key: "b", cancelable: true });
    listener(handled);
    expect(handled.defaultPrevented).toBe(true);
    const ignored = new KeyboardEvent("keydown", { key: "x", cancelable: true });
    listener(ignored);
    expect(ignored.defaultPrevented).toBe(false);
  });

  it("defaults_to_window_so_a_keydown_anywhere_in_the_document_is_handled_until_uninstalled", () => {
    uninstall = T.K.installGlobalKeys();
    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true }));
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: "a" });
    uninstall();
    uninstall = undefined;
    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true }));
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: "a" });
  });

  it("does_not_handle_keys_typed_into_an_input_when_installed_on_window", () => {
    uninstall = T.K.installGlobalKeys();
    const input = el(`<input type="text">`);
    const ev = new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true });
    input.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(false);
    expect(routeNow()).toEqual({ view: "standup", standupId: 1, person: null });
  });
});
