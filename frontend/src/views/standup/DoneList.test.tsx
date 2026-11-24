// WP-25 tests: DoneList, the Done section of the whole board (ux-spec §6.3, §10, AGENTS invariant 1).
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - Heading: an <h2> whose text starts with "Done" (drawn in caps by CSS), plus an element whose own text is
//              "<count> <label>", e.g. "15 this sprint" or "15 since Friday" (count = tickets.length, label = the prop).
//   - Rows:    one <button> per ticket in the given order; its text contains the key and the summary; the assignee's
//              initials (SpeakerView.initials matched on TicketView.assignee) show as their own element inside the row;
//              an unassigned or unknown assignee shows "··" (as in the approved board mockup).
//              Clicking (or Enter) calls openTicketFromRow(key) (the ticket modal opens; WP-T9); DoneList has no onOpen prop.
//
// SPEC-NOTE: ux-spec §6.3 says `"15 this sprint" (or "14 days" / "since Friday")`; the heading text is read as
//   "<count> <doneLabel>". Only the "this sprint" and "since Friday" forms are asserted; how "14 days" combines with the
//   count is not specified.
// SPEC-NOTE: `cols` is layout only (grid columns filled top to bottom); the DOM order is always the ticket order. Column
//   routing is checked in BoardView.test.tsx (props passed) and Playwright, not here.
// SPEC-NOTE: Done is never collapsed to chips or a count: 100 done tickets give 100 rows.
import { fireEvent, render, screen, within } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi } from "../../api";
import type { Api } from "../../api";
import type { SpeakerView, TicketView } from "../../api/types";
import { DoneList } from "./DoneList";
import type { DoneListProps } from "./props";

const sp = (id: string, name: string, initials: string): SpeakerView => ({ id, name, first: name.split(" ")[0]!, initials, unassigned: false, included: true, active: 0, done: 0 });
const SPEAKERS = [sp("p1", "Iris Calloway", "IC"), sp("p2", "Silas Marchetti", "SM")];

function done(key: string, assignee: string, over: Partial<TicketView> = {}): TicketView {
  return {
    id: Number(key.replace(/\D/g, "")), sourceId: key, key, summary: `Summary of ${key}`, url: "", type: "story", assignee, lane: "done", statusName: "Done",
    daysInLane: 1, overLimit: false, blocked: false, doneAt: 1_763_800_000_000, pulse: { segs: [], marks: [], blocked: [], label: `pulse of ${key}` }, ...over,
  };
}

const SOME = [done("MOB-1464", "p1"), done("MOB-1473", "p1"), done("MOB-1519", "p2"), done("MOB-1663", "")];

function renderDone(over: Partial<DoneListProps> = {}) {
  return render(<DoneList tickets={SOME} label="this sprint" cols={2} speakers={SPEAKERS} {...over} />);
}

const rowOpen = vi.hoisted(() => ({ fn: (_key: string): unknown => undefined }));
vi.mock("../../state/ticketSteps", () => ({ openTicketFromRow: (key: string) => rowOpen.fn(key) }));
let openIssue: ReturnType<typeof vi.fn>;
beforeEach(() => {
  openIssue = vi.fn(async () => {});
  rowOpen.fn = openIssue as unknown as (key: string) => unknown; // rows open the ticket modal (WP-T9); the opener is replaced by this spy
  setApi({ app: { openIssue } } as unknown as Api);
});
afterEach(() => setApi(wailsApi));

const rows = () => screen.queryAllByRole("button");
function rowAt(i: number): HTMLElement {
  const r = rows()[i];
  expect(r, `row ${i}`).toBeDefined();
  return r as HTMLElement;
}
const AXE = { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } };

describe("DoneList heading", () => {
  it("is a Done heading with '<count> this sprint'", () => {
    renderDone();
    expect(screen.getByRole("heading", { level: 2, name: /^Done/ })).toBeTruthy();
    expect(screen.getByText("4 this sprint")).toBeTruthy();
  });

  it("uses the window label it is given", () => {
    renderDone({ label: "since Friday" });
    expect(screen.getByText("4 since Friday")).toBeTruthy();
  });

  it("counts every ticket given (15 here)", () => {
    const many = Array.from({ length: 15 }, (_, i) => done(`MOB-${2000 + i}`, "p1"));
    renderDone({ tickets: many });
    expect(screen.getByText("15 this sprint")).toBeTruthy();
  });
});

describe("DoneList rows", () => {
  it("renders every done ticket as a button in the given order, with key and summary", () => {
    renderDone();
    const list = rows();
    expect(list).toHaveLength(4);
    expect(list.map((b) => /MOB-\d+/.exec(b.textContent ?? "")?.[0])).toEqual(["MOB-1464", "MOB-1473", "MOB-1519", "MOB-1663"]);
    expect(list[0]!.textContent).toContain("Summary of MOB-1464");
  });

  it("shows the assignee's initials, and '··' when unassigned", () => {
    renderDone();
    expect(within(rowAt(0)).getByText("IC")).toBeTruthy();
    expect(within(rowAt(2)).getByText("SM")).toBeTruthy();
    expect(within(rowAt(3)).getByText("··")).toBeTruthy();
  });

  it("never collapses: 100 done tickets give 100 rows", () => {
    const many = Array.from({ length: 100 }, (_, i) => done(`MOB-${3000 + i}`, i % 2 ? "p1" : "p2"));
    renderDone({ tickets: many, cols: 1 });
    expect(rows()).toHaveLength(100);
    expect(screen.getByText("100 this sprint")).toBeTruthy();
  });

  it("opens the issue on click", () => {
    renderDone();
    fireEvent.click(rowAt(1));
    expect(openIssue).toHaveBeenCalledTimes(1);
    expect(openIssue).toHaveBeenCalledWith("MOB-1473");
  });

  it("opens the issue once on Enter", async () => {
    renderDone();
    const user = userEvent.setup();
    rowAt(2).focus();
    await user.keyboard("{Enter}");
    expect(openIssue).toHaveBeenCalledTimes(1);
    expect(openIssue).toHaveBeenCalledWith("MOB-1519");
  });
});

describe("DoneList tokens and accessibility", () => {
  it("uses no inline colours", () => {
    const { container } = renderDone();
    expect(screen.getByRole("heading", { level: 2, name: /^Done/ })).toBeTruthy(); // anchor
    for (const el of Array.from(container.querySelectorAll("[style]"))) {
      expect(el.getAttribute("style")).not.toMatch(/#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(/i);
    }
  });

  it.each([1, 2])("has no axe violations with %i columns", async (cols) => {
    const { container } = renderDone({ cols });
    expect(rows()).toHaveLength(4); // anchor
    expect((await axe.run(container, AXE)).violations.map((v) => v.id)).toEqual([]);
  });
});
