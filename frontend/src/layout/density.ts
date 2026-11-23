// FROZEN SIGNATURES (ux-spec §5.5). Implementation: WP-13. Pure; no DOM.
export type DensityStep = "roomy" | "roomy-side" | "compact" | "compact-side" | "tight-side";

export interface DensitySpec {
  step: DensityStep;
  row: number; // row height px
  side: boolean; // side-gutter headings
  headCost: number; // px per group
  titleFont: number;
  keyFont: number;
  label: string; // plain name for the title-row tag, e.g. "compact rows, side headings"
}

/** The five steps in order (ux-spec §5.5 table). */
export const DENSITY_STEPS: readonly DensitySpec[] = [
  { step: "roomy", row: 46, side: false, headCost: 40, titleFont: 20, keyFont: 15, label: "roomy rows" },
  { step: "roomy-side", row: 46, side: true, headCost: 7, titleFont: 20, keyFont: 15, label: "roomy rows, side headings" },
  { step: "compact", row: 38, side: false, headCost: 32, titleFont: 18, keyFont: 14, label: "compact rows" },
  { step: "compact-side", row: 38, side: true, headCost: 7, titleFont: 18, keyFont: 14, label: "compact rows, side headings" },
  { step: "tight-side", row: 34, side: true, headCost: 7, titleFont: 17, keyFont: 14, label: "tight rows, side headings" },
];

export interface GroupCount {
  label: string; // "QA", "To Do", "Done"
  count: number;
}

export interface DensityResult {
  density: DensitySpec;
  need: number; // px needed at the chosen step
  scroll: boolean; // need > availH
}

function stepAt(i: number): DensitySpec {
  const s = DENSITY_STEPS[i];
  if (!s) throw new Error(`density ladder has no step ${i}`);
  return s;
}

function needAt(groups: GroupCount[], d: DensitySpec): number {
  let n = 0;
  for (const g of groups) n += d.headCost + g.count * d.row;
  return n;
}

/** need(step) = Σ(headCost + count × row); roomy mode → roomy; else first step with need ≤ availH, else tight-side. */
export function chooseDensity(groups: GroupCount[], availH: number, rowsMode: "fit" | "roomy"): DensityResult {
  if (rowsMode === "roomy") {
    const d = stepAt(0);
    const need = needAt(groups, d);
    return { density: d, need, scroll: need > availH };
  }
  for (const d of DENSITY_STEPS) {
    const need = needAt(groups, d);
    if (need <= availH) return { density: d, need, scroll: false };
  }
  const last = stepAt(DENSITY_STEPS.length - 1);
  const need = needAt(groups, last);
  return { density: last, need, scroll: need > availH };
}
