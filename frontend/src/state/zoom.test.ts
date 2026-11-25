// F15: the per-standup zoom state (state/zoom.ts): the nine steps, seeding from boot, write-through, scroll anchoring.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi, type Api } from "../api";
import { contentScale, settings } from "./app";
import { route } from "./route";
import {
  captureAnchor,
  currentZoom,
  resetZoom,
  restoreAnchor,
  seedZooms,
  setZoom,
  stepFrom,
  zoomBy,
  zoomFlash,
  zoomOf,
  ZOOM_STEPS,
} from "./zoom";

let setUIState: ReturnType<typeof vi.fn>;
beforeEach(() => {
  setUIState = vi.fn(async () => undefined);
  setApi({ app: { setUIState } } as unknown as Api);
  seedZooms({});
  settings.value = {};
  route.value = { view: "standup", standupId: 1, person: null };
});
afterEach(() => {
  setApi(wailsApi);
  document.body.innerHTML = "";
});

describe("steps", () => {
  it("are 70, 80, 90, 100, 110, 125, 150, 175, 200", () => {
    expect([...ZOOM_STEPS]).toEqual([70, 80, 90, 100, 110, 125, 150, 175, 200]);
  });

  it("stepFrom moves one step either way and stops at the ends", () => {
    expect(stepFrom(100, 1)).toBe(110);
    expect(stepFrom(100, -1)).toBe(90);
    expect(stepFrom(200, 1)).toBe(200);
    expect(stepFrom(70, -1)).toBe(70);
    expect(stepFrom(125, 1)).toBe(150);
    expect(stepFrom(105, 1)).toBe(110); // a value between steps goes to the next one in that direction
    expect(stepFrom(105, -1)).toBe(100);
  });
});

describe("seeding and reading", () => {
  it("absent means 100, and a value that is not a step reads as 100", () => {
    seedZooms({ "1": 150, "2": 105, "3": undefined });
    expect(zoomOf(1)).toBe(150);
    expect(zoomOf(2)).toBe(100);
    expect(zoomOf(3)).toBe(100);
    expect(zoomOf(99)).toBe(100);
  });

  it("is per standup: switching standups restores that standup's zoom", () => {
    seedZooms({ "1": 150, "2": 80 });
    expect(currentZoom()).toBe(150);
    route.value = { view: "standup", standupId: 2, person: "x" };
    expect(currentZoom()).toBe(80); // the person view of the same standup shares it
    route.value = { view: "standup", standupId: 3, person: null };
    expect(currentZoom()).toBe(100);
  });

  it("is 100 off the standup screen, so Configure keeps the text size scale only", () => {
    seedZooms({ "1": 150 });
    route.value = { view: "configure", standupId: 1 };
    expect(currentZoom()).toBe(100);
    expect(contentScale.value).toBe(1);
  });
});

describe("content scale = text size x zoom", () => {
  it("multiplies (Large 1.25 x 150% = 1.875)", () => {
    seedZooms({ "1": 150 });
    expect(contentScale.value).toBe(1.5);
    settings.value = { text_size: "large" };
    expect(contentScale.value).toBe(1.875);
  });
});

describe("setZoom", () => {
  it("changes the level, persists it with the standup id at once, and flashes", () => {
    const before = zoomFlash.value.seq;
    expect(setZoom(1, 150)).toBe(true);
    expect(zoomOf(1)).toBe(150);
    expect(setUIState).toHaveBeenCalledWith({ standupId: 1, zoom: 150 });
    expect(zoomFlash.value).toEqual({ seq: before + 1, percent: 150 });
  });

  it("does nothing for the same level or a value that is not a step", () => {
    expect(setZoom(1, 100)).toBe(false);
    expect(setZoom(1, 105)).toBe(false);
    expect(setUIState).not.toHaveBeenCalled();
  });

  it("keeps other standups' levels", () => {
    setZoom(1, 150);
    setZoom(2, 80);
    expect([zoomOf(1), zoomOf(2)]).toEqual([150, 80]);
  });

  it("a failed save does not throw", async () => {
    setUIState.mockRejectedValueOnce(new Error("db"));
    expect(() => setZoom(1, 125)).not.toThrow();
    await Promise.resolve();
    expect(zoomOf(1)).toBe(125);
  });
});

