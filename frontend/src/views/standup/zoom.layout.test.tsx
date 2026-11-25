// F15 golden cases (ux-spec §16.3): zoom re-runs the responsive rules on the effective size, so the same 1920 x 1080
// window gives 3 / 2 / 2 / 1 board columns at 80 / 100 / 150 / 175 %, the board's lane split reads in speaking order, and the
// person view at 150 % walks the density ladder down to side headings. Real BoardView and PersonView, the MOB fixture.
import { act, cleanup, render } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Bootstrap } from "../../api/types";
import boot from "../../../tests/fixtures/bootstrap_mob.json";
import { settings, standups } from "../../state/app";
import { route } from "../../state/route";
import { views } from "../../state/standup";
import { seedZooms } from "../../state/zoom";
import { StandupScreen } from "./StandupScreen";

vi.mock("./Header", async () => {
  const { h } = await import("preact");
  return { Header: (p: { vw: number }) => h("header", { "data-testid": "header", "data-vw": String(p.vw) }) };
});

const b = boot as unknown as Bootstrap;

function mount(w: number, h: number, zoom: number, person: string | null, textSize?: string) {
  Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: w });
  Object.defineProperty(window, "innerHeight", { configurable: true, writable: true, value: h });
  settings.value = textSize ? { text_size: textSize } : {};
  standups.value = b.standups;
  views.value = new Map([[b.view!.standupId, b.view!]]);
  seedZooms({ [String(b.view!.standupId)]: zoom });
  route.value = { view: "standup", standupId: b.view!.standupId, person };
  return render(<StandupScreen />);
}

beforeEach(() => {
  settings.value = {};
});
afterEach(() => {
  cleanup();
  seedZooms({});
  settings.value = {};
});

const lanesIn = (root: Element) =>
  Array.from(root.querySelectorAll(".board-col")).map((c) => Array.from(c.querySelectorAll(".board-lane-name")).map((n) => n.textContent));

describe("whole board at 1920 x 1080, by zoom", () => {
  it.each([
    [80, 3],
    [100, 2],
    [150, 2],
    [175, 1],
  ])("zoom %i gives %i column(s)", (zoom, cols) => {
    const { container } = mount(1920, 1080, zoom, null);
    expect(container.querySelectorAll(".board-col")).toHaveLength(cols);
    expect(container.querySelector(".board-cols")?.getAttribute("style")).toMatch(new RegExp(`--cols:\\s*${cols};`));
  });

  it("80% lists Done in 3 grid columns and Backlog in 4 (three board columns)", () => {
    const { container } = mount(1920, 1080, 80, null);
    const grids = Array.from(container.querySelectorAll<HTMLElement>(".board-bottom .board-list-grid")).map((g) => g.getAttribute("style"));
    expect(grids[0]).toMatch(/--cols:\s*3;/);
    expect(grids[1]).toMatch(/--cols:\s*4;/);
  });

  it("the lanes read in speaking order, down then across, at every column count", () => {
    const flat = (zoom: number) => {
      const { container, unmount } = mount(1920, 1080, zoom, null);
      const out = lanesIn(container).flat();
      unmount();
      return out;
    };
    const one = flat(175);
    expect(one[0]).toBe("Unassigned");
    expect(flat(100)).toEqual(one);
    expect(flat(80)).toEqual(one);
    expect(flat(150)).toEqual(one);
  });

  it("100% cuts the MOB lanes Unassigned, Iris, Silas | the rest (the handoff's split)", () => {
    const { container } = mount(1920, 1080, 100, null);
    const cols = lanesIn(container);
    expect(cols[0]?.slice(0, 3).map((n) => n?.split(" ")[0])).toEqual(["Unassigned", "Iris", "Silas"]);
    expect(cols[1]?.[0]).not.toBe("Silas Marchetti");
  });

  it("80% cuts Unassigned, Iris | Silas, Noor | Otto, Amara, Jonas, Wren (the mockup's split)", () => {
    const { container } = mount(1920, 1080, 80, null);
    expect(lanesIn(container).map((c) => c.map((n) => n?.split(" ")[0]))).toEqual([
      ["Unassigned", "Iris"],
      ["Silas", "Noor"],
      ["Otto", "Amara", "Jonas", "Wren"],
    ]);
  });

  it("the zoom is a CSS scale on the content only: --zoom on the body, none on the header", () => {
    const { container } = mount(1920, 1080, 150, null);
    expect(container.querySelector<HTMLElement>(".sf-body")?.style.getPropertyValue("--zoom")).toBe("1.5");
    expect(container.querySelector(".sf-screen")?.getAttribute("style")).toBeNull();
    expect(container.querySelector("[data-testid=header]")?.getAttribute("data-vw")).toBe("1920"); // the header keeps the text scale
  });

  it("150% runs the rail rule on the effective width: 1280, so the rail goes compact", () => {
    expect(mount(1920, 1080, 100, null).container.querySelector(".sf-rail--full")).not.toBeNull();
    cleanup();
    expect(mount(1920, 1080, 150, null).container.querySelector(".sf-rail--compact")).not.toBeNull();
  });
});

