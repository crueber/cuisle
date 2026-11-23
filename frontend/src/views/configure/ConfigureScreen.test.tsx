/* eslint-disable @typescript-eslint/no-explicit-any */
// ConfigureScreen: frame, rail, jump bar, section layout, save indicator, Done/Esc (ux-spec §9.1, §9.2, §9.5;
// architecture §5.3; AGENTS.md invariants 3 and 4).
//
// SPEC-NOTE (approach): ConfigureScreen is tested on its own frame (WP-26). The REAL StandupList, JumpBar and the REAL
//   shared components (SavedIndicator ...) render, but the nine section modules (owned by WP-26/27/28) are replaced by
//   fakes ("./sections/<Name>") that render <section><h2>Name</h2></section> and record the props they receive, so this
//   file can pass inside WP-26 alone. State side effects are replaced: vi.mock("state/config") (loadConfig,
//   createStandup, duplicateStandup, deleteStandup, moveStandup, leaveConfigure ... as vi.fn, real signals kept; the
//   async ones resolve), vi.mock("state/save") (retryFailed only), vi.mock("state/route") (goConfigure/navigate/... as
//   vi.fn) and vi.mock("state/app") (setAppSetting/... as vi.fn). Signals (`config`, `standups`, `route`, `connection`,
//   `settings`, `views`, `saveState`) are seeded directly. The facade is a fake Api (setApi); the event bus is the
//   in-memory one. Never calls WP-15 stubs that throw (initFromBoot, flush, showToast, ...).
// SPEC-NOTE: every section receives { config: <the `config` signal value, same object>, columns: 1 | 2 | 3 } (the
//   app-wide Display and Connection sections receive { columns } and no config).
// SPEC-NOTE: the selected standup is route.value.standupId; the loaded config is the `config` signal. When the signal is
//   null (or holds another standup) ConfigureScreen calls loadConfig(selectedId) itself on mount.
// SPEC-NOTE: choosing another standup in the rail results in loadConfig(id) OR goConfigure(id) (either is accepted:
//   state/route is mocked, so a route effect cannot run). Choosing "+ New standup" calls createStandup().
// SPEC-NOTE: the number of columns is derived from window.innerWidth (ux-spec §3 "Configure": mainW >= 1500 -> 3,
//   >= 960 -> 2, else 1; the rail/padding subtraction is the implementation's, and the widths used here (800, 1500,
//   2400) are unambiguous under both the standup-rail and the configure-rail formulas). The component re-evaluates on
//   window "resize". With 1 or 2 columns a jump bar (<nav>) is shown; with 3 it is not.
// SPEC-NOTE: sections appear as headings "Source", "Speakers", "Person view", "Pulse", "Whole board", "Status mapping",
//   "Export", "Display", "Connection" (name starts with that text), and are in DOM order column by column per the §9.2
//   table (3 columns: Source, Person view, Export, Speakers, Display, Connection, Pulse, Whole board, Status mapping;
//   2 columns: Source, Speakers, Whole board, Export, Person view, Pulse, Status mapping, Display, Connection; 1 column:
//   Source, Speakers, Person view, Pulse, Whole board, Status mapping, Export, Display, Connection).
// SPEC-NOTE: a jump-bar click calls element.scrollIntoView() on the section element (the element that contains the
//   section's heading); jsdom has no scrollIntoView so the test installs a spy.
// SPEC-NOTE: the title row is a heading with the standup name plus the text "standup N of M" (N = 1-based rail
//   position, M = rail length), a "DRAFT" tag when config.draft (so a draft appears twice: rail + title), and two buttons
//   "Duplicate" and "Delete standup" -> duplicateStandup(id) / deleteStandup(id) (the Undo toast is state's job).
// SPEC-NOTE: the header shows "CUISLE" and "Configure", the Saved indicator (role=status; text per saveState:
//   saved -> "Saved", settled -> "All changes saved", failed -> "Not saved" with a Retry button calling save.retryFailed)
//   and one primary "Done" button. No control is named "Save". No native <select> exists anywhere.
// SPEC-NOTE: Done (click) and Esc (keydown, nothing else open) each call leaveConfigure() exactly once.
// SPEC-NOTE: opening Configure triggers no sync and no Jira lookup (no sync.* / connection.search* / validateFilter /
//   connect calls) -- invariant 2.

