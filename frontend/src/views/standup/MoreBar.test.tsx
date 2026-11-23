// WP-24 tests: MoreBar (ux-spec §5.6, §10).
//
// SPEC-NOTE: the bar has no testable role; the contract is an element with aria-live="polite" that holds the text.
//   Tests find it with [aria-live="polite"].
// SPEC-NOTE: when atEnd is true the bar owns its copy: "End of list" with "every ticket is shown above", whatever
//   `summary` says. (The parent passes the below-the-fold summary unconditionally; the component swaps the words.)
// SPEC-NOTE: visible=false renders no "Scroll for more" or "End of list" text at all (the bar is only shown when the
//   list scrolls). Whether an empty live region stays mounted is not specified and not tested.
import { render, screen } from "@testing-library/preact";
import axe from "axe-core";
import { describe, expect, it } from "vitest";
import { MoreBar } from "./MoreBar";
import type { MoreBarProps } from "./props";

function renderBar(over: Partial<MoreBarProps> = {}) {
  return render(<MoreBar visible atEnd={false} summary="2 To Do · 6 Done below" {...over} />);
}

function live(container: Element): HTMLElement {
  const el = container.querySelector<HTMLElement>('[aria-live="polite"]');
  if (!el) throw new Error('no [aria-live="polite"] element rendered');
  return el;
}

describe("MoreBar", () => {
  it("says 'Scroll for more' with the summary while there is more below", () => {
    const { container } = renderBar();
    expect(screen.getByText(/Scroll for more/)).toBeTruthy();
    expect(screen.getByText("2 To Do · 6 Done below")).toBeTruthy();
    expect(screen.queryByText(/End of list/)).toBeNull();
    expect(live(container).textContent).toContain("Scroll for more");
  });

  it("is a polite live region", () => {
    const { container } = renderBar();
    expect(live(container).textContent).toContain("2 To Do · 6 Done below");
  });

  it("changes to 'End of list' / 'every ticket is shown above' at the end of the list", () => {
    const { container } = renderBar({ atEnd: true });
    expect(screen.getByText(/End of list/)).toBeTruthy();
    expect(screen.getByText("every ticket is shown above")).toBeTruthy();
    expect(screen.queryByText(/Scroll for more/)).toBeNull();
    expect(live(container).textContent).toContain("End of list");
  });

  it("does not show the stale below-the-fold summary at the end of the list", () => {
    renderBar({ atEnd: true, summary: "2 To Do · 6 Done below" });
    expect(screen.getByText(/End of list/)).toBeTruthy(); // anchor
    expect(screen.queryByText("2 To Do · 6 Done below")).toBeNull();
  });

  it("updates in place when the summary changes", () => {
    const { rerender } = renderBar();
    expect(screen.getByText("2 To Do · 6 Done below")).toBeTruthy();
    rerender(<MoreBar visible atEnd={false} summary="6 Done below" />);
    expect(screen.getByText("6 Done below")).toBeTruthy();
    expect(screen.queryByText("2 To Do · 6 Done below")).toBeNull();
  });

  it("shows nothing when not visible, and shows it once visible", () => {
    const hidden = renderBar({ visible: false });
    expect(screen.queryByText(/Scroll for more/)).toBeNull();
    expect(screen.queryByText(/End of list/)).toBeNull();
    hidden.unmount();
    renderBar({ visible: true });
    expect(screen.getByText(/Scroll for more/)).toBeTruthy(); // positive variant in the same test
  });

  it("shows nothing at the end of the list when not visible", () => {
    renderBar({ visible: true, atEnd: true });
    expect(screen.getByText(/End of list/)).toBeTruthy(); // anchor
    const { container } = renderBar({ visible: false, atEnd: true });
    expect(container.textContent).toBe("");
  });

  it("has no axe violations (scroll and end states)", async () => {
    const a = renderBar();
    expect(screen.getByText(/Scroll for more/)).toBeTruthy(); // anchor
    const opts = { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } };
    expect((await axe.run(a.container, opts)).violations.map((v) => v.id)).toEqual([]);
    a.unmount();
    const b = renderBar({ atEnd: true });
    expect(screen.getByText(/End of list/)).toBeTruthy();
    expect((await axe.run(b.container, opts)).violations.map((v) => v.id)).toEqual([]);
  });
});
