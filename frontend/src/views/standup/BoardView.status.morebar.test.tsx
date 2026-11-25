// F13 tests: the "Scroll for more" bar in status mode names the sections past the fold (ux-spec §6.5, §6.4):
// a lane section with the number of its rows below the fold ("In Progress (3)", which also covers a lane whose header
// is itself below), Done and Backlog by their full label once their heading is below. Layout is faked as in
// BoardView.morebar.test.tsx: jsdom has no layout, so rows get a bottom edge by their order on the page.
import { act, fireEvent, render, screen } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AxisDay, BacklogItem, LaneInfo, SpeakerView, StandupView, TicketView } from "../../api/types";
import { seedBoardGroups } from "../../state/standup";
import { BoardView } from "./BoardView";

vi.mock("./PulseTrack", async () => {
  const { h } = await import("preact");
  return { PulseTrack: () => h("div", { "data-testid": "track" }) };
});
vi.mock("./TrackBackdrop", async () => {
  const { h } = await import("preact");
  return { TrackBackdrop: () => h("div", { "aria-hidden": "true" }) };
});
vi.mock("./Legend", async () => {
  const { h } = await import("preact");
  return { Legend: () => h("div", { "data-testid": "legend" }) };
});

const AXIS: AxisDay[] = Array.from({ length: 21 }, (_, i) => {
  const d = new Date(Date.UTC(2026, 10, 3 + i));
  const dow = d.getUTCDay();
  return { date: d.toISOString().slice(0, 10), label: String(d.getUTCDate()), weekend: dow === 0 || dow === 6, monday: dow === 1 };
});
const L = (lane: string, label: string): LaneInfo => ({ lane, label, defaultLabel: "", kind: lane === "done" ? "done" : "working", custom: false, hue: 0, limitDays: 0, columns: [label] });
const LANES = [L("qa", "QA"), L("prog", "In Progress"), L("todo", "To Do"), L("done", "Done")];
const SPEAKERS: SpeakerView[] = [{ id: "p1", name: "Iris", first: "Iris", initials: "I", unassigned: false, included: true, active: 24, done: 15 }];
const BACKLOG: BacklogItem[] = Array.from({ length: 12 }, (_, i) => ({ issueId: 5000 + i, key: `MOB-${5000 + i}`, summary: `Backlog ${i}`, type: "story", url: "" }));

let n = 0;
const tk = (lane: string): TicketView => {
  n += 1;
  return {
    id: n, sourceId: String(n), key: `MOB-${2000 + n}`, summary: `Summary ${n}`, url: "", type: "story", assignee: "p1", lane, statusName: lane, daysInLane: 100 - n,
    overLimit: false, blocked: false, doneAt: lane === "done" ? 1_763_800_000_000 : 0, pulse: { segs: [], marks: [], blocked: [], label: "" },
  };
};

function makeView(): StandupView {
  n = 0;
  const tickets = [
    ...Array.from({ length: 6 }, () => tk("qa")),
    ...Array.from({ length: 10 }, () => tk("prog")),
    ...Array.from({ length: 8 }, () => tk("todo")),
    ...Array.from({ length: 15 }, () => tk("done")),
  ];
  return {
    standupId: 1, rev: 1, generatedAt: 1, today: "2026-11-23", asOf: 1, live: true, name: "S", spaceKey: "MOB", boardName: "B", boardType: "kanban", scope: "kanban",
    pulseDays: 21, axis: AXIS, lanes: LANES, speakers: SPEAKERS, tickets, backlog: BACKLOG, backlogTotal: 40,
    counts: { active: 24, unassigned: 0, done: 15, backlog: 40 },
    options: { rowsMode: "fit", showCommits: true, showPRs: true, showComments: true, shadeWeekends: true, doneWindow: "sprint", doneLabel: "this sprint", devActivity: "ok" },
    sync: { standupId: 1, runId: 0, state: "idle", mode: "", phase: "", done: 0, total: 0, lastSuccessAt: 0, lastAttemptAt: 0, lastStatus: "", warnings: [], errorCode: "", errorMessage: "", needsSync: false },
    warnings: [], previewKey: "", lastSyncedAt: 0,
  } as unknown as StandupView;
}

const metrics = { scrollHeight: 0, clientHeight: 0, scrollTop: 0 };
const originalRect = Element.prototype.getBoundingClientRect;
const LISTS: Record<string, number> = { Done: 1400, Backlog: 1500 };

