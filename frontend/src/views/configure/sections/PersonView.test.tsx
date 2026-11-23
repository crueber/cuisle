/* eslint-disable @typescript-eslint/no-explicit-any */
// Configure > Person view section (ux-spec §9.4 Person view).
//
// SPEC-NOTE (approach): state functions mocked via vi.mock("state/config"); Segmented replaced by a recording fake
//   (see Source.test.tsx).
// SPEC-NOTE (lane limits live in Pulse, not here): the orchestrator brief listed lane-limit Steppers under Person view,
//   but ux-spec §9.4 puts "Status limits" in the Pulse section and the approved configure mockup does too. This section
//   therefore renders NO steppers; the limits are tested in Pulse.test.tsx.
// SPEC-NOTE: StandupConfig.lanes is in walk order and may or may not contain the "done" lane. The walk-order list shows
//   only the four walk lanes (todo/prog/review/qa), one role="listitem" row each in walk order, with the lane label,
//   a mono "Jira: ‹columns›" line (columns joined with ", "), and up/down buttons (accessible names /\bup\b/i and
//   /\bdown\b/i, disabled at the ends). Done is a separate locked row (not a walk-order listitem) whose text contains
//   "always last". Alt+Arrow keydown on a row also reorders (row-level handler).
// SPEC-NOTE: every reorder/preset calls setWalkOrder(fullOrderOfFourLaneIds) once, e.g. moving QA down from
//   [qa,review,prog,todo] gives [review,qa,prog,todo]. Presets: "Right to left" = [qa,review,prog,todo],
//   "Left to right" = [todo,prog,review,qa] (buttons named exactly so).
// SPEC-NOTE: the subtitle (inside the section heading block) reads "Right to left (QA first)", "Left to right (To Do
//   first)" or "Custom order", derived from the current order.
// SPEC-NOTE: Rows is a Segmented labelled /rows/i with values "fit" | "roomy" (labels "Fit when possible" / "Always
//   roomy") -> saveField("rowsMode", value); the hint "Tighten before scrolling" is plain text.


import { act, fireEvent, render, screen, within } from "@testing-library/preact";
import { beforeEach, describe, expect, it } from "vitest";
import type { LaneInfo, StandupConfig } from "../../../api/types";
import { PersonViewSection } from "./PersonView";

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

