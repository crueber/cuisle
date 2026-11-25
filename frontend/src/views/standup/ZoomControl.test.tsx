// F15: the zoom control and its toast (ux-spec §16.2): "− 100% +", the level button ink-filled when zoom is not 100%,
// disabled ends, a role=group named Zoom, and a role=status toast "150% · 2 columns" that lasts 1.2 s.
import { act, cleanup, fireEvent, render, screen } from "@testing-library/preact";
import axe from "axe-core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi, type Api } from "../../api";
import { route } from "../../state/route";
import { seedZooms, setZoom, zoomOf } from "../../state/zoom";
import { ZoomControl } from "./ZoomControl";

let setUIState: ReturnType<typeof vi.fn>;
beforeEach(() => {
  vi.useFakeTimers();
  setUIState = vi.fn(async () => undefined);
  setApi({ app: { setUIState } } as unknown as Api);
  seedZooms({});
  route.value = { view: "standup", standupId: 1, person: null };
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  seedZooms({});
  setApi(wailsApi);
});

const out = () => screen.getByRole("button", { name: "Zoom out (−)" });
const inn = () => screen.getByRole("button", { name: "Zoom in (+)" });
const level = () => screen.getByRole("button", { name: /Reset zoom to 100%/ });

describe("ZoomControl", () => {
  it("is a group named Zoom with the three labelled buttons", () => {
    render(<ZoomControl note="2 columns" />);
    const group = screen.getByRole("group", { name: "Zoom" });
    expect(group.querySelectorAll("button")).toHaveLength(3);
    expect(out().textContent).toBe("−");
    expect(inn().textContent).toBe("+");
    expect(level().textContent).toBe("100%");
    expect(level().getAttribute("aria-label")).toBe("Reset zoom to 100% (0)");
  });

  it("keeps its visible text in its accessible name when zoomed (WCAG 2.5.3)", () => {
    seedZooms({ "1": 150 });
    render(<ZoomControl note="2 columns" />);
    expect(level().getAttribute("aria-label")).toBe("150%. Reset zoom to 100% (0)");
    expect(level().getAttribute("aria-label")).toContain(level().textContent);
  });

  it("is ink-filled (data-zoomed) only when the zoom is not 100%", () => {
    seedZooms({ "1": 150 });
    render(<ZoomControl note="2 columns" />);
    expect(level().textContent).toBe("150%");
    expect(level().getAttribute("data-zoomed")).toBe("true");
    cleanup();
    seedZooms({});
    render(<ZoomControl note="2 columns" />);
    expect(level().getAttribute("data-zoomed")).toBe("false");
  });

  it("+ and − step the level, and the middle button resets to 100%", () => {
    render(<ZoomControl note="2 columns" />);
    fireEvent.click(inn());
    expect(zoomOf(1)).toBe(110);
    fireEvent.click(out());
    fireEvent.click(out());
    expect(zoomOf(1)).toBe(90);
    fireEvent.click(level());
    expect(zoomOf(1)).toBe(100);
    expect(setUIState).toHaveBeenLastCalledWith({ standupId: 1, zoom: 100 });
  });

  it("disables − at 70% and + at 200%", () => {
    seedZooms({ "1": 70 });
    render(<ZoomControl note="1 column" />);
    expect((out() as HTMLButtonElement).disabled).toBe(true);
    expect((inn() as HTMLButtonElement).disabled).toBe(false);
    cleanup();
    seedZooms({ "1": 200 });
    render(<ZoomControl note="1 column" />);
    expect((inn() as HTMLButtonElement).disabled).toBe(true);
    expect((out() as HTMLButtonElement).disabled).toBe(false);
  });

  it("renders nothing off the standup screen", () => {
    route.value = { view: "configure", standupId: 1 };
    const { container } = render(<ZoomControl note="2 columns" />);
    expect(container.textContent).toBe("");
  });
});

describe("the toast", () => {
  it("shows '150% · 2 columns' in a status region after a change and goes after 1.2 s", () => {
    render(<ZoomControl note="2 columns" />);
    expect(screen.queryByText(/columns/)).toBeNull(); // nothing on first show
    act(() => {
      setZoom(1, 150);
    });
    const toast = screen.getByText("150% · 2 columns");
    expect(toast.closest("[role=status]")).not.toBeNull();
    act(() => {
      vi.advanceTimersByTime(1100);
    });
    expect(screen.queryByText("150% · 2 columns")).not.toBeNull();
    act(() => {
      vi.advanceTimersByTime(150);
    });
    expect(screen.queryByText("150% · 2 columns")).toBeNull();
  });

  it("the status region exists before the first change, so a screen reader announces it", () => {
    render(<ZoomControl note="2 columns" />);
    expect(screen.getByRole("status")).toBeTruthy();
  });

  it("names the layout the caller gives it (a density on the person view)", () => {
    render(<ZoomControl note="side headings" />);
    act(() => {
      setZoom(1, 150);
    });
    expect(screen.getByText("150% · side headings")).toBeTruthy();
  });

  it("a second change restarts the 1.2 s and shows the new level", () => {
    render(<ZoomControl note="2 columns" />);
    act(() => {
      setZoom(1, 125);
    });
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    act(() => {
      setZoom(1, 150);
    });
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(screen.getByText("150% · 2 columns")).toBeTruthy();
  });

  it("a control mounted after a change does not replay the old toast (switching views)", () => {
    setZoom(1, 150);
    render(<ZoomControl note="2 columns" />);
    expect(screen.queryByText(/150% ·/)).toBeNull();
  });
});

describe("a11y", () => {
  it("has no axe violations at 100% and zoomed", async () => {
    vi.useRealTimers();
    const { container } = render(<ZoomControl note="2 columns" />);
    expect((await axe.run(container)).violations).toEqual([]);
    cleanup();
    seedZooms({ "1": 150 });
    const again = render(<ZoomControl note="2 columns" />);
    expect((await axe.run(again.container)).violations).toEqual([]);
  });
});