describe("person view at 1920 x 1080, by zoom", () => {
  const first = b.view!.speakers.find((s) => s.included && !s.unassigned)!.id;
  const heading = (c: Element) => c.querySelector(".person-group")?.getAttribute("data-heading");
  const tag = (c: Element) => c.querySelector(".person-density-tag")?.textContent ?? null;

  it("150% walks the density ladder down to side headings (tight rows)", () => {
    const { container } = mount(1920, 1080, 150, first);
    expect(heading(container)).toBe("side");
    expect(tag(container)).toBe("tight rows, side headings");
  });

  it("100% keeps today's density: more room than at 150%, so a roomier step", () => {
    const { container } = mount(1920, 1080, 100, first);
    expect(tag(container)).not.toBe("tight rows, side headings");
  });

  it("the control shows the level, and the toast names the density", () => {
    const { container } = mount(1920, 1080, 150, first);
    expect(container.querySelector(".zoom-level")?.textContent).toBe("150%");
    expect(container.querySelector(".zoom-level")?.getAttribute("data-zoomed")).toBe("true");
  });
});

describe("the toast names what the layout did", () => {
  const toast = (c: Element) => c.querySelector(".zoom-toast")?.textContent ?? null;
  const stepTo = (c: Element, label: "Zoom in (+)" | "Zoom out (−)", times: number) => {
    for (let i = 0; i < times; i++) act(() => c.querySelector<HTMLButtonElement>(`[aria-label="${label}"]`)!.click());
  };

  it("board: 150% · 2 columns, 175% · 1 column, 80% · 3 columns", () => {
    const { container } = mount(1920, 1080, 100, null);
    stepTo(container, "Zoom in (+)", 3); // 110, 125, 150
    expect(toast(container)).toBe("150% · 2 columns");
    stepTo(container, "Zoom in (+)", 1);
    expect(toast(container)).toBe("175% · 1 column");
    stepTo(container, "Zoom out (−)", 5); // 150, 125, 110, 100, 90 ... down to 80 is 6 steps from 175
    stepTo(container, "Zoom out (−)", 1);
    expect(toast(container)).toBe("80% · 3 columns");
  });

  it("person: 150% · side headings", () => {
    const first = b.view!.speakers.find((s) => s.included && !s.unassigned)!.id;
    const { container } = mount(1920, 1080, 125, first);
    stepTo(container, "Zoom in (+)", 1);
    expect(toast(container)).toBe("150% · side headings");
  });
});

describe("Large text multiplies with zoom", () => {
  it("Large 1.25 x 150% decides like a 1024-wide window (1920 / 1.875)", () => {
    const { container } = mount(1920, 1080, 150, null, "large");
    expect(container.querySelector(".sf-rail--compact")).not.toBeNull();
    expect(container.querySelectorAll(".board-col")).toHaveLength(1);
  });
});
