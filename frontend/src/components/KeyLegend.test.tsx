// KeyLegend: the "?" popover (ux-spec §8 keyboard map, §7 pulse legend, §10).
//
// SPEC-NOTE: When open the legend is a role="dialog" named /key|legend/i. Each key in the §8 table is rendered in a
//   <kbd> element whose text is exactly the key: "←", "→", "B", "1–9" (en dash), "R", "Shift+R", "/", ",", "?", "Esc",
//   "Enter". Descriptions are asserted loosely by keyword (case-insensitive) on the dialog text.
// SPEC-NOTE: The pulse legend lists the lanes (To Do, In Progress, In Review, QA), Blocked and Over limit, and, as
//   toggles allow, entries mentioning "commit", "PR" and "comment". Entry text is matched by keyword on the dialog text,
//   so the §8 key descriptions must not themselves contain the words "commit", "PR" or "comment".
// SPEC-NOTE: devActivity === "unavailable" hides the commit and PR entries (their data does not exist). "ok" and
//   "unknown" are both treated as available. showCommits/showPRs/showComments each hide only their own entry.
//   devActivity does not affect the comment entry.
// SPEC-NOTE: Esc closes via onClose through a document-level keydown listener that exists only while open, so it works
//   whichever element has focus. Esc while closed does not call onClose.
// SPEC-NOTE: When closed, KeyLegend renders no dialog.

import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { KeyLegend } from "./KeyLegend";
import type { KeyLegendProps } from "./props";

function props(over: Partial<KeyLegendProps> = {}): KeyLegendProps {
  return { open: true, onClose: () => {}, showCommits: true, showPRs: true, showComments: true, devActivity: "ok", ...over };
}
const dialog = () => screen.getByRole("dialog", { name: /key|legend/i });
const dialogText = () => dialog().textContent ?? "";
const keycaps = () => Array.from(dialog().querySelectorAll("kbd")).map((k) => k.textContent?.trim());

describe("KeyLegend", () => {
  it("renders no dialog when closed", () => {
    const opened = render(<KeyLegend {...props({ open: true })} />);
    expect(dialog()).toBeTruthy(); // anchor: the same component does render a dialog when open
    opened.unmount();
    render(<KeyLegend {...props({ open: false })} />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("renders a named dialog when open and no dialog before it opens", () => {
    const { rerender } = render(<KeyLegend {...props({ open: false })} />);
    expect(screen.queryByRole("dialog")).toBeNull();
    rerender(<KeyLegend {...props({ open: true })} />);
    expect(dialog()).toBeTruthy();
  });

  it("lists every key from the ux-spec §8 keyboard map", () => {
    render(<KeyLegend {...props()} />);
    expect(keycaps()).toEqual(expect.arrayContaining(["←", "→", "B", "G", "1–9", "R", "Shift+R", "/", ",", "?", "Esc", "Enter"]));
  });

  it("describes what the keys do", () => {
    render(<KeyLegend {...props()} />);
    const t = dialogText();
    expect(t).toMatch(/group by Assignee or Status/i);
    expect(t).toMatch(/speaker/i);
    expect(t).toMatch(/whole board/i);
    expect(t).toMatch(/standup/i);
    expect(t).toMatch(/resync/i);
    expect(t).toMatch(/configure/i);
    expect(t).toMatch(/jira/i);
  });

  it("shows the pulse legend lanes, blocked and over limit", () => {
    render(<KeyLegend {...props()} />);
    const t = dialogText();
    expect(t).toMatch(/to do/i);
    expect(t).toMatch(/in progress/i);
    expect(t).toMatch(/in review/i);
    expect(t).toMatch(/\bQA\b/i);
    expect(t).toMatch(/blocked/i);
    expect(t).toMatch(/over limit/i);
  });

  it("shows commit, PR and comment entries when everything is on and dev activity is ok", () => {
    render(<KeyLegend {...props()} />);
    const t = dialogText();
    expect(t).toMatch(/commit/i);
    expect(t).toMatch(/\bPR\b|pull request/i);
    expect(t).toMatch(/comment/i);
  });

  it("hides the commit and PR entries when dev activity is unavailable but keeps comments", () => {
    render(<KeyLegend {...props({ devActivity: "unavailable" })} />);
    const t = dialogText();
    expect(t).not.toMatch(/commit/i);
    expect(t).not.toMatch(/\bPR\b|pull request/i);
    expect(t).toMatch(/comment/i);
    expect(t).toMatch(/blocked/i); // the rest of the legend is intact
  });

  it("treats an unknown dev activity as available", () => {
    render(<KeyLegend {...props({ devActivity: "unknown" })} />);
    const t = dialogText();
    expect(t).toMatch(/commit/i);
    expect(t).toMatch(/\bPR\b|pull request/i);
  });

  it("hides only the commit entry when showCommits is off", () => {
    render(<KeyLegend {...props({ showCommits: false })} />);
    const t = dialogText();
    expect(t).not.toMatch(/commit/i);
    expect(t).toMatch(/\bPR\b|pull request/i);
    expect(t).toMatch(/comment/i);
  });

  it("hides only the PR entry when showPRs is off", () => {
    render(<KeyLegend {...props({ showPRs: false })} />);
    const t = dialogText();
    expect(t).not.toMatch(/\bPR\b|pull request/i);
    expect(t).toMatch(/commit/i);
    expect(t).toMatch(/comment/i);
  });

  it("hides only the comment entry when showComments is off", () => {
    render(<KeyLegend {...props({ showComments: false })} />);
    const t = dialogText();
    expect(t).not.toMatch(/comment/i);
    expect(t).toMatch(/commit/i);
    expect(t).toMatch(/\bPR\b|pull request/i);
  });

  it("calls onClose when Esc is pressed while open, whichever element has focus", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<KeyLegend {...props({ onClose })} />);
    expect(dialog()).toBeTruthy();
    (document.activeElement as HTMLElement | null)?.blur?.();
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not call onClose on Esc when closed, or on other keys when open", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const { rerender } = render(<KeyLegend {...props({ open: false, onClose })} />);
    expect(screen.queryByRole("dialog")).toBeNull();
    await user.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();
    rerender(<KeyLegend {...props({ open: true, onClose })} />);
    expect(dialog()).toBeTruthy();
    await user.keyboard("a{Enter}{ArrowRight}");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("stops listening for Esc once closed again", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const { rerender } = render(<KeyLegend {...props({ open: true, onClose })} />);
    expect(dialog()).toBeTruthy();
    rerender(<KeyLegend {...props({ open: false, onClose })} />);
    expect(screen.queryByRole("dialog")).toBeNull();
    await user.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("has no axe violations open (everything on, dev activity unavailable)", async () => {
    const { container, rerender } = render(<KeyLegend {...props()} />);
    expect(dialog()).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
    rerender(<KeyLegend {...props({ devActivity: "unavailable", showComments: false })} />);
    expect(dialog()).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
  });

  it("has no axe violations closed", async () => {
    const { container } = render(<KeyLegend {...props({ open: false })} />);
    // Anchor: the same props opened do render the dialog (guards against a vacuous pass on an empty stub).
    const open = render(<KeyLegend {...props({ open: true })} />);
    expect(open.getByRole("dialog")).toBeTruthy();
    open.unmount();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
  });
});
