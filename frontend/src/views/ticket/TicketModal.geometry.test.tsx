// WP-T7 tests: modalGeometry(vw, vh) exported by TicketModal.tsx (ux-spec §14.2; ModalGeometry in props.ts).
//
// SPEC-NOTE (CONFLICT INSIDE ux-spec §14.2, resolved for the table): the prose rule "modal = min(1320, vw − 2·margin) ×
//   min(1000, vh − 2·margin) with margins 40/32/24/12" and "content column = min(880, modal − 380)" do NOT reproduce the
//   spec's own table at three of the four sizes: 1440 wide gives 1320 by the rule but 1240 in the table and the mockup;
//   1280 gives 1232 vs 1216; and at 1024 the rule gives a 620 content column (rail 380, so labels would never stack) vs the
//   table's 700 (rail ≈ 292, "labels stacked"). The four TABLE rows (which match the approved mockups) are pinned here
//   literally for modalW, modalH and contentW; nothing between them is interpolated. railW is "the rest": it must lie
//   within 12 px under (modalW − contentW) (borders and padding), i.e. 428..440 at 1920 and 288..300 at 1024. Everything
//   else is pinned as invariants.
//   The window sizes are UNSCALED CSS px (props.ts), so a 1024-wide window at Large (1.25) is vw 819.2.
// SPEC-NOTE: flags: short = vh < 800 (799 short, 800 not); narrow = vw < 900 (899 narrow, 900 not); stackedLabels =
//   railW < 340 (the spec's "labels stacked under 340px").
import { describe, expect, it } from "vitest";
import { modalGeometry } from "./TicketModal";

describe("modalGeometry: the four table rows", () => {
  it.each([
    // [vw, vh, modalW, modalH, contentW, short, stacked]
    [1920, 1080, 1320, 1000, 880, false, false],
    [1440, 900, 1240, 836, 840, false, false],
    [1280, 720, 1216, 672, 836, true, false],
    [1024, 640, 1000, 616, 700, true, true],
  ])("windowAt_%ix%i", (vw, vh, modalW, modalH, contentW, short, stacked) => {
    const g = modalGeometry(vw, vh);
    expect(g.modalW).toBe(modalW);
    expect(g.modalH).toBe(modalH);
    expect(g.contentW).toBe(contentW);
    expect(g.railW).toBeGreaterThanOrEqual(modalW - contentW - 12);
    expect(g.railW).toBeLessThanOrEqual(modalW - contentW);
    expect(g.short).toBe(short);
    expect(g.narrow).toBe(false);
    expect(g.stackedLabels).toBe(stacked);
  });
});

describe("modalGeometry: modes", () => {
  it("shortModeStartsBelow800High", () => {
    expect(modalGeometry(1920, 799).short).toBe(true);
    expect(modalGeometry(1920, 800).short).toBe(false);
  });

  it("narrowStartsBelow900Wide_SoAnUnscaled819PxWindowIsNarrow", () => {
    expect(modalGeometry(899, 900).narrow).toBe(true);
    expect(modalGeometry(900, 900).narrow).toBe(false);
    const largeAt1024 = modalGeometry(1024 / 1.25, 640 / 1.25);
    expect(largeAt1024.narrow).toBe(true);
    expect(largeAt1024.short).toBe(true);
  });

  it("stackedLabelsMeansARailUnder340", () => {
    for (const [vw, vh] of [[1920, 1080], [1440, 900], [1280, 720], [1024, 640], [1000, 700], [1100, 800]] as const) {
      const g = modalGeometry(vw, vh);
      expect([vw, vh, g.stackedLabels]).toEqual([vw, vh, g.railW < 340]);
    }
  });
});

describe("modalGeometry: invariants at other sizes", () => {
  it.each([
    [800, 600], [900, 700], [1100, 800], [1366, 768], [1600, 1000], [2560, 1440], [3840, 2160],
  ])("fitsTheWindowAndTheCaps_%ix%i", (vw, vh) => {
    const g = modalGeometry(vw, vh);
    expect(g.modalW).toBeGreaterThan(0);
    expect(g.modalW).toBeLessThanOrEqual(Math.min(1320, vw));
    expect(g.modalH).toBeGreaterThan(0);
    expect(g.modalH).toBeLessThanOrEqual(Math.min(1000, vh));
    expect(g.contentW).toBeGreaterThan(0);
    expect(g.contentW).toBeLessThanOrEqual(880);
    // Narrow windows (< 900) have no rail column: railW may be 0 there, but is never negative.
    if (g.narrow) expect(g.railW).toBeGreaterThanOrEqual(0);
    else expect(g.railW).toBeGreaterThan(0);
    expect(g.contentW + g.railW).toBeLessThanOrEqual(g.modalW);
    expect(g.short).toBe(vh < 800);
    expect(g.narrow).toBe(vw < 900);
  });

  it("capsAtTheLargestWindow", () => {
    const g = modalGeometry(3840, 2160);
    expect(g.modalW).toBe(1320);
    expect(g.modalH).toBe(1000);
    expect(g.contentW).toBe(880);
  });
});
