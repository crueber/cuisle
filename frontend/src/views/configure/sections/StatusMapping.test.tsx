/* eslint-disable @typescript-eslint/no-explicit-any */
// Configure > Status mapping section (ux-spec §9.4 Status mapping).
//
// SPEC-NOTE (approach): state functions mocked via vi.mock("state/config"); shared Combobox replaced by a recording
//   fake (see Source.test.tsx for the full rationale). Persistence goes only through state/config.ts.
// SPEC-NOTE: one Combobox per board column, in config.columns order. Its accessible label contains the column name
//   (matched as a case-insensitive substring of the column name, e.g. /code review/i).
// SPEC-NOTE: the lane options are config.laneOptions (from Go), in its order, and getKey returns the lane id:
//   Backlog(backlog) / To Do(todo) / In Progress(prog) / In Review(review) / QA(qa) / [custom lanes] / Done(done) /
//   Hidden(hidden). getKey(option) of the selected value equals ColumnMapping.lane. Selecting calls
//   setColumnLane(columnName, lane). Typing a new name offers onCreate → createLane(name, columnName).
// SPEC-NOTE (custom lanes): a "Lanes" list has one name field per working lane and Done (rename → renameLane, 400 ms
//   debounce in state); lanes the user named have a Delete button. With no columns it deletes at once; otherwise a
//   "Move its columns to" Combobox must be chosen first, then Delete lane calls deleteLane(lane, target).
// SPEC-NOTE: there is no "auto" tag (removed 2026-11-24: it only marked which rows the user had changed, and read as
//   noise). ColumnMapping.auto is still carried for the data, just not shown.
// SPEC-NOTE: the unmapped warning is one text node naming the statuses (config.unmappedStatuses) and containing
//   "map" (e.g. "2 statuses aren't mapped: Blocked, Waiting"); it is absent when the list is empty. With no board
//   chosen (new standup) an explanatory paragraph mentioning "board" replaces the rows and no combobox is rendered.


import { act, fireEvent, render, screen } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { LaneInfo, StandupConfig } from "../../../api/types";
import { config as configSignal } from "../../../state/config";
import { StatusMappingSection } from "./StatusMapping";

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
  loadConfig: vi.fn(async () => {}), saveField: vi.fn(), setSpace: vi.fn(), setBoard: vi.fn(), setColumnLane: vi.fn(), moveColumn: vi.fn(),
  setWalkOrder: vi.fn(), setLaneLimit: vi.fn(), setSpeakerIncluded: vi.fn(), moveSpeaker: vi.fn(), sortSpeakers: vi.fn(),
  addSpeaker: vi.fn(), removeSpeaker: vi.fn(), validateFilter: vi.fn(), createStandup: vi.fn(async () => {}), duplicateStandup: vi.fn(async () => {}),
  copySettingsFrom: vi.fn(async () => {}), deleteStandup: vi.fn(async () => {}), moveStandup: vi.fn(async () => {}), leaveConfigure: vi.fn(async () => {}),
  createLane: vi.fn(), renameLane: vi.fn(), deleteLane: vi.fn(),
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

const opt = (lane: string, label: string, kind = "working", extra: Partial<LaneInfo> = {}): LaneInfo => ({
  lane, label, defaultLabel: label, kind, custom: false, hue: 0, limitDays: 0, columns: [], ...extra,
});
const OPTIONS = (custom: LaneInfo[] = []): LaneInfo[] => [
  opt("backlog", "Backlog", "backlog"), opt("todo", "To Do"), opt("prog", "In Progress"), opt("review", "In Review"),
  opt("qa", "QA"), ...custom, opt("done", "Done", "done"), opt("hidden", "Hidden", "hidden"),
];

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
    laneOptions: OPTIONS(), walkLeftToRight: ["todo", "prog", "review", "qa"],
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

beforeEach(() => {
  reg.clear();
  Object.values(st).forEach((f) => f.mockClear());
});

const LANES = [
  ["Backlog", "backlog"], ["To Do", "todo"], ["In Progress", "prog"], ["In Review", "review"], ["QA", "qa"], ["Done", "done"], ["Hidden", "hidden"],
];

