// The mock's ticket finder (F14): a TS port of internal/keyfind (key tiers and the title matcher) plus the ordering of
// TicketService.FindByKey (ux-spec §15.3), over the tickets of the mock views. Pure functions; mock.ts feeds them. Keep
// in step with the Go side (internal/keyfind/title.go, internal/app/find.go); the same example table is in both tests.
// Indexes are code point indexes (Go: rune indexes), which is how the palette's Hl walks a string.
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

export type Tier = "exact" | "prefix" | "close" | "title";

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

// ---- Title matching (port of internal/keyfind/title.go) ----

const SCORE_WHOLE = 4000;
const SCORE_START = 3000;
const SCORE_SUBSTR = 2000;
const MIN_SUBSEQ = 3;
const NEG = -(2 ** 30);
const [F_MATCH, F_BOUNDARY, F_CONSEC, F_GAP_OPEN, F_GAP_MORE] = [16, 8, 8, 3, 1];
const [SPAN_PER, SPAN_PLUS] = [3, 3];

const fold = (ch: string): string => {
  const l = ch.toLowerCase();
  return [...l].length === 1 ? l : ch; // Go folds one rune to one rune; so do we
};
const isWordCh = (ch: string): boolean => /[\p{L}\p{Nd}]/u.test(ch);
const boundary = (s: readonly string[], i: number): boolean => i === 0 || !isWordCh(s[i - 1] as string);

/** Words of a title query (each an array of lower-cased code points), or null when it is not a title query: fewer than two
 *  non-space characters. Numbers search titles too; a word with a digit only matches contiguously (no subsequence). */
export function parseTitle(raw: string): string[][] | null {
  const words = raw
    .split(/\s+/)
    .filter((w) => w !== "")
    .slice(0, 8)
    .map((w) => [...w].slice(0, 64).map(fold));
  const count = words.reduce((n, w) => n + w.length, 0);
  if (count < 2 || !/[\p{L}\p{Nd}]/u.test(raw)) return null;
  return words;
}

function subseq(w: readonly string[], s: readonly string[], out: number[] | null): number {
  const m = w.length;
  const n = s.length;
  const bonus = (i: number): number => (boundary(s, i) ? F_BOUNDARY : 0);
  const M: number[][] = [];
  const from: number[][] = [];
  M[0] = s.map((ch, i) => (ch === w[0] ? F_MATCH + bonus(i) : NEG));
  from[0] = s.map(() => -1);
  for (let j = 1; j < m; j++) {
    const prev = M[j - 1] as number[];
    const row = new Array<number>(n).fill(NEG);
    const fr = new Array<number>(n).fill(-1);
    let gap = NEG;
    let gapAt = -1;
    for (let i = 0; i < n; i++) {
      if (i >= 2) {
        if (gap > NEG) gap -= F_GAP_MORE;
        const v = prev[i - 2] as number;
        if (v > NEG && v - F_GAP_OPEN > gap) {
          gap = v - F_GAP_OPEN;
          gapAt = i - 2;
        }
      }
      if (s[i] !== w[j] || i === 0) continue;
      let pick = NEG;
      let at = -1;
      const pv = prev[i - 1] as number;
      if (pv > NEG) {
        pick = pv + F_CONSEC;
        at = i - 1;
      }
      if (gap > pick) {
        pick = gap;
        at = gapAt;
      }
      if (at < 0) continue;
      row[i] = pick + F_MATCH + bonus(i);
      fr[i] = at;
    }
    M[j] = row;
    from[j] = fr;
  }
  const last = M[m - 1] as number[];
  let bi = -1;
  let bs = NEG;
  last.forEach((v, i) => {
    if (v > bs) {
      bs = v;
      bi = i;
    }
  });
  if (bi < 0) return -1;
  const path: number[] = [];
  for (let j = m - 1, i = bi; j >= 0; j--) {
    path.push(i);
    i = (from[j] as number[])[i] as number;
  }
  if ((path[0] as number) - (path[m - 1] as number) + 1 > SPAN_PER * m + SPAN_PLUS) return -1;
  if (out) for (let j = m - 1; j >= 0; j--) out.push(path[j] as number);
  return bs;
}

