// WP-13: belowFold, boardBelow, atEnd (ux-spec §5.6, §6.4, testing.md §4).
//
// SPEC-NOTE: "past the fold" is strict (bottom edge > foldY), as in the prototype (`y > fold`). A row whose bottom edge
//   equals foldY is NOT below. boardBelow uses the same strictness for header tops (top > foldY + scrollTop).
// SPEC-NOTE: boardBelow's BelowItem.count is undefined by the contract, so tests assert labels and summary only.
// SPEC-NOTE: summary text is items joined with " · " followed by " below" (person: "<n> <label>", board: "<label>").
import { describe, expect, it } from "vitest";
import { atEnd, belowFold, boardBelow, type BoardSection } from "./below";
import { DENSITY_STEPS, type DensitySpec, type DensityStep, type GroupCount } from "./density";

function stepSpec(step: DensityStep): DensitySpec {
  const spec = DENSITY_STEPS.find((s) => s.step === step);
  if (!spec) throw new Error(`unknown density step ${step}`);
  return spec;
}

const TIGHT = stepSpec("tight-side"); // row 34, headCost 7
const GOLDEN: GroupCount[] = [
  { label: "QA", count: 3 },
  { label: "Review", count: 3 },
  { label: "Prog", count: 5 },
  { label: "To Do", count: 3 },
  { label: "Done", count: 6 },
];
// Row bottoms at tight-side with scrollTop 0:
//   QA 41/75/109, Review 150/184/218, Prog 259..395, To Do 436/470/504, Done 545/579/613/647/681/715.

describe("belowFold (person view)", () => {
  it("reports '2 To Do · 6 Done below' at fold 442 (H 478 - 36)", () => {
    const r = belowFold(GOLDEN, TIGHT, 442);
    expect(r.summary).toBe("2 To Do · 6 Done below");
    expect(r.items).toEqual([
      { label: "To Do", count: 2 },
      { label: "Done", count: 6 },
    ]);
  });

  it("counts the whole To Do group when the fold is above its first row (fold 435)", () => {
    const r = belowFold(GOLDEN, TIGHT, 435);
    expect(r.summary).toBe("3 To Do · 6 Done below");
  });

  it("treats a row whose bottom edge equals the fold as visible (strict >)", () => {
    // To Do first row bottom is exactly 436
    expect(belowFold(GOLDEN, TIGHT, 436).summary).toBe("2 To Do · 6 Done below");
    expect(belowFold(GOLDEN, TIGHT, 435).summary).toBe("3 To Do · 6 Done below");
  });

  it("lists only Done when the fold is past To Do (fold 522)", () => {
    const r = belowFold(GOLDEN, TIGHT, 522);
    expect(r.summary).toBe("6 Done below");
    expect(r.items).toEqual([{ label: "Done", count: 6 }]);
  });

  it("drops one Done row when the fold sits exactly on its bottom edge (fold 545)", () => {
    expect(belowFold(GOLDEN, TIGHT, 545).summary).toBe("5 Done below");
  });

  it("returns an empty summary and no items when nothing is below (fold 715)", () => {
    const r = belowFold(GOLDEN, TIGHT, 715);
    expect(r.summary).toBe("");
    expect(r.items).toEqual([]);
  });

  it("returns an empty summary for no groups", () => {
    expect(belowFold([], TIGHT, 100)).toEqual({ items: [], summary: "" });
  });

  it("subtracts scrollTop from every position (scrollTop 100 keeps all Done below fold 442)", () => {
    expect(belowFold(GOLDEN, TIGHT, 442, 100).summary).toBe("6 Done below");
  });

  it("shrinks the summary as the list scrolls (scrollTop 230 leaves 2 Done)", () => {
    const r = belowFold(GOLDEN, TIGHT, 442, 230);
    expect(r.summary).toBe("2 Done below");
    expect(r.items).toEqual([{ label: "Done", count: 2 }]);
  });

  it("is empty at the very end of the list (scrollTop 300, total 715)", () => {
    expect(belowFold(GOLDEN, TIGHT, 442, 300).summary).toBe("");
  });

  it("uses the density's headCost and row height (compact-side: row 38, head 7)", () => {
    const compactSide = stepSpec("compact-side");
    // single group of 3: bottoms 45, 83, 121; fold 83 => 1 below
    const r = belowFold([{ label: "Done", count: 3 }], compactSide, 83);
    expect(r.summary).toBe("1 Done below");
  });

  it("uses stacked head cost for stacked steps (compact: head 32, row 38)", () => {
    const compact = stepSpec("compact");
    // bottoms 70, 108; fold 70 => 1 below
    const r = belowFold([{ label: "To Do", count: 2 }], compact, 70);
    expect(r.summary).toBe("1 To Do below");
  });

  it("skips empty groups", () => {
    const r = belowFold(
      [
        { label: "QA", count: 0 },
        { label: "Done", count: 2 },
      ],
      TIGHT,
      0,
    );
    expect(r.items).toEqual([{ label: "Done", count: 2 }]);
    expect(r.summary).toBe("2 Done below");
  });
});

