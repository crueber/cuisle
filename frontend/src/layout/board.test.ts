// WP-13: board layout (ux-spec §6.2, §6.3, testing.md §4). Pure; no DOM.
//
// SPEC-NOTE (MOB fixture): the lane sizes come from design/reference/sample-data.json, standup "mob", counting ACTIVE
//   (non-done) tickets per person: iris 14, silas 4, noor 4, otto 3, sam 4, jonas 3, wren 4, unassigned 4
//   (speaking order = the people array order). At row 23 lane heights are 30 + rows × 23 + 8, so the greedy balance gives
//   column 1 = [unassigned, silas, noor, sam, wren] (650) and column 2 = [iris, otto, jonas] (574).
// SPEC-NOTE: Unassigned with rows = 0 is omitted from the result (ux-spec §6.2: Unassigned "if it has tickets"; the
//   prototype only adds it when it has rows). Speaker lanes with 0 rows are not tested.
// SPEC-NOTE: when the input lists the unassigned lane anywhere (even last), it is hoisted to the head of column 1.
// SPEC-NOTE: boardTitleWidth/doneBacklogLayout use integer inputs; the prototype rounds, the spec does not.
import { describe, expect, it } from "vitest";
import {
  balanceLanes,
  boardColumns,
  boardTitleWidth,
  doneBacklogLayout,
  laneHeight,
  laneRowHeight,
  type LaneInput,
} from "./board";

const speaker = (id: string, rows: number): LaneInput => ({ id, rows, unassigned: false });
const SVC_SPEAKERS: LaneInput[] = [
  speaker("iris", 14),
  speaker("silas", 4),
  speaker("noor", 4),
  speaker("otto", 3),
  speaker("amara", 4),
  speaker("jonas", 3),
  speaker("wren", 4),
];
const SVC_UNASSIGNED: LaneInput = { id: "unassigned", rows: 4, unassigned: true };
const ids = (cols: LaneInput[][]) => cols.map((c) => c.map((l) => l.id));

describe("boardColumns", () => {
  it.each([
    [1656, 2],
    [1150, 2],
    [1149, 1],
    [1000, 1],
    [2400, 2],
  ])("boardColumns(%i) is %i", (w, n) => {
    expect(boardColumns(w)).toBe(n);
  });
});

describe("laneHeight", () => {
  it("is 30 + rows × rowH + 8", () => {
    expect(laneHeight(4, 23)).toBe(130);
    expect(laneHeight(14, 23)).toBe(360);
    expect(laneHeight(3, 20)).toBe(98);
    expect(laneHeight(0, 23)).toBe(38);
  });
});

describe("balanceLanes: MOB fixture at mainW 1656, row 23", () => {
  it("makes 2 columns with Unassigned first in column 1 and speaking order preserved within each column", () => {
    const r = balanceLanes([...SVC_SPEAKERS, SVC_UNASSIGNED], boardColumns(1656), 23);
    expect(r.columns).toHaveLength(2);
    expect(ids(r.columns)).toEqual([
      ["unassigned", "silas", "noor", "amara", "wren"],
      ["iris", "otto", "jonas"],
    ]);
    expect(r.heights).toEqual([650, 574]);
  });

  it("gives the same result when Unassigned is listed first", () => {
    const r = balanceLanes([SVC_UNASSIGNED, ...SVC_SPEAKERS], 2, 23);
    expect(ids(r.columns)).toEqual([
      ["unassigned", "silas", "noor", "amara", "wren"],
      ["iris", "otto", "jonas"],
    ]);
  });

  it("re-balances at row 20 (heights 30 + rows×20 + 8)", () => {
    // unassigned 118; iris 318 -> col2; silas 118 -> col1 (236); noor 118 -> col1 (354);
    // otto 98 -> col2 (416); sam 118 -> col1 (472); jonas 98 -> col2 (514); wren 118 -> col1 (590)
    const r = balanceLanes([...SVC_SPEAKERS, SVC_UNASSIGNED], 2, 20);
    expect(ids(r.columns)).toEqual([
      ["unassigned", "silas", "noor", "amara", "wren"],
      ["iris", "otto", "jonas"],
    ]);
    expect(r.heights).toEqual([590, 514]);
  });

  it("puts everything in one column when cols = 1 (Unassigned first, order preserved)", () => {
    const r = balanceLanes([...SVC_SPEAKERS, SVC_UNASSIGNED], 1, 23);
    expect(ids(r.columns)).toEqual([["unassigned", "iris", "silas", "noor", "otto", "amara", "jonas", "wren"]]);
    expect(r.heights).toEqual([130 + 360 + 130 + 130 + 107 + 130 + 107 + 130]);
  });
});

