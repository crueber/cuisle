import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

// ux-spec §10: text >= 4.5:1, UI glyphs and lane segments >= 3:1, in both themes. axe only checks rendered text, so the
// token pairs that are drawn together are checked here straight from design/tokens.json (no browser needed).
// Not enforced (decorative, never carry meaning alone): rule-mid, chip-border, lane-done-history (a deliberately
// recessive history colour on finished tickets).

const TOKENS = fileURLToPath(new URL("../../../design/tokens.json", import.meta.url));
type Tree = { [k: string]: string | Tree };
const colors = (JSON.parse(readFileSync(TOKENS, "utf8")) as { color: Record<"light" | "dark", Tree> }).color;

function flat(tree: Tree): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(tree)) {
    if (typeof v === "string") out[k] = v;
    else for (const [k2, v2] of Object.entries(v)) out[`${k}-${k2}`] = v2 as string;
  }
  return out;
}

function lum(hex: string): number {
  const c = [1, 3, 5]
    .map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0]! + 0.7152 * c[1]! + 0.0722 * c[2]!;
}
function ratio(a: string, b: string): number {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

const TEXT = 4.5;
const UI = 3;
const BACKDROPS = ["bg", "surface", "rail-bg", "field-bg"];
const LANES = ["todo", "prog", "review", "qa", "done", "backlog"];
const PAIRS: ReadonlyArray<readonly [string, string, number]> = [
  ...BACKDROPS.flatMap((b) => [["fg", b, TEXT], ["muted", b, TEXT]] as const),
  ["done-fg", "bg", TEXT],
  ["done-fg", "surface", TEXT],
  ["alert-text", "bg", TEXT],
  ["alert-text", "surface", TEXT],
  ["alert-text", "rail-bg", TEXT],
  ["alert-fg", "alert-bg", TEXT],
  ["tag-fg", "tag-bg", TEXT],
  ["selected-fg", "selected-bg", TEXT],
  ["button-fg", "button-bg", TEXT],
  ["more-bar-fg", "more-bar-bg", TEXT],
  ...["story", "bug", "task", "spike", "other"].flatMap((t) => [[`type-${t}`, "bg", TEXT], [`type-${t}`, "surface", TEXT]] as const),
  ["done-icon", "bg", UI],
  ["done-icon", "surface", UI],
  ["tick", "bg", UI],
  ["tick-comment", "bg", UI],
  ["today", "bg", UI],
  ["hatch", "bg", UI],
  ["unassigned-border", "bg", UI],
  ["focus-ring", "bg", UI],
  ["focus-ring", "surface", UI],
  ["focus-ring", "rail-bg", UI],
  ...LANES.flatMap((l) => ["bg", "weekend", "surface"].map((b) => [`lane-${l}`, b, UI] as const)),
];

for (const theme of ["light", "dark"] as const) {
  test.describe(`token contrast, ${theme} theme`, () => {
    const c = flat(colors[theme]);
    for (const [fg, bg, min] of PAIRS) {
      test(`${fg} on ${bg} >= ${min}`, () => {
        const r = ratio(c[fg]!, c[bg]!);
        expect(r, `${fg} ${c[fg]} on ${bg} ${c[bg]} = ${r.toFixed(2)}`).toBeGreaterThanOrEqual(min);
      });
    }
  });
}
