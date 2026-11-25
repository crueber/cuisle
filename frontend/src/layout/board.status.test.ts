// F13: whole board grouped by status (ux-spec §6.5). Pure; no DOM.
//
// SPEC-NOTE (golden): the mockup data (design/mockups/board-status-*.png, Saffron sample): QA 7, In Review 8,
//   In Progress 14, To Do 11 active tickets, Done 15 (two-column grid, 8 rows), Backlog 12 (two-column grid, 6 rows,
//   with the "more ranked below" footnote). At row 23 the sequence heights (37 + rows x 23) are 198, 221, 359, 290, then the lists 258
//   and 226; the cut that minimises the taller column is after In Progress: QA, In Review, In Progress | To Do, Done,
//   Backlog, exactly the mockup.
import { describe, expect, it } from "vitest";
import type { StandupView, TicketView } from "../api/types";
import { listSectionHeight, sortStatusRows, splitSequence, statusLayout, statusSectionHeight, statusSections } from "./board";

let seq = 0;
const tk = (lane: string, over: Partial<TicketView> = {}): TicketView => {
  seq += 1;
  return {
    id: seq, sourceId: String(seq), key: `MOB-${seq}`, summary: `s${seq}`, url: "", type: "story", assignee: "iris", lane, statusName: lane,
    daysInLane: 1, overLimit: false, blocked: false, doneAt: 0, pulse: { segs: [], marks: [], blocked: [], label: "" }, ...over,
  } as TicketView;
};
const LANES = [
  { lane: "todo", label: "To Do", limitDays: 10, columns: ["To Do"] },
  { lane: "prog", label: "In Progress", limitDays: 5, columns: ["In Progress"] },
  { lane: "review", label: "In Review", limitDays: 2, columns: ["In Review"] },
  { lane: "qa", label: "QA", limitDays: 3, columns: ["QA"] },
  { lane: "done", label: "Done", limitDays: 0, columns: ["Done"] },
];
// Walk order is the reverse of the flow: QA first, then In Review, In Progress, To Do.
const WALK = [...LANES.slice(0, 4)].reverse().concat(LANES[4]!);
const viewOf = (tickets: TicketView[], lanes = WALK): StandupView => ({ tickets, lanes }) as unknown as StandupView;
const many = (lane: string, n: number) => Array.from({ length: n }, () => tk(lane));

describe("statusSections", () => {
  it("lists one section per lane in walk order, skipping Done", () => {
    const v = viewOf([...many("todo", 11), ...many("prog", 14), ...many("review", 8), ...many("qa", 7), ...many("done", 15)]);
    expect(statusSections(v).map((s) => [s.lane, s.label, s.tickets.length])).toEqual([
      ["qa", "QA", 7],
      ["review", "In Review", 8],
      ["prog", "In Progress", 14],
      ["todo", "To Do", 11],
    ]);
  });

  it("omits a lane with no active ticket", () => {
    const v = viewOf([...many("todo", 2), ...many("prog", 1), ...many("done", 3)]);
    expect(statusSections(v).map((s) => s.lane)).toEqual(["prog", "todo"]);
  });

  it("follows whatever lanes the view declares (no hard-coded lane list)", () => {
    const v = viewOf([tk("spike"), tk("prog"), tk("spike")], [
      { lane: "spike", label: "Spike", limitDays: 0, columns: [] },
      { lane: "prog", label: "In Progress", limitDays: 5, columns: [] },
      { lane: "done", label: "Done", limitDays: 0, columns: [] },
    ]);
    expect(statusSections(v).map((s) => [s.lane, s.label, s.tickets.length, s.limitDays])).toEqual([
      ["spike", "Spike", 2, 0],
      ["prog", "In Progress", 1, 5],
    ]);
  });

  it("counts over-limit, blocked and unassigned tickets per section", () => {
    const v = viewOf([
      tk("prog", { overLimit: true }),
      tk("prog", { overLimit: true, blocked: true }),
      tk("prog", { assignee: "" }),
      tk("prog", { blocked: true, assignee: "" }),
      tk("prog"),
    ]);
    const [s] = statusSections(v);
    expect(s).toMatchObject({ overLimit: 2, blocked: 2, unassigned: 2, limitDays: 5 });
  });

  it("never counts a Done ticket", () => {
    const v = viewOf([tk("done", { assignee: "" }), tk("todo")]);
    expect(statusSections(v)).toHaveLength(1);
  });
});

