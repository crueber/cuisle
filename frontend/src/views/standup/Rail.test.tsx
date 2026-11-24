// WP-23 tests: Rail, the speaker rail (ux-spec §2.2, §10).
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - Root:      <nav aria-label="Speakers"> holding only <button>s plus the "Speaking order" label and the footer hint.
//   - Pressed:   the selected button (Whole board when `selected` is null, else that speaker) has aria-pressed="true";
//                every other rail button has aria-pressed="false" (the approved reference snapshots use aria-pressed).
//   - Full rail: buttons are "Whole board", then one per included speaker (first name, active count; no initials), in the order
//                given. The Unassigned speaker (unassigned=true, included=true) is rendered in place with the text
//                "Unassigned" and its count.
//   - Compact:   each speaker button shows initials and the count only; its `title` and `aria-label` are the full name
//                (SpeakerView.name). Unassigned shows "Unas." + the count (aria-label "Unassigned"). The Whole board button
//                is an icon button named "Whole board".
//
// SPEC-NOTE: ux-spec says "Unassigned per position ... hidden when skip, first when first". The rail does no repositioning
//   of its own: the speakers array already has the Unassigned entry first/last, or with included=false for "skip"; the
//   rail renders included speakers in array order and never renders an included=false speaker (ux-spec: "rail shows
//   Included only"; an excluded speaker with tickets still appears on the whole board, not in the rail).
// SPEC-NOTE: the rail never hides people; with 30 speakers all 30 buttons exist in the DOM (it scrolls vertically).
// SPEC-NOTE: the footer hint ("↑ ↓ speaker", "← → standup", "B board ⇄ person", "1–N jump") exists in the full rail only.
import { fireEvent, render, screen, within } from "@testing-library/preact";
import axe from "axe-core";
import { describe, expect, it, vi } from "vitest";
import type { SpeakerView } from "../../api/types";
import { Rail } from "./Rail";
import type { RailProps } from "./props";

const sp = (id: string, name: string, initials: string, active: number, over: Partial<SpeakerView> = {}): SpeakerView => ({
  id,
  name,
  first: name.split(" ")[0]!,
  initials,
  unassigned: false,
  included: true,
  active,
  done: 0,
  ...over,
});

const PRIYA = sp("p1", "Iris Calloway", "IC", 14);
const MARCUS = sp("p2", "Silas Marchetti", "SM", 4);
const JONAH = sp("p3", "Noor Abadi", "NA", 5);
const EXCLUDED = sp("p4", "Otto Diaz", "OD", 2, { included: false });
const UNASSIGNED: SpeakerView = { id: "unassigned", name: "Unassigned", first: "Unassigned", initials: "··", unassigned: true, included: true, active: 4, done: 0 };

function renderRail(over: Partial<RailProps> = {}) {
  const onSelect = vi.fn();
  const props: RailProps = { mode: "full", speakers: [PRIYA, MARCUS, JONAH, EXCLUDED, UNASSIGNED], selected: null, onSelect, ...over };
  return { onSelect, ...render(<Rail {...props} />) };
}

const nav = () => screen.getByRole("navigation", { name: "Speakers" });
const railButtons = () => within(nav()).getAllByRole("button");
const AXE = { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } };

