// Configure jump bar (ux-spec §9.2: sticky pill links under the title when there are 1 or 2 columns).
//
// SPEC-NOTE: the bar is a <nav> landmark (role navigation) holding one focusable pill per section (a button or a link),
//   in the order given by `sections`, showing `label`. Activating a pill (click, or Enter on the focused pill) calls
//   onJump(id) exactly once. The JumpBar does not scroll by itself (ConfigureScreen owns that).

import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { JumpBar } from "./JumpBar";

const SECTIONS = [
  { id: "sec-source", label: "Source" },
  { id: "sec-speakers", label: "Speakers" },
  { id: "sec-person", label: "Person view" },
  { id: "sec-pulse", label: "Pulse" },
  { id: "sec-board", label: "Whole board" },
  { id: "sec-mapping", label: "Status mapping" },
  { id: "sec-export", label: "Export" },
  { id: "sec-display", label: "Display" },
  { id: "sec-connection", label: "Connection" },
];

describe("JumpBar", () => {
  it("renders a navigation landmark with one pill per section, in order", () => {
    render(<JumpBar sections={SECTIONS} onJump={() => {}} />);
    const found = [...screen.getByRole("navigation").querySelectorAll("a, button")].map((e) => e.textContent?.trim());
    expect(found).toEqual(SECTIONS.map((s) => s.label));
  });

  it("calls onJump with the section id when a pill is clicked", async () => {
    const onJump = vi.fn();
    const user = userEvent.setup();
    render(<JumpBar sections={SECTIONS} onJump={onJump} />);
    await user.click(screen.getByText("Whole board"));
    expect(onJump).toHaveBeenCalledTimes(1);
    expect(onJump).toHaveBeenCalledWith("sec-board");
  });

  it("each pill jumps to its own section", async () => {
    const onJump = vi.fn();
    const user = userEvent.setup();
    render(<JumpBar sections={SECTIONS} onJump={onJump} />);
    for (const s of SECTIONS) await user.click(screen.getByText(s.label));
    expect(onJump.mock.calls.map((c) => c[0])).toEqual(SECTIONS.map((s) => s.id));
  });

  it("is keyboard operable: Tab reaches the first pill and Enter activates it", async () => {
    const onJump = vi.fn();
    const user = userEvent.setup();
    render(<JumpBar sections={SECTIONS} onJump={onJump} />);
    await user.tab();
    expect(document.activeElement?.textContent?.trim()).toBe("Source");
    await user.keyboard("{Enter}");
    expect(onJump).toHaveBeenCalledTimes(1);
    expect(onJump).toHaveBeenCalledWith("sec-source");
  });

  it("renders only the sections it is given", () => {
    render(<JumpBar sections={SECTIONS.slice(0, 2)} onJump={() => {}} />);
    const found = [...screen.getByRole("navigation").querySelectorAll("a, button")].map((e) => e.textContent?.trim());
    expect(found).toEqual(["Source", "Speakers"]);
    expect(screen.queryByText("Display")).toBeNull();
  });
});