describe("boardBelow (whole board)", () => {
  const SECTIONS: BoardSection[] = [
    { label: "Unassigned", top: 0 },
    { label: "Iris", top: 0 },
    { label: "Silas", top: 400 },
    { label: "Noor", top: 520 },
    { label: "Done (15)", top: 700 },
    { label: "Backlog (12)", top: 900 },
  ];

  it("joins sections whose header top is past the fold: 'Silas · Noor · Done (15) · Backlog (12) below'", () => {
    const r = boardBelow(SECTIONS, 300);
    expect(r.summary).toBe("Silas · Noor · Done (15) · Backlog (12) below");
    expect(r.items.map((i) => i.label)).toEqual(["Silas", "Noor", "Done (15)", "Backlog (12)"]);
  });

  it("matches the ux-spec example 'Iris · Silas · Done (15) below'", () => {
    const r = boardBelow(
      [
        { label: "Amara", top: 0 },
        { label: "Iris", top: 500 },
        { label: "Silas", top: 600 },
        { label: "Done (15)", top: 800 },
      ],
      400,
    );
    expect(r.summary).toBe("Iris · Silas · Done (15) below");
  });

  it("adds scrollTop to the fold", () => {
    const r = boardBelow(SECTIONS, 300, 450);
    // fold position = 750 => only Backlog (900) is past it
    expect(r.summary).toBe("Backlog (12) below");
    expect(r.items.map((i) => i.label)).toEqual(["Backlog (12)"]);
  });

  it("treats a header whose top equals fold + scrollTop as visible (strict >)", () => {
    expect(boardBelow(SECTIONS, 400).items.map((i) => i.label)).toEqual(["Noor", "Done (15)", "Backlog (12)"]);
  });

  it("is empty when every header is above the fold", () => {
    const r = boardBelow(SECTIONS, 5000);
    expect(r.summary).toBe("");
    expect(r.items).toEqual([]);
  });

  it("is empty for no sections", () => {
    expect(boardBelow([], 100)).toEqual({ items: [], summary: "" });
  });

  it("preserves the given order", () => {
    const r = boardBelow(
      [
        { label: "B", top: 900 },
        { label: "A", top: 800 },
      ],
      100,
    );
    expect(r.items.map((i) => i.label)).toEqual(["B", "A"]);
    expect(r.summary).toBe("B · A below");
  });
});

describe("atEnd", () => {
  const cases: Array<[number, number, number, boolean, string]> = [
    [0, 400, 406, true, "exactly at the -6 tolerance"],
    [0, 400, 407, false, "one px beyond the tolerance"],
    [100, 300, 400, true, "scrolled to the exact bottom"],
    [99, 300, 400, true, "within tolerance above the bottom"],
    [94, 300, 400, true, "6 px above the bottom"],
    [93, 300, 400, false, "7 px above the bottom"],
    [0, 500, 400, true, "content shorter than the viewport"],
    [0, 400, 400, true, "content exactly the viewport"],
    [0, 100, 1000, false, "top of a long list"],
  ];
  it.each(cases)("atEnd(%i, %i, %i) is %s (%s)", (st, ch, sh, expected) => {
    expect(atEnd(st, ch, sh)).toBe(expected);
  });
});
