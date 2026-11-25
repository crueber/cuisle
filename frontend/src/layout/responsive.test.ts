// WP-13: responsive thresholds (ux-spec §2, §3, §9.2, §11). Inputs are CSS px already divided by --scale.
//
// SPEC-NOTE: mainWidth follows the frozen doc comment, vw − railWidth − 2 × padding.x, with no extra rail-border px
//   (the prototype subtracted 2 more). 1920 -> 1920 − 208 − 64 = 1648; 1440 -> 1440 − 208 − 40 = 1192.
// SPEC-NOTE: every threshold is inclusive (≥), tested at t and t − 1.
import { describe, expect, it } from "vitest";
import { boardColumns } from "./board";
import {
  personTrackWidth,
  contentViewport,
  HEADER_H,
  configureColumns,
  headerCollapse,
  legendExtra,
  boardLegendMode,
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

  // F15: the zoom control beside the legend raises only the short threshold; the full legend keeps its own.
  it("controlRoom raises the short threshold only", () => {
    expect(legendMode(1560, 0, 150)).toBe("full");
    expect(legendMode(1559, 0, 150)).toBe("short");
    expect(legendMode(1450, 0, 150)).toBe("short");
    expect(legendMode(1449, 0, 150)).toBe("button");
    expect(boardLegendMode(1648, 0, 150)).toBe("short");
    expect(boardLegendMode(1449, 0, 150)).toBe("button");
  });

  // Lanes the user names make the legend wider: each threshold rises by legendExtra (none for the built-in lanes).
  const L = (lane: string, label: string) => ({ lane, label });
  const BUILTIN = [L("qa", "QA"), L("review", "In Review"), L("prog", "In Progress"), L("todo", "To Do"), L("done", "Done")];
  it("legendExtra is 0 for the four built-in lanes (Done is not in the legend)", () => {
    expect(legendExtra(BUILTIN)).toBe(0);
  });
  it("legendExtra grows with custom lanes and longer names, and moves the thresholds", () => {
    const extra = legendExtra([...BUILTIN, L("c1", "Peer Review")]);
    expect(extra).toBe(11 * 8 + 45);
    expect(legendMode(1560, extra)).toBe("short");
    expect(legendMode(1560 + extra, extra)).toBe("full");
    const many = Array.from({ length: 12 }, (_, i) => L(`c${i + 1}`, "x".repeat(40)));
    expect(legendMode(1648, legendExtra([...BUILTIN, ...many]))).toBe("button");
  });
});

describe("boardLegendMode (F13)", () => {
  it.each([
    [1800, "short"],
    [1560, "short"],
    [1300, "short"],
    [1299, "button"],
    [900, "button"],
  ])("boardLegendMode(%i) is %s: the board never lists the mark keys", (w, mode) => {
    expect(boardLegendMode(w)).toBe(mode);
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

// F15 (ux-spec §16.3): the content below the header lays out on the window divided by text size x zoom; the header
// keeps the text scale, so its 64px comes off first and goes back at design size.
describe("contentViewport", () => {
  it("is the plain unscaled window at zoom 100 (content scale = text scale)", () => {
    expect(contentViewport(1920, 1080, 1, 1)).toEqual({ vw: 1920, vh: 1080 });
    expect(contentViewport(1920, 1080, 1.25, 1.25)).toEqual({ vw: 1536, vh: 864 });
  });

  it("divides the width by the content scale: 80% sees 2400, 150% sees 1280, 175% sees ~1097", () => {
    expect(contentViewport(1920, 1080, 1, 0.8).vw).toBe(2400);
    expect(contentViewport(1920, 1080, 1, 1.5).vw).toBe(1280);
    expect(contentViewport(1920, 1080, 1, 1.75).vw).toBeCloseTo(1097.14, 1);
  });

  it("keeps the header out of the zoom: the height is the content's plus the 64px header at design size", () => {
    expect(HEADER_H).toBe(64);
    expect(contentViewport(1920, 1080, 1, 1.5).vh).toBeCloseTo((1080 - 64) / 1.5 + 64, 6);
    expect(contentViewport(1920, 1080, 1, 0.8).vh).toBeCloseTo((1080 - 64) / 0.8 + 64, 6);
  });

  it("multiplies with Large text: 1.25 x 150% = 1.875", () => {
    expect(contentViewport(1920, 1080, 1.25, 1.875).vw).toBeCloseTo(1024, 6);
  });
});

describe("personTrackWidth", () => {
  it("is trackWidth wherever the row has room for it (every width that fits today)", () => {
    for (const w of [1656, 1500, 1200, 1150, 908, 900, 700]) expect(personTrackWidth(w)).toBe(trackWidth(w));
  });

  it("shrinks below 300 once a high zoom leaves a narrow effective width (the row never scrolls sideways)", () => {
    expect(personTrackWidth(615)).toBe(255);
    expect(personTrackWidth(524)).toBe(164);
    expect(personTrackWidth(300)).toBe(100); // never below 100
  });
});

describe("rules at the effective sizes of a 1920 x 1080 window (the F15 golden sizes)", () => {
  // zoom 80 / 100 / 150 / 175 -> effective width, rail mode, main width and board columns
  it.each([
    [0.8, 2400, "full", 2128, 3],
    [1, 1920, "full", 1648, 2],
    [1.5, 1280, "compact", 1164, 2],
    [1.75, 1097, "compact", 981, 1],
  ])("zoom x%f: vw %i, rail %s, mainW %i, %i board columns", (z, vw, rail, mainW, cols) => {
    const v = contentViewport(1920, 1080, 1, z).vw;
    expect(Math.round(v)).toBe(vw);
    expect(railMode(v)).toBe(rail);
    expect(Math.round(mainWidth(v))).toBe(mainW);
    expect(boardColumns(mainWidth(v))).toBe(cols);
  });
});
