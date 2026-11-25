// P3 tests: the registry, command ranking and the initial command set (F14, ux-spec §15.4).
import { describe, expect, it } from "vitest";
import { BUILTIN } from "./builtin";
import { availability, DEFAULT_SUGGESTED, isVisible, registerCommands, searchCommands, suggestedCommands, titleOf, allCommands } from "./registry";
import { makeCtx, tab } from "./testutil";
import type { Command } from "./types";
import type { SyncStatus, UpdateStatus } from "../api/types";

const ids = (hits: { command: Command }[]): string[] => hits.map((h) => h.command.id);
const byId = (id: string): Command => BUILTIN.find((c) => c.id === id)!;

describe("registry", () => {
  it("registers every builtin once, in a stable order, with unique ids", () => {
    const all = allCommands().map((c) => c.id);
    expect(new Set(all).size).toBe(all.length);
    expect(all).toEqual(expect.arrayContaining(BUILTIN.map((c) => c.id)));
  });

  it("re-registering an id replaces it in place", () => {
    const before = allCommands().length;
    registerCommands([{ id: "test.replace", title: "One", category: "App" }]);
    registerCommands([{ id: "test.replace", title: "Two", category: "App" }]);
    expect(allCommands().length).toBe(before + 1);
    expect(titleOf(allCommands().find((c) => c.id === "test.replace")!, makeCtx())).toBe("Two");
  });

  it("a command is either runnable or nested or opens a mode, never two of them", () => {
    for (const c of BUILTIN) {
      const kinds = [c.run, c.children, c.mode].filter((x) => x !== undefined && x !== "").length;
      expect(kinds, c.id).toBeLessThanOrEqual(1);
    }
  });

  it("every command has a registered category from the spec", () => {
    const ok = ["Navigate", "Sync", "View", "Display", "Ticket", "Configure", "App"];
    for (const c of BUILTIN) expect(ok).toContain(c.category);
  });
});

describe("searchCommands", () => {
  const ctx = makeCtx();
  it("`sta` ranks Group board by status first, as in the mockup", () => {
    const hits = searchCommands(BUILTIN, "sta", ctx);
    expect(hits[0]!.title).toBe("Group board by status");
    expect(hits[0]!.indexes).toEqual([15, 16, 17]);
    expect(ids(hits)).toEqual(expect.arrayContaining(["nav.standup", "cfg.new", "display.text_size", "app.update.install"]));
  });

  it("ties break by recent use, then registry order", () => {
    const base = ids(searchCommands(BUILTIN, "sta", ctx));
    expect(base.indexOf("view.group")).toBeLessThan(base.indexOf("nav.standup"));
    const used = ids(searchCommands(BUILTIN, "sta", ctx, { "cfg.new": 5 }));
    expect(used.indexOf("cfg.new")).toBeLessThan(used.indexOf("nav.standup"));
    expect(used.indexOf("cfg.new")).toBeLessThan(used.indexOf("view.group"));
  });

  it("matches aliases: dark finds Theme, pull finds Resync", () => {
    expect(ids(searchCommands(BUILTIN, "dark", ctx))).toContain("display.theme");
    expect(searchCommands(BUILTIN, "pull", ctx)[0]!.command.id).toBe("sync.resync");
  });

  it("an empty query lists the visible commands in registry order", () => {
    const hits = searchCommands(BUILTIN, "", ctx);
    expect(ids(hits)).toEqual(BUILTIN.filter((c) => isVisible(c, ctx)).map((c) => c.id));
  });

  it("no match, no hit", () => {
    expect(searchCommands(BUILTIN, "qqqqzz", ctx)).toEqual([]);
  });
});

