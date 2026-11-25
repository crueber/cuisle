// FROZEN SIGNATURES (ux-spec §6.2, §6.3), plus §6.5 group-by-status. Implementation: WP-13, F13. Pure; no DOM.
import type { StandupView, TicketView } from "../api/types";

export interface LaneInput {
  id: string; // speaker id or "unassigned"
  rows: number; // active tickets in the lane
  unassigned: boolean;
}

export interface Balanced {
  columns: LaneInput[][];
  heights: number[];
}

/** 3 when mainW ≥ 1720, 2 when ≥ 1150, else 1 (effective px, so zoom changes it: ux-spec §6.2, §16.3). */
export function boardColumns(mainW: number): 1 | 2 | 3 {
  if (mainW >= 1720) return 3;
  return mainW >= 1150 ? 2 : 1;
}

/** Lane height = 30 + rows × rowH + 8. */
export function laneHeight(rows: number, rowH: number): number {
  return 30 + rows * rowH + 8;
}

/**
 * Splits one ordered sequence into `cols` (1 to 3) contiguous runs without reordering it, choosing the cut that minimises
 * the tallest column. Ties: column 1 fills first (the later first cut wins); with three columns the last column then takes
 * as much as fits and the middle one what is left (the earlier second cut wins), which is the split the zoom mockups draw
 * (Unassigned, Iris | Silas, Noor | Otto, Amara, Jonas, Wren at 80%). A middle column is never left empty while the last
 * one is not.
 */
export function splitSequence(heights: number[], cols: number): Split {
  const n = heights.length;
  const idx = heights.map((_, i) => i);
  const pre = [0];
  for (const h of heights) pre.push((pre[pre.length - 1] ?? 0) + h);
  const sum = (a: number, b: number) => (pre[b] ?? 0) - (pre[a] ?? 0);
  if (cols < 2) return { columns: [idx], heights: [sum(0, n)] };
  if (cols === 2) {
    let cut = n;
    let best = Infinity;
    for (let c = 0; c <= n; c++) {
      const taller = Math.max(sum(0, c), sum(c, n));
      if (taller <= best) {
        best = taller;
        cut = c;
      }
    }
    return { columns: [idx.slice(0, cut), idx.slice(cut)], heights: [sum(0, cut), sum(cut, n)] };
  }
  let cut1 = n;
  let cut2 = n;
  let best = Infinity;
  for (let c1 = 0; c1 <= n; c1++) {
    for (let c2 = Math.min(c1 + 1, n); c2 <= n; c2++) {
      const taller = Math.max(sum(0, c1), sum(c1, c2), sum(c2, n));
      if (taller < best || (taller === best && c1 > cut1)) {
        best = taller;
        cut1 = c1;
        cut2 = c2;
      }
    }
  }
  return {
    columns: [idx.slice(0, cut1), idx.slice(cut1, cut2), idx.slice(cut2)],
    heights: [sum(0, cut1), sum(cut1, cut2), sum(cut2, n)],
  };
}

/**
 * Assignee mode (ux-spec §6.2): Unassigned first (when it has rows), then the lanes in speaking order, cut into `cols`
 * contiguous runs that minimise the tallest column. Speaking order therefore always reads down, then across.
 */
export function splitLanes(lanes: LaneInput[], cols: number, rowH: number): Balanced {
  const ordered = [...lanes.filter((l) => l.unassigned && l.rows > 0), ...lanes.filter((l) => !l.unassigned)];
  const split = splitSequence(ordered.map((l) => laneHeight(l.rows, rowH)), cols);
  return { columns: split.columns.map((c) => c.flatMap((i) => (ordered[i] ? [ordered[i] as LaneInput] : []))), heights: split.heights };
}

/** 23, or 20 when the tallest column at 23 exceeds availH. */
export function laneRowHeight(lanes: LaneInput[], cols: number, availH: number): 23 | 20 {
  const tallest = splitLanes(lanes, cols, 23).heights.reduce((m, h) => Math.max(m, h), 0);
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
  doneCols: number; // 3 with three board columns, 2 when (mainW − 40) ≥ 1300, else 1
  backlogCols: number; // 4 with three board columns, 3 at ≥ 1300, 2 at ≥ 1000, else 1
}

