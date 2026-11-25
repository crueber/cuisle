// F14: the mock's ticket finder (a TS port of internal/keyfind) and the mock facade's find / byIds / people / fetchByKey.
import { describe, expect, it } from "vitest";
import { AppError } from "./index";
import { createMockApi } from "./mock";
import { classify, parseQuery, parseTitle, scoreTitle } from "./mock-find";

describe("parseQuery / classify (port of internal/keyfind)", () => {
  it("normalises", () => {
    expect(parseQuery("1429")).toEqual({ letters: "", digits: "1429" });
    expect(parseQuery("MOB-1429")).toEqual({ letters: "mob", digits: "1429" });
    expect(parseQuery("14 29")).toEqual({ letters: "", digits: "1429" });
    expect(parseQuery("m14")).toEqual({ letters: "m", digits: "14" });
    expect(parseQuery("mob")).toBeNull();
    expect(parseQuery("")).toBeNull();
    expect(parseQuery("14a2")).toBeNull();
  });
  it("tiers and ink indexes", () => {
    const q = (s: string) => parseQuery(s)!;
    expect(classify(q("1429"), "MOB-1429")).toEqual({ tier: "exact", match: [4, 5, 6, 7] });
    expect(classify(q("mob1429"), "MOB-1429")?.match).toEqual([0, 1, 2, 4, 5, 6, 7]);
    expect(classify(q("1429"), "MOB-1428")).toEqual({ tier: "close", match: [4, 5, 6] });
    expect(classify(q("1429"), "MOB-1249")).toEqual({ tier: "close", match: [4, 7] });
    expect(classify(q("m14"), "MOB-1407")).toEqual({ tier: "prefix", match: [0, 4, 5] });
    expect(classify(q("pl41"), "MOB-412")).toBeNull();
    expect(classify(q("1429"), "MOB-1327")).toBeNull();
    expect(classify(q("5"), "MOB-6")).toBeNull();
  });
});

describe("mock facade: finder", () => {
  it("find: exact first, then starts-with, then close; every hit is placed in a standup", async () => {
    const api = createMockApi();
    const r = await api.ticket.find("1429", 1, 20);
    expect(r.hits[0]).toMatchObject({ key: "MOB-1429", tier: "exact", standupId: 1, removed: false });
    expect(r.hits[0]?.match).toEqual([4, 5, 6, 7]);
    const tiers = r.hits.map((h) => h.tier);
    expect(tiers).toEqual([...tiers].sort((a, b) => ["exact", "prefix", "close"].indexOf(a) - ["exact", "prefix", "close"].indexOf(b)));
    expect(r.hits.some((h) => h.tier === "close")).toBe(true);
    for (const h of r.hits) expect(h.standupIds.length).toBeGreaterThan(0);
  });

  it("find: no digits or an unknown project finds nothing; limits hold", async () => {
    const api = createMockApi();
    expect((await api.ticket.find("mob", 1, 20)).hits).toEqual([]);
    expect((await api.ticket.find("zzz14", 1, 20)).hits).toEqual([]);
    const many = await api.ticket.find("1", 1, 20);
    expect(many.hits.filter((h) => h.tier === "prefix").length).toBeLessThanOrEqual(10);
    expect(many.hits.length).toBeLessThanOrEqual(20);
  });

  it("byIds keeps the order, skips unknown ids and leaves tier empty", async () => {
    const api = createMockApi();
    const v = await api.standup.view(1);
    const [a, b] = [v.tickets[3]!, v.tickets[0]!];
    const hits = await api.ticket.byIds([a.id, 424242, b.id], 1);
    expect(hits.map((h) => h.issueId)).toEqual([a.id, b.id]);
    expect(hits[0]).toMatchObject({ tier: "", match: [], key: a.key });
  });

  it("people: deduplicated, tab order, no unassigned", async () => {
    const api = createMockApi();
    const people = await api.standup.people();
    expect(people.length).toBeGreaterThan(10);
    expect(new Set(people.map((p) => p.accountId)).size).toBe(people.length);
    expect(people.some((p) => p.accountId === "unassigned")).toBe(false);
    expect(people[0]?.standups[0]?.standupId).toBe(1);
  });
});

describe("mock facade: fetchByKey (magic keys)", () => {
  const code = async (p: Promise<unknown>): Promise<string> => p.then(() => "ok", (e: unknown) => (e as AppError).code);

  it("stores a key of a known project on no board; find then lists it as not on board", async () => {
    const api = createMockApi();
    expect(await api.ticket.find("mob-1777", 1, 20).then((r) => r.hits.filter((h) => h.tier === "exact"))).toEqual([]);
    const d = await api.ticket.fetchByKey(1, "mob-1777");
    expect(d.row.key).toBe("MOB-1777");
    const hit = (await api.ticket.find("mob-1777", 1, 20)).hits[0];
    expect(hit).toMatchObject({ key: "MOB-1777", tier: "exact", removed: true, standupId: 1, standupIds: [] });
  });

  it("fetching a synced key just opens it", async () => {
    const api = createMockApi();
    const d = await api.ticket.fetchByKey(1, "MOB-1421");
    expect(d.row.key).toBe("MOB-1421");
  });

  it("errors: 404, 403, 500 (offline), unknown project, bad shape", async () => {
    const api = createMockApi();
    expect(await code(api.ticket.fetchByKey(1, "MOB-1404"))).toBe("not_found");
    expect(await code(api.ticket.fetchByKey(1, "MOB-1403"))).toBe("forbidden");
    expect(await code(api.ticket.fetchByKey(1, "MOB-1500"))).toBe("offline");
    expect(await code(api.ticket.fetchByKey(1, "NOPE-12"))).toBe("not_found");
    expect(await code(api.ticket.fetchByKey(1, "MOB-5000"))).toBe("not_found");
    expect(await code(api.ticket.fetchByKey(1, "nonsense"))).toBe("validation");
  });
});

