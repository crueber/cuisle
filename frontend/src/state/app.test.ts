// WP-15 tests: app-level stores (state/app.ts; architecture §4.3, §5.3, §6.4).
//
// SPEC-NOTE: initFromBoot also seeds the other stores named in the doc comment ("seeds every store"): the
//   `views` cache from Bootstrap.view (keyed by view.standupId), `syncStatus` from Bootstrap.sync (keyed by
//   standupId) and `route` from initialRoute(boot). Bootstrap.settings null becomes {}.
// SPEC-NOTE: appearance attributes are `<html data-theme="light|dark">` (system resolves through
//   matchMedia("(prefers-color-scheme: dark)") to light or dark, never the literal "system") and
//   `<html data-text="standard|large">`. Defaults when the settings lack a key: theme system, text size standard.
// SPEC-NOTE: the matchMedia fake supports both addEventListener("change") and the legacy addListener, so either
//   subscription style passes.
// SPEC-NOTE: setAppSetting saves under the key `app:<key>` and is immediate (no debounce); it is verified by
//   behaviour (per-key serialisation and last-write-wins coalescing), not by spying on the queue.
// SPEC-NOTE: whether initFromBoot raises the settings-loss banner for `recovered` is not asserted (only the
//   `recovered` signal).
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Api, Bootstrap, StandupTab, StandupView, SyncStatus } from "../api/index";

type Fake = Record<string, Record<string, Mock>>;
const DEFAULTS: Record<string, unknown> = { "standup.list": [], "sync.status": [], "sync.start": 1 };

async function boot() {
  vi.resetModules();
  const idx = await import("../api/index");
  const events = await import("../api/events");
  const fake: Fake = {};
  for (const [svc, methods] of Object.entries(idx.wailsApi)) {
    const f: Record<string, Mock> = (fake[svc] = {});
    for (const m of Object.keys(methods)) f[m] = vi.fn(async () => structuredClone(DEFAULTS[`${svc}.${m}`]));
  }
  // A realistic default: StandupService.View always answers with a minimal view of the requested standup, so code that
  // reads the result of an un-mocked call does not throw. Tests override it where the content matters.
  fake["standup"]!["view"]!.mockImplementation(async (id: number) => ({ standupId: id, rev: 1, name: `S${id}`, speakers: [], tickets: [], lanes: [], axis: [], backlog: [], warnings: [] }));
  idx.setApi(fake as unknown as Api);
  const bus = events.createMemoryBus();
  events.setEventBus(bus);
  return {
    fake,
    A: await import("./app"),
    R: await import("./route"),
    St: await import("./standup"),
    Sy: await import("./sync"),
  };
}

function tab(id: number, over: Partial<StandupTab> = {}): StandupTab {
  return { id, rev: 1, name: `Standup ${id}`, spaceKey: `K${id}`, position: id, activeCount: 0, speakerCount: 0, configured: true, needsSync: false, lastSyncedAt: 0, ...over };
}
function view(standupId: number): StandupView {
  return { standupId, rev: 1, name: `Standup ${standupId}`, speakers: [], tickets: [], lanes: [], axis: [], backlog: [], warnings: [] } as unknown as StandupView;
}
function bootstrap(over: Partial<Record<keyof Bootstrap, unknown>> = {}): Bootstrap {
  return {
    build: { version: "1.2.3", commit: "abc", date: "", goVersion: "", os: "darwin", arch: "arm64", dev: false },
    settings: { theme: "dark", text_size: "large" },
    connection: { state: "ok", source: "jira", baseUrl: "https://acme.atlassian.net", account: "me@acme.com", displayName: "Me", devActivity: "ok", capabilities: {} },
    standups: [tab(1), tab(2)],
    ui: { lastStandupId: 2, lastView: "standup", lastPerson: { 2: "iris" }, window: null },
    view: view(2),
    sync: [],
    recovered: false,
    ...over,
  } as unknown as Bootstrap;
}

