// P4 tests: the palette's pure model (F14, ux-spec §15.2-15.5).
import { describe, expect, it } from "vitest";
import { BUILTIN } from "../../commands/builtin";
import { makeCtx } from "../../commands/testutil";
import type { PersonHit, TicketFind, TicketHit } from "../../api/types";
import { applyPrefix, buildSections, flatten, fullKey, hasDigit, matchedTicket, type ModelInput } from "./model";

const hit = (id: number, key: string, tier: TicketHit["tier"], over: Partial<TicketHit> = {}): TicketHit => ({
  issueId: id, key, summary: `Summary ${key}`, type: "story", lane: "prog", statusName: "In Progress", assignee: "u1", assigneeInitials: "IC",
  daysInLane: 3, overLimit: false, done: false, removed: false, standupId: 1, standupIds: [1], tier, match: [], ...over,
});
const find = (hits: TicketHit[], prefixMore = 0): TicketFind => ({ hits, prefixMore });
const person = (id: string, name: string, standups: [number, number][]): PersonHit => ({
  accountId: id, displayName: name, initials: name.slice(0, 2).toUpperCase(), standups: standups.map(([standupId, active]) => ({ standupId, active })),
});

function input(over: Partial<ModelInput> = {}): ModelInput {
  return {
    mode: "", parent: null, text: "", ctx: makeCtx(), usage: {}, commands: BUILTIN, find: null, findFor: "", recents: [], people: [],
    standupLabel: (id) => (id === 1 ? "MOB" : id === 2 ? "WEB" : ""), ...over,
  };
}
const titles = (s: { title: string; hint?: string }[]) => s.map((x) => (x.hint ? `${x.title} ${x.hint}` : x.title));

describe("applyPrefix", () => {
  it("> # @ as the first character switch the mode and are consumed", () => {
    expect(applyPrefix(">sta", "", false)).toEqual({ mode: ">", text: "sta" });
    expect(applyPrefix("#14", "", false)).toEqual({ mode: "#", text: "14" });
    expect(applyPrefix("@", "", false)).toEqual({ mode: "@", text: "" });
  });
  it("does nothing mid-text, inside a mode, or inside a nested level; `/` is not a prefix", () => {
    expect(applyPrefix("a>b", "", false)).toEqual({ mode: "", text: "a>b" });
    expect(applyPrefix(">x", ">", false)).toEqual({ mode: ">", text: ">x" });
    expect(applyPrefix("@x", "", true)).toEqual({ mode: "", text: "@x" });
    expect(applyPrefix("/x", "", false)).toEqual({ mode: "", text: "/x" });
  });
});

describe("keys", () => {
  it("fullKey wants letters, a hyphen and digits", () => {
    expect(fullKey("mob-1729")).toBe("MOB-1729");
    expect(fullKey("  Plat-7 ")).toBe("PLAT-7");
    expect(fullKey("mob1729")).toBeNull();
    expect(fullKey("1729")).toBeNull();
    expect(fullKey("m-1")).toBeNull();
  });
  it("hasDigit", () => {
    expect(hasDigit("sta")).toBe(false);
    expect(hasDigit("m14")).toBe(true);
  });
});

describe("empty query", () => {
  it("recent tickets (3) then suggested commands", () => {
    const recents = [1, 2, 3, 4].map((i) => hit(i, `MOB-${i}`, ""));
    const s = buildSections(input({ recents }));
    expect(titles(s)).toEqual(["RECENT TICKETS", "COMMANDS"]);
    expect(s[0]!.rows).toHaveLength(3);
    expect(s[1]!.rows.map((r) => (r.kind === "command" ? r.hit.command.id : ""))).toEqual(["sync.resync", "view.group", "nav.person", "nav.standup", "nav.configure"]);
  });
  it("no recents: commands only", () => {
    expect(titles(buildSections(input()))).toEqual(["COMMANDS"]);
  });
});

describe("section order (ux-spec §15.2)", () => {
  const f = find([hit(9, "MOB-1429", "exact"), hit(8, "MOB-1428", "close")]);
  const people = [person("u1", "Noor Abadi", [[1, 4]])];
  it("a digit in the query puts tickets first, then commands, then people", () => {
    const s = buildSections(input({ text: "14", find: f, findFor: "14", people: [person("u1", "Iris 14", [[1, 4]])] }));
    expect(s[0]!.id).toBe("tickets-exact");
    expect(s.map((x) => x.id)).toEqual(expect.arrayContaining(["tickets-exact", "tickets-close"]));
    const order = s.map((x) => x.title);
    expect(order.indexOf("TICKETS")).toBeLessThan(order.indexOf("PEOPLE") === -1 ? 99 : order.indexOf("PEOPLE"));
  });
  it("no digit: commands first, then people; tickets are not asked for", () => {
    const s = buildSections(input({ text: "no", people, find: f, findFor: "no" }));
    expect(s.map((x) => x.title)).toEqual(["COMMANDS", "PEOPLE"]);
  });
  it("empty sections are omitted", () => {
    expect(buildSections(input({ text: "qqqzz" }))).toEqual([]);
  });
});