export function doneBacklogLayout(mainW: number): DoneBacklogLayout {
  const bottomW = mainW - 40;
  if (boardColumns(mainW) === 3) return { sideBySide: true, doneCols: 3, backlogCols: 4 };
  let backlogCols = 1;
  if (bottomW >= 1300) backlogCols = 3;
  else if (bottomW >= 1000) backlogCols = 2;
  return {
    sideBySide: bottomW >= 1100,
    doneCols: bottomW >= 1300 ? 2 : 1,
    backlogCols,
  };
}

// ───────────────────────── Group by Status (ux-spec §6.5) ─────────────────────────
// Go computes the data; this only groups, sorts and arranges (ADR 0008). Sections come from StandupView.Lanes in walk
// order, so a lane the standup defines later (custom lanes) gets its section with no change here.

export type BoardGroup = "assignee" | "status";

export interface StatusSection {
  lane: string; // LaneInfo.lane: also the data-lane key of the header swatch
  label: string; // LaneInfo.label
  limitDays: number; // 0 = no limit
  tickets: TicketView[]; // sorted by sortStatusRows
  overLimit: number;
  blocked: number;
  unassigned: number;
}

/**
 * Over-limit first, then daysInLane descending, then board rank (the order the view already lists tickets in, so a
 * rank tie keeps stuck work first). Stable and non-mutating.
 */
export function sortStatusRows(tickets: TicketView[]): TicketView[] {
  return tickets
    .map((t, rank) => ({ t, rank }))
    .sort((a, b) => Number(b.t.overLimit) - Number(a.t.overLimit) || b.t.daysInLane - a.t.daysInLane || a.rank - b.rank)
    .map((x) => x.t);
}

/** One section per lane in walk order (Done and Backlog are listed separately, §6.3). A lane with no active ticket has none. */
export function statusSections(view: StandupView): StatusSection[] {
  const all = view.tickets ?? [];
  const out: StatusSection[] = [];
  for (const l of view.lanes ?? []) {
    if (l.lane === "done" || l.lane === "backlog") continue;
    const own = all.filter((t) => t.lane === l.lane);
    if (own.length === 0) continue;
    out.push({
      lane: l.lane,
      label: l.label,
      limitDays: l.limitDays,
      tickets: sortStatusRows(own),
      overLimit: own.filter((t) => t.overLimit).length,
      blocked: own.filter((t) => t.blocked).length,
      unassigned: own.filter((t) => t.assignee === "").length,
    });
  }
  return out;
}

export interface Split {
  columns: number[][]; // indexes into the sequence, order preserved
  heights: number[];
}

/** A status section: 30px header (its 2px ink rule included) + 3px + rows × rowH + the 4px gap below (§6.5). */
export function statusSectionHeight(rows: number, rowH: number): number {
  return 30 + 3 + rows * rowH + 4;
}

/** Done / Backlog heading row (36) + the grid's top padding (6) + rows × 26 (+ footnote) + the 8px gap below. */
export function listSectionHeight(items: number, gridCols: number, footnote = false): number {
  const rows = Math.max(1, Math.ceil(items / Math.max(1, gridCols)));
  return 36 + 6 + rows * 26 + (footnote ? 20 : 0) + 8;
}

export interface StatusLayout extends Split {
  rowH: 23 | 20;
}

/**
 * The whole status-mode sequence is the lane sections (their row counts), then the fixed-height lists (Done, Backlog).
 * Row height is 23, or 20 when the taller column at 23 exceeds availH; the split is chosen at the row height used.
 */
export function statusLayout(laneRows: number[], listHeights: number[], cols: number, availH: number): StatusLayout {
  const at = (rowH: number) => splitSequence([...laneRows.map((n) => statusSectionHeight(n, rowH)), ...listHeights], cols);
  const normal = at(23);
  if (normal.heights.reduce((m, h) => Math.max(m, h), 0) <= availH) return { ...normal, rowH: 23 };
  return { ...at(20), rowH: 20 };
}
