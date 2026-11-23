// Natural tab widths for layout/tabs.ts tabMode (ux-spec §4). Estimated from character counts with the frame's font
// metrics (IBM Plex Sans at 18px bold ≈ 9px a glyph; IBM Plex Mono at 12px ≈ 7.5px, at 16px ≈ 9.6px), because the
// tabs must choose their mode before anything is drawn. Deliberately a little generous so the chosen mode never overflows.
import type { StandupTab } from "../../api/types";
import type { TabMeasure } from "../../layout/tabs";

const PAD = 28; // 0 14px tab padding
const NUMBER_BOX = 26; // mono 12 digit in a 2px box
const GAP = 9;
const SANS_GLYPH = 9;
const MONO_SMALL = 7.5;
const MONO_KEY = 9.6;

export function tabMeasure(t: StandupTab): TabMeasure {
  const full = PAD + NUMBER_BOX + GAP + SANS_GLYPH * t.name.length + GAP + MONO_SMALL * t.spaceKey.length;
  const key = PAD + NUMBER_BOX + GAP + MONO_KEY * t.spaceKey.length;
  return { full: Math.ceil(full) + 4, key: Math.ceil(key) + 4 };
}

/** Width reserved for the "+N more ▾" control when some standups are hidden. */
export const MORE_WIDTH = 104;
