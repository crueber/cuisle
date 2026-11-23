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

/** full when mainW ≥ 1560, short when ≥ 1300, else button. */
export function legendMode(mainW: number): LegendMode {
  if (mainW >= 1560) return "full";
  if (mainW >= 1300) return "short";
  return "button";
}

/** 600 when mainW ≥ 1500, 440 when ≥ 1150, else 300. */
export function trackWidth(mainW: number): 600 | 440 | 300 {
  if (mainW >= 1500) return 600;
  if (mainW >= 1150) return 440;
  return 300;
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
