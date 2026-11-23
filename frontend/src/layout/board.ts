// FROZEN SIGNATURES (ux-spec §6.2, §6.3). Implementation: WP-13. Pure; no DOM.
export interface LaneInput {
  id: string; // speaker id or "unassigned"
  rows: number; // active tickets in the lane
  unassigned: boolean;
}

export interface Balanced {
  columns: LaneInput[][];
  heights: number[];
}

/** 2 when mainW ≥ 1150, else 1. */
export function boardColumns(mainW: number): 1 | 2 {
  return mainW >= 1150 ? 2 : 1;
}

/** Lane height = 30 + rows × rowH + 8. */
export function laneHeight(rows: number, rowH: number): number {
  return 30 + rows * rowH + 8;
}

interface Column {
  lanes: LaneInput[];
  height: number;
}

/**
 * Unassigned (if rows > 0) to column 1 first; then lanes in the given (speaking) order, each into the column with the
 * smaller height so far (ties → the left column). Order within a column is preserved.
 */
export function balanceLanes(lanes: LaneInput[], cols: number, rowH: number): Balanced {
  const columns: Column[] = Array.from({ length: cols }, () => ({ lanes: [], height: 0 }));
  const first = columns[0];
  const unassigned = lanes.find((l) => l.unassigned);
  if (first && unassigned && unassigned.rows > 0) {
    first.lanes.push(unassigned);
    first.height += laneHeight(unassigned.rows, rowH);
  }
  for (const lane of lanes) {
    if (lane.unassigned) continue;
    let best: Column | undefined;
    for (const c of columns) {
      if (!best || c.height < best.height) best = c;
    }
    if (!best) break;
    best.lanes.push(lane);
    best.height += laneHeight(lane.rows, rowH);
  }
  return {
    columns: columns.map((c) => c.lanes),
    heights: columns.map((c) => c.height),
  };
}

/** 23, or 20 when the tallest column at 23 exceeds availH. */
export function laneRowHeight(lanes: LaneInput[], cols: number, availH: number): 23 | 20 {
  const tallest = balanceLanes(lanes, cols, 23).heights.reduce((m, h) => Math.max(m, h), 0);
  return tallest <= availH ? 23 : 20;
}

/** titleW = mainW ≥ 1500 ? 250 : clamp(colW − keyW − 14 − 36 − 32 − 240, 150, 300). */
export function boardTitleWidth(mainW: number, colW: number, keyW: number): number {
  if (mainW >= 1500) return 250;
  const w = colW - keyW - 14 - 36 - 32 - 240;
  return Math.min(300, Math.max(150, w));
}

export interface DoneBacklogLayout {
  sideBySide: boolean; // mainW − 40 ≥ 1100
  doneCols: number; // 2 when (mainW − 40) ≥ 1300 else 1
  backlogCols: number; // 3 at ≥ 1300, 2 at ≥ 1000, else 1
}

export function doneBacklogLayout(mainW: number): DoneBacklogLayout {
  const bottomW = mainW - 40;
  let backlogCols = 1;
  if (bottomW >= 1300) backlogCols = 3;
  else if (bottomW >= 1000) backlogCols = 2;
  return {
    sideBySide: bottomW >= 1100,
    doneCols: bottomW >= 1300 ? 2 : 1,
    backlogCols,
  };
}