describe("zoomBy and resetZoom", () => {
  it("step the standup on screen, and stop at 70 and 200", () => {
    expect(zoomBy(1)).toBe(true);
    expect(zoomOf(1)).toBe(110);
    resetZoom();
    expect(zoomOf(1)).toBe(100);
    for (let i = 0; i < 12; i++) zoomBy(1);
    expect(zoomOf(1)).toBe(200);
    for (let i = 0; i < 12; i++) zoomBy(-1);
    expect(zoomOf(1)).toBe(70);
  });

  it("do nothing off the standup screen", () => {
    route.value = { view: "configure", standupId: 1 };
    expect(zoomBy(1)).toBe(false);
    expect(resetZoom()).toBe(false);
    expect(setUIState).not.toHaveBeenCalled();
  });
});

describe("scroll anchoring", () => {
  // jsdom has no layout: give each box a rect and let scrollTop be a plain number.
  function scene(scrollTop: number, tops: Record<string, number>) {
    document.body.innerHTML = `<main class="sf-main">${Object.keys(tops).map((k) => `<section data-anchor="${k}"></section>`).join("")}</main>`;
    const main = document.querySelector<HTMLElement>(".sf-main")!;
    main.scrollTop = scrollTop;
    main.getBoundingClientRect = () => ({ top: 100 }) as DOMRect;
    document.querySelectorAll<HTMLElement>("[data-anchor]").forEach((el) => {
      el.getBoundingClientRect = () => ({ top: 100 + (tops[el.dataset.anchor!] ?? 0) }) as DOMRect;
    });
    return main;
  }

  it("puts the topmost fully visible lane back at the top after the reflow", () => {
    const main = scene(300, { a: -40, b: 25, c: 400 }); // a is cut off at the top, b is the topmost fully visible
    captureAnchor();
    // after the reflow b sits 600px down in the new layout
    document.querySelector<HTMLElement>('[data-anchor="b"]')!.getBoundingClientRect = () => ({ top: 100 + 600 }) as DOMRect;
    restoreAnchor();
    expect(main.scrollTop).toBe(300 + 600);
  });

  it("stays at the top when the screen was at the top (the board's title row scrolls with the lanes)", () => {
    const main = scene(0, { a: 87, b: 300 });
    captureAnchor();
    document.querySelector<HTMLElement>('[data-anchor="a"]')!.getBoundingClientRect = () => ({ top: 100 + 140 }) as DOMRect;
    restoreAnchor();
    expect(main.scrollTop).toBe(0);
  });

  it("is a no-op when nothing was remembered, and works once", () => {
    const main = scene(200, { a: 10 });
    restoreAnchor();
    expect(main.scrollTop).toBe(200);
    captureAnchor();
    restoreAnchor();
    const after = main.scrollTop;
    restoreAnchor();
    expect(main.scrollTop).toBe(after);
  });

  it("prefers the person view's scroll area when it exists", () => {
    document.body.innerHTML = `<main class="sf-main"><div class="person-scroll"><section data-anchor="group:qa"></section></div></main>`;
    const box = document.querySelector<HTMLElement>(".person-scroll")!;
    box.scrollTop = 50;
    box.getBoundingClientRect = () => ({ top: 10 }) as DOMRect;
    const g = document.querySelector<HTMLElement>("[data-anchor]")!;
    g.getBoundingClientRect = () => ({ top: 12 }) as DOMRect;
    captureAnchor();
    g.getBoundingClientRect = () => ({ top: 90 }) as DOMRect;
    restoreAnchor();
    expect(box.scrollTop).toBe(50 + 80);
  });
});