import { act, render, screen, within } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../api";
import { createMemoryBus, setEventBus } from "../../api/events";
import type { Connection, StandupConfig, StandupTab } from "../../api/types";
import { connection, settings, standups } from "../../state/app";
import { config } from "../../state/config";
import { route } from "../../state/route";
import { saveState } from "../../state/save";
import { views } from "../../state/standup";
import { ConfigureScreen } from "./ConfigureScreen";

const st = vi.hoisted(() => ({
  loadConfig: vi.fn(async () => {}), saveField: vi.fn(), setSpace: vi.fn(), setBoard: vi.fn(), setColumnLane: vi.fn(),
  setWalkOrder: vi.fn(), setLaneLimit: vi.fn(), setSpeakerIncluded: vi.fn(), moveSpeaker: vi.fn(), sortSpeakers: vi.fn(),
  addSpeaker: vi.fn(), removeSpeaker: vi.fn(), validateFilter: vi.fn(), createStandup: vi.fn(async () => {}), duplicateStandup: vi.fn(async () => {}),
  copySettingsFrom: vi.fn(async () => {}), deleteStandup: vi.fn(async () => {}), moveStandup: vi.fn(async () => {}), leaveConfigure: vi.fn(async () => {}),
}));
const others = vi.hoisted(() => ({
  retryFailed: vi.fn(), goConfigure: vi.fn(), goStandup: vi.fn(), goConnect: vi.fn(), navigate: vi.fn(),
  setAppSetting: vi.fn(), applyAppearance: vi.fn(), refreshStandups: vi.fn(async () => {}), initFromBoot: vi.fn(), tabStandups: vi.fn(() => []),
}));
vi.mock("../../state/config", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../state/config")>()), ...st }));
vi.mock("../../state/save", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../state/save")>()), retryFailed: others.retryFailed }));
vi.mock("../../state/route", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../state/route")>()),
  goConfigure: others.goConfigure, goStandup: others.goStandup, goConnect: others.goConnect, navigate: others.navigate,
}));
vi.mock("../../state/app", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../state/app")>()),
  setAppSetting: others.setAppSetting, applyAppearance: others.applyAppearance, refreshStandups: others.refreshStandups,
  initFromBoot: others.initFromBoot, tabStandups: others.tabStandups,
}));

const sec = vi.hoisted(() => new Map<string, any>());
vi.mock("./sections/Source", async () => { const { h } = await import("preact"); return { SourceSection: (p: any) => { sec.set("Source", p); return h("section", {}, h("h2", {}, "Source")); } }; });
vi.mock("./sections/Speakers", async () => { const { h } = await import("preact"); return { SpeakersSection: (p: any) => { sec.set("Speakers", p); return h("section", {}, h("h2", {}, "Speakers")); } }; });
vi.mock("./sections/PersonView", async () => { const { h } = await import("preact"); return { PersonViewSection: (p: any) => { sec.set("Person view", p); return h("section", {}, h("h2", {}, "Person view")); } }; });
vi.mock("./sections/Pulse", async () => { const { h } = await import("preact"); return { PulseSection: (p: any) => { sec.set("Pulse", p); return h("section", {}, h("h2", {}, "Pulse")); } }; });
vi.mock("./sections/WholeBoard", async () => { const { h } = await import("preact"); return { WholeBoardSection: (p: any) => { sec.set("Whole board", p); return h("section", {}, h("h2", {}, "Whole board")); } }; });
vi.mock("./sections/StatusMapping", async () => { const { h } = await import("preact"); return { StatusMappingSection: (p: any) => { sec.set("Status mapping", p); return h("section", {}, h("h2", {}, "Status mapping")); } }; });
vi.mock("./sections/Export", async () => { const { h } = await import("preact"); return { ExportSection: (p: any) => { sec.set("Export", p); return h("section", {}, h("h2", {}, "Export")); } }; });
vi.mock("./sections/Display", async () => { const { h } = await import("preact"); return { DisplaySection: (p: any) => { sec.set("Display", p); return h("section", {}, h("h2", {}, "Display")); } }; });
vi.mock("./sections/Connection", async () => { const { h } = await import("preact"); return { ConnectionSection: (p: any) => { sec.set("Connection", p); return h("section", {}, h("h2", {}, "Connection")); } }; });

