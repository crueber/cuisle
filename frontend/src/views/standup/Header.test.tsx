// WP-23 tests: Header (ux-spec §2.1, §4, §10; jira-sync §4.7 offline copy).
// Tabs is replaced by a double that exposes its props (the real Tabs is covered by Tabs.test.tsx), so these tests cover
// the header's own behaviour: wordmark, wiring to Tabs, the right cluster and what collapses as the window narrows.
// State comes from the real signals (state/app, route, standup, sync); the api facade is a hand-built fake.
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - Root:        <header> (role "banner") containing the wordmark text "CUISLE" (any case).
//   - Resync:      a <button> named exactly "Resync (R)". Its `title` contains the sync label text ("synced 2 min ago", ...)
//                  so the label is still reachable when `vw < 1700` hides it.
//   - Sync label:  an element whose own text is the status string. data-alert="true" for the offline string, "false" for
//                  the others. Strings (exact): "synced 2 min ago", "synced 2 min ago · partial", "offline · data from 3 h ago",
//                  "Syncing · issues 120/300". Relative ages: "N min ago" (< 1 h) and "N h ago" (>= 1 h).
//   - Cancel:      while a sync is running, a <button> whose name matches /cancel/i replaces the Resync button's job.
//   - Date:        an element with the text "Mon 23 Nov", taken from StandupView.today (a local-date string), not from the
//                  clock. Hidden when `vw < 1360`.
//   - Configure:   a <button> whose name matches /Configure/. Its visible text "Configure" shows when `vw >= 1280`; below
//                  that it is an icon button (the name still matches via aria-label).
//
// SPEC-NOTE: "synced N ago" is computed from the current standup's last successful sync time (view.lastSyncedAt, equal to
//   syncStatus.lastSuccessAt in these fixtures) and Date.now() at render. No timers or tickers: the label refreshes only
//   when the header re-renders (AGENTS invariant 2).
// SPEC-NOTE: the offline string is shown when the status is a failure with errorCode "offline" (SyncStatus.state "failed",
//   lastStatus "failed"); there is NO banner for it (ux-spec §2.1), so `banners` must stay empty.
// SPEC-NOTE: the sync label (vw >= 1700), date (vw >= 1360) and Configure text (vw >= 1280) boundaries are inclusive.
// SPEC-NOTE: Header filters drafts: Tabs receives only configured standups (configured=true), in `standups` order, and
//   activeId is route.standupId. It passes a numeric availW (header width minus wordmark and right cluster and 56).
// SPEC-NOTE: Tabs `onAdd` creates a draft through the api (config.create); `onSelect(id)` goes through state/standup
//   switchStandup, observable as route.standupId.
import { act, cleanup, fireEvent, render, screen } from "@testing-library/preact";
import axe from "axe-core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi } from "../../api";
import type { Api } from "../../api";
import type { StandupTab, StandupView, SyncStatus } from "../../api/types";
import { standups } from "../../state/app";
import { route } from "../../state/route";
import { views } from "../../state/standup";
import { syncStatus } from "../../state/sync";
import { banners } from "../../state/ui";
import { Header } from "./Header";

vi.mock("./Tabs", async () => {
  const { h } = await import("preact");
  type P = { tabs: { id: number }[]; activeId: number; availW: number; onSelect: (id: number) => void; onAdd: () => void };
  return {
    Tabs: (p: P) =>
      h(
        "div",
        { "data-testid": "tabs", "data-ids": p.tabs.map((t) => t.id).join(","), "data-active": String(p.activeId), "data-availw": String(p.availW) },
        h("button", { type: "button", onClick: () => p.onSelect(2) }, "pick-standup-2"),
        h("button", { type: "button", onClick: () => p.onAdd() }, "add-standup-double"),
      ),
  };
});

// 2026-11-23 15:00:00 UTC, a Monday.
const NOW = Date.UTC(2026, 10, 23, 15, 0, 0);
const MIN = 60_000;
const HOUR = 60 * MIN;

const tab = (id: number, name: string, configured = true): StandupTab => ({
  id, rev: 1, name, spaceKey: name.slice(0, 3).toUpperCase(), position: id, activeCount: 3, speakerCount: 2, configured, needsSync: false, lastSyncedAt: 0,
});

function status(over: Partial<SyncStatus> = {}): SyncStatus {
  return {
    standupId: 1, runId: 1, state: "idle", mode: "incremental", phase: "", done: 0, total: 0,
    lastSuccessAt: NOW - 2 * MIN, lastAttemptAt: NOW - 2 * MIN, lastStatus: "ok", warnings: [], errorCode: "", errorMessage: "", needsSync: false,
    ...over,
  };
}

function viewFor(id: number, lastSyncedAt: number): StandupView {
  return {
    standupId: id, rev: 1, generatedAt: NOW, today: "2026-11-23", asOf: NOW, live: true, name: `Standup ${id}`, spaceKey: "MOB", boardName: "Board", boardType: "scrum",
    scope: "sprint", sprint: null, pulseDays: 21, axis: [], lanes: [], speakers: [], tickets: [], backlog: [], backlogTotal: 0, counts: { active: 0, unassigned: 0, done: 0, backlog: 0 },
    options: { rowsMode: "fit", showCommits: true, showPRs: true, showComments: true, shadeWeekends: true, doneWindow: "sprint", doneLabel: "this sprint", devActivity: "ok" },
    sync: status(), warnings: [], previewKey: "", lastSyncedAt,
  };
}