describe("ticket tiers", () => {
  it("exact, starts with (with the more row), one character off", () => {
    const hits = [hit(1, "MOB-14", "exact"), hit(2, "MOB-140", "prefix"), hit(3, "MOB-141", "prefix"), hit(4, "MOB-15", "close")];
    const s = buildSections(input({ mode: "#", text: "14", find: find(hits, 7), findFor: "14" }));
    expect(titles(s)).toEqual(["TICKETS exact", "TICKETS starts with", "TICKETS one character off"]);
    expect(s[1]!.rows.map((r) => r.kind)).toEqual(["ticket", "ticket", "more"]);
    expect(s[1]!.rows[2]).toMatchObject({ text: "↓ 7 more · keep typing" });
    expect(flatten(s)).toHaveLength(4); // the more row is not selectable
  });
  it("ignores a find result for a different query (stale)", () => {
    const s = buildSections(input({ mode: "#", text: "145", find: find([hit(1, "MOB-14", "exact")]), findFor: "14" }));
    expect(s).toEqual([]);
  });
  it("# mode with nothing typed lists up to 8 recents", () => {
    const recents = Array.from({ length: 8 }, (_, i) => hit(i + 1, `MOB-${i + 1}`, ""));
    const s = buildSections(input({ mode: "#", recents }));
    expect(s[0]!.rows).toHaveLength(8);
  });
});

describe("not synced", () => {
  it("a full key with no exact hit puts the fetch row first", () => {
    const s = buildSections(input({ text: "mob-1729", find: find([hit(1, "MOB-1727", "close")]), findFor: "mob-1729" }));
    expect(s[0]!.rows[0]).toMatchObject({ kind: "fetch", key: "MOB-1729" });
    expect(flatten(s)[0]!.kind).toBe("fetch");
  });
  it("not when the key is synced, nor before the answer has arrived, nor without a standup", () => {
    expect(flatten(buildSections(input({ text: "mob-1429", find: find([hit(9, "MOB-1429", "exact")]), findFor: "mob-1429" })))[0]!.kind).toBe("ticket");
    expect(flatten(buildSections(input({ text: "mob-1729", find: null, findFor: "" }))).some((r) => r.kind === "fetch")).toBe(false);
    const none = makeCtx({ standupId: null });
    expect(flatten(buildSections(input({ text: "mob-1729", ctx: none, find: find([]), findFor: "mob-1729" }))).some((r) => r.kind === "fetch")).toBe(false);
  });
});

describe("people", () => {
  const people = [person("u1", "Noor Abadi", [[1, 4]]), person("u2", "Kenji Mori", [[2, 5], [1, 2]]), person("u3", "Arjun Bose", [[2, 4]])];
  it("@ mode: fuzzy matches, the current standup preferred, Unassigned under ALSO", () => {
    const ctx = makeCtx({ view: { counts: { unassigned: 4 } } as never });
    const s = buildSections(input({ mode: "@", text: "no", people, ctx }));
    expect(titles(s)).toEqual(["PEOPLE go to their person view", "ALSO"]);
    const rows = s[0]!.rows;
    const by = (n: string) => rows.find((r) => r.kind === "person" && r.name === n);
    expect(rows).toHaveLength(3);
    expect(rows[0]).toMatchObject({ name: "Noor Abadi" }); // the best match first
    expect(by("Kenji Mori")).toMatchObject({ standupId: 1, active: 2, standupLabel: "MOB" }); // speaks in the current standup too
    expect(by("Arjun Bose")).toMatchObject({ standupId: 2, standupLabel: "WEB" });
    expect(s[1]!.rows[0]).toMatchObject({ unassigned: true, active: 4 });
  });
  it("outside @ mode people show only on a match and never include Unassigned", () => {
    const s = buildSections(input({ text: "noor", people }));
    expect(s.find((x) => x.id === "people")!.rows).toHaveLength(1);
    expect(s.some((x) => x.id === "also")).toBe(false);
  });
});

describe("> mode and nested levels", () => {
  it("> lists every visible command when nothing is typed", () => {
    const s = buildSections(input({ mode: ">" }));
    expect(s).toHaveLength(1);
    expect(s[0]!.rows.length).toBeGreaterThan(10);
  });
  it("a nested level shows its children under the parent's name and filters as you type", () => {
    const theme = BUILTIN.find((c) => c.id === "display.theme")!;
    const s = buildSections(input({ parent: theme }));
    expect(s[0]!.title).toBe("THEME");
    expect(s[0]!.rows).toHaveLength(3);
    const filtered = buildSections(input({ parent: theme, text: "dar" }));
    expect(filtered[0]!.rows).toHaveLength(1);
  });
  it("ticket commands appear when a ticket is matched, naming it", () => {
    const ctx = makeCtx({ ticket: { issueId: 9, key: "MOB-1429", standupId: 1 } });
    const s = buildSections(input({ text: "1429", ctx, find: find([hit(9, "MOB-1429", "exact")]), findFor: "1429" }));
    const cmds = s.find((x) => x.id === "commands")!.rows.map((r) => (r.kind === "command" ? r.hit.title : ""));
    expect(cmds).toEqual(expect.arrayContaining(["Refresh MOB-1429 from Jira", "Open MOB-1429 in Jira", "Copy MOB-1429"]));
  });
});

describe("matchedTicket", () => {
  it("prefers the exact hit, needs a fresh result", () => {
    const f = find([hit(8, "MOB-1428", "close"), hit(9, "MOB-1429", "exact", { standupId: 2 })]);
    expect(matchedTicket(f, "1429", "1429", 1)).toEqual({ issueId: 9, key: "MOB-1429", standupId: 2 });
    expect(matchedTicket(f, "142", "1429", 1)).toBeNull();
    expect(matchedTicket(null, "", "1429", 1)).toBeNull();
  });
});