vi.mock("../../../components/Stepper", async () => {
  const { h } = await import("preact");
  return {
    Stepper: (p: any) => {
      reg.rec("Stepper", p);
      return h("input", { role: "spinbutton", "aria-label": p.label, value: p.value, readOnly: true });
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
      { lane: "qa", label: "QA", limitDays: 3, columns: ["Verify"] },
      { lane: "review", label: "Review", limitDays: 2, columns: ["Code Review", "Peer Review"] },
      { lane: "prog", label: "In progress", limitDays: 5, columns: ["Doing"] },
      { lane: "todo", label: "To do", limitDays: 0, columns: ["Ready"] },
      { lane: "done", label: "Done", limitDays: 0, columns: ["Closed"] },
    ],
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

const row = (label: string): HTMLElement => {
  const el = screen.getByText(label, { exact: true }).closest('li, [role="listitem"]');
  if (!el) throw new Error(`no walk-order row for ${label}`);
  return el as HTMLElement;
};
const upBtn = (label: string) => within(row(label)).getByRole("button", { name: /\bup\b/i }) as HTMLButtonElement;
const downBtn = (label: string) => within(row(label)).getByRole("button", { name: /\bdown\b/i }) as HTMLButtonElement;

const L = (lane: string, label: string, columns: string[]): LaneInfo => ({ lane, label, limitDays: 2, columns });
const QA = L("qa", "QA", ["Verify"]);
const REVIEW = L("review", "Review", ["Code Review", "Peer Review"]);
const PROG = L("prog", "In progress", ["Doing"]);
const TODO = L("todo", "To do", ["Ready"]);
const DONE = L("done", "Done", ["Closed"]);
const withOrder = (...lanes: LaneInfo[]) => mkConfig({ lanes: [...lanes, DONE] });

beforeEach(() => {
  reg.clear();
  Object.values(st).forEach((f) => f.mockClear());
});

describe("PersonViewSection: walk order list", () => {
  it("lists the four walk lanes in config order with their Jira columns", () => {
    render(<PersonViewSection config={mkConfig()} columns={3} />);
    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(4);
    expect(items.map((li) => ["QA", "Review", "In progress", "To do"].findIndex((l) => li.textContent?.includes(l)))).toEqual([0, 1, 2, 3]);
    expect(row("Review").textContent).toContain("Jira: Code Review, Peer Review");
    expect(row("QA").textContent).toContain("Jira: Verify");
  });

  it("follows a different config order", () => {
    render(<PersonViewSection config={withOrder(TODO, PROG, REVIEW, QA)} columns={3} />);
    const items = screen.getAllByRole("listitem");
    expect(items[0]?.textContent).toContain("To do");
    expect(items[3]?.textContent).toContain("QA");
  });

  it("shows Done as a locked last row ('always last') with no reorder buttons", () => {
    render(<PersonViewSection config={mkConfig()} columns={3} />);
    const note = screen.getByText(/always last/i);
    expect(note.textContent).toMatch(/first below the fold/i);
    expect(screen.getAllByRole("listitem")).toHaveLength(4);
    const doneRow = note.closest("li, div") as HTMLElement;
    expect(within(doneRow).queryByRole("button", { name: /\b(up|down)\b/i })).toBeNull();
  });

  it("has no lane-limit steppers (limits live in Pulse)", () => {
    render(<PersonViewSection config={mkConfig()} columns={3} />);
    expect(screen.getAllByRole("listitem")).toHaveLength(4);
    expect(reg.labels("Stepper")).toEqual([]);
    expect(screen.queryByRole("spinbutton")).toBeNull();
  });
});

describe("PersonViewSection: reordering", () => {
  it("moving QA down calls setWalkOrder with the full new order", () => {
    render(<PersonViewSection config={mkConfig()} columns={3} />);
    act(() => { downBtn("QA").click(); });
    expect(st.setWalkOrder).toHaveBeenCalledTimes(1);
    expect(st.setWalkOrder).toHaveBeenCalledWith(["review", "qa", "prog", "todo"]);
  });

  it("moving To do up calls setWalkOrder with the full new order", () => {
    render(<PersonViewSection config={mkConfig()} columns={3} />);
    act(() => { upBtn("To do").click(); });
    expect(st.setWalkOrder).toHaveBeenCalledWith(["qa", "review", "todo", "prog"]);
  });

  it("disables up on the first walk row and down on the last", () => {
    render(<PersonViewSection config={mkConfig()} columns={3} />);
    expect(upBtn("QA").disabled).toBe(true);
    expect(downBtn("QA").disabled).toBe(false);
    expect(upBtn("To do").disabled).toBe(false);
    expect(downBtn("To do").disabled).toBe(true);
  });

  it("Alt+ArrowDown / Alt+ArrowUp on a row reorders it", () => {
    render(<PersonViewSection config={mkConfig()} columns={3} />);
    fireEvent.keyDown(upBtn("Review"), { key: "ArrowDown", altKey: true });
    expect(st.setWalkOrder).toHaveBeenLastCalledWith(["qa", "prog", "review", "todo"]);
    fireEvent.keyDown(upBtn("In progress"), { key: "ArrowUp", altKey: true });
    expect(st.setWalkOrder).toHaveBeenLastCalledWith(["qa", "prog", "review", "todo"]);
    expect(st.setWalkOrder).toHaveBeenCalledTimes(2);
  });

  it("ignores plain arrows and Alt+Arrow past the ends", () => {
    render(<PersonViewSection config={mkConfig()} columns={3} />);
    fireEvent.keyDown(upBtn("Review"), { key: "ArrowDown" });
    fireEvent.keyDown(upBtn("QA"), { key: "ArrowUp", altKey: true });
    fireEvent.keyDown(upBtn("To do"), { key: "ArrowDown", altKey: true });
    expect(st.setWalkOrder).not.toHaveBeenCalled();
    expect(screen.getAllByRole("listitem")).toHaveLength(4);
  });
});

describe("PersonViewSection: presets and subtitle", () => {
  it("Right to left sets QA first", () => {
    render(<PersonViewSection config={withOrder(TODO, PROG, REVIEW, QA)} columns={3} />);
    act(() => { screen.getByRole("button", { name: "Right to left" }).click(); });
    expect(st.setWalkOrder).toHaveBeenCalledTimes(1);
    expect(st.setWalkOrder).toHaveBeenCalledWith(["qa", "review", "prog", "todo"]);
  });

  it("Left to right sets To do first", () => {
    render(<PersonViewSection config={mkConfig()} columns={3} />);
    act(() => { screen.getByRole("button", { name: "Left to right" }).click(); });
    expect(st.setWalkOrder).toHaveBeenCalledTimes(1);
    expect(st.setWalkOrder).toHaveBeenCalledWith(["todo", "prog", "review", "qa"]);
  });

  it.each([
    ["Right to left (QA first)", [QA, REVIEW, PROG, TODO]],
    ["Left to right (To Do first)", [TODO, PROG, REVIEW, QA]],
    ["Custom order", [REVIEW, QA, PROG, TODO]],
    ["Custom order", [QA, REVIEW, TODO, PROG]],
  ] as [string, LaneInfo[]][])("shows the subtitle '%s' for the matching order", (subtitle, lanes) => {
    render(<PersonViewSection config={withOrder(...lanes)} columns={3} />);
    expect(screen.getByText(subtitle)).toBeTruthy();
  });
});

describe("PersonViewSection: rows mode", () => {
  it("offers Fit when possible / Always roomy with the stored value and a hint", () => {
    render(<PersonViewSection config={mkConfig({ rowsMode: "roomy" })} columns={3} />);
    const seg = reg.find("Segmented", /rows/i);
    expect(seg.value).toBe("roomy");
    expect(seg.options.map((o: any) => o.value)).toEqual(["fit", "roomy"]);
    expect(seg.options.map((o: any) => o.label)).toEqual(["Fit when possible", "Always roomy"]);
    expect(screen.getByText(/Tighten before scrolling/i)).toBeTruthy();
  });

  it("autosaves a rows mode change immediately", () => {
    render(<PersonViewSection config={mkConfig({ rowsMode: "fit" })} columns={3} />);
    act(() => reg.find("Segmented", /rows/i).onChange("roomy"));
    expect(st.saveField).toHaveBeenCalledTimes(1);
    expect(st.saveField).toHaveBeenCalledWith("rowsMode", "roomy");
  });

  it("shows the heading 'Person view'", () => {
    render(<PersonViewSection config={mkConfig()} columns={3} />);
    expect(screen.getByRole("heading", { name: /^Person view/i })).toBeTruthy();
  });
});
