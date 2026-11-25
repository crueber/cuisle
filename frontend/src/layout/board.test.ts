// WP-13, F15: board layout (ux-spec §6.2, §6.3, §16.3, testing.md §4). Pure; no DOM.
//
// SPEC-NOTE (MOB fixture): the lane sizes come from design/reference/sample-data.json, standup "mob", counting ACTIVE
//   (non-done) tickets per person: iris 14, silas 4, noor 4, otto 3, amara 4, jonas 3, wren 4, unassigned 4
//   (speaking order = the people array order). At row 23 lane heights are 30 + rows × 23 + 8. F15 replaced the greedy
//   balance with F13's order-preserving cut (splitSequence): Unassigned first, then speakers in speaking order, cut
//   into contiguous runs that minimise the tallest column, so 2 columns are [unassigned, iris, silas] (620) and
//   [noor, otto, amara, jonas, wren] (604), and 3 columns are [unassigned, iris] (490) | [silas, noor] (260) | [otto, amara, jonas, wren] (474), the split the 80% mockup draws.
// SPEC-NOTE: Unassigned with rows = 0 is omitted from the result (ux-spec §6.2: Unassigned "if it has tickets"; the
//   prototype only adds it when it has rows). Speaker lanes with 0 rows are not tested.
// SPEC-NOTE: when the input lists the unassigned lane anywhere (even last), it is hoisted to the head of column 1.
// SPEC-NOTE: boardColumns is 1 | 2 | 3 (F15): 3 at effective mainW ≥ 1720, so zoom 80% on a 1920 window gives 3.
// SPEC-NOTE: boardTitleWidth/doneBacklogLayout use integer inputs; the prototype rounds, the spec does not.
import { describe, expect, it } from "vitest";
import {
  boardColumns,
  boardTitleWidth,
  doneBacklogLayout,
  laneHeight,
  laneRowHeight,
  splitLanes,
  splitSequence,
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
    [1719, 2],
    [1720, 3],
    [2128, 3],
    [2400, 3],
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

describe("splitLanes: MOB fixture, row 23", () => {
  it("2 columns: Unassigned, Iris, Silas | Noor, Otto, Amara, Jonas, Wren (the handoff's split)", () => {
    const r = splitLanes([...SVC_SPEAKERS, SVC_UNASSIGNED], boardColumns(1656), 23);
    expect(r.columns).toHaveLength(2);
    expect(ids(r.columns)).toEqual([
      ["unassigned", "iris", "silas"],
      ["noor", "otto", "amara", "jonas", "wren"],
    ]);
    expect(r.heights).toEqual([620, 604]);
  });

  it("3 columns: speaking order reads down, then across", () => {
    const r = splitLanes([...SVC_SPEAKERS, SVC_UNASSIGNED], boardColumns(2128), 23);
    expect(r.columns).toHaveLength(3);
    // the mockup's split (zoom-board-80): another cut, [silas, noor, otto | amara, jonas, wren] = 367 | 367, is as short
    // (490 is the tallest either way); a tie gives column 1 the most, then the last column, then the middle one the rest
    expect(ids(r.columns)).toEqual([["unassigned", "iris"], ["silas", "noor"], ["otto", "amara", "jonas", "wren"]]);
    expect(r.heights).toEqual([490, 260, 474]);
  });

  it("the flattened order is always Unassigned then the speakers in order, at any column count", () => {
    const order = ["unassigned", "iris", "silas", "noor", "otto", "amara", "jonas", "wren"];
    for (const cols of [1, 2, 3]) {
      for (const rowH of [23, 20]) {
        expect(ids(splitLanes([...SVC_SPEAKERS, SVC_UNASSIGNED], cols, rowH).columns).flat()).toEqual(order);
      }
    }
  });

  it("gives the same result when Unassigned is listed first", () => {
    const r = splitLanes([SVC_UNASSIGNED, ...SVC_SPEAKERS], 2, 23);
    expect(ids(r.columns)).toEqual([
      ["unassigned", "iris", "silas"],
      ["noor", "otto", "amara", "jonas", "wren"],
    ]);
  });

  it("re-cuts at row 20 (heights 30 + rows×20 + 8)", () => {
    // un 118, iris 318, silas 118, noor 118, otto 98, amara 118, jonas 98, wren 118 = 1104; cut after silas = 554 | 550
    const r = splitLanes([...SVC_SPEAKERS, SVC_UNASSIGNED], 2, 20);
    expect(ids(r.columns)).toEqual([
      ["unassigned", "iris", "silas"],
      ["noor", "otto", "amara", "jonas", "wren"],
    ]);
    expect(r.heights).toEqual([554, 550]);
  });

  it("puts everything in one column when cols = 1 (Unassigned first, order preserved)", () => {
    const r = splitLanes([...SVC_SPEAKERS, SVC_UNASSIGNED], 1, 23);
    expect(ids(r.columns)).toEqual([["unassigned", "iris", "silas", "noor", "otto", "amara", "jonas", "wren"]]);
    expect(r.heights).toEqual([130 + 360 + 130 + 130 + 107 + 130 + 107 + 130]);
  });
});

describe("splitLanes: rules", () => {
  it("omits Unassigned when it has no rows", () => {
    const r = splitLanes([speaker("a", 1), { id: "unassigned", rows: 0, unassigned: true }], 2, 23);
    expect(ids(r.columns).flat()).toEqual(["a"]);
  });

  it("hoists Unassigned to the head of column 1 even when listed last", () => {
    const r = splitLanes([speaker("a", 1), speaker("b", 1), { id: "unassigned", rows: 1, unassigned: true }], 2, 23);
    expect(r.columns[0]?.[0]?.id).toBe("unassigned");
  });

  it("returns one empty column per requested column for no lanes", () => {
    for (const cols of [1, 2, 3]) {
      const r = splitLanes([], cols, 23);
      expect(r.columns).toEqual(Array.from({ length: cols }, () => []));
      expect(r.heights).toEqual(Array.from({ length: cols }, () => 0));
    }
  });

  it("never reorders: a tall lane stays where speaking order puts it", () => {
    const r = splitLanes([speaker("big", 10), speaker("small", 1), { id: "unassigned", rows: 1, unassigned: true }], 2, 23);
    expect(ids(r.columns)).toEqual([["unassigned", "big"], ["small"]]);
    expect(r.heights).toEqual([61 + 268, 61]);
  });
});

describe("splitSequence", () => {
  it("one column keeps everything", () => {
    expect(splitSequence([3, 5, 2], 1)).toEqual({ columns: [[0, 1, 2]], heights: [10] });
  });

  it("two columns cut where the taller column is shortest; a tie takes the later cut (column 1 fills first)", () => {
    expect(splitSequence([5, 5, 5, 5], 2).columns).toEqual([[0, 1], [2, 3]]);
    expect(splitSequence([2, 2, 2], 2).columns).toEqual([[0, 1], [2]]);
    expect(splitSequence([9, 1, 1], 2).columns).toEqual([[0], [1, 2]]);
  });

  it("three columns minimise the tallest run, in order", () => {
    const r = splitSequence([4, 4, 4, 4, 4, 4], 3);
    expect(r.columns).toEqual([[0, 1], [2, 3], [4, 5]]);
    expect(r.heights).toEqual([8, 8, 8]);
    expect(splitSequence([10, 1, 1, 1, 10], 3).columns).toEqual([[0], [1, 2, 3], [4]]);
  });

  it("three columns: a tie gives column 1 the most, then the last column; the middle one takes what is left", () => {
    // 10 | 2 | 8 and 10 | 4 | 6 are both 10 tall at most: the first is taken (the mockup's rule, ux-spec §6.2)
    expect(splitSequence([10, 2, 2, 3, 3], 3).columns).toEqual([[0], [1], [2, 3, 4]]);
    // an equally tall cut with a taller first column wins over one with a taller later column
    expect(splitSequence([6, 4, 2, 4, 6], 3).columns).toEqual([[0, 1], [2], [3, 4]]);
  });

  it("three columns never leave the middle one empty while the last has items", () => {
    expect(splitSequence([7, 7], 3).columns).toEqual([[0], [1], []]);
    expect(splitSequence([7], 3).columns).toEqual([[0], [], []]);
    expect(splitSequence([5, 5, 5], 3).columns).toEqual([[0], [1], [2]]);
  });

  it("three columns with fewer items than columns leave the last ones empty", () => {
    expect(splitSequence([7, 7], 3).columns).toEqual([[0], [1], []]);
    expect(splitSequence([], 3).columns).toEqual([[], [], []]);
  });
});

describe("laneRowHeight", () => {
  const lanes = [...SVC_SPEAKERS, SVC_UNASSIGNED];

  it("is 23 when the tallest column at 23 fits exactly (620)", () => {
    expect(laneRowHeight(lanes, 2, 620)).toBe(23);
  });

  it("is 20 when the tallest column at 23 exceeds availH by one px (619)", () => {
    expect(laneRowHeight(lanes, 2, 619)).toBe(20);
  });

  it("is 23 for a tall window (1080: availH 908)", () => {
    expect(laneRowHeight(lanes, 2, 908)).toBe(23);
  });

  it("is 20 for a short window (1024x640: availH 484)", () => {
    expect(laneRowHeight(lanes, 2, 484)).toBe(20);
  });

  it("depends on the column count (1 column needs 1224 at row 23, 3 columns 490)", () => {
    expect(laneRowHeight(lanes, 1, 1224)).toBe(23);
    expect(laneRowHeight(lanes, 1, 1223)).toBe(20);
    expect(laneRowHeight(lanes, 3, 490)).toBe(23);
    expect(laneRowHeight(lanes, 3, 489)).toBe(20);
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
  // bottomW = mainW − 40; three board columns (mainW ≥ 1720): done 3, backlog 4; side by side at ≥ 1100;
  // done 2 cols and backlog 3 at ≥ 1300; backlog 2 at ≥ 1000
  const cases: Array<[number, boolean, number, number]> = [
    [2128, true, 3, 4],
    [1720, true, 3, 4],
    [1719, true, 2, 3],
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