describe("sortStatusRows", () => {
  it("puts over-limit first, then days in lane descending, then board rank", () => {
    const a = tk("prog", { daysInLane: 2 });
    const b = tk("prog", { daysInLane: 9, overLimit: true });
    const c = tk("prog", { daysInLane: 5 });
    const d = tk("prog", { daysInLane: 5 }); // ties c: board rank (list order) decides
    const e = tk("prog", { daysInLane: 6, overLimit: true });
    expect(sortStatusRows([a, b, c, d, e]).map((t) => t.id)).toEqual([b.id, e.id, c.id, d.id, a.id]);
  });

  it("does not mutate its input", () => {
    const rows = [tk("prog", { daysInLane: 1 }), tk("prog", { daysInLane: 4 })];
    const before = rows.map((r) => r.id);
    sortStatusRows(rows);
    expect(rows.map((r) => r.id)).toEqual(before);
  });

  it("ranks an over-limit ticket above a longer in-limit one", () => {
    const long = tk("prog", { daysInLane: 30 });
    const over = tk("prog", { daysInLane: 6, overLimit: true });
    expect(sortStatusRows([long, over]).map((t) => t.id)).toEqual([over.id, long.id]);
  });
});

describe("splitSequence", () => {
  it("keeps everything in one column when cols is 1", () => {
    expect(splitSequence([10, 20, 30], 1)).toEqual({ columns: [[0, 1, 2]], heights: [60] });
  });

  it("cuts once, preserving order, to minimise the taller column", () => {
    expect(splitSequence([10, 10, 10, 10], 2)).toEqual({ columns: [[0, 1], [2, 3]], heights: [20, 20] });
    expect(splitSequence([50, 10, 10, 10, 10], 2)).toEqual({ columns: [[0], [1, 2, 3, 4]], heights: [50, 40] });
    expect(splitSequence([10, 10, 10, 50], 2)).toEqual({ columns: [[0, 1, 2], [3]], heights: [30, 50] });
  });

  it("fills column 1 first on a tie and never leaves it empty for a single section", () => {
    expect(splitSequence([100], 2)).toEqual({ columns: [[0], []], heights: [100, 0] });
    expect(splitSequence([], 2)).toEqual({ columns: [[], []], heights: [0, 0] });
  });
});

describe("statusLayout (mockup golden)", () => {
  const laneRows = [7, 8, 14, 11]; // QA, In Review, In Progress, To Do
  const lists = [listSectionHeight(15, 2), listSectionHeight(12, 2, true)];

  it("matches the section heights worked out in the spec note", () => {
    expect([7, 8, 14, 11].map((n) => statusSectionHeight(n, 23))).toEqual([198, 221, 359, 290]);
    expect(lists).toEqual([258, 226]);
  });

  it("splits QA, In Review, In Progress | To Do, Done, Backlog at 1920x1080", () => {
    const l = statusLayout(laneRows, lists, 2, 1080 - 180);
    expect(l.rowH).toBe(23);
    expect(l.columns).toEqual([[0, 1, 2], [3, 4, 5]]);
    expect(l.heights).toEqual([778, 774]);
  });

  it("falls back to 20px rows at 1280x720 and keeps the same split", () => {
    const l = statusLayout(laneRows, lists, 2, 720 - 180);
    expect(l.rowH).toBe(20);
    expect(l.columns).toEqual([[0, 1, 2], [3, 4, 5]]);
  });

  it("stacks everything in one column below mainW 1150 (cols 1)", () => {
    const l = statusLayout(laneRows, lists, 1, 900);
    expect(l.columns).toEqual([[0, 1, 2, 3, 4, 5]]);
  });

  it("uses 23px when the taller column just fits and 20px one pixel under", () => {
    expect(statusLayout(laneRows, lists, 2, 778).rowH).toBe(23);
    expect(statusLayout(laneRows, lists, 2, 777).rowH).toBe(20);
  });

  it("omits an empty lane (no section, no height)", () => {
    const l = statusLayout([7, 14, 11], lists, 2, 1080 - 180);
    expect(l.columns.flat()).toEqual([0, 1, 2, 3, 4]);
  });
});
