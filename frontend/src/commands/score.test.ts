// Golden tests for the palette's fuzzy scorer (F14, ux-spec §15.4).
import { describe, expect, it } from "vitest";
import { fuzzy, fuzzyBest } from "./score";

/** The text with the matched characters in [brackets]. */
const marked = (q: string, text: string): string => {
  const m = fuzzy(q, text);
  if (!m) return "";
  return [...text]
    .map((c, i) => (m.indexes.includes(i) ? `[${c}]` : c))
    .join("")
    .replace(/\]\[/g, "");
};

describe("fuzzy", () => {
  it("matches a subsequence, case-insensitively, and returns the matched indexes", () => {
    expect(marked("sta", "Group board by status")).toBe("Group board by [sta]tus");
    expect(marked("STA", "Group board by status")).toBe("Group board by [sta]tus");
    expect(marked("grb", "Group board by status")).toBe("[Gr]oup [b]oard by status");
  });

  it("is null when the query is not a subsequence", () => {
    expect(fuzzy("xyz", "Resync")).toBeNull();
    expect(fuzzy("ncysre", "Resync")).toBeNull();
    expect(fuzzy("resyncc", "Resync")).toBeNull();
  });

  it("an empty query matches everything with score 0", () => {
    expect(fuzzy("", "anything")).toEqual({ score: 0, indexes: [] });
    expect(fuzzy("   ", "anything")).toEqual({ score: 0, indexes: [] });
  });

  it("ignores spaces in the query", () => {
    expect(fuzzy("group status", "Group board by status")).not.toBeNull();
  });

  it("prefers a word start over the middle of a word", () => {
    expect(fuzzy("sta", "New standup")!.score).toBeGreaterThan(fuzzy("sta", "Install update")!.score);
  });

  it("scores a word-start run the same wherever the word is (ties fall to usage, then registry order)", () => {
    expect(fuzzy("sta", "Group board by status")!.score).toBe(fuzzy("sta", "Switch standup…")!.score);
  });

  it("prefers a contiguous run over a scattered match", () => {
    expect(fuzzy("rsy", "Resync")!.score).toBeLessThan(fuzzy("resy", "Resync")!.score);
    expect(fuzzy("sync", "Resync")!.score).toBeGreaterThan(fuzzy("sync", "xsxyxnxc")!.score);
  });

  it("penalises gaps: a tighter match wins", () => {
    expect(fuzzy("gb", "Go back")!.score).toBeGreaterThan(fuzzy("gb", "Group of the board")!.score);
  });

  it("finds the best alignment, not the first", () => {
    expect(marked("tus", "Group board by status")).toBe("Group board by sta[tus]");
  });

  it("treats a camelCase hump as a word start", () => {
    expect(marked("fb", "fooBar")).toBe("[f]oo[B]ar");
  });
});

describe("fuzzyBest", () => {
  it("falls back to aliases, which carry no indexes and rank below a title match", () => {
    const viaAlias = fuzzyBest("dark", "Theme", ["dark", "light"])!;
    expect(viaAlias.indexes).toEqual([]);
    const viaTitle = fuzzyBest("dark", "Dark mode", ["dark"])!;
    expect(viaTitle.indexes).toHaveLength(4);
    expect(viaTitle.score).toBeGreaterThan(viaAlias.score);
    expect(fuzzyBest("zzz", "Theme", ["dark"])).toBeNull();
  });
});