function installLayout(m: Partial<typeof metrics>) {
  Object.assign(metrics, { scrollHeight: 0, clientHeight: 0, scrollTop: 0 }, m);
  Object.defineProperty(HTMLElement.prototype, "scrollHeight", { configurable: true, get: () => metrics.scrollHeight });
  Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => metrics.clientHeight });
  Object.defineProperty(HTMLElement.prototype, "scrollTop", { configurable: true, get: () => metrics.scrollTop, set: (v: number) => void (metrics.scrollTop = v) });
  Object.defineProperty(Element.prototype, "getBoundingClientRect", {
    configurable: true,
    value(this: Element) {
      let bottom = 0; // the scroller itself
      if (this.classList.contains("board-row")) {
        // row i (in page order) ends at 100 + 23 x (i + 1) in content px
        const i = Array.from(document.querySelectorAll(".board-row")).indexOf(this);
        bottom = 100 + 23 * (i + 1) - metrics.scrollTop;
      } else if (this.classList.contains("board-list-title")) {
        bottom = (LISTS[(this.textContent ?? "").trim()] ?? 0) - metrics.scrollTop;
      }
      return { top: bottom, bottom, left: 0, right: 0, width: 0, height: 0, x: 0, y: bottom, toJSON: () => ({}) } as DOMRect;
    },
  });
}

function restoreLayout() {
  for (const k of ["scrollHeight", "clientHeight", "scrollTop"]) delete (HTMLElement.prototype as unknown as Record<string, unknown>)[k];
  Object.defineProperty(Element.prototype, "getBoundingClientRect", { configurable: true, value: originalRect });
}

function renderInScroller() {
  render(
    <div style="overflow-y: auto" data-testid="scroller">
      <BoardView view={makeView()} mainW={1000} vh={1080} vw={1200} onSelectPerson={() => {}} />
    </div>,
  );
  return screen.getByTestId("scroller");
}

async function scrollTo(scroller: HTMLElement, top: number) {
  metrics.scrollTop = top;
  fireEvent.scroll(scroller);
  await act(async () => {
    vi.advanceTimersByTime(64);
  });
}

const summary = () => document.querySelector(".more-bar-summary")?.textContent;

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "requestAnimationFrame", "cancelAnimationFrame", "Date"] });
  seedBoardGroups({ "1": "status" });
});
afterEach(() => {
  restoreLayout();
  seedBoardGroups({});
  vi.useRealTimers();
});

describe("BoardView more bar, grouped by status", () => {
  // One column (mainW 1000): the rows run QA 0-5, In Progress 6-15, To Do 16-23. Fold = 450 - 36 = 414: a row ends at
  // 100 + 23 (i + 1), so rows from index 13 (bottom 422) are below it.
  it("names the lane sections with rows below the fold and their counts, then Done and Backlog by full label", async () => {
    installLayout({ clientHeight: 450, scrollHeight: 2000, scrollTop: 0 });
    const scroller = renderInScroller();
    await scrollTo(scroller, 0);
    expect(screen.getByText("⌄ Scroll for more")).toBeTruthy();
    expect(summary()).toBe("In Progress (3) · To Do (8) · Done (15) · Backlog (40) below");
  });

  it("drops sections as they scroll into view, then ends with End of list", async () => {
    installLayout({ clientHeight: 450, scrollHeight: 2000, scrollTop: 0 });
    const scroller = renderInScroller();
    await scrollTo(scroller, 200); // fold at 614: rows from index 22 (bottom 629) are below: only the last To Do rows
    expect(summary()).toBe("To Do (2) · Done (15) · Backlog (40) below");
    await scrollTo(scroller, 1000); // fold at 1414: Done's heading (1400) is above, Backlog's (1500) is not
    expect(summary()).toBe("Backlog (40) below");
    await scrollTo(scroller, 2000 - 450);
    expect(screen.getByText("End of list")).toBeTruthy();
  });

  it("shows no bar when the board does not overflow", async () => {
    installLayout({ clientHeight: 450, scrollHeight: 450, scrollTop: 0 });
    const scroller = renderInScroller();
    await scrollTo(scroller, 0);
    expect(document.querySelector(".more-bar")).toBeNull();
  });
});
