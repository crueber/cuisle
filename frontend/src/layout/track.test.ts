// WP-13: pulse track geometry (ux-spec §7, testing.md §4). Positions are in DAY UNITS. Pure; no DOM.
//
// SPEC-NOTE: segs are [from, to) (width = to − from, as in the golden case); spans are INCLUSIVE (width = to − from + 1).
// SPEC-NOTE (PR vs commit when a toggle is off): the PR diamond replaces the commit tick only when the diamond is
//   actually drawn. With showPRs = false a day that has both shows the commit tick (if showCommits). With
//   showCommits = false and showPRs = true the day shows the diamond.
// SPEC-NOTE: ux-spec §7 says a comment tick is drawn alongside commit ticks (only PR replaces commit), so a day with
//   commits and comments yields both a commit box and a comment box. A day with commits, PRs and comments yields a PR
//   box and a comment box (no commit).
// SPEC-NOTE: markBoxes does not specify box order within a day; tests sort by (day, kind) before comparing.
// SPEC-NOTE (PR `bottom`): the frozen MarkBox comment says "bottom: … (commit 2, pr 12 centre, comment 8)". The PR
//   `bottom` assertion (isolated in its own test) takes the contract literally: bottom = 12. ux-spec §7 says the diamond
//   is "centred 12px above the segment top"; if the orchestrator prefers CSS-bottom = 8 (centre 12 with height 8), change
//   only that one test.
// SPEC-NOTE: segTop values 28 / 22 / 18 are used for mark heights (units 6 / 4 / 3); the prototype's max(3, …) clamp is
//   not in the spec and is not tested.
import { describe, expect, it } from "vitest";
import type { PulseMark, PulseSeg, PulseSpan } from "../api/types";
import {
  axisLabelMode,
  commitLevel,
  markBoxes,
  segBoxes,
  spanBoxes,
  trackGeometry,
  type MarkBox,
  type MarkOptions,
} from "./track";

describe("trackGeometry", () => {
  const cases: Array<[number, number, number]> = [
    // [rowH, segH, segTop]
    [46, 12, 28],
    [44, 12, 26],
    [40, 12, 22],
    [39, 10, 23],
    [38, 10, 22],
    [34, 10, 18],
    [32, 10, 16],
    [31, 8, 17],
    [30, 8, 16],
    [20, 8, 6],
  ];
  it.each(cases)("row %i -> segH %i, segTop %i", (row, segH, segTop) => {
    expect(trackGeometry(row)).toEqual({ segH, segTop });
  });

  it("uses 11 / 6 for lane rows regardless of the row height", () => {
    expect(trackGeometry(23, true)).toEqual({ segH: 11, segTop: 6 });
    expect(trackGeometry(20, true)).toEqual({ segH: 11, segTop: 6 });
    expect(trackGeometry(46, true)).toEqual({ segH: 11, segTop: 6 });
  });

  it("treats lane = false like the default", () => {
    expect(trackGeometry(38, false)).toEqual({ segH: 10, segTop: 22 });
  });
});

describe("segBoxes", () => {
  it("golden: history [todo 5, prog 6, review 3] with N = 21 starts at day 7 with widths 5, 6, 3", () => {
    const segs: PulseSeg[] = [
      { lane: "todo", from: 7, to: 12 },
      { lane: "prog", from: 12, to: 18 },
      { lane: "review", from: 18, to: 21 },
    ];
    expect(segBoxes(segs)).toEqual([
      { lane: "todo", x: 7, w: 5 },
      { lane: "prog", x: 12, w: 6 },
      { lane: "review", x: 18, w: 3 },
    ]);
  });

  it("makes the last segment end at day N (x + w = 21)", () => {
    const boxes = segBoxes([
      { lane: "todo", from: 7, to: 12 },
      { lane: "review", from: 12, to: 21 },
    ]);
    const last = boxes[boxes.length - 1];
    expect((last?.x ?? 0) + (last?.w ?? 0)).toBe(21);
  });

  it("shows a ticket already in progress from the left edge (from 0)", () => {
    expect(segBoxes([{ lane: "prog", from: 0, to: 21 }])).toEqual([{ lane: "prog", x: 0, w: 21 }]);
  });

  it("returns an empty list for no segments and preserves order", () => {
    expect(segBoxes([])).toEqual([]);
    const boxes = segBoxes([
      { lane: "qa", from: 15, to: 18 },
      { lane: "done", from: 18, to: 21 },
    ]);
    expect(boxes.map((b) => b.lane)).toEqual(["qa", "done"]);
  });
});

describe("spanBoxes", () => {
  it("is inclusive: width = To − From + 1", () => {
    const spans: PulseSpan[] = [
      { from: 3, to: 5 },
      { from: 20, to: 20 },
      { from: 0, to: 20 },
    ];
    expect(spanBoxes(spans)).toEqual([
      { x: 3, w: 3 },
      { x: 20, w: 1 },
      { x: 0, w: 21 },
    ]);
  });

  it("returns an empty list for no spans", () => {
    expect(spanBoxes([])).toEqual([]);
  });
});

describe("commitLevel", () => {
  it.each([
    [0, 0],
    [1, 1],
    [2, 2],
    [3, 2],
    [4, 3],
    [5, 3],
    [20, 3],
  ])("commitLevel(%i) is %i", (count, level) => {
    expect(commitLevel(count)).toBe(level);
  });
});

