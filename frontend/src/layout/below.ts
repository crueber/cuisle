// FROZEN SIGNATURES (ux-spec §5.6, §6.4). Implementation: WP-13. Pure; no DOM.
import type { DensitySpec, GroupCount } from "./density";

export interface BelowItem {
  label: string;
  count: number;
}

export interface BelowResult {
  items: BelowItem[]; // groups with rows past the fold, in order
  summary: string; // "2 To Do · 6 Done below" ("" when nothing is below)
}

function summarize(parts: string[]): string {
  return parts.length === 0 ? "" : `${parts.join(" · ")} below`;
}

/**
 * Person view: walk groups top to bottom (each group = headCost then count × row, starting at y = -scrollTop);
 * count rows whose bottom edge is past foldY (= visible height − 36).
 */
export function belowFold(groups: GroupCount[], density: DensitySpec, foldY: number, scrollTop = 0): BelowResult {
  const items: BelowItem[] = [];
  let y = -scrollTop;
  for (const g of groups) {
    y += density.headCost;
    let below = 0;
    for (let i = 0; i < g.count; i++) {
      y += density.row;
      if (y > foldY) below++;
    }
    if (below > 0) items.push({ label: g.label, count: below });
  }
  return { items, summary: summarize(items.map((i) => `${i.count} ${i.label}`)) };
}

export interface BoardSection {
  label: string; // lane: speaker first name; "Done (15)"; "Backlog (12)"
  top: number; // header top in content px
}

/** Board: sections whose header top is past foldY + scrollTop, joined "Iris · Silas · Done (15) below". */
export function boardBelow(sections: BoardSection[], foldY: number, scrollTop = 0): BelowResult {
  const items: BelowItem[] = sections
    .filter((s) => s.top > foldY + scrollTop)
    .map((s) => ({ label: s.label, count: 1 }));
  return { items, summary: summarize(items.map((i) => i.label)) };
}

/** End of list: scrollTop + clientHeight ≥ scrollHeight − 6. */
export function atEnd(scrollTop: number, clientHeight: number, scrollHeight: number): boolean {
  return scrollTop + clientHeight >= scrollHeight - 6;
}
