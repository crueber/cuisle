// FROZEN SIGNATURES (ux-spec §4). Implementation: WP-13. Pure; no DOM.
export type TabMode = "full" | "key" | "menu";

export interface TabMeasure {
  full: number; // natural width of the full tab
  key: number; // natural width of the key-only tab
}

/** full if Σfull ≤ availW, else key if Σkey ≤ availW, else menu. Only the visible tabs (≤ 7 + "+N more") count. */
export function tabMode(tabs: TabMeasure[], availW: number, moreW = 0): TabMode {
  let full = moreW;
  let key = moreW;
  for (const t of tabs.slice(0, 7)) {
    full += t.full;
    key += t.key;
  }
  if (full <= availW) return "full";
  if (key <= availW) return "key";
  return "menu";
}

/** Available width = header − wordmark block − right cluster − 56 (the + button). */
export function tabsAvailableWidth(headerW: number, wordmarkW: number, rightClusterW: number): number {
  return headerW - wordmarkW - rightClusterW - 56;
}

export interface VisibleTabs {
  indices: number[]; // tab indexes shown, in order
  more: number; // hidden count ("+N more")
}

/** More than max tabs → first max, with the active one swapped into the last visible slot. */
export function visibleTabs(count: number, activeIndex: number, max = 7): VisibleTabs {
  const n = Math.max(0, Math.min(count, max));
  const indices = Array.from({ length: n }, (_, i) => i);
  if (count > max && max > 0 && activeIndex >= max && activeIndex < count) {
    indices[max - 1] = activeIndex;
  }
  return { indices, more: Math.max(0, count - max) };
}
