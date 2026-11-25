/* eslint-disable @typescript-eslint/no-explicit-any */
// Configure > Source section (ux-spec §9.4 Source, §9.5; architecture §5.3).
//
// SPEC-NOTE (approach): section tests isolate the section from the state layer and the shared components.
//   - State functions are replaced with vi.fn via vi.mock("state/config") (real signals kept). Sections persist ONLY
//     through state/config.ts (props.ts: "mutations go through state/config.ts"), so the assertion is the call itself.
//     Text debounce lives in save.ts: text fields are asserted as saveField(field, value, { text: true }).
//   - Shared components (Combobox/Segmented/TextField) are replaced by thin fakes that record their props by `label`
//     (registry `reg`). Control labels are matched case-insensitively against the ux-spec §9.4 field names (/name/i,
//     /space/i, /board/i, /scope/i, /filter/i, /start from/i). Their real DOM is specified by src/components/*.test.tsx.
//   - Only calls with no state wrapper (connection.searchSpaces/searchBoards) use a fake Api installed with setApi.
// SPEC-NOTE: Space and Board comboboxes use `load` (async) and call the facade with the raw query:
//   connection.searchSpaces(query) and connection.searchBoards(config.space.key, query). onSelect hands the option to
//   state setSpace(option) / setBoard(option). Board is `disabled` with placeholder "Pick a space first" until a
//   space exists.
// SPEC-NOTE: Scope option values are "sprint" | "board". On a Kanban board "sprint" is disabled, a hint mentioning
//   "Kanban" is shown, and the displayed value is forced to "board" even if config.scope still says "sprint".
// SPEC-NOTE: Extra filter errors come from config.filterCheck (the server-shaped result of validateFilter()):
//   TextField.error contains every message of filterCheck.errors when !ok, and is empty when ok or never checked.
//   validateFilter() is called from the TextField's onCommit (blur / Enter), not on every keystroke.
// SPEC-NOTE: "Start from" is shown only while config.draft; its options are static: one "Defaults" option plus one
//   "Copy ‹standup›" option per OTHER standup in state/app `standups` (option label contains the standup name; the
//   current standup is excluded). Choosing a copy option calls copySettingsFrom(thatStandupId); Defaults calls nothing.


import { act, render, screen } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../../api";
import type { BoardOption, SpaceOption, StandupConfig, StandupTab } from "../../../api/types";
import { standups } from "../../../state/app";
import { SourceSection } from "./Source";

const reg = vi.hoisted(() => {
  type P = any;
  const m = new Map<string, Map<string, P>>();
  const keys = (kind: string) => [...(m.get(kind)?.keys() ?? [])];
  return {
    rec(kind: string, p: P) {
      let k = m.get(kind);
      if (!k) m.set(kind, (k = new Map()));
      k.set(String(p.label), p);
    },
    clear() { m.clear(); },
    labels: keys,
    has(kind: string, re: RegExp): boolean { return keys(kind).some((l) => re.test(l)); },
    find(kind: string, re: RegExp): P {
      const hit = [...(m.get(kind)?.entries() ?? [])].find(([l]) => re.test(l));
      if (!hit) throw new Error(`no ${kind} labelled ${re} (have: ${keys(kind).join(" | ") || "none"})`);
      return hit[1];
    },
  };
});

const st = vi.hoisted(() => ({
  loadConfig: vi.fn(async () => {}), saveField: vi.fn(), setSpace: vi.fn(), setBoard: vi.fn(), setColumnLane: vi.fn(),
  setWalkOrder: vi.fn(), setLaneLimit: vi.fn(), setSpeakerIncluded: vi.fn(), moveSpeaker: vi.fn(), sortSpeakers: vi.fn(),
  addSpeaker: vi.fn(), removeSpeaker: vi.fn(), validateFilter: vi.fn(), createStandup: vi.fn(async () => {}), duplicateStandup: vi.fn(async () => {}),
  copySettingsFrom: vi.fn(async () => {}), deleteStandup: vi.fn(async () => {}), moveStandup: vi.fn(async () => {}), leaveConfigure: vi.fn(async () => {}),
}));
vi.mock("../../../state/config", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../state/config")>()),
  ...st,
}));

