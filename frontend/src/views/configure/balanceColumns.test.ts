import { describe, expect, it } from "vitest";
import { balanceColumns } from "./ConfigureScreen";

type Id = Parameters<typeof balanceColumns>[0][number];
const ids = (n: number) => Array.from({ length: n }, (_, i) => `s${i}` as unknown as Id);

describe("balanceColumns", () => {
  it("placesTallestFirst_ThenKeepsTheUsualOrderInsideEachColumn_FirstSectionTopLeft", () => {
    const order = ids(5);
    const h = new Map([["s0", 100], ["s1", 500], ["s2", 100], ["s3", 300], ["s4", 100]]);
    // s1 → A (500), s3 → B (300), s0 → B (400), s2 → B (500), s4 → A (600); B holds s0, so it comes first.
    expect(balanceColumns(order, h, 2, 0)).toEqual([["s0", "s2", "s3"], ["s1", "s4"]]);
  });

  it("countsTheGapBetweenSections", () => {
    const order = ids(3);
    const h = new Map([["s0", 100], ["s1", 100], ["s2", 100]]);
    // s0 → c0 (100), s1 → c1 (100), s2 → c0 (100 + 24 + 100)
    expect(balanceColumns(order, h, 2, 24)).toEqual([["s0", "s2"], ["s1"]]);
  });

  it("beatsInOrderPlacementWhenABigSectionComesLast", () => {
    const order = ids(4);
    const h = new Map([["s0", 100], ["s1", 100], ["s2", 100], ["s3", 300]]);
    expect(balanceColumns(order, h, 2, 0)).toEqual([["s0", "s1", "s2"], ["s3"]]);
  });

  it("keepsEveryColumnWithinOneSectionOfTheOthers", () => {
    const order = ids(11);
    const heights = [420, 610, 380, 300, 260, 720, 120, 230, 150, 170, 90];
    const h = new Map(heights.map((v, i) => [`s${i}`, v]));
    for (const n of [2, 3]) {
      const cols = balanceColumns(order, h, n, 24)!;
      expect(cols.flat().sort()).toEqual([...order].sort());
      const tot = cols.map((c) => c.reduce((a, id, i) => a + (i ? 24 : 0) + (h.get(id as unknown as string) ?? 0), 0));
      expect(Math.max(...tot) - Math.min(...tot)).toBeLessThanOrEqual(Math.max(...heights) + 24);
    }
  });

  it("returnsNullWhenAHeightIsUnknownOrThereIsOneColumn", () => {
    const order = ids(2);
    expect(balanceColumns(order, new Map([["s0", 10], ["s1", 0]]), 2, 0)).toBeNull();
    expect(balanceColumns(order, new Map([["s0", 10], ["s1", 10]]), 1, 0)).toBeNull();
  });
});
