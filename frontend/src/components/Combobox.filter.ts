// Pure helpers for the shared Combobox (architecture §4.6). No DOM, no Preact.

/** Filters static options: case-insensitive; prefix matches first, then substring matches; source order kept in each group. */
export function filterOptions<T>(options: readonly T[], getLabel: (o: T) => string, query: string): T[] {
  const q = query.toLowerCase();
  if (q === "") return [...options];
  const prefix: T[] = [];
  const substring: T[] = [];
  for (const o of options) {
    const label = getLabel(o).toLowerCase();
    if (label.startsWith(q)) prefix.push(o);
    else if (label.includes(q)) substring.push(o);
  }
  return [...prefix, ...substring];
}

export interface Box {
  top: number;
  bottom: number;
  left: number;
  width: number;
}

export interface PopupPosition {
  top: number;
  left: number;
  width: number;
}

/** Fixed popup placement under the anchor; flips above when there is no room below and there is room above. */
export function popupPosition(anchor: Box, popupHeight: number, viewportHeight: number, gap = 4): PopupPosition {
  const below = anchor.bottom + gap;
  const fitsBelow = below + popupHeight <= viewportHeight;
  const above = anchor.top - gap - popupHeight;
  const flip = !fitsBelow && above >= 0;
  return { top: flip ? above : below, left: anchor.left, width: anchor.width };
}
