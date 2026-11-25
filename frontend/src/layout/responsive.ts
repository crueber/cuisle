// FROZEN SIGNATURES (ux-spec §2, §3, §9.2, §11). Implementation: WP-13. Pure; inputs are CSS px already divided by --scale.
export type RailMode = "full" | "compact";
export type LegendMode = "full" | "short" | "button";

/** full (208px) when vw ≥ 1360, else compact (76px). */
export function railMode(vw: number): RailMode {
  return vw >= 1360 ? "full" : "compact";
}

export function railWidth(mode: RailMode): number {
  return mode === "full" ? 208 : 76;
}

/**
 * full when mainW ≥ 1560, short when ≥ 1300, else button; each threshold rises by `extra` (legendExtra). `controlRoom` is the
 * zoom control that now sits beside the legend (F15): the short legend needs that much more, while the full legend's own
 * threshold already leaves it room, so the default view keeps its full legend.
 */
export function legendMode(mainW: number, extra = 0, controlRoom = 0): LegendMode {
  if (mainW >= 1560 + extra) return "full";
  if (mainW >= 1300 + extra + controlRoom) return "short";
  return "button";
}

/**
 * Extra width (px) the header legend needs beyond the four built-in lanes: lanes the user named and longer names
 * (≈8px a character, 45px a swatch). Lanes are the view's lanes; Done is not in the legend.
 */
export function legendExtra(lanes: readonly { lane: string; label: string }[]): number {
  const walk = lanes.filter((l) => l.lane !== "done");
  const chars = walk.reduce((n, l) => n + l.label.length, 0);
  return Math.max(0, (chars - 27) * 8 + (walk.length - 4) * 45);
}

/**
 * The whole board's legend: the same thresholds, but never `full`. Board lane tracks draw no commit / PR / comment marks
 * (ux-spec §6.2), so their keys would only crowd the stats line beside the Assignee | Status switch (§6.5).
 */
export function boardLegendMode(mainW: number, extra = 0, controlRoom = 0): LegendMode {
  const m = legendMode(mainW, extra, controlRoom);
  return m === "full" ? "short" : m;
}

/** 600 when mainW ≥ 1500, 440 when ≥ 1150, else 300. */
export function trackWidth(mainW: number): 600 | 440 | 300 {
  if (mainW >= 1500) return 600;
  if (mainW >= 1150) return 440;
  return 300;
}

/**
 * The person view's pulse track: trackWidth, but never wider than the row leaves once the gutter, icon, key, days chip and
 * their gaps (≈ 360px) are taken out, so a high zoom on a small window (a narrow effective width) shrinks the track and
 * the row never scrolls sideways (ux-spec §16.3, AGENTS invariant 16). At every width that fits today it is trackWidth.
 */
export function personTrackWidth(mainW: number): number {
  return Math.min(trackWidth(mainW), Math.max(100, mainW - 360));
}

/** 3 when mainW ≥ 1500, 2 when ≥ 960, else 1. */
export function configureColumns(mainW: number): 1 | 2 | 3 {
  if (mainW >= 1500) return 3;
  if (mainW >= 960) return 2;
  return 1;
}

export interface MainPadding {
  x: number; // 32 when vw ≥ 1600 else 20
  top: number; // 20 when vh ≥ 800 else 12
  bottom: number; // 10
}

export function mainPadding(vw: number, vh: number): MainPadding {
  return {
    x: vw >= 1600 ? 32 : 20,
    top: vh >= 800 ? 20 : 12,
    bottom: 10,
  };
}

/** mainW = vw − railWidth − 2 × padding.x. */
export function mainWidth(vw: number): number {
  return vw - railWidth(railMode(vw)) - 2 * mainPadding(vw, 0).x;
}

export interface TitleSizes {
  person: number; // 36 / 28
  board: number; // 30 / 24
  personRow: number; // 56 / 46
  boardRow: number; // 48 / 40
}

export function titleSizes(vh: number): TitleSizes {
  if (vh >= 800) return { person: 36, board: 30, personRow: 56, boardRow: 48 };
  return { person: 28, board: 24, personRow: 46, boardRow: 40 };
}

export interface HeaderCollapse {
  syncLabel: boolean; // vw ≥ 1700
  date: boolean; // vw ≥ 1360
  configureLabel: boolean; // vw ≥ 1280
}

export function headerCollapse(vw: number): HeaderCollapse {
  return {
    syncLabel: vw >= 1700,
    date: vw >= 1360,
    configureLabel: vw >= 1280,
  };
}

/** Divides a measured px value by the text scale (1 or 1.25). */
export function unscale(px: number, scale: number): number {
  return px / scale;
}

/** The header's height in design px (tokens --size-header): the one part of the screen that does not zoom. */
export const HEADER_H = 64;

/**
 * The standup screen content's size in effective design px (ux-spec §16.3): the window divided by the content scale
 * (text size × zoom), so every layout function above runs as if the window were that big. The header keeps the text
 * scale only, so its height is taken off first and added back at design size: the height constants of the person view and
 * the board count the 64px header, and this keeps them right at every zoom. With zoom 100, cs = ts and this is w/ts, h/ts.
 */
export function contentViewport(w: number, h: number, textScale: number, contentScale: number): { vw: number; vh: number } {
  return { vw: w / contentScale, vh: (h - HEADER_H * textScale) / contentScale + HEADER_H };
}