// The same example table as internal/keyfind/title_test.go (TestTitleMatchTable, TestParseTitle, TestTitleScoreOrder):
// the mock's matcher is a port, so the two must agree.
describe("title matcher (port of internal/keyfind/title.go)", () => {
  const S = "Recipe card crops the hero photo";
  const ink = (q: string, summary: string): number[] | null => {
    const w = parseTitle(q);
    return w ? (scoreTitle(w, summary)?.indexes ?? null) : null;
  };
  const score = (q: string, summary: string): number => scoreTitle(parseTitle(q)!, summary)!.score;

  it("parseTitle: two non-space characters, at least one letter or digit", () => {
    expect(parseTitle("recipe card")).toHaveLength(2);
    expect(parseTitle("  Recipe   CARD ")).toEqual([[..."recipe"], [..."card"]]);
    for (const q of ["a", "a ", " ", "", "-", "--", "寿"]) expect(parseTitle(q), q).toBeNull();
    for (const q of ["ab", "mob1429", "v2", "1429", "14 29", "1429 share", "寿司"]) expect(parseTitle(q), q).not.toBeNull();
  });

  it("numbers match titles only contiguously", () => {
    const T = "Upgrade to node 2024 and 14 more";
    expect(ink("2024", T)).toEqual([16, 17, 18, 19]);
    expect(ink("14", T)).toEqual([25, 26]);
    expect(ink("224", T)).toBeNull(); // digits scattered across "2024" do not match
  });

  it("matches the Go table: words, order, partial words, case, substring, subsequence, compactness", () => {
    expect(ink("recipe card", S)).toEqual([0, 1, 2, 3, 4, 5, 7, 8, 9, 10]);
    expect(ink("card recipe", S)).toEqual([0, 1, 2, 3, 4, 5, 7, 8, 9, 10]);
    expect(ink("rec ca", S)).toEqual([0, 1, 2, 7, 8]);
    expect(ink("RECIPE CARD", S)).toEqual([0, 1, 2, 3, 4, 5, 7, 8, 9, 10]);
    expect(ink("cip", S)).toEqual([2, 3, 4]);
    expect(ink("rcd", S)).toEqual([0, 7, 10]);
    expect(ink("recipe photo cat zzz", S)).toBeNull();
    expect(ink("zebra", S)).toBeNull();
    expect(ink("ce", "Recipe card")).toBeNull(); // 2-rune words must be contiguous
    expect(ink("rpe", "Recipe card")).not.toBeNull();
    expect(ink("mob", "Meal plan templates for busy weeks")).toBeNull(); // not compact
    expect(ink("mpt", "Meal plan templates")).toEqual([0, 5, 10]);
  });

  it("scores: whole word > word start > substring > subsequence; two whole words beat two starts", () => {
    const whole = score("card", "Recipe card crops");
    const start = score("card", "Cardinal photo crops");
    const sub = score("card", "Discard the crops");
    const seq = score("crd", "Recipe card crops");
    expect(whole).toBeGreaterThan(start);
    expect(start).toBeGreaterThan(sub);
    expect(sub).toBeGreaterThan(seq);
    expect(seq).toBeGreaterThan(0);
    expect(score("recipe card", "Recipe card")).toBeGreaterThan(score("recipe card", "Recipes cardinal"));
    expect(score("rcp", "Recipe card crops")).toBeGreaterThan(score("rcp", "xrxxcxxpx"));
  });

  it("indexes are code point indexes (an emoji is one), case folding keeps positions", () => {
    const s = "🍳 Crème brûlée soufflé";
    const idx = ink("brûlée", s)!;
    expect([...s].slice(idx[0], idx[idx.length - 1]! + 1).join("")).toBe("brûlée");
    expect(ink("été", "Été à Paris")).toEqual([0, 1, 2]);
    expect(ink("寿司", "今日の寿司メニュー")).toEqual([3, 4]);
  });
});

describe("mock facade: title finder", () => {
  it("recipe card: a title hit with the summary's characters inked; key tiers are not involved", async () => {
    const api = createMockApi();
    const r = await api.ticket.find("recipe card", 1, 20);
    expect(r.hits.length).toBeGreaterThan(0);
    expect(r.hits.every((h) => h.tier === "title" && h.match.length === 0)).toBe(true);
    const h = r.hits[0]!;
    expect(h.summary).toMatch(/^Recipe card crops/);
    expect([...h.summary].filter((_, i) => h.titleMatch.includes(i)).join("")).toBe("Recipecard");
  });

  it("key matches come first; short or symbol-only queries find nothing", async () => {
    const api = createMockApi();
    const k = await api.ticket.find("mob1429", 1, 20);
    expect(k.hits[0]).toMatchObject({ key: "MOB-1429", tier: "exact" });
    expect(k.hits.some((h) => h.tier === "title")).toBe(false);
    for (const q of ["r", "-", "--"]) expect((await api.ticket.find(q, 1, 20)).hits, q).toHaveLength(0);
  });

  it("the title tier shows ten and counts the rest", async () => {
    const api = createMockApi();
    const r = await api.ticket.find("re", 1, 40);
    const n = r.hits.filter((h) => h.tier === "title").length;
    expect(n).toBeLessThanOrEqual(10);
    if (n === 10) expect(r.titleMore).toBeGreaterThanOrEqual(0);
  });
});
