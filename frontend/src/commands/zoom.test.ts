// F15: the three zoom commands in the palette (ux-spec §15.4, §16.2): category View, shortcuts + − 0, detail "now 150%".
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi, type Api } from "../api";
import { route } from "../state/route";
import { seedZooms, zoomOf } from "../state/zoom";
import { BUILTIN } from "./builtin";
import { availability, searchCommands } from "./registry";
import { makeCtx } from "./testutil";
import type { Command } from "./types";

const cmd = (id: string): Command => BUILTIN.find((c) => c.id === id)!;

beforeEach(() => {
  setApi({ app: { setUIState: vi.fn(async () => undefined) } } as unknown as Api);
  seedZooms({});
  route.value = { view: "standup", standupId: 1, person: null };
});
afterEach(() => {
  setApi(wailsApi);
  seedZooms({});
});

describe("zoom commands", () => {
  it("are Zoom in, Zoom out and Reset zoom in View with the + − 0 shortcuts", () => {
    expect([cmd("view.zoom.in"), cmd("view.zoom.out"), cmd("view.zoom.reset")].map((c) => [c.title, c.category, c.shortcut])).toEqual([
      ["Zoom in", "View", "+"],
      ["Zoom out", "View", "−"],
      ["Reset zoom", "View", "0"],
    ]);
  });

  it("show the current level as their detail", () => {
    seedZooms({ "1": 150 });
    expect(cmd("view.zoom.in").detail?.(makeCtx())).toBe("now 150%");
    expect(cmd("view.zoom.reset").detail?.(makeCtx())).toBe("now 150%");
  });

  it("run through the same state as the keys", () => {
    void cmd("view.zoom.in").run!(makeCtx());
    expect(zoomOf(1)).toBe(110);
    void cmd("view.zoom.out").run!(makeCtx());
    void cmd("view.zoom.out").run!(makeCtx());
    expect(zoomOf(1)).toBe(90);
    void cmd("view.zoom.reset").run!(makeCtx());
    expect(zoomOf(1)).toBe(100);
  });

  it("are unavailable off the standup, at the ends of the range and (reset) at 100%", () => {
    expect(availability(cmd("view.zoom.in"), makeCtx({ onStandup: false }))).not.toBe(true);
    expect(availability(cmd("view.zoom.reset"), makeCtx())).not.toBe(true); // already 100%
    seedZooms({ "1": 200 });
    expect(availability(cmd("view.zoom.in"), makeCtx())).not.toBe(true);
    expect(availability(cmd("view.zoom.out"), makeCtx())).toBe(true);
    seedZooms({ "1": 70 });
    expect(availability(cmd("view.zoom.out"), makeCtx())).not.toBe(true);
    expect(availability(cmd("view.zoom.reset"), makeCtx())).toBe(true);
  });

  it("are found by typing zoom", () => {
    const hits = searchCommands(BUILTIN, "zoom", makeCtx()).map((h) => h.command.id);
    expect(hits).toEqual(expect.arrayContaining(["view.zoom.in", "view.zoom.out", "view.zoom.reset"]));
  });
});