describe("StatusMappingSection: rows", () => {
  it("renders one combobox per board column in board order", () => {
    render(<StatusMappingSection config={mkConfig()} columns={3} />);
    expect(reg.labels("Combobox")).toHaveLength(6);
    const order = ["Icebox", "Ready", "Doing", "Code Review", "Verify", "Closed"];
    reg.labels("Combobox").forEach((label, i) => expect(label.toLowerCase()).toContain((order[i] ?? "").toLowerCase()));
  });

  it("shows each column's current lane", () => {
    render(<StatusMappingSection config={mkConfig()} columns={3} />);
    const expected: Record<string, string> = { icebox: "backlog", ready: "todo", doing: "prog", "code review": "review", verify: "qa", closed: "done" };
    for (const [col, lane] of Object.entries(expected)) {
      const c = reg.find("Combobox", new RegExp(col, "i"));
      expect(c.getKey(c.value)).toBe(lane);
    }
  });

  it("offers the seven lane options in order with MapLane keys", () => {
    render(<StatusMappingSection config={mkConfig()} columns={3} />);
    const c = reg.find("Combobox", /doing/i);
    expect(c.options.map((o: any) => c.getLabel(o))).toEqual(LANES.map(([l]) => l));
    expect(c.options.map((o: any) => c.getKey(o))).toEqual(LANES.map(([, k]) => k));
  });

  it.each(LANES)("choosing %s on a column calls setColumnLane(column, '%s') once", (label, lane) => {
    render(<StatusMappingSection config={mkConfig()} columns={3} />);
    const c = reg.find("Combobox", /code review/i);
    act(() => c.onSelect(c.options.find((o: any) => c.getKey(o) === lane)));
    expect(st.setColumnLane).toHaveBeenCalledTimes(1);
    expect(st.setColumnLane).toHaveBeenCalledWith("Code Review", lane);
    void label;
  });

  it("is not clearable (a column always has a lane)", () => {
    render(<StatusMappingSection config={mkConfig()} columns={3} />);
    expect(reg.find("Combobox", /doing/i).clearable).toBeFalsy();
  });
});

describe("StatusMappingSection: no auto tag", () => {
  it("showsNoAutoTag_WhetherARowWasAutoMatchedOrChanged", () => {
    render(<StatusMappingSection config={mkConfig()} columns={3} />);
    expect(reg.labels("Combobox")).toHaveLength(6);
    expect(screen.queryAllByText("auto")).toHaveLength(0);
  });
});

describe("StatusMappingSection: unmapped warning", () => {
  it("names the unmapped statuses in a warning line", () => {
    render(<StatusMappingSection config={mkConfig({ unmappedStatuses: ["Blocked", "Waiting"] })} columns={3} />);
    const warn = screen.getByText(/Blocked/);
    expect(warn.textContent).toMatch(/Waiting/);
    expect(warn.textContent).toMatch(/map/i);
  });

  it("shows no warning when every status is mapped", () => {
    render(<StatusMappingSection config={mkConfig({ unmappedStatuses: [] })} columns={3} />);
    expect(reg.labels("Combobox")).toHaveLength(6);
    expect(screen.queryByText(/not mapped|aren.t mapped|unmapped/i)).toBeNull();
  });
});

describe("StatusMappingSection: new standup", () => {
  it("explains what to do until a board is chosen and renders no rows", () => {
    render(<StatusMappingSection config={mkConfig({ draft: true, space: null, board: null, columns: [], lanes: [] })} columns={3} />);
    expect(screen.getByRole("heading", { name: /^Status mapping/i })).toBeTruthy();
    expect(reg.labels("Combobox")).toHaveLength(0);
    expect(screen.getAllByText(/board/i).length).toBeGreaterThan(0);
  });
});