describe("balanceLanes: rules", () => {
  it("sends a tie to the left column", () => {
    const r = balanceLanes([speaker("a", 2), speaker("b", 2), speaker("c", 2)], 2, 23);
    // a -> col1 (tie at 0), b -> col2 (0 < 84), c -> col1 (84 tie -> left)
    expect(ids(r.columns)).toEqual([["a", "c"], ["b"]]);
    expect(r.heights).toEqual([168, 84]);
  });

  it("omits Unassigned when it has no rows", () => {
    const r = balanceLanes([speaker("a", 1), { id: "unassigned", rows: 0, unassigned: true }], 2, 23);
    expect(ids(r.columns).flat()).toEqual(["a"]);
  });

  it("hoists Unassigned to the head of column 1 even when listed last", () => {
    const r = balanceLanes([speaker("a", 1), speaker("b", 1), { id: "unassigned", rows: 1, unassigned: true }], 2, 23);
    expect(r.columns[0]?.[0]?.id).toBe("unassigned");
  });

  it("returns one empty column per requested column for no lanes", () => {
    const r = balanceLanes([], 2, 23);
    expect(r.columns).toEqual([[], []]);
    expect(r.heights).toEqual([0, 0]);
  });

  it("places Unassigned first and then greedily by height (a tall lane goes to the emptier column)", () => {
    // unassigned 38+23=61... (30 + 1×23 + 8 = 61) col1; big (30+10×23+8 = 268) -> col2; small -> col1
    const r = balanceLanes(
      [speaker("big", 10), speaker("small", 1), { id: "unassigned", rows: 1, unassigned: true }],
      2,
      23,
    );
    expect(ids(r.columns)).toEqual([["unassigned", "small"], ["big"]]);
    expect(r.heights).toEqual([61 + 61, 268]);
  });
});

describe("laneRowHeight", () => {
  const lanes = [...SVC_SPEAKERS, SVC_UNASSIGNED];

  it("is 23 when the tallest column at 23 fits exactly (650)", () => {
    expect(laneRowHeight(lanes, 2, 650)).toBe(23);
  });

  it("is 20 when the tallest column at 23 exceeds availH by one px (649)", () => {
    expect(laneRowHeight(lanes, 2, 649)).toBe(20);
  });

  it("is 23 for a tall window (1080: availH 908)", () => {
    expect(laneRowHeight(lanes, 2, 908)).toBe(23);
  });

  it("is 20 for a short window (1024x640: availH 484)", () => {
    expect(laneRowHeight(lanes, 2, 484)).toBe(20);
  });

  it("depends on the column count (1 column needs 1224 at row 23)", () => {
    expect(laneRowHeight(lanes, 1, 1224)).toBe(23);
    expect(laneRowHeight(lanes, 1, 1223)).toBe(20);
  });

  it("is 23 for no lanes", () => {
    expect(laneRowHeight([], 2, 100)).toBe(23);
  });
});

describe("boardTitleWidth", () => {
  it("is 250 when mainW ≥ 1500 regardless of the column", () => {
    expect(boardTitleWidth(1500, 700, 80)).toBe(250);
    expect(boardTitleWidth(1656, 300, 200)).toBe(250);
  });

  it("is colW − keyW − 14 − 36 − 32 − 240 below 1500 (colW 700, keyW 80 -> 298)", () => {
    expect(boardTitleWidth(1499, 700, 80)).toBe(298);
  });

  it("clamps to 300 at most", () => {
    expect(boardTitleWidth(1400, 800, 80)).toBe(300);
  });

  it("clamps to 150 at least", () => {
    expect(boardTitleWidth(1200, 500, 80)).toBe(150);
  });

  it("returns exactly 150 and 300 on the clamp edges", () => {
    // colW − keyW − 322 = 150 -> colW 552 with keyW 80; = 300 -> colW 702
    expect(boardTitleWidth(1200, 552, 80)).toBe(150);
    expect(boardTitleWidth(1200, 702, 80)).toBe(300);
    expect(boardTitleWidth(1200, 701, 80)).toBe(299);
  });
});

describe("doneBacklogLayout", () => {
  // bottomW = mainW − 40; side by side at ≥ 1100; done 2 cols and backlog 3 at ≥ 1300; backlog 2 at ≥ 1000
  const cases: Array<[number, boolean, number, number]> = [
    [1656, true, 2, 3],
    [1340, true, 2, 3],
    [1339, true, 1, 2],
    [1140, true, 1, 2],
    [1139, false, 1, 2],
    [1040, false, 1, 2],
    [1039, false, 1, 1],
    [800, false, 1, 1],
  ];
  it.each(cases)("mainW %i -> sideBySide %s, doneCols %i, backlogCols %i", (w, side, d, b) => {
    expect(doneBacklogLayout(w)).toEqual({ sideBySide: side, doneCols: d, backlogCols: b });
  });
});
