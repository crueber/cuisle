// Header of one status section on the whole board (ux-spec §6.5): lane swatch, NAME, "7 tickets", the alert counts,
// and (right) the lane limit. Not interactive. The swatch takes its colour from data-lane like every lane colour.
import type { StatusSection } from "../../layout/board";
import { hueOf } from "../../state/lanes";

export interface StatusHeadText {
  count: string; // "7 tickets" ("7" when compact)
  alerts: string[]; // "2 over limit" ("2 over" when compact), "1 blocked", "1 unassigned"; only the non-zero ones
  limit: string; // "limit 3d · longest first" ("limit 3d" when compact); "" when the lane has no limit
}

type HeadCounts = Pick<StatusSection, "label" | "tickets" | "overLimit" | "blocked" | "unassigned" | "limitDays">;
type Wording = "full" | "limit" | "compact";

function wording(s: HeadCounts, how: Wording): StatusHeadText {
  const n = s.tickets.length;
  const compact = how === "compact";
  const alerts: string[] = [];
  if (s.overLimit > 0) alerts.push(`${s.overLimit} ${compact ? "over" : "over limit"}`);
  if (s.blocked > 0) alerts.push(`${s.blocked} blocked`);
  if (s.unassigned > 0) alerts.push(`${s.unassigned} unassigned`);
  const limit = s.limitDays > 0 ? (how === "full" ? `limit ${s.limitDays}d · longest first` : `limit ${s.limitDays}d`) : "";
  return { count: compact ? String(n) : `${n} ${n === 1 ? "ticket" : "tickets"}`, alerts, limit };
}

/** The header text. Compact (below 1360) is the spec's short wording; the full wording is the default. */
export function statusHeadText(s: HeadCounts, compact: boolean): StatusHeadText {
  return wording(s, compact ? "compact" : "full");
}

// Design-px estimates (one mono character at 13 / 12px, one caps sans character at 15px bold with 0.12em tracking).
const CH_MONO_13 = 7.8;
const CH_MONO_12 = 7.3;
const CH_NAME = 12;

function estimate(s: HeadCounts, t: StatusHeadText): number {
  const alerts = t.alerts.reduce((w, a) => w + (a.length + 2) * CH_MONO_13 + 10, 0);
  return 14 + 10 + s.label.length * CH_NAME + 10 + t.count.length * CH_MONO_13 + alerts + (t.limit ? 10 + t.limit.length * CH_MONO_12 : 0);
}

/**
 * statusHeadText that also gives way when the column is too narrow for the wording (a four-part header in a 580px
 * column): first "· longest first" goes, then the short wording. Nothing is cut off mid-word.
 */
export function fitStatusHead(s: HeadCounts, compact: boolean, colW: number | undefined): StatusHeadText {
  const order: Wording[] = compact ? ["compact"] : ["full", "limit", "compact"];
  if (colW === undefined) return wording(s, order[0] as Wording);
  for (const how of order) {
    const t = wording(s, how);
    if (estimate(s, t) <= colW) return t;
  }
  return wording(s, "compact");
}

export function StatusSectionHeader({ section, compact, colW }: { section: StatusSection; compact: boolean; colW?: number }) {
  const t = fitStatusHead(section, compact, colW);
  return (
    <div class="board-sec-head" data-sec-head={section.lane}>
      <span class="legend-swatch board-sec-swatch" data-lane={section.lane} data-hue={hueOf(section.lane)} aria-hidden="true" />
      <h2 class="board-sec-name">{section.label}</h2>
      <span class="board-sec-count">{t.count}</span>
      {t.alerts.map((a) => (
        <span key={a} class="board-sec-alert" data-alert="true">{`· ${a}`}</span>
      ))}
      {t.limit ? <span class="board-sec-limit">{t.limit}</span> : null}
    </div>
  );
}
