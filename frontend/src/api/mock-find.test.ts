// F14: the mock's ticket finder (a TS port of internal/keyfind) and the mock facade's find / byIds / people / fetchByKey.
import { describe, expect, it } from "vitest";
import { AppError } from "./index";
import { createMockApi } from "./mock";
import { classify, parseQuery } from "./mock-find";

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
