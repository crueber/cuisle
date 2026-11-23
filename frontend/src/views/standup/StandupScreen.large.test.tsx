// WP-38F: at Large (TV) text the screen divides the window size by the scale before every layout decision
// (ux-spec §11): 1920x1080 Large decides like 1536x864 Standard.
import { cleanup, render, screen } from "@testing-library/preact";
import { afterEach, describe, expect, it, vi } from "vitest";
import { settings, standups } from "../../state/app";
import { route } from "../../state/route";
import { views } from "../../state/standup";
import { StandupScreen } from "./StandupScreen";
import type { StandupTab, StandupView } from "../../api/types";

vi.mock("./Header", async () => {
  const { h } = await import("preact");
  return { Header: (p: { vw: number }) => h("header", { "data-testid": "header", "data-vw": String(p.vw) }) };
});
vi.mock("./Rail", async () => {
  const { h } = await import("preact");
  return { Rail: (p: { mode: string }) => h("nav", { "data-testid": "rail", "data-mode": p.mode }) };
});
vi.mock("./PersonView", async () => {
  const { h } = await import("preact");
  return { PersonView: () => h("div", {}) };
});
vi.mock("./BoardView", async () => {
  const { h } = await import("preact");
  return { BoardView: (p: { mainW: number; vh: number }) => h("div", { "data-testid": "board", "data-mainw": String(p.mainW), "data-vh": String(p.vh) }) };
});

const TAB: StandupTab = { id: 1, rev: 1, name: "S", spaceKey: "K", position: 0, activeCount: 0, speakerCount: 0, configured: true, needsSync: false, lastSyncedAt: 0 };

function mount(w: number, h: number, textSize?: string) {
  Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: w });
  Object.defineProperty(window, "innerHeight", { configurable: true, writable: true, value: h });
  settings.value = textSize ? { text_size: textSize } : {};
  standups.value = [TAB];
  views.value = new Map([[1, { standupId: 1, rev: 1, speakers: [], tickets: [], lanes: [], axis: [], backlog: [], options: {} } as unknown as StandupView]]);
  route.value = { view: "standup", standupId: 1, person: null };
  render(<StandupScreen />);
  const read = () => ({
    vw: screen.getByTestId("header").getAttribute("data-vw"),
    rail: screen.getByTestId("rail").getAttribute("data-mode"),
    mainW: screen.getByTestId("board").getAttribute("data-mainw"),
    vh: screen.getByTestId("board").getAttribute("data-vh"),
  });
  const out = read();
  cleanup();
  return out;
}

afterEach(() => {
  settings.value = {};
});

describe("StandupScreen at Large text", () => {
  it("1920x1080 Large gives its children exactly what 1536x864 Standard does", () => {
    const standard = mount(1536, 864);
    const large = mount(1920, 1080, "large");
    expect(large).toEqual(standard);
    expect(large.vw).toBe("1536");
    expect(large.vh).toBe("864");
  });

  it("Large at 1920 drops to the compact-rail decisions that Standard 1280 would make", () => {
    expect(mount(1600, 900, "large").rail).toBe("compact"); // 1280 design px
    expect(mount(1600, 900).rail).toBe("full");
  });
});
