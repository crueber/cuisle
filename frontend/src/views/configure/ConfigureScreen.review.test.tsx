// WP-38F: ConfigureScreen review fixes: Large text columns, a fresh load on every visit, stable sections across a
// column-count change, flush when leaving.
import { act, cleanup, render } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { StandupConfig, StandupTab } from "../../api/types";
import { settings, standups } from "../../state/app";
import { config } from "../../state/config";
import { route } from "../../state/route";
import { ConfigureScreen } from "./ConfigureScreen";

const m = vi.hoisted(() => ({ loadConfig: vi.fn(async () => {}), flush: vi.fn(async () => {}) }));
vi.mock("../../state/config", async (orig) => ({ ...(await orig<typeof import("../../state/config")>()), loadConfig: m.loadConfig, leaveConfigure: vi.fn(async () => {}) }));
vi.mock("../../state/save", async (orig) => ({ ...(await orig<typeof import("../../state/save")>()), flush: m.flush }));

// Each section module is replaced by a fake that renders its name and one input (to see state survive).
const fake = vi.hoisted(() => (name: string) => async () => {
  const { h } = await import("preact");
  return { [`${name}Section`]: () => h("section", { "data-sec": name }, h("h2", {}, name), h("input", { "aria-label": `${name} draft` })) };
});
vi.mock("./sections/Source", fake("Source"));
vi.mock("./sections/Speakers", fake("Speakers"));
vi.mock("./sections/PersonView", fake("PersonView"));
vi.mock("./sections/Pulse", fake("Pulse"));
vi.mock("./sections/WholeBoard", fake("WholeBoard"));
vi.mock("./sections/StatusMapping", fake("StatusMapping"));
vi.mock("./sections/Export", fake("Export"));
vi.mock("./sections/Display", fake("Display"));
vi.mock("./sections/Connection", fake("Connection"));

const tab = (id: number): StandupTab => ({ id, rev: 1, name: `S${id}`, spaceKey: "K", position: id, activeCount: 0, speakerCount: 0, configured: true, needsSync: false, lastSyncedAt: 0 });
const cfg = { id: 1, name: "S1", draft: false } as unknown as StandupConfig;

function setWindow(w: number, h: number) {
  Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: w });
  Object.defineProperty(window, "innerHeight", { configurable: true, writable: true, value: h });
}

beforeEach(() => {
  m.loadConfig.mockClear();
  m.flush.mockClear();
  settings.value = {};
  standups.value = [tab(1), tab(2)];
  config.value = cfg;
  route.value = { view: "configure", standupId: 1 };
  setWindow(1920, 1080);
});
afterEach(() => cleanup());

const grid = () => document.querySelector(".cfg-grid")!;

describe("ConfigureScreen", () => {
  it("Large 1920x1080 chooses the columns Standard 1536 would (2), Standard 1920 gets 3", () => {
    render(<ConfigureScreen />);
    expect(grid().className).toContain("cfg-cols-3");
    cleanup();
    settings.value = { text_size: "large" };
    render(<ConfigureScreen />);
    expect(grid().className).toContain("cfg-cols-2");
  });

  it("loads the standup on every visit, even when the signal already holds it", () => {
    render(<ConfigureScreen />);
    expect(m.loadConfig).toHaveBeenCalledWith(1);
  });

  it("loads again when another standup is selected and flushes pending saves when leaving the selection", () => {
    render(<ConfigureScreen />);
    m.flush.mockClear();
    act(() => {
      route.value = { view: "configure", standupId: 2 };
    });
    expect(m.loadConfig).toHaveBeenLastCalledWith(2);
    expect(m.flush).toHaveBeenCalled();
  });

  it("lays sections out in explicit column containers (no CSS multi-column), one per column", () => {
    // CSS multi-column fragmented sideways in WebKit, so each column is its own container. Crossing a width threshold
    // remounts the sections; their values live in the config state, so nothing typed is lost (an open, unsent form
    // such as Reconnect resets).
    render(<ConfigureScreen />);
    expect(grid().className).toContain("cfg-cols-3");
    expect(grid().querySelectorAll(":scope > .cfg-col")).toHaveLength(3);
    act(() => {
      setWindow(1300, 900);
      window.dispatchEvent(new Event("resize"));
    });
    expect(grid().className).toContain("cfg-cols-2");
    expect(grid().querySelectorAll(":scope > .cfg-col")).toHaveLength(2);
    expect(document.querySelectorAll("[data-sec=Display]")).toHaveLength(1);
  });

  it("renders every section exactly once, in column-major order, at each column count", () => {
    const order = () => Array.from(document.querySelectorAll("[data-sec]")).map((e) => e.getAttribute("data-sec"));
    render(<ConfigureScreen />);
    expect(order()).toEqual(["Source", "PersonView", "Export", "Speakers", "Display", "Connection", "Pulse", "WholeBoard", "StatusMapping"]);
    act(() => {
      setWindow(1300, 900);
      window.dispatchEvent(new Event("resize"));
    });
    expect(order()).toEqual(["Source", "Speakers", "WholeBoard", "Export", "PersonView", "Pulse", "StatusMapping", "Display", "Connection"]);
  });
});
