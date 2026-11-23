// WP-13: responsive thresholds (ux-spec §2, §3, §9.2, §11). Inputs are CSS px already divided by --scale.
//
// SPEC-NOTE: mainWidth follows the frozen doc comment, vw − railWidth − 2 × padding.x, with no extra rail-border px
//   (the prototype subtracted 2 more). 1920 -> 1920 − 208 − 64 = 1648; 1440 -> 1440 − 208 − 40 = 1192.
// SPEC-NOTE: every threshold is inclusive (≥), tested at t and t − 1.
import { describe, expect, it } from "vitest";
import {
  configureColumns,
  headerCollapse,
  legendMode,
  mainPadding,
  mainWidth,
  railMode,
  railWidth,
  titleSizes,
  trackWidth,
  unscale,
} from "./responsive";

describe("railMode / railWidth", () => {
  it.each([
    [1920, "full"],
    [1360, "full"],
    [1359, "compact"],
    [1024, "compact"],
  ])("railMode(%i) is %s", (vw, mode) => {
    expect(railMode(vw)).toBe(mode);
  });

  it("railWidth is 208 full and 76 compact", () => {
    expect(railWidth("full")).toBe(208);
    expect(railWidth("compact")).toBe(76);
  });
});

describe("legendMode", () => {
  it.each([
    [1800, "full"],
    [1560, "full"],
    [1559, "short"],
    [1300, "short"],
    [1299, "button"],
    [900, "button"],
  ])("legendMode(%i) is %s", (w, mode) => {
    expect(legendMode(w)).toBe(mode);
  });
});

describe("trackWidth", () => {
  it.each([
    [1648, 600],
    [1500, 600],
    [1499, 440],
    [1150, 440],
    [1149, 300],
    [800, 300],
  ])("trackWidth(%i) is %i", (w, t) => {
    expect(trackWidth(w)).toBe(t);
  });
});

describe("configureColumns", () => {
  it.each([
    [1700, 3],
    [1500, 3],
    [1499, 2],
    [960, 2],
    [959, 1],
    [600, 1],
  ])("configureColumns(%i) is %i", (w, n) => {
    expect(configureColumns(w)).toBe(n);
  });
});

describe("mainPadding", () => {
  it("is 32 / 20 / 10 for a wide, tall window", () => {
    expect(mainPadding(1920, 1080)).toEqual({ x: 32, top: 20, bottom: 10 });
    expect(mainPadding(1600, 800)).toEqual({ x: 32, top: 20, bottom: 10 });
  });

  it("is 20 / 12 / 10 for a narrow, short window", () => {
    expect(mainPadding(1599, 799)).toEqual({ x: 20, top: 12, bottom: 10 });
    expect(mainPadding(1024, 640)).toEqual({ x: 20, top: 12, bottom: 10 });
  });

  it("decides x and top independently", () => {
    expect(mainPadding(1599, 800)).toEqual({ x: 20, top: 20, bottom: 10 });
    expect(mainPadding(1600, 799)).toEqual({ x: 32, top: 12, bottom: 10 });
  });
});

describe("mainWidth", () => {
  it.each([
    [1920, 1648], // 1920 − 208 − 64
    [1600, 1328], // 1600 − 208 − 64
    [1599, 1351], // 1599 − 208 − 40
    [1440, 1192], // 1440 − 208 − 40
    [1360, 1112], // 1360 − 208 − 40
    [1359, 1243], // 1359 − 76 − 40
    [1024, 908], // 1024 − 76 − 40
  ])("mainWidth(%i) is %i", (vw, w) => {
    expect(mainWidth(vw)).toBe(w);
  });
});

describe("titleSizes", () => {
  it("is 36 / 30 / 56 / 48 when vh ≥ 800", () => {
    expect(titleSizes(1080)).toEqual({ person: 36, board: 30, personRow: 56, boardRow: 48 });
    expect(titleSizes(800)).toEqual({ person: 36, board: 30, personRow: 56, boardRow: 48 });
  });

  it("is 28 / 24 / 46 / 40 when vh < 800", () => {
    expect(titleSizes(799)).toEqual({ person: 28, board: 24, personRow: 46, boardRow: 40 });
    expect(titleSizes(640)).toEqual({ person: 28, board: 24, personRow: 46, boardRow: 40 });
  });
});

describe("headerCollapse", () => {
  it.each([
    [1920, { syncLabel: true, date: true, configureLabel: true }],
    [1700, { syncLabel: true, date: true, configureLabel: true }],
    [1699, { syncLabel: false, date: true, configureLabel: true }],
    [1360, { syncLabel: false, date: true, configureLabel: true }],
    [1359, { syncLabel: false, date: false, configureLabel: true }],
    [1280, { syncLabel: false, date: false, configureLabel: true }],
    [1279, { syncLabel: false, date: false, configureLabel: false }],
    [1024, { syncLabel: false, date: false, configureLabel: false }],
  ])("headerCollapse(%i)", (vw, expected) => {
    expect(headerCollapse(vw)).toEqual(expected);
  });
});

describe("unscale", () => {
  it("divides by the text scale", () => {
    expect(unscale(1920, 1.25)).toBe(1536);
    expect(unscale(1000, 1)).toBe(1000);
    expect(unscale(125, 1.25)).toBe(100);
  });
});