describe("Rail (full)", () => {
  it("is a Speakers navigation with the Whole board button first", () => {
    renderRail();
    const buttons = railButtons();
    expect(buttons[0]!.textContent).toBe("Whole board");
  });

  it("lists included speakers in the given order, then Unassigned, and skips excluded speakers", () => {
    renderRail();
    const names = railButtons().slice(1).map((b) => b.textContent ?? "");
    expect(names).toHaveLength(4);
    expect(names[0]).toContain("Iris");
    expect(names[1]).toContain("Silas");
    expect(names[2]).toContain("Noor");
    expect(names[3]).toContain("Unassigned");
    expect(screen.queryByText(/Otto/)).toBeNull();
  });

  it("puts Unassigned first when the speakers array has it first", () => {
    renderRail({ speakers: [UNASSIGNED, PRIYA, MARCUS] });
    const names = railButtons().slice(1).map((b) => b.textContent ?? "");
    expect(names[0]).toContain("Unassigned");
    expect(names[1]).toContain("Iris");
    expect(names[2]).toContain("Silas");
  });

  it("omits Unassigned when it is not included (position = skip)", () => {
    renderRail({ speakers: [PRIYA, MARCUS, { ...UNASSIGNED, included: false }] });
    expect(railButtons()).toHaveLength(3); // Whole board + 2
    expect(screen.queryByText(/Unassigned/)).toBeNull();
  });

  it("shows the first name and active count on each speaker button, no initials", () => {
    renderRail();
    const iris = screen.getByRole("button", { name: /Iris/ });
    expect(within(iris).queryByText("IC")).toBeNull();
    expect(within(iris).getByText("Iris")).toBeTruthy();
    expect(within(iris).getByText("14")).toBeTruthy();
    const un = screen.getByRole("button", { name: /Unassigned/ });
    expect(within(un).getByText("4")).toBeTruthy();
  });

  it("shows full names for people who share a first name", () => {
    const a = sp("a", "Gabriel Souza", "GS", 2);
    const b = sp("b", "Gabriel Lima", "GL", 1);
    renderRail({ speakers: [a, b, PRIYA] });
    expect(screen.getByText("Gabriel Souza")).toBeTruthy();
    expect(screen.getByText("Gabriel Lima")).toBeTruthy();
    expect(screen.getByText("Iris")).toBeTruthy();
  });

  it("labels the list SPEAKING ORDER and shows the keyboard hint", () => {
    renderRail();
    expect(screen.getByText(/speaking order/i)).toBeTruthy();
    expect(screen.getByText(/↑ ↓ speaker/)).toBeTruthy();
    expect(screen.getByText(/← → standup/)).toBeTruthy();
  });

  it("marks Whole board pressed when nothing is selected", () => {
    renderRail({ selected: null });
    const pressed = railButtons().map((b) => b.getAttribute("aria-pressed"));
    expect(pressed[0]).toBe("true");
    expect(pressed.slice(1).every((p) => p === "false")).toBe(true);
  });

  it("marks only the selected speaker pressed", () => {
    renderRail({ selected: "p2" });
    const byName = (re: RegExp) => screen.getByRole("button", { name: re }).getAttribute("aria-pressed");
    expect(byName(/Silas/)).toBe("true");
    expect(byName(/Iris/)).toBe("false");
    expect(byName(/Whole board/)).toBe("false");
    expect(byName(/Unassigned/)).toBe("false");
  });

  it("marks Unassigned pressed when selected is 'unassigned'", () => {
    renderRail({ selected: "unassigned" });
    expect(screen.getByRole("button", { name: /Unassigned/ }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: /Whole board/ }).getAttribute("aria-pressed")).toBe("false");
  });

  it("selects a speaker by id, Unassigned by 'unassigned', and the whole board by null", () => {
    const { onSelect } = renderRail({ selected: "p1" });
    fireEvent.click(screen.getByRole("button", { name: /Silas/ }));
    expect(onSelect).toHaveBeenLastCalledWith("p2");
    fireEvent.click(screen.getByRole("button", { name: /Unassigned/ }));
    expect(onSelect).toHaveBeenLastCalledWith("unassigned");
    fireEvent.click(screen.getByRole("button", { name: /Whole board/ }));
    expect(onSelect).toHaveBeenLastCalledWith(null);
    expect(onSelect).toHaveBeenCalledTimes(3);
  });

  it("never hides people: 30 speakers give 30 speaker buttons", () => {
    const many = Array.from({ length: 30 }, (_, i) => sp(`s${i}`, `Person${i} Lastname`, "XX", i));
    renderRail({ speakers: many });
    expect(railButtons()).toHaveLength(31);
    expect(screen.getByRole("button", { name: /Person29/ })).toBeTruthy();
  });

  it("has no axe violations", async () => {
    const { container } = renderRail({ selected: "p1" });
    expect(railButtons().length).toBeGreaterThan(1); // anchor
    expect((await axe.run(container, AXE)).violations.map((v) => v.id)).toEqual([]);
  });
});

describe("Rail (compact)", () => {
  it("shows initials and count only, with the full name in title and aria-label", () => {
    renderRail({ mode: "compact" });
    const iris = screen.getByRole("button", { name: /Iris Calloway/ });
    expect(iris.getAttribute("title")).toBe("Iris Calloway");
    expect(iris.getAttribute("aria-label")).toContain("Iris Calloway");
    expect(within(iris).getByText("IC")).toBeTruthy();
    expect(within(iris).getByText("14")).toBeTruthy();
    expect(screen.queryByText("Iris")).toBeNull(); // first name is not drawn
  });

  it("names the whole-board icon button 'Whole board'", () => {
    renderRail({ mode: "compact" });
    expect(railButtons()[0]!.getAttribute("aria-label") ?? railButtons()[0]!.textContent).toMatch(/Whole board/);
  });

  it("shows Unassigned as 'Unas.' plus its count", () => {
    renderRail({ mode: "compact" });
    const un = screen.getByRole("button", { name: /Unassigned/ });
    expect(within(un).getByText("Unas.")).toBeTruthy();
    expect(within(un).getByText("4")).toBeTruthy();
  });

  it("omits the keyboard hint and the SPEAKING ORDER label", () => {
    renderRail({ mode: "compact" });
    expect(railButtons().length).toBeGreaterThan(1); // anchor
    expect(screen.queryByText(/↑ ↓ speaker/)).toBeNull();
    expect(screen.queryByText(/← → standup/)).toBeNull();
    expect(screen.queryByText(/speaking order/i)).toBeNull();
  });

  it("keeps the same selection and click behaviour", () => {
    const { onSelect } = renderRail({ mode: "compact", selected: "p3" });
    expect(screen.getByRole("button", { name: /Noor Abadi/ }).getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: /Silas Marchetti/ }));
    expect(onSelect).toHaveBeenCalledWith("p2");
    fireEvent.click(screen.getByRole("button", { name: /Whole board/ }));
    expect(onSelect).toHaveBeenLastCalledWith(null);
  });

  it("does not render excluded speakers", () => {
    renderRail({ mode: "compact" });
    expect(screen.getByRole("button", { name: /Iris Calloway/ })).toBeTruthy(); // anchor
    expect(screen.queryByRole("button", { name: /Otto/ })).toBeNull();
  });

  it("has no axe violations", async () => {
    const { container } = renderRail({ mode: "compact", selected: null });
    expect(railButtons().length).toBeGreaterThan(1); // anchor
    expect((await axe.run(container, AXE)).violations.map((v) => v.id)).toEqual([]);
  });
});