vi.mock("../../../components/Combobox", async () => {
  const { h } = await import("preact");
  return {
    Combobox: (p: any) => {
      reg.rec("Combobox", p);
      return h("input", { role: "combobox", "aria-label": p.label, disabled: p.disabled, placeholder: p.placeholder, value: p.value ? p.getLabel(p.value) : "", readOnly: true });
    },
  };
});

vi.mock("../../../components/Segmented", async () => {
  const { h } = await import("preact");
  return {
    Segmented: (p: any) => {
      reg.rec("Segmented", p);
      return h("div", { role: "radiogroup", "aria-label": p.label }, p.options.map((o: any) =>
        h("button", { type: "button", role: "radio", "aria-checked": String(o.value === p.value), disabled: !!o.disabled, onClick: () => p.onChange(o.value) }, o.label)));
    },
  };
});

vi.mock("../../../components/TextField", async () => {
  const { h } = await import("preact");
  return {
    TextField: (p: any) => {
      reg.rec("TextField", p);
      return h("div", {},
        h("input", { type: p.type ?? "text", "aria-label": p.label, value: p.value, placeholder: p.placeholder, "aria-invalid": p.error ? "true" : undefined, readOnly: true }),
        p.error ? h("span", { role: "alert" }, p.error) : null);
    },
  };
});

function mkConfig(over: Partial<StandupConfig> = {}): StandupConfig {
  return {
    id: 1, rev: 3, position: 0, name: "Saffron Mobile",
    space: { id: "10001", key: "MOB", name: "Saffron Mobile" },
    board: { id: "42", name: "MOB board", type: "scrum" },
    scope: "sprint", extraFilter: "", filterCheck: null,
    columns: [
      { columnName: "Icebox", statusNames: ["Open"], lane: "backlog", auto: true, order: 0 },
      { columnName: "Ready", statusNames: ["Selected for Development"], lane: "todo", auto: true, order: 0 },
      { columnName: "Doing", statusNames: ["In Progress"], lane: "prog", auto: true, order: 0 },
      { columnName: "Code Review", statusNames: ["In Review", "Peer Review"], lane: "review", auto: false, order: 0 },
      { columnName: "Verify", statusNames: ["Testing"], lane: "qa", auto: true, order: 0 },
      { columnName: "Closed", statusNames: ["Done"], lane: "done", auto: true, order: 0 },
    ],
    lanes: [
      { lane: "qa", label: "QA", defaultLabel: "QA", kind: "working", custom: false, hue: 0, limitDays: 3, columns: ["Verify"] },
      { lane: "review", label: "Review", defaultLabel: "In Review", kind: "working", custom: false, hue: 0, limitDays: 2, columns: ["Code Review"] },
      { lane: "prog", label: "In progress", defaultLabel: "In Progress", kind: "working", custom: false, hue: 0, limitDays: 5, columns: ["Doing"] },
      { lane: "todo", label: "To do", defaultLabel: "To Do", kind: "working", custom: false, hue: 0, limitDays: 0, columns: ["Ready"] },
      { lane: "done", label: "Done", defaultLabel: "Done", kind: "done", custom: false, hue: 0, limitDays: 0, columns: ["Closed"] },
    ],
    laneOptions: [], walkLeftToRight: [],
    unmappedStatuses: [],
    rowsMode: "fit",
    speakers: [
      { accountId: "u-dana", name: "Dana Whitaker", initials: "DW", position: 0, included: true, manual: false, active: 3, note: "" },
      { accountId: "u-iris", name: "Iris Calloway", initials: "IC", position: 1, included: true, manual: false, active: 2, note: "" },
      { accountId: "u-amara", name: "Amara Osei", initials: "AO", position: 2, included: false, manual: false, active: 0, note: "no tickets right now" },
      { accountId: "u-lee", name: "Lee Tran", initials: "LT", position: 3, included: true, manual: true, active: 1, note: "" },
    ],
    unassignedPosition: "last", reshuffleDaily: false,
    pulseDays: 14, showCommits: true, showPRs: true, showComments: false, shadeWeekends: true,
    doneWindow: "sprint", backlogRows: 8, needsSync: false, draft: false,
    ...over,
  };
}
const kanban = { id: "77", name: "Ops board", type: "kanban" };
function installFakeApi(over: Record<string, (...a: any[]) => any> = {}) {
  const calls: string[] = [];
  const fake = new Proxy({}, {
    get: (_t, svc: string) => new Proxy({}, {
      get: (_s, m: string) => (...a: unknown[]) => {
        const key = `${svc}.${m}`;
        calls.push(key);
        const f = over[key];
        return f ? f(...a) : Promise.reject(new Error(`unstubbed ${key}`));
      },
    }),
  }) as unknown as Api;
  setApi(fake);
  return { calls, over };
}

