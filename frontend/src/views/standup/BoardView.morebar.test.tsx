// WP-38 tests: the whole-board "Scroll for more" bar (ux-spec §5.6, §6.4, AGENTS invariants 1 and 11).
// BoardView is rendered with the real MoreBar, DoneList, BacklogList and the real layout functions (boardBelow, atEnd).
//
// SPEC-NOTE: jsdom has no layout. The scroll container is a wrapper with inline `overflow-y: auto` (BoardView finds the
//   nearest scrolling ancestor with getComputedStyle). Scroll metrics are installed on HTMLElement.prototype, and header
//   positions come from a mocked getBoundingClientRect keyed by the lane first name (.board-lane-head) or the list title
//   (.board-list-title), so the test states where each section starts in content px.
// SPEC-NOTE: the bar is recomputed on scroll, throttled to one animation frame (tests advance fake timers by 64ms).
import { act, fireEvent, render, screen } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AxisDay, BacklogItem, LaneInfo, SpeakerView, StandupView, TicketView } from "../../api/types";
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
const LANES: LaneInfo[] = [
  { lane: "todo", label: "To Do", limitDays: 0, columns: ["To Do"] },
  { lane: "done", label: "Done", limitDays: 0, columns: ["Done"] },
];

let counter = 0;
const tk = (lane: string, assignee: string): TicketView => {
  counter += 1;
  const key = `MOB-${2000 + counter}`;
  return {
    id: counter, sourceId: String(counter), key, summary: `Summary of ${key}`, url: "", type: "story", assignee, lane, statusName: lane, daysInLane: 2,
    overLimit: false, blocked: false, doneAt: lane === "done" ? 1_763_800_000_000 : 0,
    pulse: { segs: [], marks: [], blocked: [], label: `pulse of ${key}` },
  };
};
const sp = (id: string, name: string, active: number): SpeakerView => ({
  id, name, first: name, initials: name[0] ?? "", unassigned: false, included: true, active, done: 0,
});
const SPEAKERS = [sp("p1", "Iris", 6), sp("p2", "Silas", 6), sp("p3", "Noor", 6)];

const BACKLOG: BacklogItem[] = Array.from({ length: 12 }, (_, i) => ({ key: `MOB-${5000 + i}`, summary: `Backlog ${i}`, type: "story", url: "" }));

function makeView(): StandupView {
  counter = 0;
  const tickets: TicketView[] = [];
  for (const s of SPEAKERS) for (let i = 0; i < 6; i++) tickets.push(tk("todo", s.id));
  for (let i = 0; i < 15; i++) tickets.push(tk("done", "p1"));
  return {
    standupId: 1, rev: 1, generatedAt: 1, today: "2026-11-23", asOf: 1, live: true, name: "Saffron Mobile", spaceKey: "MOB", boardName: "Board", boardType: "kanban", scope: "kanban",
    pulseDays: 21, axis: AXIS, lanes: LANES, speakers: SPEAKERS, tickets, backlog: BACKLOG, backlogTotal: 40,
    counts: { active: 18, unassigned: 0, done: 15, backlog: 40 },
    options: { rowsMode: "fit", showCommits: true, showPRs: true, showComments: true, shadeWeekends: true, doneWindow: "sprint", doneLabel: "this sprint", devActivity: "ok" },
    sync: { standupId: 1, runId: 0, state: "idle", mode: "", phase: "", done: 0, total: 0, lastSuccessAt: 0, lastAttemptAt: 0, lastStatus: "", warnings: [], errorCode: "", errorMessage: "", needsSync: false },
    warnings: [], previewKey: "", lastSyncedAt: 0,
  } as unknown as StandupView;
}

// ───────────────────────── scroll and layout metrics ─────────────────────────
const metrics = { scrollHeight: 0, clientHeight: 0, scrollTop: 0 };
// Section starts in content px: lane first names and the list titles ("Done", "Backlog").
const TOPS: Record<string, number> = {};
const originalRect = Element.prototype.getBoundingClientRect;

