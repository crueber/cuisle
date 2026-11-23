// WP-13: chooseDensity (ux-spec §5.5, testing.md §4). Pure; no DOM.
//
// SPEC-NOTE: need(step) is computed by hand from the §5.5 table for the golden group set
//   (QA 3, Review 3, Prog 5, To Do 3, Done 6 = 5 groups, 20 rows):
//   roomy 1120, roomy-side 955, compact 920, compact-side 795, tight-side 715.
//   Boundaries are inclusive (need ≤ H fits), pinned at 795/794 and 715/714.
// SPEC-NOTE: an empty group list needs 0 px, so in "fit" mode it picks step 1 (`roomy`) without scrolling.
import { describe, expect, it } from "vitest";
import { chooseDensity, DENSITY_STEPS, type GroupCount } from "./density";

const GOLDEN: GroupCount[] = [
  { label: "QA", count: 3 },
  { label: "Review", count: 3 },
  { label: "Prog", count: 5 },
  { label: "To Do", count: 3 },
  { label: "Done", count: 6 },
];

describe("chooseDensity: testing.md §4 golden rows", () => {
  it("chooses compact-side without scrolling at H=900 (need 795)", () => {
    const r = chooseDensity(GOLDEN, 900, "fit");
    expect(r.density.step).toBe("compact-side");
    expect(r.need).toBe(795);
    expect(r.scroll).toBe(false);
  });

  it("chooses tight-side without scrolling at H=720 (need 715)", () => {
    const r = chooseDensity(GOLDEN, 720, "fit");
    expect(r.density.step).toBe("tight-side");
    expect(r.need).toBe(715);
    expect(r.scroll).toBe(false);
  });

  it("chooses tight-side and scrolls at H=558", () => {
    const r = chooseDensity(GOLDEN, 558, "fit");
    expect(r.density.step).toBe("tight-side");
    expect(r.need).toBe(715);
    expect(r.scroll).toBe(true);
  });

  it("chooses tight-side and scrolls at H=478 (1024x640 window)", () => {
    const r = chooseDensity(GOLDEN, 478, "fit");
    expect(r.density.step).toBe("tight-side");
    expect(r.scroll).toBe(true);
  });

  it("chooses roomy and scrolls in roomy mode at H=900 (need 1120)", () => {
    const r = chooseDensity(GOLDEN, 900, "roomy");
    expect(r.density.step).toBe("roomy");
    expect(r.need).toBe(1120);
    expect(r.scroll).toBe(true);
  });
});

describe("chooseDensity: ladder boundaries", () => {
  const cases: Array<[number, string, number, boolean]> = [
    // [H, step, need, scroll]
    [1120, "roomy", 1120, false],
    [1119, "roomy-side", 955, false],
    [955, "roomy-side", 955, false],
    [954, "compact", 920, false],
    [920, "compact", 920, false],
    [919, "compact-side", 795, false],
    [795, "compact-side", 795, false],
    [794, "tight-side", 715, false],
    [715, "tight-side", 715, false],
    [714, "tight-side", 715, true],
    [0, "tight-side", 715, true],
  ];
  it.each(cases)("H=%i gives %s (need %i, scroll %s)", (h, step, need, scroll) => {
    const r = chooseDensity(GOLDEN, h, "fit");
    expect(r.density.step).toBe(step);
    expect(r.need).toBe(need);
    expect(r.scroll).toBe(scroll);
  });

  it("returns the matching DENSITY_STEPS entry (row, headCost, label)", () => {
    const r = chooseDensity(GOLDEN, 900, "fit");
    const spec = DENSITY_STEPS.find((s) => s.step === "compact-side");
    expect(r.density).toEqual(spec);
    expect(r.density.row).toBe(38);
    expect(r.density.headCost).toBe(7);
    expect(r.density.label).toBe("compact rows, side headings");
  });

  it("does not scroll in roomy mode when everything fits at step 1", () => {
    const r = chooseDensity(GOLDEN, 1120, "roomy");
    expect(r.density.step).toBe("roomy");
    expect(r.scroll).toBe(false);
  });

  it("keeps roomy mode on roomy even when much taller than needed", () => {
    const r = chooseDensity(GOLDEN, 5000, "roomy");
    expect(r.density.step).toBe("roomy");
    expect(r.need).toBe(1120);
    expect(r.scroll).toBe(false);
  });
});

describe("chooseDensity: small and degenerate inputs", () => {
  it("sums headCost per group plus count x row (single group, roomy)", () => {
    // 1 group, 2 rows: roomy 40 + 2*46 = 132
    const r = chooseDensity([{ label: "To Do", count: 2 }], 132, "fit");
    expect(r.density.step).toBe("roomy");
    expect(r.need).toBe(132);
    expect(r.scroll).toBe(false);
  });

  it("falls to roomy-side when only the stacked heading no longer fits", () => {
    // 1 group, 2 rows: roomy 132, roomy-side 7 + 92 = 99
    const r = chooseDensity([{ label: "To Do", count: 2 }], 131, "fit");
    expect(r.density.step).toBe("roomy-side");
    expect(r.need).toBe(99);
  });

  it("picks roomy with no scroll for an empty group list", () => {
    const r = chooseDensity([], 100, "fit");
    expect(r.density.step).toBe("roomy");
    expect(r.need).toBe(0);
    expect(r.scroll).toBe(false);
  });
});