function tab(id: number, name: string, over: Partial<StandupTab> = {}): StandupTab {
  return { id, rev: 1, name, spaceKey: "MOB", position: id - 1, activeCount: 4, speakerCount: 3, configured: true, needsSync: false, lastSyncedAt: 0, ...over };
}

const spaces: SpaceOption[] = [{ id: "10001", key: "MOB", name: "Saffron Mobile" }, { id: "10002", key: "PAY", name: "Payments" }];
const boards: BoardOption[] = [{ id: "42", name: "MOB board", type: "scrum" }, kanban];
const signal = () => new AbortController().signal;

beforeEach(() => {
  reg.clear();
  Object.values(st).forEach((f) => f.mockClear());
  standups.value = [];
});

describe("SourceSection: name", () => {
  it("shows the standup name in a text field", () => {
    render(<SourceSection config={mkConfig({ name: "Saffron Mobile" })} columns={3} />);
    expect(reg.find("TextField", /name/i).value).toBe("Saffron Mobile");
  });

  it("autosaves the name with the text debounce flag on every keystroke", () => {
    render(<SourceSection config={mkConfig()} columns={3} />);
    act(() => reg.find("TextField", /name/i).onInput("Service Platf"));
    act(() => reg.find("TextField", /name/i).onInput("Saffron Mobile 2"));
    expect(st.saveField).toHaveBeenNthCalledWith(1, "name", "Service Platf", { text: true });
    expect(st.saveField).toHaveBeenNthCalledWith(2, "name", "Saffron Mobile 2", { text: true });
  });

  it("has no Save button", () => {
    render(<SourceSection config={mkConfig()} columns={3} />);
    expect(reg.has("TextField", /name/i)).toBe(true);
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
  });
});