// ── matchMedia fake ─────────────────────────────────────────────────────────────────────────────
let osDark = false;
const listeners = new Set<(e: { matches: boolean }) => void>();
function setOsDark(dark: boolean) {
  osDark = dark;
  for (const l of [...listeners]) l({ matches: dark });
}
function installMatchMedia() {
  osDark = false;
  listeners.clear();
  const impl = (query: string) => {
    const matchesNow = () => (query.includes("dark") ? osDark : query.includes("light") ? !osDark : false);
    const mql = {
      media: query,
      get matches() {
        return matchesNow();
      },
      onchange: null as null | ((e: unknown) => void),
      addEventListener: (_t: string, cb: (e: { matches: boolean }) => void) => void listeners.add(cb),
      removeEventListener: (_t: string, cb: (e: { matches: boolean }) => void) => void listeners.delete(cb),
      addListener: (cb: (e: { matches: boolean }) => void) => void listeners.add(cb),
      removeListener: (cb: (e: { matches: boolean }) => void) => void listeners.delete(cb),
      dispatchEvent: () => true,
    };
    return mql;
  };
  Object.defineProperty(window, "matchMedia", { configurable: true, writable: true, value: impl });
}
const html = () => document.documentElement;

let T: Awaited<ReturnType<typeof boot>>;
beforeEach(async () => {
  vi.useFakeTimers();
  installMatchMedia();
  html().removeAttribute("data-theme");
  html().removeAttribute("data-text");
  T = await boot();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("initFromBoot", () => {
  it("seeds_the_app_signals_from_the_boot_object", () => {
    const b = bootstrap({ recovered: true });
    T.A.initFromBoot(b);
    expect(T.A.settings.value).toEqual({ theme: "dark", text_size: "large" });
    expect(T.A.connection.value).toEqual(b.connection);
    expect(T.A.standups.value).toEqual(b.standups);
    expect(T.A.build.value).toEqual(b.build);
    expect(T.A.recovered.value).toBe(true);
  });

  it("treats_null_settings_as_an_empty_map", () => {
    T.A.initFromBoot(bootstrap({ settings: null }));
    expect(T.A.settings.value).toEqual({});
  });

  it("keeps_drafts_in_the_standups_signal", () => {
    const standups = [tab(1), tab(2, { configured: false, spaceKey: "" })];
    T.A.initFromBoot(bootstrap({ standups }));
    expect(T.A.standups.value).toHaveLength(2);
  });

  it("seeds_the_view_cache_with_the_boot_view", () => {
    const v = view(2);
    T.A.initFromBoot(bootstrap({ view: v }));
    expect(T.St.views.value.get(2)).toEqual(v);
  });

  it("leaves_the_view_cache_empty_when_there_is_no_boot_view", () => {
    T.A.initFromBoot(bootstrap({ view: null, standups: [] }));
    expect(T.St.views.value.size).toBe(0);
  });

  it("seeds_sync_status_by_standup_id", () => {
    const sync = [{ standupId: 1, state: "idle", lastStatus: "ok" }, { standupId: 2, state: "running", lastStatus: "" }] as unknown as SyncStatus[];
    T.A.initFromBoot(bootstrap({ sync }));
    expect(T.Sy.syncStatus.value.get(1)?.lastStatus).toBe("ok");
    expect(T.Sy.syncStatus.value.get(2)?.state).toBe("running");
  });

  it("sets_the_route_from_initialRoute", () => {
    const b = bootstrap();
    T.A.initFromBoot(b);
    expect(T.R.route.value).toEqual(T.R.initialRoute(b));
    expect(T.R.route.value).toEqual({ view: "standup", standupId: 2, person: "iris" });
  });

  it("applies_the_saved_theme_and_text_size_to_the_html_element", () => {
    T.A.initFromBoot(bootstrap({ settings: { theme: "dark", text_size: "large" } }));
    expect(html().dataset.theme).toBe("dark");
    expect(html().dataset.text).toBe("large");
  });

  it("defaults_to_system_theme_and_standard_text_when_settings_are_empty", () => {
    setOsDark(true);
    T.A.initFromBoot(bootstrap({ settings: {} }));
    expect(html().dataset.theme).toBe("dark");
    expect(html().dataset.text).toBe("standard");
  });
});

describe("tabStandups", () => {
  it("returns_only_configured_standups_in_position_order", () => {
    T.A.standups.value = [tab(10, { position: 3 }), tab(11, { position: 0, configured: false, spaceKey: "" }), tab(12, { position: 1 }), tab(13, { position: 2 })];
    expect(T.A.tabStandups().map((t) => t.id)).toEqual([12, 13, 10]);
  });

  it("is_empty_when_there_are_only_drafts", () => {
    T.A.standups.value = [tab(1, { configured: false, spaceKey: "" })];
    expect(T.A.tabStandups()).toEqual([]);
  });
});

describe("applyAppearance", () => {
  it("sets_explicit_light_and_dark_themes_and_the_text_size", () => {
    T.A.applyAppearance("light", "standard");
    expect(html().dataset.theme).toBe("light");
    expect(html().dataset.text).toBe("standard");
    T.A.applyAppearance("dark", "large");
    expect(html().dataset.theme).toBe("dark");
    expect(html().dataset.text).toBe("large");
  });

  it("resolves_system_through_matchMedia", () => {
    setOsDark(true);
    T.A.applyAppearance("system", "standard");
    expect(html().dataset.theme).toBe("dark");
    T.A.applyAppearance("light", "standard");
    setOsDark(false);
    T.A.applyAppearance("system", "standard");
    expect(html().dataset.theme).toBe("light");
  });

  it("follows_OS_theme_changes_while_set_to_system", () => {
    setOsDark(false);
    T.A.applyAppearance("system", "standard");
    expect(html().dataset.theme).toBe("light");
    setOsDark(true);
    expect(html().dataset.theme).toBe("dark");
    setOsDark(false);
    expect(html().dataset.theme).toBe("light");
  });

  it("stops_following_the_OS_once_an_explicit_theme_is_chosen", () => {
    T.A.applyAppearance("system", "standard");
    T.A.applyAppearance("light", "standard");
    setOsDark(true);
    expect(html().dataset.theme).toBe("light");
  });
});

describe("setAppSetting", () => {
  it("applies_the_theme_to_html_at_once_and_updates_the_settings_signal", () => {
    T.A.settings.value = { theme: "light" };
    T.A.setAppSetting("theme", "dark");
    expect(html().dataset.theme).toBe("dark");
    expect(T.A.settings.value.theme).toBe("dark");
  });

  it("applies_the_text_size_at_once", () => {
    T.A.setAppSetting("text_size", "large");
    expect(html().dataset.text).toBe("large");
  });

  it("merges_into_existing_settings_without_dropping_other_keys", () => {
    T.A.settings.value = { theme: "light", sync_on_open: "stale" };
    T.A.setAppSetting("sync_stale_hours", "12");
    expect(T.A.settings.value).toEqual({ theme: "light", sync_on_open: "stale", sync_stale_hours: "12" });
  });

  it("saves_through_AppService_SetAppSetting_immediately", async () => {
    T.A.setAppSetting("sync_on_open", "always");
    await vi.advanceTimersByTimeAsync(0);
    expect(T.fake.app!.setAppSetting).toHaveBeenCalledTimes(1);
    expect(T.fake.app!.setAppSetting).toHaveBeenCalledWith("sync_on_open", "always");
  });

  it("serialises_and_coalesces_writes_to_the_same_setting", async () => {
    let release!: () => void;
    T.fake.app!.setAppSetting!.mockImplementationOnce(() => new Promise<void>((r) => (release = () => r())));
    T.A.setAppSetting("theme", "dark"); // in flight
    await vi.advanceTimersByTimeAsync(0);
    T.A.setAppSetting("theme", "light"); // pending, replaced
    T.A.setAppSetting("theme", "system"); // pending, wins
    await vi.advanceTimersByTimeAsync(1_000);
    expect(T.fake.app!.setAppSetting).toHaveBeenCalledTimes(1);
    release();
    await vi.advanceTimersByTimeAsync(0);
    expect(T.fake.app!.setAppSetting!.mock.calls).toEqual([
      ["theme", "dark"],
      ["theme", "system"],
    ]);
  });

  it("does_not_make_one_setting_wait_for_another", async () => {
    T.fake.app!.setAppSetting!.mockImplementationOnce(() => new Promise<void>(() => undefined));
    T.A.setAppSetting("theme", "dark"); // never completes
    T.A.setAppSetting("text_size", "large");
    await vi.advanceTimersByTimeAsync(0);
    expect(T.fake.app!.setAppSetting!.mock.calls).toEqual([
      ["theme", "dark"],
      ["text_size", "large"],
    ]);
  });
});

describe("refreshStandups", () => {
  it("re_reads_StandupService_List_into_the_standups_signal", async () => {
    const fresh = [tab(5), tab(6, { configured: false, spaceKey: "" })];
    T.fake.standup!.list!.mockResolvedValue(fresh);
    await T.A.refreshStandups();
    expect(T.fake.standup!.list).toHaveBeenCalledTimes(1);
    expect(T.A.standups.value).toEqual(fresh);
  });
});