function mkConfig(over: Partial<StandupConfig> = {}): StandupConfig {
  return {
    id: 1, rev: 3, position: 0, name: "Saffron Mobile",
    space: { id: "10001", key: "MOB", name: "Saffron Mobile" },
    board: { id: "42", name: "MOB board", type: "scrum" },
    scope: "sprint", extraFilter: "", filterCheck: null,
    columns: [
      { columnName: "Ready", statusNames: ["Selected for Development"], lane: "todo", auto: true, order: 0 },
      { columnName: "Doing", statusNames: ["In Progress"], lane: "prog", auto: true, order: 0 },
      { columnName: "Code Review", statusNames: ["In Review"], lane: "review", auto: false, order: 0 },
      { columnName: "Verify", statusNames: ["Testing"], lane: "qa", auto: true, order: 0 },
      { columnName: "Closed", statusNames: ["Done"], lane: "done", auto: true, order: 0 },
    ],
    lanes: [
      { lane: "qa", label: "QA", limitDays: 3, columns: ["Verify"] },
      { lane: "review", label: "Review", limitDays: 2, columns: ["Code Review"] },
      { lane: "prog", label: "In progress", limitDays: 5, columns: ["Doing"] },
      { lane: "todo", label: "To do", limitDays: 10, columns: ["Ready"] },
      { lane: "done", label: "Done", limitDays: 0, columns: ["Closed"] },
    ],
    unmappedStatuses: [], rowsMode: "fit",
    speakers: [
      { accountId: "u-dana", name: "Dana Whitaker", initials: "DW", position: 0, included: true, manual: false, active: 3, note: "" },
      { accountId: "u-lee", name: "Lee Tran", initials: "LT", position: 1, included: true, manual: true, active: 1, note: "" },
    ],
    unassignedPosition: "last", reshuffleDaily: false, pulseDays: 14, showCommits: true, showPRs: true, showComments: false,
    shadeWeekends: true, doneWindow: "sprint", backlogRows: 8, needsSync: false, draft: false,
    ...over,
  };
}
function tab(id: number, name: string, over: Partial<StandupTab> = {}): StandupTab {
  return { id, rev: 1, name, spaceKey: "MOB", position: id - 1, activeCount: 12, speakerCount: 7, configured: true, needsSync: false, lastSyncedAt: 0, ...over };
}
const TABS = [
  tab(1, "Saffron Mobile", { spaceKey: "MOB", speakerCount: 7 }),
  tab(2, "Payments", { spaceKey: "PAY", speakerCount: 5, needsSync: true }),
  tab(3, "Fresh idea", { spaceKey: "", speakerCount: 0, configured: false }),
];
const CONN: Connection = { state: "ok", source: "jira", baseUrl: "https://acme.atlassian.net", account: "me@acme.com", displayName: "Morgan Ellis", devActivity: "ok", capabilities: {} as any };

function installFakeApi(over: Record<string, (...a: any[]) => any> = {}) {
  const calls: string[] = [];
  const impl: Record<string, (...a: any[]) => any> = {
    "sink.kinds": async () => [], "sink.list": async () => [], "sink.outbox": async () => [], ...over,
  };
  const fake = new Proxy({}, {
    get: (_t, svc: string) => new Proxy({}, {
      get: (_s, m: string) => (...a: unknown[]) => {
        const key = `${svc}.${m}`;
        calls.push(key);
        const f = impl[key];
        return f ? f(...a) : Promise.reject(new Error(`unstubbed ${key}`));
      },
    }),
  }) as unknown as Api;
  setApi(fake);
  return { calls };
}
const setWidth = (w: number) => Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: w });
const resize = (w: number) => { setWidth(w); act(() => { window.dispatchEvent(new Event("resize")); }); };
const headingsInOrder = () =>
  screen.getAllByRole("heading").map((h) => h.textContent?.trim() ?? "").map((t) =>
    ["Source", "Speakers", "Person view", "Pulse", "Whole board", "Status mapping", "Export", "Display", "Connection"].find((n) => t.startsWith(n)) ?? "",
  ).filter(Boolean);
