// WP-24 tests: TrackBackdrop (ux-spec §7 "Backdrop").
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - Root:        aria-hidden="true", inline style `width: <width>px`, custom property --days = axis.length.
//   - Weekend day: [data-weekend] inside the root, one per axis day with weekend=true, --x = the day index (one day wide).
//   - Today line:  [data-today-line="2"|"3"] inside the root, exactly one, the value is the todayLine prop (px thickness).
// SPEC-NOTE: the backdrop is purely decorative, so it is aria-hidden and axe must stay clean.
import { render } from "@testing-library/preact";
import axe from "axe-core";
import { describe, expect, it } from "vitest";
import type { AxisDay } from "../../api/types";
import { TrackBackdrop } from "./TrackBackdrop";
import type { TrackBackdropProps } from "./props";

// 21 days, Tue 2026-11-03 .. Mon 2026-11-23. Weekends at index 4,5 (7-8 Nov), 11,12, 18,19; Mondays at 6, 13, 20.
const AXIS: AxisDay[] = Array.from({ length: 21 }, (_, i) => {
  const d = new Date(Date.UTC(2026, 10, 3 + i));
  const dow = d.getUTCDay();
  return { date: d.toISOString().slice(0, 10), label: String(d.getUTCDate()), weekend: dow === 0 || dow === 6, monday: dow === 1 };
});
const WEEKEND_INDEXES = [4, 5, 11, 12, 18, 19];

function cssVar(el: Element, name: string): number {
  const m = new RegExp(`${name}\\s*:\\s*(-?[0-9.]+)`).exec(el.getAttribute("style") ?? "");
  if (!m) throw new Error(`${name} not set on element`);
  return parseFloat(m[1]!);
}

function renderBackdrop(over: Partial<TrackBackdropProps> = {}) {
  return render(<TrackBackdrop axis={AXIS} width={440} shadeWeekends todayLine={3} {...over} />);
}

function root(container: Element): HTMLElement {
  const el = container.firstElementChild as HTMLElement | null;
  if (!el) throw new Error("nothing rendered");
  return el;
}

describe("TrackBackdrop", () => {
  it("is decorative: aria-hidden root sized to the track width", () => {
    const { container } = renderBackdrop({ width: 600 });
    const el = root(container);
    expect(el.getAttribute("aria-hidden")).toBe("true");
    expect(el.style.width).toBe("600px");
  });

  it("exposes the day count to CSS as --days", () => {
    const { container } = renderBackdrop();
    expect(cssVar(root(container), "--days")).toBe(21);
  });

  it("shades one element per weekend day at that day's index when shading is on", () => {
    const { container } = renderBackdrop();
    const weekends = Array.from(container.querySelectorAll("[data-weekend]"));
    expect(weekends.map((w) => cssVar(w, "--x"))).toEqual(WEEKEND_INDEXES);
  });

  it("shades nothing when shading is off, but still draws the today line", () => {
    const { container } = renderBackdrop({ shadeWeekends: false });
    expect(container.querySelectorAll("[data-today-line]")).toHaveLength(1); // anchor
    expect(container.querySelectorAll("[data-weekend]")).toHaveLength(0);
  });

  it("draws a single 3px today line in the person view", () => {
    const { container } = renderBackdrop({ todayLine: 3 });
    const lines = container.querySelectorAll("[data-today-line]");
    expect(lines).toHaveLength(1);
    expect(lines[0]!.getAttribute("data-today-line")).toBe("3");
  });

  it("draws a single 2px today line on the board", () => {
    const { container } = renderBackdrop({ todayLine: 2 });
    const lines = container.querySelectorAll("[data-today-line]");
    expect(lines).toHaveLength(1);
    expect(lines[0]!.getAttribute("data-today-line")).toBe("2");
  });

  it("follows the axis: a 7-day axis with no weekend days shades nothing", () => {
    const weekdays = AXIS.slice(6, 11); // Mon..Fri
    const { container } = renderBackdrop({ axis: weekdays });
    expect(cssVar(root(container), "--days")).toBe(5);
    expect(container.querySelectorAll("[data-weekend]")).toHaveLength(0);
  });

  it("uses no inline colours", () => {
    const { container } = renderBackdrop();
    const styled = Array.from(container.querySelectorAll("[style]"));
    expect(styled.length).toBeGreaterThan(0); // anchor: the root carries inline size
    for (const el of styled) expect(el.getAttribute("style")).not.toMatch(/#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(/i);
  });

  it("has no axe violations", async () => {
    const { container } = renderBackdrop();
    expect(container.querySelectorAll("[data-today-line]")).toHaveLength(1); // anchor
    const res = await axe.run(container, { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } });
    expect(res.violations.map((v) => v.id)).toEqual([]);
  });
});
