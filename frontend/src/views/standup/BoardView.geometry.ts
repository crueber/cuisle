// Pure px geometry for the whole-board lane rows and their per-column axis (ux-spec §6.2). No DOM.
// The CSS grid and the axis flex row do the real layout; these estimates set the inline track width and decide
// which axis labels would collide. Estimates are rounded up so they never let a track or label run past its column.

/** Icon (14px) + the three gaps between icon, key and title, and title and track (8px each). */
export const ROW_ICON_PX = 14;
export const ROW_GAP_PX = 8;
/** Days chip column (36px) plus the gap before it. */
export const ROW_CHIP_PX = 36;
/** Upper bound for one mono character at the board's axis and key sizes. */
export const CHAR_PX = 8;
/** "today" is 5 characters; the Monday label keeps a gap from it. */
export const TODAY_LABEL_PX = 5 * CHAR_PX + ROW_GAP_PX;

/**
 * Width of the flexible track in one lane row: whatever remains of the column after icon, key, title, chip and the
 * four gaps. Never negative.
 */
export function laneTrackWidth(colW: number, keyPx: number, titleW: number): number {
  const fixed = ROW_ICON_PX + keyPx + titleW + ROW_CHIP_PX + 4 * ROW_GAP_PX;
  return Math.max(0, Math.floor(colW - fixed));
}

/**
 * The lane track in real px at text scale k. The design track leaves four 8px gaps; the gaps do not scale (they are
 * spacing tokens), while the column and every other part of the row do, so only the rest is multiplied.
 */
export function realTrackWidth(designTrack: number, k: number): number {
  return Math.max(0, Math.floor(k * (designTrack + 4 * ROW_GAP_PX) - 4 * ROW_GAP_PX));
}

export interface AxisMonday {
  x: number; // day index
  text: string;
}

/**
 * The Monday labels that fit on the axis. A label reads "Mon 16", or "16" when a day is narrower than 16px. A label
 * that would run into the "today" label at the right is dropped, so "today" never overlaps a Monday.
 */
export function axisMondays(axis: { label: string; monday: boolean }[], axisPx: number): AxisMonday[] {
  const days = axis.length;
  if (days === 0 || axisPx <= 0) return [];
  const dayPx = axisPx / days;
  const limit = axisPx - TODAY_LABEL_PX;
  const out: AxisMonday[] = [];
  axis.forEach((d, x) => {
    if (!d.monday) return;
    const text = dayPx >= 16 ? `Mon ${d.label}` : d.label;
    const end = x * dayPx + 4 + text.length * CHAR_PX;
    if (end <= limit) out.push({ x, text });
  });
  return out;
}