function setup(opts: { sync?: SyncStatus; lastSyncedAt?: number } = {}) {
  const sync = opts.sync ?? status();
  const lastSyncedAt = opts.lastSyncedAt ?? sync.lastSuccessAt;
  standups.value = [tab(1, "Saffron Mobile"), tab(2, "Web & Checkout"), tab(3, "Draft Thing", false)];
  views.value = new Map([[1, viewFor(1, lastSyncedAt)], [2, viewFor(2, lastSyncedAt)]]);
  route.value = { view: "standup", standupId: 1, person: null };
  syncStatus.value = new Map([[1, sync]]);
  banners.value = [];
}

type FakeApi = Record<string, Record<string, ReturnType<typeof vi.fn>>>;
/** Every facade method is a vi.fn resolving undefined, so real state functions never hit "x is not a function". */
function makeFakeApi(over: Record<string, Record<string, (...a: never[]) => unknown>>): FakeApi {
  const fake: FakeApi = {};
  for (const [svc, methods] of Object.entries(wailsApi)) {
    fake[svc] = {};
    for (const m of Object.keys(methods)) fake[svc]![m] = vi.fn(async () => undefined);
  }
  for (const [svc, methods] of Object.entries(over)) for (const [m, impl] of Object.entries(methods)) fake[svc]![m] = vi.fn(impl as never);
  return fake;
}
let fake: FakeApi;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
  fake = makeFakeApi({
    sync: { start: async () => 7, status: async () => [] },
    config: { create: async () => ({ id: 99, draft: true, name: "", speakers: [], columns: [], lanes: [] }) as never },
    standup: { list: async () => [], view: (async (id: number) => viewFor(id, NOW)) as never },
  });
  setApi(fake as unknown as Api);
});

afterEach(() => {
  cleanup(); // unmount BEFORE resetting signals so the component never re-renders against emptied state
  vi.clearAllTimers();
  vi.useRealTimers();
  setApi(wailsApi);
  standups.value = [];
  views.value = new Map();
  syncStatus.value = new Map();
  banners.value = [];
  route.value = { view: "connect" };
});

const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0); });
const resyncBtn = () => screen.getByRole("button", { name: "Resync (R)" });

describe("Header frame", () => {
  it("shows the CUISLE wordmark in a banner landmark", () => {
    setup();
    render(<Header vw={1920} />);
    expect(screen.getByRole("banner")).toBeTruthy();
    expect(screen.getByText(/^cuisle$/i)).toBeTruthy();
  });

  it("gives Tabs only the configured standups (drafts are not tabs) and the current standup as active", () => {
    setup();
    render(<Header vw={1920} />);
    const tabs = screen.getByTestId("tabs");
    expect(tabs.getAttribute("data-ids")).toBe("1,2");
    expect(tabs.getAttribute("data-active")).toBe("1");
    expect(Number.isFinite(Number(tabs.getAttribute("data-availw")))).toBe(true);
  });

  it("follows the route's standup for the active tab", () => {
    setup();
    route.value = { view: "standup", standupId: 2, person: "p1" };
    render(<Header vw={1920} />);
    expect(screen.getByTestId("tabs").getAttribute("data-active")).toBe("2");
  });

  it("switches standup when Tabs selects one", async () => {
    setup();
    render(<Header vw={1920} />);
    fireEvent.click(screen.getByRole("button", { name: "pick-standup-2" }));
    await flush();
    expect(route.value.view).toBe("standup");
    expect(route.value.view === "standup" && route.value.standupId).toBe(2);
  });

  it("creates a draft through the api when Tabs adds a standup", async () => {
    setup();
    render(<Header vw={1920} />);
    fireEvent.click(screen.getByRole("button", { name: "add-standup-double" }));
    await flush();
    expect(fake.config!.create!).toHaveBeenCalledTimes(1);
  });
});