describe("StatusMappingSection: order within sections", () => {
  const shared = () =>
    mkConfig({
      columns: [
        { columnName: "Doing", statusNames: ["In Progress"], lane: "prog", auto: true, order: 1 },
        { columnName: "Pairing", statusNames: ["Pairing"], lane: "prog", auto: false, order: 0 },
        { columnName: "Verify", statusNames: ["Testing"], lane: "qa", auto: true, order: 0 },
      ],
    });

  it("is absent when every section has one column", () => {
    render(<StatusMappingSection config={mkConfig()} columns={3} />);
    expect(screen.queryByText("Order within sections")).toBeNull();
  });

  it("lists a shared section's columns in their order and moves them with the arrows", () => {
    render(<StatusMappingSection config={shared()} columns={3} />);
    const list = screen.getByRole("list", { name: "In Progress order" });
    const names = Array.from(list.querySelectorAll(".personview-name")).map((n) => n.textContent);
    expect(names).toEqual(["Pairing", "Doing"]);
    expect((screen.getByRole("button", { name: "Move Pairing up" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Move Doing up" }));
    expect(st.moveColumn).toHaveBeenCalledWith("Doing", 0);
    fireEvent.click(screen.getByRole("button", { name: "Move Pairing down" }));
    expect(st.moveColumn).toHaveBeenCalledWith("Pairing", 1);
    expect(screen.queryByRole("list", { name: "QA order" })).toBeNull();
  });
});

describe("StatusMappingSection: lanes you name", () => {
  const DESIGN = opt("c2", "Design", "working", { defaultLabel: "", custom: true, hue: 2, columns: ["Ready"] });
  const BLOCKED = opt("c3", "Blocked", "working", { defaultLabel: "", custom: true, hue: 3 });
  const withCustom = () => {
    const c = mkConfig({ laneOptions: OPTIONS([DESIGN, BLOCKED]) });
    c.columns = c.columns.map((col) => (col.columnName === "Ready" ? { ...col, lane: "c2" } : col));
    return c;
  };

  it("offers custom lanes between QA and Done and shows a column mapped to one", () => {
    render(<StatusMappingSection config={withCustom()} columns={3} />);
    const c = reg.find("Combobox", /ready/i);
    expect(c.options.map((o: any) => c.getLabel(o))).toEqual(["Backlog", "To Do", "In Progress", "In Review", "QA", "Design", "Blocked", "Done", "Hidden"]);
    expect(c.getKey(c.value)).toBe("c2");
  });

  it("creates a lane from the typed name for that column", () => {
    render(<StatusMappingSection config={mkConfig()} columns={3} />);
    const c = reg.find("Combobox", /code review/i);
    expect(c.createLabel("Peer check")).toBe("Create lane “Peer check”");
    act(() => c.onCreate("Peer check"));
    expect(st.createLane).toHaveBeenCalledWith("Peer check", "Code Review");
  });

  it("paints a custom lane's swatch from its palette index", () => {
    const cfg = withCustom();
    configSignal.value = cfg;
    try {
      const { container } = render(<StatusMappingSection config={cfg} columns={3} />);
      expect(container.querySelectorAll('.cfg-swatch[data-lane="c2"][data-hue="2"]').length).toBeGreaterThan(0);
      expect(container.querySelector('.cfg-swatch[data-lane="todo"]')?.hasAttribute("data-hue")).toBe(false);
    } finally {
      configSignal.value = null;
    }
  });

  it("renames built-in and custom lanes and Done from the Lanes list", () => {
    render(<StatusMappingSection config={withCustom()} columns={3} />);
    // Each row is headed by the lane's current name (custom lanes too), with the columns mapped to it underneath.
    const heads = Array.from(document.querySelectorAll("[data-lane-row] .cfg-map-name")).map((e) => e.textContent);
    expect(heads).toEqual(["To Do", "In Progress", "In Review", "QA", "Design", "Blocked", "Done"]);
    const names = screen.getAllByRole("textbox").map((t) => (t as HTMLInputElement).value);
    expect(names).toEqual(["To Do", "In Progress", "In Review", "QA", "Design", "Blocked", "Done"]);
    fireEvent.input(screen.getByLabelText("Name of the In Review lane"), { target: { value: "Code Review" } });
    expect(st.renameLane).toHaveBeenCalledWith("review", "Code Review");
    fireEvent.input(screen.getByLabelText("Name of the Design lane"), { target: { value: "Discovery" } });
    expect(st.renameLane).toHaveBeenCalledWith("c2", "Discovery");
    expect(screen.queryByRole("button", { name: "Delete In Review" })).toBeNull();
  });

  it("deletes a lane with no columns at once", () => {
    render(<StatusMappingSection config={withCustom()} columns={3} />);
    fireEvent.click(screen.getByRole("button", { name: "Delete Blocked" }));
    expect(st.deleteLane).toHaveBeenCalledWith("c3", "");
  });

  it("asks where a lane's columns go before deleting it", () => {
    render(<StatusMappingSection config={withCustom()} columns={3} />);
    fireEvent.click(screen.getByRole("button", { name: "Delete Design" }));
    expect(st.deleteLane).not.toHaveBeenCalled();
    expect(screen.getByRole("group", { name: "Delete Design: choose where its columns go" })).toBeTruthy();
    const move = reg.find("Combobox", /move the columns of design/i);
    expect(move.options.map((o: any) => o.lane)).not.toContain("c2");
    const confirm = screen.getByRole("button", { name: "Delete lane" }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    act(() => move.onSelect(move.options.find((o: any) => o.lane === "prog")));
    fireEvent.click(screen.getByRole("button", { name: "Delete lane" }));
    expect(st.deleteLane).toHaveBeenCalledWith("c2", "prog");
  });

  it("cancel keeps the lane", () => {
    render(<StatusMappingSection config={withCustom()} columns={3} />);
    fireEvent.click(screen.getByRole("button", { name: "Delete Design" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(st.deleteLane).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Delete lane" })).toBeNull();
  });
});
