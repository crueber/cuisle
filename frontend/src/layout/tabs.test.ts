// WP-13: tabMode, tabsAvailableWidth, visibleTabs (ux-spec §4, testing.md §4).
//
// SPEC-NOTE (TabMeasure numbers): ux-spec §4 gives the tab anatomy but no pixel widths, so the tests use the prototype's
//   measuring model (design/reference/prototype-logic.js `fullW` / `keyW`), as plain helpers below:
//     full(name, key) = 32 + 26 + 10 + 9.4·len(name) + 10 + 8.6·len(key)     (padding, number box, gaps, name, key)
//     key(key)        = 30 + 26 + 8 + 11·len(key)
//   Available width = header − wordmark block − right cluster − 56 (tabsAvailableWidth). The prototype's wordmark block
//   is 28 + 160 = 188; the right cluster is 24 + Configure (140, or 48 when vw < 1280) + date (140 when vw ≥ 1360)
//   + sync label (170 when vw ≥ 1700). The five sample standups (MOB, WEB, DATA, GRO, PLAT) then give
//   Σfull = 1166.8 and Σkey = 496, and available widths 1202 (1920), 892 (1440), 708 (1024).
// SPEC-NOTE (visible tabs only): ux-spec §4 says more than 7 standups show the first 7 plus "+N more". tabMode's doc
//   comment says only visible tabs count, so tabMode itself counts only the FIRST 7 entries of `tabs` and adds `moreW`
//   (the "+N more ▾" button) to the sum in both full and key mode. A caller that already passes ≤ 7 visible tabs
//   (after the active swap) is unaffected. `moreW` is added whenever it is given.
// SPEC-NOTE: sums compare with ≤ (fits when Σ === availW).
import { describe, expect, it } from "vitest";
import { tabMode, tabsAvailableWidth, visibleTabs, type TabMeasure } from "./tabs";

const measure = (name: string, key: string): TabMeasure => ({
  full: 32 + 26 + 10 + name.length * 9.4 + 10 + key.length * 8.6,
  key: 30 + 26 + 8 + key.length * 11,
});

const FIVE: TabMeasure[] = [
  measure("Saffron Mobile", "MOB"),
  measure("Web & Checkout", "WEB"),
  measure("Recipe Data", "DATA"),
  measure("Growth", "GRO"),
  measure("Platform", "PLAT"),
];

const WORDMARK = 188;
const rightCluster = (vw: number) => 24 + (vw >= 1280 ? 140 : 48) + (vw >= 1360 ? 140 : 0) + (vw >= 1700 ? 170 : 0);
const avail = (vw: number) => tabsAvailableWidth(vw, WORDMARK, rightCluster(vw));

describe("tabsAvailableWidth", () => {
  it("is header − wordmark block − right cluster − 56", () => {
    expect(tabsAvailableWidth(1920, 188, 474)).toBe(1202);
    expect(tabsAvailableWidth(1440, 188, 304)).toBe(892);
    expect(tabsAvailableWidth(1024, 188, 72)).toBe(708);
  });

  it("reserves exactly 56 px for the + button", () => {
    expect(tabsAvailableWidth(1000, 0, 0)).toBe(944);
  });

  it("matches the helper model at the three golden widths", () => {
    expect(avail(1920)).toBe(1202);
    expect(avail(1440)).toBe(892);
    expect(avail(1024)).toBe(708);
  });
});

describe("tabMode: testing.md §4 golden rows", () => {
  it("is full for 5 standups at 1920 (Σfull 1166.8 ≤ 1202)", () => {
    expect(tabMode(FIVE, avail(1920))).toBe("full");
  });

  it("is key for 5 standups at 1440 (Σfull > 892, Σkey 496 fits)", () => {
    expect(tabMode(FIVE, avail(1440))).toBe("key");
  });

  it("is key for 5 standups at 1024 (Σkey 496 ≤ 708)", () => {
    expect(tabMode(FIVE, avail(1024))).toBe("key");
  });

  it("is menu for 9 standups at 1024 when the visible keys do not fit", () => {
    // 9 standups with 4-letter keys: 108 px each; the first 7 = 756 > 708, plus the "+2 more" button.
    const nine = Array.from({ length: 9 }, (_, i) => measure(`Standup ${i + 1}`, "ABCD"));
    expect(tabMode(nine, avail(1024), 90)).toBe("menu");
  });
});

