// The palette's fuzzy scorer (F14, ux-spec §15.4). fzf-style, written here (no runtime dependency): a case-insensitive
// subsequence match scored by a small dynamic programme with a bonus for matches that start a word, a bonus for
// contiguous runs and a penalty for gaps. It returns the best alignment's score and the matched character indexes (the
// ones the palette inks).

export interface FuzzyMatch {
  score: number;
  /** Indexes into the text of the matched characters, ascending. */
  indexes: number[];
}

const MATCH = 16;
const BOUNDARY = 10; // the character starts a word (after a space or punctuation, or a camelCase hump)
const FIRST = 4; // extra for the very first character of the text
const CONSECUTIVE = 8; // directly after the previous matched character
const GAP_START = -3; // opening a gap between two matched characters
const GAP_EXTEND = -1; // each further skipped character

const isAlnum = (c: string): boolean => /[\p{L}\p{N}]/u.test(c);

function bonusAt(text: string, j: number): number {
  if (j === 0) return BOUNDARY + FIRST;
  const prev = text[j - 1]!;
  const cur = text[j]!;
  if (!isAlnum(prev)) return BOUNDARY;
  if (prev === prev.toLowerCase() && cur !== cur.toLowerCase()) return BOUNDARY; // camelCase
  return 0;
}

/** The query without whitespace, lower-cased; "group  status" matches "Group board by status". */
export const normaliseQuery = (q: string): string => q.replace(/\s+/g, "").toLowerCase();

/** Best fuzzy alignment of `query` in `text`, or null when it is not a subsequence. An empty query matches with score 0. */
export function fuzzy(query: string, text: string): FuzzyMatch | null {
  const q = normaliseQuery(query);
  if (q === "") return { score: 0, indexes: [] };
  const t = text.toLowerCase();
  const n = q.length;
  const m = t.length;
  if (n > m) return null;

  // Quick reject: the subsequence must exist at all.
  for (let i = 0, j = 0; i < n; i++, j++) {
    while (j < m && t[j] !== q[i]) j++;
    if (j === m) return null;
  }

  // best[i][j]: the best score with q[i] matched at text[j] (-Infinity when impossible); from[i][j]: the column of q[i-1].
  const NEG = Number.NEGATIVE_INFINITY;
  const best: number[][] = [];
  const from: number[][] = [];
  for (let i = 0; i < n; i++) {
    const row = new Array<number>(m).fill(NEG);
    const back = new Array<number>(m).fill(-1);
    for (let j = i; j < m; j++) {
      if (t[j] !== q[i]) continue;
      const base = MATCH + bonusAt(text, j);
      if (i === 0) {
        row[j] = base;
        continue;
      }
      const prev = best[i - 1]!;
      let top = NEG;
      let topK = -1;
      for (let k = i - 1; k < j; k++) {
        const s = prev[k]!;
        if (s === NEG) continue;
        const gap = j - k - 1;
        const v = s + (gap === 0 ? CONSECUTIVE : GAP_START + GAP_EXTEND * (gap - 1));
        if (v > top) {
          top = v;
          topK = k;
        }
      }
      if (topK >= 0) {
        row[j] = base + top;
        back[j] = topK;
      }
    }
    best.push(row);
    from.push(back);
  }

  const last = best[n - 1]!;
  let end = -1;
  let score = NEG;
  for (let j = 0; j < m; j++) {
    if (last[j]! > score) {
      score = last[j]!;
      end = j;
    }
  }
  if (end < 0) return null;
  const indexes = new Array<number>(n);
  for (let i = n - 1, j = end; i >= 0; i--) {
    indexes[i] = j;
    j = from[i]![j]!;
  }
  return { score, indexes };
}

/** The best of several texts (a command's title first, then its keywords at a penalty); keyword hits carry no indexes. */
export function fuzzyBest(query: string, title: string, keywords: readonly string[] = []): FuzzyMatch | null {
  let out = fuzzy(query, title);
  for (const k of keywords) {
    const m = fuzzy(query, k);
    if (m && (!out || m.score - 12 > out.score)) out = { score: m.score - 12, indexes: [] };
  }
  return out;
}
