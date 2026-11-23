// FROZEN SIGNATURES (ux-spec §7). Implementation: WP-13. Pure; no DOM. Positions are in DAY UNITS.
import type { PulseMark, PulseSeg, PulseSpan } from "../api/types";

export interface SegBox {
  lane: string;
  x: number; // from day
  w: number; // width in days
}

export interface SpanBox {
  x: number;
  w: number; // inclusive span → To − From + 1 days
}

export type MarkKind = "commit" | "pr" | "comment";

export interface MarkBox {
  day: number;
  kind: MarkKind;
  level: 0 | 1 | 2 | 3; // commits only (1, 2–3, 4+); 0 otherwise
  height: number; // px: commit 3 + level × unit; pr 8; comment 6
  bottom: number; // px above the segment top (commit 2, pr 12 centre, comment 8)
}

export interface TrackGeometry {
  segH: number;
  segTop: number;
}

/** Row ≥ 40 → 12 / row−18; ≥ 32 → 10 / row−16; else 8 / row−14. lane: 11 / 6. */
export function trackGeometry(rowH: number, lane?: boolean): TrackGeometry {
  if (lane) return { segH: 11, segTop: 6 };
  if (rowH >= 40) return { segH: 12, segTop: rowH - 18 };
  if (rowH >= 32) return { segH: 10, segTop: rowH - 16 };
  return { segH: 8, segTop: rowH - 14 };
}

export function segBoxes(segs: PulseSeg[]): SegBox[] {
  return segs.map((s) => ({ lane: s.lane, x: s.from, w: s.to - s.from }));
}

export function spanBoxes(spans: PulseSpan[]): SpanBox[] {
  return spans.map((s) => ({ x: s.from, w: s.to - s.from + 1 }));
}

/** 0 for 0, 1 for 1, 2 for 2–3, 3 for 4+. */
export function commitLevel(count: number): 0 | 1 | 2 | 3 {
  if (count <= 0) return 0;
  if (count === 1) return 1;
  if (count <= 3) return 2;
  return 3;
}

export interface MarkOptions {
  showCommits: boolean;
  showPRs: boolean;
  showComments: boolean;
  segTop: number;
}

/** PR diamond replaces the commit tick on a day with both; unit = floor((segTop − 9) / 3). */
export function markBoxes(marks: PulseMark[], opts: MarkOptions): MarkBox[] {
  const unit = Math.floor((opts.segTop - 9) / 3);
  const out: MarkBox[] = [];
  for (const m of marks) {
    const prDrawn = opts.showPRs && m.prs > 0;
    if (opts.showCommits && m.commits > 0 && !prDrawn) {
      const level = commitLevel(m.commits);
      out.push({ day: m.day, kind: "commit", level, height: 3 + level * unit, bottom: 2 });
    }
    if (prDrawn) {
      out.push({ day: m.day, kind: "pr", level: 0, height: 8, bottom: 12 });
    }
    if (opts.showComments && m.comments > 0) {
      out.push({ day: m.day, kind: "comment", level: 0, height: 6, bottom: 8 });
    }
  }
  return out;
}

/** Every day labelled when a day is ≥ 18px wide, else Mondays only. */
export function axisLabelMode(trackW: number, days: number): "all" | "mondays" {
  return trackW / days >= 18 ? "all" : "mondays";
}