function installLayout(m: Partial<typeof metrics>) {
  Object.assign(metrics, { scrollHeight: 0, clientHeight: 0, scrollTop: 0 }, m);
  Object.defineProperty(HTMLElement.prototype, "scrollHeight", { configurable: true, get: () => metrics.scrollHeight });
  Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => metrics.clientHeight });
  Object.defineProperty(HTMLElement.prototype, "scrollTop", { configurable: true, get: () => metrics.scrollTop, set: (v: number) => void (metrics.scrollTop = v) });
  Object.defineProperty(Element.prototype, "getBoundingClientRect", {
    configurable: true,
    value(this: Element) {
      const key = this.classList.contains("board-lane-head")
        ? (this as HTMLElement).dataset.laneFirst ?? ""
        : this.classList.contains("board-list-title")
          ? (this.textContent ?? "").trim()
          : "";
      // Viewport position: content position minus how far the scroller has moved, as a real layout reports it.
      // The scroller itself stays at the top of the viewport (key "").
      const top = key === "" ? 0 : (TOPS[key] ?? 0) - metrics.scrollTop;
      // Headers are 0px tall here, so the bottom edge equals the top.
      return { top, bottom: top, left: 0, right: 0, width: 0, height: 0, x: 0, y: top, toJSON: () => ({}) } as DOMRect;
    },
  });
}

function restoreLayout() {
  for (const k of ["scrollHeight", "clientHeight", "scrollTop"]) delete (HTMLElement.prototype as unknown as Record<string, unknown>)[k];
  Object.defineProperty(Element.prototype, "getBoundingClientRect", { configurable: true, value: originalRect });
}

function renderInScroller() {
  const utils = render(
    <div style="overflow-y: auto" data-testid="scroller">
      <BoardView view={makeView()} mainW={1656} vh={1080} onSelectPerson={() => {}} />
    </div>,
  );
  const scroller = screen.getByTestId("scroller");
  return { ...utils, scroller };
}

async function scrollTo(scroller: HTMLElement, top: number) {
  metrics.scrollTop = top;
  fireEvent.scroll(scroller);
  await act(async () => {
    vi.advanceTimersByTime(64);
  });
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "requestAnimationFrame", "cancelAnimationFrame", "Date"] });
  for (const k of Object.keys(TOPS)) delete TOPS[k];
  Object.assign(TOPS, { Iris: 40, Silas: 300, Noor: 700, Done: 1400, Backlog: 1500 });
});

afterEach(() => {
  restoreLayout();
  vi.useRealTimers();
});

describe("BoardView more bar", () => {
  it("shows the summary of sections below the fold, then End of list at the end", async () => {
    installLayout({ clientHeight: 600, scrollHeight: 2000, scrollTop: 0 });
    const { scroller } = renderInScroller();
    await scrollTo(scroller, 0);
    // Fold = 600 - 36 = 564: Noor (700), Done (1400) and Backlog (1500) start below it.
    expect(screen.getByText("⌄ Scroll for more")).toBeTruthy();
    expect(screen.getByText("Noor · Done (15) · Backlog (40) below")).toBeTruthy();

    await scrollTo(scroller, 2000 - 600);
    expect(screen.getByText("End of list")).toBeTruthy();
    expect(screen.getByText("every ticket is shown above")).toBeTruthy();
    expect(screen.queryByText("⌄ Scroll for more")).toBeNull();
  });

  it("drops sections that scroll into view from the summary", async () => {
    installLayout({ clientHeight: 600, scrollHeight: 2000, scrollTop: 0 });
    const { scroller } = renderInScroller();
    // scrollTop 500: fold at 1064, so only Done and Backlog remain below.
    await scrollTo(scroller, 500);
    console.log("DBG", document.querySelector(".more-bar")?.textContent, [...document.querySelectorAll(".board-lane-head")].map((e) => (e as HTMLElement).dataset.laneFirst), [...document.querySelectorAll(".board-list-title")].map((e) => e.textContent));
    expect(screen.getByText("Done (15) · Backlog (40) below")).toBeTruthy();
    expect(document.querySelector(".more-bar-summary")?.textContent).toBe("Done (15) · Backlog (40) below");
  });

  it("shows no bar when the board does not overflow", async () => {
    installLayout({ clientHeight: 600, scrollHeight: 600, scrollTop: 0 });
    const { scroller } = renderInScroller();
    await scrollTo(scroller, 0);
    expect(screen.queryByText("⌄ Scroll for more")).toBeNull();
    expect(screen.queryByText("End of list")).toBeNull();
    expect(document.querySelector(".more-bar")).toBeNull();
  });
});