describe("visibility and availability", () => {
  it("Cancel sync shows only while a sync runs", () => {
    const running = { state: "running" } as SyncStatus;
    expect(isVisible(byId("sync.cancel"), makeCtx())).toBe(false);
    expect(isVisible(byId("sync.cancel"), makeCtx({ sync: running }))).toBe(true);
    expect(availability(byId("sync.resync"), makeCtx({ sync: running }))).toBe("already syncing");
    expect(availability(byId("sync.resync"), makeCtx())).toBe(true);
  });

  it("Reconnect Jira shows only when auth is invalid", () => {
    expect(isVisible(byId("app.reconnect"), makeCtx())).toBe(false);
    expect(isVisible(byId("app.reconnect"), makeCtx({ authInvalid: true }))).toBe(true);
  });

  it("ticket commands need a ticket and name its key", () => {
    const refresh = byId("ticket.refresh");
    expect(isVisible(refresh, makeCtx())).toBe(false);
    const withTicket = makeCtx({ ticket: { issueId: 9, key: "MOB-1429", standupId: 1 } });
    expect(isVisible(refresh, withTicket)).toBe(true);
    expect(titleOf(refresh, withTicket)).toBe("Refresh MOB-1429 from Jira");
    expect(titleOf(byId("ticket.open"), withTicket)).toBe("Open MOB-1429 in Jira");
    expect(titleOf(byId("ticket.copy"), withTicket)).toBe("Copy MOB-1429");
  });

  it("Install update is disabled with 'none waiting' until a release is available", () => {
    const install = byId("app.update.install");
    expect(availability(install, makeCtx())).toBe("none waiting");
    const update = { channel: "github", available: { version: "v1.2.0" } } as unknown as UpdateStatus;
    expect(availability(install, makeCtx({ update }))).toBe(true);
    expect(availability(byId("app.update.check"), makeCtx())).toBe("not in this build");
    expect(availability(byId("app.update.check"), makeCtx({ update }))).toBe(true);
  });

  it("standup-only commands are disabled with a reason on Configure", () => {
    const onConfigure = makeCtx({ route: { view: "configure", standupId: 1 }, onStandup: false, onBoard: false });
    expect(availability(byId("view.group"), onConfigure)).toBe("open a standup first");
    expect(availability(byId("nav.configure"), onConfigure)).toBe("open a standup first");
    expect(availability(byId("sync.resync"), onConfigure)).toBe(true);
  });

  it("Switch standup needs two standups", () => {
    expect(availability(byId("nav.standup"), makeCtx({ tabs: [tab(1, "MOB", "MOB", 0)] }))).toBe("only one standup");
    expect(availability(byId("nav.standup"), makeCtx())).toBe(true);
  });

  it("a disabled command is listed with its reason", () => {
    const hit = searchCommands(BUILTIN, "install update", makeCtx())[0]!;
    expect(hit.command.id).toBe("app.update.install");
    expect(hit.disabled).toBe("none waiting");
  });
});

describe("titles and details follow the context", () => {
  it("Group board by… names the other mode and shows the current one", () => {
    const g = byId("view.group");
    expect(titleOf(g, makeCtx({ group: "assignee" }))).toBe("Group board by status");
    expect(titleOf(g, makeCtx({ group: "status" }))).toBe("Group board by assignee");
    expect(g.detail!(makeCtx({ group: "status" }))).toBe("now: status");
  });

  it("Resync's detail is the sync label", () => {
    expect(byId("sync.resync").detail!(makeCtx())).toBe("synced 1 min ago");
  });
});

describe("nested commands", () => {
  it("Theme offers System, Light and Dark with the current one checked", () => {
    const kids = byId("display.theme").children!(makeCtx({ theme: "dark" }));
    expect(kids.map((k) => titleOf(k, makeCtx()))).toEqual(["System", "Light", "Dark"]);
    expect(kids.map((k) => k.checked!(makeCtx({ theme: "dark" })))).toEqual([false, false, true]);
    expect(kids[0]!.detail!(makeCtx({ resolvedTheme: "light" }))).toContain("currently light");
  });

  it("Text size offers Standard and Large (TV)", () => {
    const kids = byId("display.text_size").children!(makeCtx({ textSize: "large" }));
    expect(kids.map((k) => titleOf(k, makeCtx()))).toEqual(["Standard", "Large (TV)"]);
    expect(kids.map((k) => k.checked!(makeCtx({ textSize: "large" })))).toEqual([false, true]);
  });

  it("Switch standup lists the standups in tab order with the current one checked and 1-9 shortcuts", () => {
    const ctx = makeCtx({ standupId: 2 });
    const kids = byId("nav.standup").children!(ctx);
    expect(kids.map((k) => titleOf(k, ctx))).toEqual(["MOB", "WEB", "DATA"]);
    expect(kids.map((k) => k.checked!(ctx))).toEqual([false, true, false]);
    expect(kids.map((k) => k.shortcut)).toEqual(["1", "2", "3"]);
  });

  it("Go to person opens the @ section instead of running", () => {
    const p = byId("nav.person");
    expect(p.mode).toBe("@");
    expect(p.run).toBeUndefined();
  });
});

describe("suggestedCommands", () => {
  const ctx = makeCtx();
  it("falls back to Resync, Group board by…, Go to person…, Switch standup…, Open Configure", () => {
    expect(ids(suggestedCommands(BUILTIN, ctx))).toEqual([...DEFAULT_SUGGESTED]);
  });

  it("puts the most-run commands first and tops up from the defaults", () => {
    const hits = suggestedCommands(BUILTIN, ctx, { "cfg.new": 9, "nav.board": 4 });
    expect(ids(hits)).toEqual(["cfg.new", "nav.board", "sync.resync", "view.group", "nav.person"]);
  });

  it("never suggests a hidden command", () => {
    expect(ids(suggestedCommands(BUILTIN, ctx, { "sync.cancel": 50 }))).not.toContain("sync.cancel");
  });
});