describe("SourceSection: space and board", () => {
  it("renders Space as an async combobox holding the current space", async () => {
    const { over } = installFakeApi({ "connection.searchSpaces": vi.fn(async () => spaces) });
    render(<SourceSection config={mkConfig()} columns={3} />);
    const space = reg.find("Combobox", /space/i);
    expect(space.getKey(space.value)).toBe("10001");
    expect(space.load).toBeTypeOf("function");
    await expect(space.load("pay", signal())).resolves.toEqual(spaces);
    expect(over["connection.searchSpaces"]).toHaveBeenCalledWith("pay");
  });

  it("selecting a space calls setSpace with the option and nothing else", () => {
    installFakeApi({ "connection.searchSpaces": vi.fn(async () => spaces) });
    render(<SourceSection config={mkConfig()} columns={3} />);
    act(() => reg.find("Combobox", /space/i).onSelect(spaces[1]));
    expect(st.setSpace).toHaveBeenCalledTimes(1);
    expect(st.setSpace).toHaveBeenCalledWith(spaces[1]);
    expect(st.saveField).not.toHaveBeenCalled();
  });

  it("loads boards for the chosen space key", async () => {
    const { over } = installFakeApi({ "connection.searchBoards": vi.fn(async () => boards) });
    render(<SourceSection config={mkConfig()} columns={3} />);
    const board = reg.find("Combobox", /board/i);
    expect(board.disabled).toBeFalsy();
    expect(board.getKey(board.value)).toBe("42");
    await expect(board.load("plat", signal())).resolves.toEqual(boards);
    expect(over["connection.searchBoards"]).toHaveBeenCalledWith("MOB", "plat");
  });

  it("selecting a board calls setBoard with the option", () => {
    installFakeApi({ "connection.searchBoards": vi.fn(async () => boards) });
    render(<SourceSection config={mkConfig()} columns={3} />);
    act(() => reg.find("Combobox", /board/i).onSelect(boards[1]));
    expect(st.setBoard).toHaveBeenCalledTimes(1);
    expect(st.setBoard).toHaveBeenCalledWith(boards[1]);
  });

  it("disables Board until a space is chosen and says 'Pick a space first'", () => {
    installFakeApi();
    render(<SourceSection config={mkConfig({ space: null, board: null, draft: true })} columns={3} />);
    const board = reg.find("Combobox", /board/i);
    expect(board.disabled).toBe(true);
    expect(board.value).toBeNull();
    expect(board.placeholder).toBe("Pick a space first");
    expect(reg.find("Combobox", /space/i).value).toBeNull();
  });

  it("makes no network call just by rendering", () => {
    const { calls } = installFakeApi();
    render(<SourceSection config={mkConfig()} columns={3} />);
    expect(reg.has("Combobox", /space/i)).toBe(true);
    expect(calls).toEqual([]);
  });
});

describe("SourceSection: scope", () => {
  it("offers Active sprint / Whole board with the stored value", () => {
    render(<SourceSection config={mkConfig({ scope: "board" })} columns={3} />);
    const seg = reg.find("Segmented", /scope/i);
    expect(seg.value).toBe("board");
    expect(seg.options.map((o: any) => o.value)).toEqual(["sprint", "board"]);
    expect(seg.options.map((o: any) => o.label)).toEqual(["Active sprint", "Whole board"]);
    expect(seg.options.every((o: any) => !o.disabled)).toBe(true);
    expect(screen.queryByText(/kanban/i)).toBeNull();
  });

  it("autosaves a scope change immediately", () => {
    render(<SourceSection config={mkConfig({ scope: "sprint" })} columns={3} />);
    act(() => reg.find("Segmented", /scope/i).onChange("board"));
    expect(st.saveField).toHaveBeenCalledTimes(1);
    expect(st.saveField).toHaveBeenCalledWith("scope", "board");
  });

  it("disables Active sprint on a Kanban board, shows a hint, and forces the value to board", () => {
    render(<SourceSection config={mkConfig({ board: kanban, scope: "sprint" })} columns={3} />);
    const seg = reg.find("Segmented", /scope/i);
    expect(seg.options.find((o: any) => o.value === "sprint").disabled).toBe(true);
    expect(seg.options.find((o: any) => o.value === "board").disabled).toBeFalsy();
    expect(seg.value).toBe("board");
    expect(screen.getByText(/kanban/i)).toBeTruthy();
  });
});

