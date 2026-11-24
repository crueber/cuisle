// WP-T8: AnchorStrip (ux-spec §14.4 "Anchor strip"; props.ts AnchorStripProps).
//
// SPEC-NOTE (DOM): the strip is a <nav> (aria-label matching /sections/i) holding one <button> per entry of `sections`,
//   in the given order. A button's accessible name is "<LABEL> <count>" (just "<LABEL>" when count is ""), compared
//   case-insensitively, so CSS upper-casing or literal caps both pass. The active entry carries aria-current="true";
//   every other entry has no aria-current. The component never omits or reorders entries: omitting empty sections is
//   the caller's job (TicketContent), the strip draws exactly what it is given.
// SPEC-NOTE: clicking an entry calls onJump(id) exactly once, including the entry that is already active.
// SPEC-NOTE: the strip makes no api calls and holds no state of its own: `active` is purely a prop.
import { fireEvent, render, screen, within } from "@testing-library/preact";
import { describe, expect, it, vi } from "vitest";
import { AnchorStrip } from "./AnchorStrip";
import type { AnchorSection, AnchorStripProps } from "./props";

const FULL: AnchorSection[] = [
  { id: "description", label: "DESCRIPTION", count: "" },
  { id: "attachments", label: "ATTACHMENTS", count: "5" },
  { id: "subtasks", label: "SUBTASKS", count: "3/5" },
  { id: "links", label: "LINKS", count: "2" },
  { id: "comments", label: "COMMENTS", count: "7" },
];

function strip(over: Partial<AnchorStripProps> = {}) {
  const onJump = vi.fn();
  const props: AnchorStripProps = { sections: FULL, active: "description", onJump, ...over };
  const view = render(<AnchorStrip {...props} />);
  return { onJump, props, ...view };
}

const buttons = () => within(screen.getByRole("navigation", { name: /sections/i })).getAllByRole("button");

describe("AnchorStrip: entries", () => {
  it("draws one button per section in the given order, named label and count", () => {
    strip();
    expect(buttons().map((b) => (b.textContent ?? "").replace(/\s+/g, " ").trim().toUpperCase())).toEqual([
      "DESCRIPTION",
      "ATTACHMENTS 5",
      "SUBTASKS 3/5",
      "LINKS 2",
      "COMMENTS 7",
    ]);
  });

  it("exposes the count in the accessible name (Subtasks shows done/total)", () => {
    strip();
    expect(screen.getByRole("button", { name: /^subtasks\s*3\/5$/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /^attachments\s*5$/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /^description$/i })).toBeTruthy();
  });

  it("draws only the sections it is given (empty sections are omitted by the caller)", () => {
    strip({
      sections: [
        { id: "description", label: "DESCRIPTION", count: "" },
        { id: "comments", label: "COMMENTS", count: "0" },
      ],
    });
    expect(buttons()).toHaveLength(2);
    expect(screen.queryByRole("button", { name: /attachments/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /subtasks/i })).toBeNull();
  });
});

describe("AnchorStrip: active section", () => {
  it.each(["description", "attachments", "subtasks", "links", "comments"] as const)(
    "marks %s as the one current entry",
    (active) => {
      strip({ active });
      const current = buttons().filter((b) => b.getAttribute("aria-current") === "true");
      expect(current).toHaveLength(1);
      expect((current[0]?.textContent ?? "").toLowerCase()).toContain(active);
      for (const b of buttons()) if (b !== current[0]) expect(b.hasAttribute("aria-current")).toBe(false);
    },
  );

  it("moves the mark when the active prop changes", () => {
    const { rerender, onJump } = strip({ active: "links" });
    rerender(<AnchorStrip sections={FULL} active="comments" onJump={onJump} />);
    const current = buttons().filter((b) => b.getAttribute("aria-current") === "true");
    expect(current).toHaveLength(1);
    expect((current[0]?.textContent ?? "").toLowerCase()).toContain("comments");
  });
});

describe("AnchorStrip: jumping", () => {
  it("calls onJump with the section id on click", () => {
    const { onJump } = strip();
    fireEvent.click(screen.getByRole("button", { name: /^links\s*2$/i }));
    expect(onJump).toHaveBeenCalledTimes(1);
    expect(onJump).toHaveBeenCalledWith("links");
  });

  it("calls onJump even for the section that is already active", () => {
    const { onJump } = strip({ active: "comments" });
    fireEvent.click(screen.getByRole("button", { name: /^comments\s*7$/i }));
    expect(onJump).toHaveBeenCalledTimes(1);
    expect(onJump).toHaveBeenCalledWith("comments");
  });

  it("does not call onJump on render or on re-render", () => {
    const { onJump, rerender } = strip();
    rerender(<AnchorStrip sections={FULL} active="subtasks" onJump={onJump} />);
    expect(buttons()).toHaveLength(5); // it did render
    expect(onJump).not.toHaveBeenCalled();
  });
});