const SECTION_NAMES = ["Source", "Speakers", "Person view", "Pulse", "Whole board", "Status mapping", "Export", "Display", "Connection"];
const scrollSpy = vi.fn();

beforeEach(() => {
  Object.values(st).forEach((f) => f.mockClear());
  Object.values(others).forEach((f) => f.mockClear());
  scrollSpy.mockReset();
  sec.clear();
  (Element.prototype as any).scrollIntoView = scrollSpy;
  installFakeApi();
  setEventBus(createMemoryBus());
  standups.value = TABS;
  connection.value = CONN;
  settings.value = { theme: "system", text_size: "standard", sync_on_open: "stale", sync_stale_hours: "6" };
  views.value = new Map();
  config.value = mkConfig();
  route.value = { view: "configure", standupId: 1 };
  saveState.value = "idle";
  setWidth(2400);
});
afterEach(() => setWidth(1024));

describe("ConfigureScreen: frame", () => {
  it("has the Configure header, a Done button and the save indicator", () => {
    render(<ConfigureScreen />);
    expect(screen.getByRole("button", { name: "Done" })).toBeTruthy();
    expect(screen.getAllByRole("status").length).toBeGreaterThan(0);
    expect(screen.getByText("CUISLE")).toBeTruthy();
    expect(screen.getByText(/^Configure$/)).toBeTruthy();
  });

  it("has no Save button anywhere (autosave) and no native select (every dropdown is a Combobox)", () => {
    const { container } = render(<ConfigureScreen />);
    expect(screen.getByRole("button", { name: "Done" })).toBeTruthy();
    expect(screen.queryAllByRole("button").filter((b) => /save/i.test(b.getAttribute("aria-label") ?? b.textContent ?? ""))).toHaveLength(0);
    expect(screen.queryByText(/^save(\s|$|&)/i)).toBeNull();
    expect(container.querySelector("select")).toBeNull();
  });

  it("does not start a sync or look anything up in Jira just by opening", () => {
    const { calls } = installFakeApi();
    render(<ConfigureScreen />);
    expect(screen.getByRole("button", { name: "Done" })).toBeTruthy();
    expect(calls.filter((c) => /^(sync\.|connection\.(search|validate|connect|disconnect))/.test(c))).toEqual([]);
    expect(st.validateFilter).not.toHaveBeenCalled();
  });

  it("loads the config of the routed standup when none is loaded", () => {
    config.value = null;
    route.value = { view: "configure", standupId: 2 };
    render(<ConfigureScreen />);
    expect(st.loadConfig).toHaveBeenCalledWith(2);
    expect(screen.getByRole("button", { name: "Done" })).toBeTruthy();
  });
});