describe("tabMode: thresholds", () => {
  const tabs: TabMeasure[] = [
    { full: 100, key: 40 },
    { full: 100, key: 40 },
  ];

  it("is full when Σfull equals availW exactly", () => {
    expect(tabMode(tabs, 200)).toBe("full");
  });

  it("is key one px below Σfull", () => {
    expect(tabMode(tabs, 199)).toBe("key");
  });

  it("is key when Σkey equals availW exactly", () => {
    expect(tabMode(tabs, 80)).toBe("key");
  });

  it("is menu one px below Σkey", () => {
    expect(tabMode(tabs, 79)).toBe("menu");
  });

  it("is menu when there is no room at all", () => {
    expect(tabMode(tabs, 0)).toBe("menu");
  });

  it("adds moreW to the full sum", () => {
    expect(tabMode(tabs, 200, 0)).toBe("full");
    expect(tabMode(tabs, 200, 1)).toBe("key");
    expect(tabMode(tabs, 230, 30)).toBe("full");
  });

  it("adds moreW to the key sum", () => {
    expect(tabMode(tabs, 100, 20)).toBe("key");
    expect(tabMode(tabs, 99, 20)).toBe("menu");
  });
});

describe("tabMode: more than 7 standups counts only the 7 visible tabs", () => {
  const key100 = (n: number): TabMeasure[] => Array.from({ length: n }, () => ({ full: 300, key: 100 }));

  it("ignores tabs beyond the seventh (9 tabs: 7 × 100 = 700 fits 708)", () => {
    expect(tabMode(key100(9), 708)).toBe("key");
  });

  it("is menu when the seven visible keys plus moreW exceed availW", () => {
    expect(tabMode(key100(9), 708, 9)).toBe("menu");
    expect(tabMode(key100(9), 708, 8)).toBe("key");
  });

  it("uses only the first 7 for the full sum (7 × 300 = 2100)", () => {
    expect(tabMode(key100(9), 2100)).toBe("full");
    expect(tabMode(key100(9), 2099)).toBe("key");
  });
});

describe("visibleTabs", () => {
  it("shows every tab when there are 7 or fewer", () => {
    expect(visibleTabs(5, 2)).toEqual({ indices: [0, 1, 2, 3, 4], more: 0 });
    expect(visibleTabs(7, 6)).toEqual({ indices: [0, 1, 2, 3, 4, 5, 6], more: 0 });
    expect(visibleTabs(1, 0)).toEqual({ indices: [0], more: 0 });
  });

  it("shows nothing for zero standups", () => {
    expect(visibleTabs(0, 0)).toEqual({ indices: [], more: 0 });
  });

  it("shows the first 7 and '+2 more' for 9 standups when the active one is visible", () => {
    expect(visibleTabs(9, 2)).toEqual({ indices: [0, 1, 2, 3, 4, 5, 6], more: 2 });
    expect(visibleTabs(9, 6)).toEqual({ indices: [0, 1, 2, 3, 4, 5, 6], more: 2 });
  });

  it("swaps a hidden active tab into the last visible slot", () => {
    expect(visibleTabs(9, 8)).toEqual({ indices: [0, 1, 2, 3, 4, 5, 8], more: 2 });
    expect(visibleTabs(9, 7)).toEqual({ indices: [0, 1, 2, 3, 4, 5, 7], more: 2 });
  });

  it("handles exactly 8 standups with the eighth active", () => {
    expect(visibleTabs(8, 7)).toEqual({ indices: [0, 1, 2, 3, 4, 5, 7], more: 1 });
  });

  it("honours a custom max", () => {
    expect(visibleTabs(5, 4, 3)).toEqual({ indices: [0, 1, 4], more: 2 });
    expect(visibleTabs(5, 1, 3)).toEqual({ indices: [0, 1, 2], more: 2 });
  });

  it("defaults to the first max tabs when there is no active standup (index -1)", () => {
    expect(visibleTabs(9, -1)).toEqual({ indices: [0, 1, 2, 3, 4, 5, 6], more: 2 });
  });
});