describe("markBoxes", () => {
  const ALL_ON = (segTop: number): MarkOptions => ({ showCommits: true, showPRs: true, showComments: true, segTop });
  const mark = (day: number, commits = 0, prs = 0, comments = 0): PulseMark => ({ day, commits, prs, comments });
  const sorted = (boxes: MarkBox[]) =>
    [...boxes].sort((a, b) => a.day - b.day || a.kind.localeCompare(b.kind));

  describe("commit ticks", () => {
    // unit = floor((segTop − 9) / 3); height = 3 + level × unit; bottom 2
    const table: Array<[number, number, number, number, number]> = [
      // [segTop, commits, level, height, unit]
      [28, 1, 1, 9, 6],
      [28, 2, 2, 15, 6],
      [28, 3, 2, 15, 6],
      [28, 4, 3, 21, 6],
      [28, 9, 3, 21, 6],
      [22, 1, 1, 7, 4],
      [22, 2, 2, 11, 4],
      [22, 4, 3, 15, 4],
      [18, 1, 1, 6, 3],
      [18, 2, 2, 9, 3],
      [18, 4, 3, 12, 3],
    ];
    it.each(table)("segTop %i, %i commits -> level %i, height %i (unit %i)", (segTop, commits, level, height) => {
      expect(markBoxes([mark(5, commits)], ALL_ON(segTop))).toEqual([{ day: 5, kind: "commit", level, height, bottom: 2 }]);
    });
  });

  it("draws a PR diamond 8px tall at level 0", () => {
    const boxes = markBoxes([mark(7, 0, 2)], ALL_ON(28));
    expect(boxes).toHaveLength(1);
    expect(boxes[0]).toMatchObject({ day: 7, kind: "pr", level: 0, height: 8 });
  });

  it("places the PR diamond per the contract comment (bottom 12)", () => {
    expect(markBoxes([mark(7, 0, 1)], ALL_ON(28))[0]?.bottom).toBe(12);
  });

  it("draws a comment tick 6px tall, 8px above the segment, at level 0", () => {
    expect(markBoxes([mark(3, 0, 0, 4)], ALL_ON(28))).toEqual([{ day: 3, kind: "comment", level: 0, height: 6, bottom: 8 }]);
  });

  it("replaces the commit tick with the PR diamond on a day with both", () => {
    const boxes = markBoxes([mark(9, 3, 1)], ALL_ON(28));
    expect(boxes.map((b) => b.kind)).toEqual(["pr"]);
    expect(boxes[0]?.day).toBe(9);
  });

  it("keeps the commit tick on a day with both when PR marks are hidden", () => {
    const boxes = markBoxes([mark(9, 3, 1)], { ...ALL_ON(28), showPRs: false });
    expect(boxes).toEqual([{ day: 9, kind: "commit", level: 2, height: 15, bottom: 2 }]);
  });

  it("shows the PR diamond on a day with both when commit marks are hidden", () => {
    const boxes = markBoxes([mark(9, 3, 1)], { ...ALL_ON(28), showCommits: false });
    expect(boxes.map((b) => b.kind)).toEqual(["pr"]);
  });

  it("draws commit and comment ticks together on the same day", () => {
    const boxes = sorted(markBoxes([mark(4, 1, 0, 2)], ALL_ON(28)));
    expect(boxes.map((b) => b.kind)).toEqual(["comment", "commit"]);
    expect(boxes.every((b) => b.day === 4)).toBe(true);
  });

  it("draws PR and comment (no commit) on a day with all three", () => {
    const boxes = sorted(markBoxes([mark(4, 5, 2, 1)], ALL_ON(28)));
    expect(boxes.map((b) => b.kind)).toEqual(["comment", "pr"]);
  });

  describe("toggles", () => {
    const m = [mark(1, 2), mark(2, 0, 1), mark(3, 0, 0, 1)];

    it("returns nothing when every kind is hidden", () => {
      expect(markBoxes(m, { showCommits: false, showPRs: false, showComments: false, segTop: 28 })).toEqual([]);
    });

    it("hides commits only", () => {
      const boxes = sorted(markBoxes(m, { ...ALL_ON(28), showCommits: false }));
      expect(boxes.map((b) => b.kind)).toEqual(["pr", "comment"]);
    });

    it("hides PRs only", () => {
      const boxes = sorted(markBoxes(m, { ...ALL_ON(28), showPRs: false }));
      expect(boxes.map((b) => b.kind)).toEqual(["commit", "comment"]);
    });

    it("hides comments only", () => {
      const boxes = sorted(markBoxes(m, { ...ALL_ON(28), showComments: false }));
      expect(boxes.map((b) => b.kind)).toEqual(["commit", "pr"]);
    });
  });

  it("emits nothing for days with zero activity or an empty list", () => {
    expect(markBoxes([], ALL_ON(28))).toEqual([]);
    expect(markBoxes([mark(2)], ALL_ON(28))).toEqual([]);
  });

  it("keeps each box on its own day across several days", () => {
    const boxes = sorted(markBoxes([mark(2, 1), mark(8, 4), mark(20, 0, 3)], ALL_ON(28)));
    expect(boxes.map((b) => [b.day, b.kind])).toEqual([
      [2, "commit"],
      [8, "commit"],
      [20, "pr"],
    ]);
  });
});

describe("axisLabelMode", () => {
  it("labels every day when a day is at least 18px wide", () => {
    expect(axisLabelMode(600, 21)).toBe("all");
    expect(axisLabelMode(440, 21)).toBe("all");
    expect(axisLabelMode(378, 21)).toBe("all"); // exactly 18
  });

  it("labels Mondays only when a day is narrower than 18px", () => {
    expect(axisLabelMode(377, 21)).toBe("mondays");
    expect(axisLabelMode(300, 21)).toBe("mondays");
  });

  it("works for other window lengths", () => {
    expect(axisLabelMode(300, 14)).toBe("all"); // 21.4 px
    expect(axisLabelMode(300, 28)).toBe("mondays"); // 10.7 px
  });
});