describe("SourceSection: extra filter", () => {
  it("is a mono text field holding the stored JQL", () => {
    render(<SourceSection config={mkConfig({ extraFilter: "labels = standup" })} columns={3} />);
    const f = reg.find("TextField", /filter/i);
    expect(f.value).toBe("labels = standup");
    expect(f.mono).toBe(true);
  });

  it("autosaves typed text with the debounce flag and does not validate while typing", () => {
    render(<SourceSection config={mkConfig()} columns={3} />);
    act(() => reg.find("TextField", /filter/i).onInput("labels = standup"));
    expect(st.saveField).toHaveBeenCalledWith("extraFilter", "labels = standup", { text: true });
    expect(st.validateFilter).not.toHaveBeenCalled();
  });

  it("validates on commit (blur or Enter)", () => {
    render(<SourceSection config={mkConfig({ extraFilter: "labels = standup" })} columns={3} />);
    act(() => reg.find("TextField", /filter/i).onCommit("labels = standup"));
    expect(st.validateFilter).toHaveBeenCalledTimes(1);
  });

  it("shows every FilterCheck error inline on the field", () => {
    const filterCheck = { ok: false, errors: ["Field 'sprnt' does not exist.", "Unbalanced parenthesis near ')'."], checkedAt: 1 };
    render(<SourceSection config={mkConfig({ extraFilter: "sprnt = 1 )", filterCheck })} columns={3} />);
    const f = reg.find("TextField", /filter/i);
    expect(f.error).toContain("Field 'sprnt' does not exist.");
    expect(f.error).toContain("Unbalanced parenthesis near ')'.");
    expect(screen.getByText(/does not exist/)).toBeTruthy();
  });

  it.each([
    ["never checked", null],
    ["checked and ok", { ok: true, errors: [], checkedAt: 1 }],
  ])("shows no error when %s", (_n, filterCheck) => {
    render(<SourceSection config={mkConfig({ filterCheck })} columns={3} />);
    expect(reg.find("TextField", /filter/i).error || "").toBe("");
  });
});

describe("SourceSection: start from (new standups only)", () => {
  it("is absent for a configured standup", () => {
    standups.value = [tab(1, "Saffron Mobile"), tab(2, "Payments")];
    render(<SourceSection config={mkConfig({ draft: false })} columns={3} />);
    expect(reg.has("Combobox", /space/i)).toBe(true);
    expect(reg.has("Combobox", /start from/i)).toBe(false);
  });

  it("lists Defaults plus a Copy option per other standup, excluding the current one", () => {
    standups.value = [tab(1, "Saffron Mobile"), tab(2, "Payments"), tab(3, "Ops", { configured: false })];
    render(<SourceSection config={mkConfig({ id: 3, name: "Ops", draft: true, space: null, board: null })} columns={3} />);
    const sf = reg.find("Combobox", /start from/i);
    const labels: string[] = sf.options.map((o: any) => sf.getLabel(o));
    expect(labels).toHaveLength(3);
    expect(labels.some((l) => /defaults/i.test(l))).toBe(true);
    expect(labels.some((l) => /Saffron Mobile/.test(l))).toBe(true);
    expect(labels.some((l) => /Payments/.test(l))).toBe(true);
    expect(labels.some((l) => /Ops/.test(l))).toBe(false);
  });

  it("choosing a standup copies its settings via copySettingsFrom(id)", () => {
    standups.value = [tab(1, "Saffron Mobile"), tab(2, "Payments"), tab(3, "Ops", { configured: false })];
    render(<SourceSection config={mkConfig({ id: 3, name: "Ops", draft: true, space: null, board: null })} columns={3} />);
    const sf = reg.find("Combobox", /start from/i);
    const opt = sf.options.find((o: any) => /Payments/.test(sf.getLabel(o)));
    act(() => sf.onSelect(opt));
    expect(st.copySettingsFrom).toHaveBeenCalledTimes(1);
    expect(st.copySettingsFrom).toHaveBeenCalledWith(2);
  });

  it("choosing Defaults changes nothing", () => {
    standups.value = [tab(1, "Saffron Mobile"), tab(3, "Ops", { configured: false })];
    render(<SourceSection config={mkConfig({ id: 3, name: "Ops", draft: true, space: null, board: null })} columns={3} />);
    const sf = reg.find("Combobox", /start from/i);
    act(() => sf.onSelect(sf.options.find((o: any) => /defaults/i.test(sf.getLabel(o)))));
    expect(st.copySettingsFrom).not.toHaveBeenCalled();
    expect(st.saveField).not.toHaveBeenCalled();
  });
});

describe("SourceSection: frame", () => {
  it("shows the heading 'Source'", () => {
    render(<SourceSection config={mkConfig()} columns={1} />);
    expect(screen.getByRole("heading", { name: /^Source/i })).toBeTruthy();
  });
});
