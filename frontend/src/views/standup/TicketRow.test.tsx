// WP-24 tests: TicketRow (ux-spec §5.4, §8, §10, AGENTS invariants 11).
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - Row:       the ONLY <button> in the component. Its inline style height = density.row px. Its accessible name
//                includes the key and the title.
//   - Key:       the element whose text is exactly the key; inline style width = `calc(<keyCh>ch + 6px)`.
//   - Title:     the element whose text is exactly ticket.summary; it carries title="<full summary>".
//   - BLOCKED:   an element with the text "BLOCKED", present only when ticket.blocked.
//   - Days chip: the element whose text is "<n>d" (n = max(1, daysInLane)); data-alert="true" when the ticket is over its
//                lane limit or blocked, otherwise data-alert="false".
//
// SPEC-NOTE: PulseTrack is replaced by a double that exposes its props as data attributes, so this file checks how the
//   row drives the track (width, rowH, days, done, options) without depending on the track's internals (PulseTrack.test).
// SPEC-NOTE: `days` in TicketRowProps is the pulse window (21), NOT days-in-lane; the chip comes from ticket.daysInLane.
// SPEC-NOTE: Enter and click must each call onOpen exactly once (a native button activates on Enter, so a duplicate
//   keydown handler would fire twice).
import { fireEvent, render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { describe, expect, it, vi } from "vitest";
import type { TicketView, ViewOptions } from "../../api/types";
import { DENSITY_STEPS } from "../../layout/density";
import { TicketRow } from "./TicketRow";
import type { TicketRowProps } from "./props";

vi.mock("./PulseTrack", async () => {
  const { h } = await import("preact");
  return {
    PulseTrack: (p: {
      pulse: { label: string };
      days: number;
      width: number;
      rowH: number;
      lane?: boolean;
      done?: boolean;
      options: { showCommits: boolean; showPRs: boolean; showComments: boolean; devActivity: string };
    }) =>
      h("div", {
        role: "img",
        "aria-label": p.pulse.label,
        "data-testid": "track",
        "data-width": String(p.width),
        "data-rowh": String(p.rowH),
        "data-days": String(p.days),
        "data-done": String(!!p.done),
        "data-lane-flag": String(!!p.lane),
        "data-commits": String(p.options.showCommits),
        "data-prs": String(p.options.showPRs),
        "data-comments": String(p.options.showComments),
        "data-dev": p.options.devActivity,
      }),
  };
});

const OPTIONS: ViewOptions = {
  rowsMode: "fit",
  showCommits: true,
  showPRs: false,
  showComments: true,
  shadeWeekends: true,
  doneWindow: "sprint",
  doneLabel: "this sprint",
  devActivity: "ok",
};

function ticket(over: Partial<TicketView> = {}): TicketView {
  return {
    id: 1,
    sourceId: "10001",
    key: "MOB-1390",
    summary: "Swipe to check off items on the shopping list",
    url: "https://example.atlassian.net/browse/MOB-1390",
    type: "story",
    assignee: "acc-iris",
    lane: "qa",
    statusName: "QA",
    daysInLane: 3,
    overLimit: false,
    blocked: false,
    doneAt: 0,
    pulse: { segs: [{ lane: "qa", from: 0, to: 21 }], marks: [], blocked: [], label: "QA 3 days" },
    ...over,
  };
}

function renderRow(over: Partial<TicketRowProps> = {}) {
  const onOpen = vi.fn();
  const props: TicketRowProps = {
    ticket: ticket(),
    density: DENSITY_STEPS[2]!, // compact, 38px
    trackW: 440,
    keyCh: 8,
    days: 21,
    options: OPTIONS,
    onOpen,
    ...over,
  };
  return { onOpen, ...render(<TicketRow {...props} />) };
}

const row = () => screen.getByRole("button");
const chip = (text: string) => screen.getByText(text);

describe("TicketRow anatomy", () => {
  it("is a single button whose name carries the key and the title", () => {
    renderRow();
    expect(screen.getAllByRole("button")).toHaveLength(1);
    const name = row().textContent ?? "";
    expect(name).toContain("MOB-1390");
    expect(name).toContain("Swipe to check off items on the shopping list");
  });

  it.each(DENSITY_STEPS.map((d) => [d.step, d.row] as const))("is %s: %ipx tall", (step, rowH) => {
    const density = DENSITY_STEPS.find((d) => d.step === step)!;
    renderRow({ density });
    expect(row().style.height).toBe(`${rowH}px`);
  });

  it("shows the key in an element sized to keyCh + 6px so columns align", () => {
    renderRow({ keyCh: 10 });
    const key = screen.getByText("MOB-1390");
    expect(key.getAttribute("style") ?? "").toMatch(/calc\(\s*10ch\s*\+\s*6px\s*\)/);
  });

  it("puts the full title in the title attribute of the title element", () => {
    const summary = "A very long ticket title that would certainly be cut with an ellipsis in a narrow row, with detail";
    renderRow({ ticket: ticket({ summary }) });
    const titleEl = screen.getByText(summary);
    expect(titleEl.getAttribute("title")).toBe(summary);
  });

  it("shows the BLOCKED tag only on a blocked ticket", () => {
    const plain = renderRow();
    expect(screen.getByText("MOB-1390")).toBeTruthy(); // anchor
    expect(screen.queryByText("BLOCKED")).toBeNull();
    plain.unmount();
    renderRow({ ticket: ticket({ blocked: true }) });
    expect(screen.getByText("BLOCKED")).toBeTruthy();
  });

  it("puts the BLOCKED tag before the summary", () => {
    renderRow({ ticket: ticket({ blocked: true }) });
    const tag = screen.getByText("BLOCKED");
    const title = document.querySelector(".ticket-title")!;
    expect(tag.compareDocumentPosition(title) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});

describe("TicketRow days chip", () => {
  it.each([
    [6, "6d"],
    [14, "14d"],
    [1, "1d"],
    [0, "1d"], // minimum is 1d
  ])("daysInLane %i is shown as %s", (daysInLane, text) => {
    renderRow({ ticket: ticket({ daysInLane }) });
    expect(chip(text)).toBeTruthy();
  });

  it("is plain (data-alert=false) when within the limit and not blocked", () => {
    renderRow({ ticket: ticket({ daysInLane: 2 }) });
    expect(chip("2d").getAttribute("data-alert")).toBe("false");
  });

  it("is an alert when over the lane limit", () => {
    renderRow({ ticket: ticket({ daysInLane: 6, overLimit: true }) });
    expect(chip("6d").getAttribute("data-alert")).toBe("true");
  });

  it("is an alert when blocked, even within the limit", () => {
    renderRow({ ticket: ticket({ daysInLane: 1, blocked: true }) });
    expect(chip("1d").getAttribute("data-alert")).toBe("true");
  });
});

describe("TicketRow opens the issue", () => {
  it("calls onOpen(key) once on click", () => {
    const { onOpen } = renderRow();
    fireEvent.click(row());
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onOpen).toHaveBeenCalledWith("MOB-1390");
  });

  it("calls onOpen(key) exactly once on Enter when focused", async () => {
    const { onOpen } = renderRow({ ticket: ticket({ key: "MOB-77" }) });
    const user = userEvent.setup();
    row().focus();
    expect(document.activeElement).toBe(row());
    await user.keyboard("{Enter}");
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onOpen).toHaveBeenCalledWith("MOB-77");
  });

  it("can be reached with Tab", async () => {
    renderRow();
    const user = userEvent.setup();
    await user.tab();
    expect(document.activeElement).toBe(row());
  });
});

describe("TicketRow track", () => {
  it("passes width, row height, window days and the pulse to the track", () => {
    renderRow({ trackW: 600, days: 28, density: DENSITY_STEPS[4]! });
    const track = screen.getByTestId("track");
    expect(track.getAttribute("data-width")).toBe("600");
    expect(track.getAttribute("data-rowh")).toBe("34");
    expect(track.getAttribute("data-days")).toBe("28");
    expect(track.getAttribute("aria-label")).toBe("QA 3 days");
    expect(track.getAttribute("data-lane-flag")).toBe("false");
  });

  it("passes the view options through to the track", () => {
    renderRow({ options: { ...OPTIONS, showCommits: false, showPRs: true, showComments: false, devActivity: "unavailable" } });
    const track = screen.getByTestId("track");
    expect(track.getAttribute("data-commits")).toBe("false");
    expect(track.getAttribute("data-prs")).toBe("true");
    expect(track.getAttribute("data-comments")).toBe("false");
    expect(track.getAttribute("data-dev")).toBe("unavailable");
  });

  it("marks an active row's track as not done", () => {
    renderRow();
    expect(screen.getByTestId("track").getAttribute("data-done")).toBe("false");
  });

  it("marks a done row's track as done (segments only, no marks)", () => {
    renderRow({ ticket: ticket({ lane: "done", doneAt: 1_700_000_000_000, daysInLane: 2 }) });
    expect(screen.getByTestId("track").getAttribute("data-done")).toBe("true");
    expect(chip("2d")).toBeTruthy(); // done rows keep the same anatomy
  });
});

describe("TicketRow tokens and accessibility", () => {
  it("has no inline colours", () => {
    const { container } = renderRow({ ticket: ticket({ blocked: true, overLimit: true }) });
    const styled = Array.from(container.querySelectorAll("[style]"));
    expect(styled.length).toBeGreaterThan(0); // anchor: the row has an inline height
    for (const el of styled) expect(el.getAttribute("style")).not.toMatch(/#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(/i);
  });

  it("has no axe violations", async () => {
    const { container } = renderRow({ ticket: ticket({ blocked: true, overLimit: true }) });
    expect(row()).toBeTruthy(); // anchor
    const res = await axe.run(container, { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } });
    expect(res.violations.map((v) => v.id)).toEqual([]);
  });
});