describe("Header sync status", () => {
  it("shows 'synced 2 min ago' and a Resync (R) button at wide widths", () => {
    setup();
    render(<Header vw={1920} />);
    expect(screen.getByText("synced 2 min ago").getAttribute("data-alert")).toBe("false");
    expect(resyncBtn()).toBeTruthy();
  });

  it("counts hours once an hour has passed: 'synced 3 h ago'", () => {
    setup({ sync: status({ lastSuccessAt: NOW - 3 * HOUR }) });
    render(<Header vw={1920} />);
    expect(screen.getByText("synced 3 h ago")).toBeTruthy();
  });

  it("starts an incremental sync of the current standup when Resync is pressed", async () => {
    setup();
    render(<Header vw={1920} />);
    fireEvent.click(resyncBtn());
    await flush();
    expect(fake.sync!.start!).toHaveBeenCalledTimes(1);
    expect(fake.sync!.start!).toHaveBeenCalledWith(1, "incremental");
  });

  it("shows progress with a cancel button while syncing, and cancel stops the run", async () => {
    setup({ sync: status({ state: "running", phase: "issues", done: 120, total: 300 }) });
    render(<Header vw={1920} />);
    expect(screen.getByText("Syncing · issues 120/300")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));
    await flush();
    expect(fake.sync!.cancel!).toHaveBeenCalledTimes(1);
    expect(fake.sync!.cancel!).toHaveBeenCalledWith(1);
  });

  it("shows sync progress text even below the 1700px label breakpoint, with a progress bar", () => {
    setup({ sync: status({ state: "running", phase: "issues", done: 120, total: 300 }) });
    render(<Header vw={1280} />);
    expect(screen.getByText("Syncing · issues 120/300")).toBeTruthy();
    const bar = screen.getByRole("progressbar", { name: /sync progress/i });
    expect(bar.getAttribute("aria-valuenow")).toBe("40");
    expect(bar.getAttribute("data-indeterminate")).toBe("false");
  });

  it("uses an indeterminate bar when the phase has no total, and none when idle", () => {
    setup({ sync: status({ state: "running", phase: "preflight", done: 0, total: 0 }) });
    const { unmount } = render(<Header vw={1280} />);
    expect(screen.getByRole("progressbar").getAttribute("data-indeterminate")).toBe("true");
    unmount();
    setup();
    render(<Header vw={1280} />);
    expect(screen.queryByRole("progressbar")).toBeNull();
  });

  it("flags a partial run: 'synced 2 min ago · partial'", () => {
    setup({ sync: status({ lastStatus: "partial", warnings: ["dev status unavailable"] }) });
    render(<Header vw={1920} />);
    expect(screen.getByText("synced 2 min ago · partial")).toBeTruthy();
  });

  it("reads 'offline · data from 3 h ago' in alert text, with no banner, after an offline sync failure", () => {
    setup({ sync: status({ state: "failed", lastStatus: "failed", errorCode: "offline", errorMessage: "offline", lastSuccessAt: NOW - 3 * HOUR, lastAttemptAt: NOW }) });
    render(<Header vw={1920} />);
    expect(screen.getByText("offline · data from 3 h ago").getAttribute("data-alert")).toBe("true");
    expect(banners.value).toHaveLength(0);
  });

  it("hides the label below 1700px but keeps Resync, with the label in its tooltip", () => {
    setup();
    const wide = render(<Header vw={1700} />);
    expect(screen.getByText("synced 2 min ago")).toBeTruthy(); // inclusive boundary
    wide.unmount();
    render(<Header vw={1699} />);
    expect(screen.queryByText("synced 2 min ago")).toBeNull();
    expect(resyncBtn().getAttribute("title") ?? "").toContain("synced 2 min ago");
  });
});

describe("Header right cluster collapses with width", () => {
  it("shows the date 'Mon 23 Nov' from the view at vw >= 1360 and hides it below", () => {
    setup();
    const wide = render(<Header vw={1360} />);
    expect(screen.getByText("Mon 23 Nov")).toBeTruthy();
    wide.unmount();
    render(<Header vw={1359} />);
    expect(screen.getByRole("button", { name: /Configure/ })).toBeTruthy(); // anchor
    expect(screen.queryByText("Mon 23 Nov")).toBeNull();
  });

  it("shows the Configure label at vw >= 1280 and an icon-only button below", () => {
    setup();
    const wide = render(<Header vw={1280} />);
    expect(screen.getByText("Configure")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Configure/ })).toBeTruthy();
    wide.unmount();
    render(<Header vw={1279} />);
    expect(screen.getByRole("button", { name: /Configure/ })).toBeTruthy();
    expect(screen.queryByText("Configure")).toBeNull();
  });

  it("opens Configure when the Configure button is pressed", async () => {
    setup();
    render(<Header vw={1920} />);
    fireEvent.click(screen.getByRole("button", { name: /Configure/ }));
    await flush();
    expect(route.value.view).toBe("configure");
  });

  it("keeps Resync and Configure reachable at the narrowest width (1024)", () => {
    setup();
    render(<Header vw={1024} />);
    expect(resyncBtn()).toBeTruthy();
    expect(screen.getByRole("button", { name: /Configure/ })).toBeTruthy();
  });
});

describe("Header accessibility and tokens", () => {
  it.each([1920, 1280, 1024])("has no axe violations at vw %i", async (vw) => {
    setup();
    vi.useRealTimers(); // axe schedules with real timers
    const { container } = render(<Header vw={vw} />);
    expect(screen.getByRole("banner")).toBeTruthy(); // anchor
    const res = await axe.run(container, { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } });
    expect(res.violations.map((v) => v.id)).toEqual([]);
  });

  it("uses no inline colours", () => {
    setup();
    const { container } = render(<Header vw={1920} />);
    expect(screen.getByRole("banner")).toBeTruthy(); // anchor
    for (const el of Array.from(container.querySelectorAll("[style]"))) {
      expect(el.getAttribute("style")).not.toMatch(/#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(/i);
    }
  });
});
