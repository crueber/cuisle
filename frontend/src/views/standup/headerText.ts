// Pure text and width helpers for the standup header (ux-spec §2.1). No DOM, no timers: callers pass `now`.
import type { SyncStatus } from "../../api/types";
import type { HeaderCollapse } from "../../layout/responsive";

const WEEKDAYS = ["Sun", "Mon", "Tues", "Wed", "Thurs", "Fri", "Sat"];
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const MS_PER_MIN = 60_000;

/** "2 min", "3h", "2 days" for an age in milliseconds (timestamps are unix ms). */
export function ageText(now: number, since: number): string {
  const mins = Math.max(0, Math.floor((now - since) / MS_PER_MIN));
  if (mins < 60) return `${Math.max(1, mins)} min`;
  const hours = Math.floor(mins / 60);
  if (hours < 48) return `${hours}h`;
  return `${Math.floor(hours / 24)} days`;
}

/** The sync status text and whether it is an alert (offline). Exact strings: jira-sync §4.7, ux-spec §2.1. */
export function syncLabel(status: SyncStatus | undefined, lastSyncedAt: number, now: number): { text: string; alert: boolean } {
  if (status?.state === "running") {
    const count = status.total > 0 ? ` ${status.done}/${status.total}` : "";
    return { text: `Syncing · ${status.phase}${count}`.replace(/ +$/, ""), alert: false };
  }
  if (status?.state === "failed" && status.errorCode === "offline") {
    const text = lastSyncedAt > 0 ? `offline · data from ${ageText(now, lastSyncedAt)} ago` : "offline · no data synced yet";
    return { text, alert: true };
  }
  if (lastSyncedAt <= 0) return { text: "not synced yet", alert: false };
  const partial = status?.lastStatus === "partial" ? " · partial" : "";
  return { text: `synced ${ageText(now, lastSyncedAt)} ago${partial}`, alert: false };
}

/** 1 → "1st", 2 → "2nd", 3 → "3rd", 11–13 → "11th"…"13th", 21 → "21st", 24 → "24th". */
export function ordinal(n: number): string {
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 13) return `${n}th`;
  switch (n % 10) {
    case 1:
      return `${n}st`;
    case 2:
      return `${n}nd`;
    case 3:
      return `${n}rd`;
    default:
      return `${n}th`;
  }
}

/** "Mon, 23rd of Nov" from a local-date string "2026-11-23" (StandupView.today). Empty when malformed. */
export function dayLabel(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
  if (!m) return "";
  const year = Number(m[1]);
  const month = Number(m[2]);
  const day = Number(m[3]);
  const weekday = WEEKDAYS[new Date(Date.UTC(year, month - 1, day)).getUTCDay()] ?? "";
  return `${weekday}, ${ordinal(day)} of ${MONTHS[month - 1] ?? ""}`;
}

/** Approximate width of the right cluster (Find, sync status, Resync, Update when shown, Configure) for the tabs'
 *  available width. The Update button ("↓ Update", or the icon alone) adds its width plus one header gap (16). */
export function rightClusterWidth(c: HeaderCollapse, update = false): number {
  const sync = (c.configureLabel ? 112 : 44) + (c.syncLabel ? 200 : 0);
  const configure = c.configureLabel ? 150 : 44;
  const upd = update ? (c.configureLabel ? 124 : 44) + 16 : 0;
  const find = (c.configureLabel ? 132 : 44) + 16; // Find (search icon, label, ⌘K chip) plus one header gap
  return sync + configure + upd + find + 48;
}

/** Approximate width of the standalone centred date ("Wednesday"-length worst case: "Thurs, 23rd of Nov"). */
export function dateWidth(c: HeaderCollapse): number {
  return c.date ? 190 : 0;
}

/** Approximate width of the CUISLE wordmark block, including its rule. */
export const WORDMARK_WIDTH = 170;