describe("ConfigureScreen: save indicator", () => {
  it.each([
    ["saved", /^Saved$/],
    ["settled", /^All changes saved$/],
  ] as const)("shows '%s' state text in a status region", (state, text) => {
    saveState.value = state;
    render(<ConfigureScreen />);
    expect(screen.getAllByRole("status").some((s) => text.test(s.textContent?.trim() ?? ""))).toBe(true);
  });

  it("shows 'Not saved' with a Retry button when a save failed, and Retry re-runs the failed saves", () => {
    saveState.value = "failed";
    render(<ConfigureScreen />);
    expect(screen.getAllByRole("status").some((s) => /Not saved/.test(s.textContent ?? ""))).toBe(true);
    act(() => { screen.getByRole("button", { name: "Retry" }).click(); });
    expect(others.retryFailed).toHaveBeenCalledTimes(1);
  });

  it("shows no Retry button when nothing failed", () => {
    saveState.value = "saved";
    render(<ConfigureScreen />);
    expect(screen.getByRole("button", { name: "Done" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  });
});

describe("ConfigureScreen: Done and Esc", () => {
  it("Done calls leaveConfigure once", () => {
    render(<ConfigureScreen />);
    act(() => { screen.getByRole("button", { name: "Done" }).click(); });
    expect(st.leaveConfigure).toHaveBeenCalledTimes(1);
  });

  it("Esc calls leaveConfigure once", async () => {
    const user = userEvent.setup();
    render(<ConfigureScreen />);
    expect(screen.getByRole("button", { name: "Done" })).toBeTruthy();
    await user.keyboard("{Escape}");
    expect(st.leaveConfigure).toHaveBeenCalledTimes(1);
  });

  it("other keys do not leave", async () => {
    const user = userEvent.setup();
    render(<ConfigureScreen />);
    expect(screen.getByRole("button", { name: "Done" })).toBeTruthy();
    await user.keyboard("{Enter}a");
    expect(st.leaveConfigure).not.toHaveBeenCalled();
  });
});

describe("ConfigureScreen: standups rail and title", () => {
  it("renders the standups rail with every standup, drafts marked DRAFT", () => {
    render(<ConfigureScreen />);
    expect(screen.getByText(/standups\s*·\s*3/i)).toBeTruthy();
    expect(screen.getByText("Payments")).toBeTruthy();
    expect(screen.getByText("Fresh idea")).toBeTruthy();
    expect(screen.getAllByText("DRAFT")).toHaveLength(1);
    expect(screen.getByRole("button", { name: /new standup/i })).toBeTruthy();
  });

  it("shows the selected standup's name as a heading with its position, and no title DRAFT tag when configured", () => {
    render(<ConfigureScreen />);
    expect(screen.getAllByRole("heading").some((h) => h.textContent?.trim() === "Saffron Mobile")).toBe(true);
    expect(screen.getByText(/standup 1 of 3/i)).toBeTruthy();
    expect(screen.getAllByText("DRAFT")).toHaveLength(1);
  });

  it("tags a draft in the title as well as in the rail", () => {
    config.value = mkConfig({ id: 3, name: "Fresh idea", draft: true, space: null, board: null, columns: [], lanes: [], speakers: [] });
    route.value = { view: "configure", standupId: 3 };
    render(<ConfigureScreen />);
    expect(screen.getByText(/standup 3 of 3/i)).toBeTruthy();
    expect(screen.getAllByText("DRAFT")).toHaveLength(2);
  });

  it("choosing another standup in the rail opens it", () => {
    render(<ConfigureScreen />);
    act(() => { (screen.getByText("Payments") as HTMLElement).click(); });
    const opened = (st.loadConfig.mock.calls as unknown[][]).some((c) => c[0] === 2) || others.goConfigure.mock.calls.some((c) => c[0] === 2);
    expect(opened).toBe(true);
  });

  it("+ New standup creates a draft", () => {
    render(<ConfigureScreen />);
    act(() => { screen.getByRole("button", { name: /new standup/i }).click(); });
    expect(st.createStandup).toHaveBeenCalledTimes(1);
  });

  it("Duplicate and Delete standup act on the selected standup", () => {
    render(<ConfigureScreen />);
    act(() => { screen.getByRole("button", { name: /duplicate/i }).click(); });
    expect(st.duplicateStandup).toHaveBeenCalledWith(1);
    act(() => { screen.getByRole("button", { name: /delete standup/i }).click(); });
    expect(st.deleteStandup).toHaveBeenCalledWith(1);
    expect(st.duplicateStandup).toHaveBeenCalledTimes(1);
    expect(st.deleteStandup).toHaveBeenCalledTimes(1);
  });
});

describe("ConfigureScreen: sections and layout", () => {
  it.each([800, 1500, 2400])("renders all nine sections at width %i", (w) => {
    setWidth(w);
    render(<ConfigureScreen />);
    for (const name of SECTION_NAMES) {
      expect(screen.getAllByRole("heading").some((h) => (h.textContent ?? "").trim().startsWith(name))).toBe(true);
    }
  });

  it("3 columns (wide): no jump bar and sections in column order", () => {
    setWidth(2400);
    render(<ConfigureScreen />);
    expect(screen.getAllByRole("heading").length).toBeGreaterThan(8);
    expect(screen.queryByRole("navigation")).toBeNull();
    expect(headingsInOrder()).toEqual(["Source", "Person view", "Export", "Speakers", "Display", "Connection", "Pulse", "Whole board", "Status mapping"]);
  });

  it("2 columns (medium): a jump bar and sections in column order", () => {
    setWidth(1500);
    render(<ConfigureScreen />);
    expect(screen.getByRole("navigation")).toBeTruthy();
    expect(headingsInOrder()).toEqual(["Source", "Speakers", "Whole board", "Export", "Person view", "Pulse", "Status mapping", "Display", "Connection"]);
  });

  it("1 column (narrow): a jump bar with all nine pills and sections in one run", () => {
    setWidth(800);
    render(<ConfigureScreen />);
    const nav = screen.getByRole("navigation");
    expect([...nav.querySelectorAll("a, button")].map((e) => e.textContent?.trim())).toEqual(SECTION_NAMES);
    expect(headingsInOrder()).toEqual(SECTION_NAMES);
  });

  it("switches layout when the window is resized", () => {
    setWidth(2400);
    render(<ConfigureScreen />);
    expect(screen.queryByRole("navigation")).toBeNull();
    resize(800);
    expect(screen.getByRole("navigation")).toBeTruthy();
    resize(2400);
    expect(screen.queryByRole("navigation")).toBeNull();
  });

  it("jump bar pills scroll their section into view", () => {
    setWidth(800);
    render(<ConfigureScreen />);
    act(() => { within(screen.getByRole("navigation")).getByText("Speakers").click(); });
    expect(scrollSpy).toHaveBeenCalledTimes(1);
    const el = scrollSpy.mock.contexts[0] as HTMLElement;
    expect(within(el).getAllByRole("heading").some((h) => (h.textContent ?? "").trim().startsWith("Speakers"))).toBe(true);
  });

  it("passes the loaded config and the column count to every section", () => {
    const cfg = mkConfig();
    config.value = cfg;
    setWidth(2400);
    render(<ConfigureScreen />);
    for (const name of SECTION_NAMES) expect(sec.has(name)).toBe(true);
    for (const name of ["Source", "Speakers", "Person view", "Pulse", "Whole board", "Status mapping", "Export"]) {
      expect(sec.get(name).config).toBe(cfg);
      expect(sec.get(name).columns).toBe(3);
    }
    for (const name of ["Display", "Connection"]) {
      expect(sec.get(name).columns).toBe(3);
      expect(sec.get(name).config).toBeUndefined();
    }
  });

  it.each([[800, 1], [1500, 2], [2400, 3]] as [number, number][])("at width %i passes columns=%i to the sections", (w, cols) => {
    setWidth(w);
    render(<ConfigureScreen />);
    expect(sec.get("Source")?.columns).toBe(cols);
    expect(sec.get("Display")?.columns).toBe(cols);
  });

  it("re-renders sections with the new config when the signal changes", () => {
    render(<ConfigureScreen />);
    expect(sec.get("Pulse")?.config.pulseDays).toBe(14);
    act(() => { config.value = mkConfig({ pulseDays: 28 }); });
    expect(sec.get("Pulse")?.config.pulseDays).toBe(28);
  });
});

describe("ConfigureScreen: accessibility", () => {
  it("has no axe violations", async () => {
    const { container } = render(<ConfigureScreen />);
    expect(screen.getByRole("button", { name: "Done" })).toBeTruthy();
    expect(screen.getAllByRole("heading").length).toBeGreaterThan(8);
    const results = await axe.run(container, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"] },
      rules: { "color-contrast": { enabled: false } },
    });
    expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)).toEqual([]);
  });

  it("has no axe violations in the one-column layout with the failed-save state", async () => {
    setWidth(800);
    saveState.value = "failed";
    const { container } = render(<ConfigureScreen />);
    expect(screen.getByRole("navigation")).toBeTruthy();
    const results = await axe.run(container, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"] },
      rules: { "color-contrast": { enabled: false } },
    });
    expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)).toEqual([]);
  });
});
