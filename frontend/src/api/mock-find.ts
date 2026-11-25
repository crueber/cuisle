// The mock's ticket finder (F14): a TS port of internal/keyfind plus the ordering of TicketService.FindByKey (ux-spec
// §15.3), over the tickets of the mock views. Pure functions; mock.ts feeds them. Keep in step with the Go side.
import type * as T from "./types";

export interface Query {
  letters: string;
  digits: string;
}

/** Lowercase, drop spaces and '-', split into the project-letters part and the digits part. Null = no ticket hits. */
export function parseQuery(raw: string): Query | null {
  const n = raw.toLowerCase().replace(/[\s-]/g, "");
  const m = /^([^0-9]*)([0-9]+)$/.exec(n);
  if (!m) return null;
  return { letters: m[1] ?? "", digits: m[2] ?? "" };
}

export function splitKey(key: string): { project: string; number: string } | null {
  const m = /^(.+)-([0-9]+)$/.exec(key);
  return m ? { project: m[1] ?? "", number: m[2] ?? "" } : null;
}

const oneOff = (a: string, b: string): boolean => {
  const diffs: number[] = [];
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) diffs.push(i);
  if (diffs.length === 1) return true;
  const [i, j] = diffs;
  return diffs.length === 2 && i !== undefined && j !== undefined && j === i + 1 && a[i] === b[j] && a[j] === b[i];
};

export type Tier = "exact" | "prefix" | "close";

export function classify(q: Query, key: string): { tier: Tier; match: number[] } | null {
  const k = splitKey(key);
  if (!k) return null;
  if (q.letters !== "" && !k.project.toLowerCase().startsWith(q.letters)) return null;
  let tier: Tier;
  if (k.number === q.digits) tier = "exact";
  else if (k.number.startsWith(q.digits)) tier = "prefix";
  else if (k.number.length === q.digits.length && q.digits.length >= 2 && oneOff(k.number, q.digits)) tier = "close";
  else return null;
  const base = k.project.length + 1;
  const match: number[] = [];
  for (let i = 0; i < q.letters.length; i++) match.push(i);
  for (let i = 0; i < q.digits.length; i++) if (tier !== "close" || k.number[i] === q.digits[i]) match.push(base + i);
  return { tier, match };
}

/** One synced issue as the finder sees it. */
export interface Cand {
  id: number;
  key: string;
  /** Live standups with the issue as a current member, in tab order. Empty = on no board. */
  standupIds: number[];
  /** The issue's row as the given standup shows it (lane mapping). */
  rowIn(standupId: number): T.TicketView;
}

export const homeOf = (standupIds: number[], current: number): number =>
  standupIds.includes(current) ? current : (standupIds[0] ?? current);

export function toHit(c: Cand, current: number, initials: (account: string) => string, tier = "", match: number[] = []): T.TicketHit {
  const home = homeOf(c.standupIds, current);
  const r = c.rowIn(home);
  return {
    issueId: c.id,
    key: c.key,
    summary: r.summary,
    type: r.type,
    lane: home === 0 ? "" : r.lane,
    statusName: r.statusName,
    assignee: r.assignee,
    assigneeInitials: r.assignee ? initials(r.assignee) : "",
    daysInLane: home === 0 ? 0 : r.daysInLane,
    overLimit: r.overLimit,
    done: r.lane === "done",
    removed: c.standupIds.length === 0,
    standupId: home,
    standupIds: [...c.standupIds],
    tier,
    match,
  };
}

/** Exact (all), starts-with (first 10), close (first 5); within a tier: the current standup, active before done, key. */
export function findHits(cands: Cand[], query: string, current: number, limit: number, initials: (account: string) => string): T.TicketFind {
  const q = parseQuery(query);
  const out: T.TicketFind = { hits: [], prefixMore: 0 };
  if (!q) return out;
  const cap = limit > 0 ? limit : 20;
  const tiers: Record<Tier, { c: Cand; match: number[]; done: boolean }[]> = { exact: [], prefix: [], close: [] };
  for (const c of cands) {
    const m = classify(q, c.key);
    if (!m) continue;
    tiers[m.tier].push({ c, match: m.match, done: c.rowIn(homeOf(c.standupIds, current)).lane === "done" });
  }
  const num = (k: string): number => Number(splitKey(k)?.number ?? 0);
  let room = cap;
  const take = (tier: Tier, max: number): number => {
    const list = tiers[tier].sort((a, b) => {
      const ac = a.c.standupIds.includes(current) ? 0 : 1;
      const bc = b.c.standupIds.includes(current) ? 0 : 1;
      if (ac !== bc) return ac - bc;
      if (a.done !== b.done) return a.done ? 1 : -1;
      const pa = splitKey(a.c.key)?.project ?? "";
      const pb = splitKey(b.c.key)?.project ?? "";
      return pa !== pb ? (pa < pb ? -1 : 1) : num(a.c.key) - num(b.c.key);
    });
    const n = Math.min(list.length, max, room);
    for (const x of list.slice(0, n)) out.hits.push(toHit(x.c, current, initials, tier, x.match));
    room -= n;
    return list.length - n;
  };
  take("exact", cap);
  out.prefixMore = take("prefix", 10);
  take("close", 5);
  return out;
}