function matchWord(w: readonly string[], s: readonly string[], out: number[] | null): number | null {
  const m = w.length;
  const n = s.length;
  if (m === 0 || m > n) return null;
  let best = 0;
  let at = -1;
  for (let p = 0; p + m <= n; p++) {
    let j = 0;
    while (j < m && s[p + j] === w[j]) j++;
    if (j < m) continue;
    let cls = SCORE_SUBSTR;
    if (boundary(s, p)) cls = p + m === n || !isWordCh(s[p + m] as string) ? SCORE_WHOLE : SCORE_START;
    if (cls > best) {
      best = cls;
      at = p;
      if (cls === SCORE_WHOLE) break;
    }
  }
  if (at >= 0) {
    if (out) for (let i = 0; i < m; i++) out.push(at + i);
    return best;
  }
  if (m < MIN_SUBSEQ || w.some((ch) => /\p{Nd}/u.test(ch))) return null;
  let k = 0;
  for (const ch of s) if (k < m && ch === w[k]) k++;
  if (k < m) return null;
  const sc = subseq(w, s, out);
  return sc < 0 ? null : 1 + Math.min(sc, 998);
}

/** Score and ink indexes (code point indexes into the summary) of a title query against a summary, or null. */
export function scoreTitle(words: string[][], summary: string): { score: number; indexes: number[] } | null {
  const s = [...summary].map(fold);
  const marks = new Set<number>();
  let score = 0;
  for (const w of words) {
    const out: number[] = [];
    const sc = matchWord(w, s, out);
    if (sc === null) return null;
    score += sc;
    out.forEach((i) => marks.add(i));
  }
  return { score, indexes: [...marks].sort((a, b) => a - b) };
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

export function toHit(c: Cand, current: number, initials: (account: string) => string, tier = "", match: number[] = [], titleMatch: number[] = []): T.TicketHit {
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
    titleMatch,
  };
}

/** Exact (all), starts-with (first 10), close (first 5), then title (first 10); within a tier: the current standup, active
 *  before done, key (a title tier: best score first). Titles are searched for every query of two or more characters, numbers included. */
export function findHits(cands: Cand[], query: string, current: number, limit: number, initials: (account: string) => string): T.TicketFind {
  const q = parseQuery(query);
  const tq = parseTitle(query);
  const out: T.TicketFind = { hits: [], prefixMore: 0, titleMore: 0 };
  if (!q && !tq) return out;
  const cap = limit > 0 ? limit : 20;
  type E = { c: Cand; match: number[]; done: boolean; score: number; titleMatch: number[] };
  const tiers: Record<Tier, E[]> = { exact: [], prefix: [], close: [], title: [] };
  const doneOf = (c: Cand): boolean => c.rowIn(homeOf(c.standupIds, current)).lane === "done";
  const keyIds = new Set<number>();
  if (q) {
    for (const c of cands) {
      const m = classify(q, c.key);
      if (!m) continue;
      tiers[m.tier].push({ c, match: m.match, done: doneOf(c), score: 0, titleMatch: [] });
      keyIds.add(c.id);
    }
  }
  if (tq) {
    for (const c of cands) {
      if (keyIds.has(c.id)) continue; // shown once, in its key tier
      const m = scoreTitle(tq, c.rowIn(homeOf(c.standupIds, current)).summary);
      if (m) tiers.title.push({ c, match: [], done: doneOf(c), score: m.score, titleMatch: m.indexes });
    }
  }
  const num = (k: string): number => Number(splitKey(k)?.number ?? 0);
  let room = cap;
  const take = (tier: Tier, max: number): number => {
    const list = tiers[tier].sort((a, b) => {
      if (a.score !== b.score) return b.score - a.score;
      const ac = a.c.standupIds.includes(current) ? 0 : 1;
      const bc = b.c.standupIds.includes(current) ? 0 : 1;
      if (ac !== bc) return ac - bc;
      if (a.done !== b.done) return a.done ? 1 : -1;
      const pa = splitKey(a.c.key)?.project ?? "";
      const pb = splitKey(b.c.key)?.project ?? "";
      return pa !== pb ? (pa < pb ? -1 : 1) : num(a.c.key) - num(b.c.key);
    });
    const n = Math.min(list.length, max, room);
    for (const x of list.slice(0, n)) out.hits.push(toHit(x.c, current, initials, tier, x.match, x.titleMatch));
    room -= n;
    return list.length - n;
  };
  take("exact", cap);
  out.prefixMore = take("prefix", 10);
  take("close", 5);
  out.titleMore = take("title", 10);
  return out;
}
