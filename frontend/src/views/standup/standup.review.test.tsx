// WP-38F: PersonView and BoardView review fixes: scroll reset per person, re-measure when the screen comes back from
// display:none (ResizeObserver), the axis label from view.pulseDays, and the Large (TV) text scale on inline px.
import { act, cleanup, render, screen } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AxisDay, LaneInfo, SpeakerView, StandupView, TicketView } from "../../api/types";
import { settings } from "../../state/app";
import type { TicketGroup } from "../../state/standup";
import { BoardView } from "./BoardView";
import { PersonView } from "./PersonView";

const AXIS = (n: number): AxisDay[] =>
  Array.from({ length: n }, (_, i) => {
    const d = new Date(Date.UTC(2026, 10, 3 + i));
    return { date: d.toISOString().slice(0, 10), label: String(d.getUTCDate()), weekend: d.getUTCDay() % 6 === 0, monday: d.getUTCDay() === 1 };
  });
const PROG: LaneInfo = { lane: "prog", label: "In Progress", defaultLabel: "In Progress", kind: "working", custom: false, hue: 0, limitDays: 5, columns: ["In Progress"] };
const sp = (id: string, name: string): SpeakerView => ({ id, name, first: name, initials: name.slice(0, 2), unassigned: false, included: true, active: 1, done: 0 });
const P1 = sp("p1", "Iris");
const P2 = sp("p2", "Silas");

function ticket(n: number, assignee: string): TicketView {
  return {
    id: n, sourceId: String(n), key: `MOB-${n}`, summary: `Summary ${n}`, url: "", type: "story", assignee, lane: "prog", statusName: "prog",
    daysInLane: 2, overLimit: false, blocked: false, doneAt: 0, pulse: { segs: [{ lane: "prog", from: 0, to: 5 }], marks: [], blocked: [], label: `p${n}` },
  };
}
function makeView(days = 21, tickets = 40): StandupView {
  return {
    standupId: 1, rev: 1, generatedAt: 1, today: "2026-11-23", asOf: 1, live: true, name: "S", spaceKey: "MOB", boardName: "B", boardType: "scrum", scope: "sprint",
    sprint: null, pulseDays: days, axis: AXIS(days), lanes: [PROG], speakers: [P1, P2],
    tickets: Array.from({ length: tickets }, (_, i) => ticket(i + 1, "p1")), backlog: [], backlogTotal: 0,
    counts: { active: tickets, unassigned: 0, done: 0, backlog: 0 },
    options: { rowsMode: "fit", showCommits: true, showPRs: true, showComments: true, shadeWeekends: true, doneWindow: "sprint", doneLabel: "done", devActivity: "ok" },
    sync: {}, warnings: [], previewKey: "", lastSyncedAt: 0,
  } as unknown as StandupView;
}
const groupsOf = (view: StandupView): TicketGroup[] => [{ lane: PROG, tickets: view.tickets }];

class FakeRO {
  static all: FakeRO[] = [];
  constructor(public cb: () => void) {
    FakeRO.all.push(this);
  }
  observe() {}
  disconnect() {}
}

const metrics = { scrollHeight: 0, clientHeight: 0, scrollTop: 0 };
function installMetrics(m: Partial<typeof metrics>) {
  Object.assign(metrics, { scrollHeight: 0, clientHeight: 0, scrollTop: 0 }, m);
  Object.defineProperty(HTMLElement.prototype, "scrollHeight", { configurable: true, get: () => metrics.scrollHeight });
  Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => metrics.clientHeight });
  Object.defineProperty(HTMLElement.prototype, "scrollTop", { configurable: true, get: () => metrics.scrollTop, set: (v: number) => void (metrics.scrollTop = v) });
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "requestAnimationFrame", "cancelAnimationFrame", "Date"] });
  settings.value = {};
  FakeRO.all = [];
  vi.stubGlobal("ResizeObserver", FakeRO);
});
afterEach(() => {
  cleanup();
  for (const k of ["scrollHeight", "clientHeight", "scrollTop"]) delete (HTMLElement.prototype as unknown as Record<string, unknown>)[k];
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("PersonView", () => {
  const view = makeView();
  const mount = (person: SpeakerView, v = view) => <PersonView view={v} person={person} groups={groupsOf(v)} mainW={1500} vh={640} />;

  it("starts at the top when the speaker changes (the scroller is reused)", () => {
    installMetrics({ clientHeight: 300, scrollHeight: 5000, scrollTop: 0 });
    const { rerender } = render(mount(P1));
    metrics.scrollTop = 800;
    rerender(mount(P2));
    expect(metrics.scrollTop).toBe(0);
  });

  it("re-measures when the scroller is resized (the screen returning from display:none)", async () => {
    installMetrics({ clientHeight: 0, scrollHeight: 0 });
    render(mount(P1));
    expect(FakeRO.all.length).toBeGreaterThan(0);
    // Measured while hidden (0 px): the bar wrongly reads "End of list".
    expect(screen.getByText("End of list")).toBeTruthy();
    metrics.clientHeight = 200;
    metrics.scrollHeight = 5000;
    await act(async () => {
      FakeRO.all.forEach((r) => r.cb());
      vi.advanceTimersByTime(64);
    });
    expect(screen.getByText("⌄ Scroll for more")).toBeTruthy();
    expect(document.querySelector(".more-bar-summary")!.textContent).toMatch(/In Progress below$/);
  });

  it("Large text multiplies the inline row height and track width, and decides density as Standard does", () => {
    // 1920x1080 Large behaves like 1536x864 Standard: the screen passes design px (vh 864), rows are drawn x1.25.
    const std = render(<PersonView view={view} person={P1} groups={groupsOf(view)} mainW={1216} vh={864} />);
    const stdRow = std.container.querySelector<HTMLElement>(".ticket-row")!;
    const stdH = parseFloat(stdRow.style.height);
    const stdTrack = parseFloat(std.container.querySelector<HTMLElement>(".pulse-track")!.style.width);
    cleanup();
    settings.value = { text_size: "large" };
    const big = render(<PersonView view={view} person={P1} groups={groupsOf(view)} mainW={1216} vh={864} />);
    const bigRow = big.container.querySelector<HTMLElement>(".ticket-row")!;
    expect(parseFloat(bigRow.style.height)).toBeCloseTo(stdH * 1.25, 5);
    expect(parseFloat(big.container.querySelector<HTMLElement>(".pulse-track")!.style.width)).toBeCloseTo(stdTrack * 1.25, 5);
    expect(bigRow.className).toBe(stdRow.className); // same density tier
  });

  it("Standard output is unchanged by the scale code (row heights are whole design px)", () => {
    const { container } = render(mount(P1));
    expect(container.querySelector<HTMLElement>(".ticket-row")!.style.height).toMatch(/^(46|38|34)px$/);
  });
});

describe("BoardView", () => {
  const boardView = (days: number) => makeView(days, 6);

  it("labels the axis from view.pulseDays", () => {
    render(<BoardView view={boardView(14)} mainW={1500} vh={900} onSelectPerson={() => {}} />);
    expect(screen.getAllByText("last 14 days").length).toBeGreaterThan(0);
    expect(screen.queryByText("last 21 days")).toBeNull();
  });

  it("re-measures the more bar when its scroller is resized", async () => {
    installMetrics({ clientHeight: 0, scrollHeight: 0 });
    render(
      <div style="overflow-y: auto">
        <BoardView view={boardView(21)} mainW={1500} vh={900} onSelectPerson={() => {}} />
      </div>,
    );
    expect(screen.queryByText(/Scroll for more|End of list/)).toBeNull();
    metrics.clientHeight = 300;
    metrics.scrollHeight = 2000;
    await act(async () => {
      FakeRO.all.forEach((r) => r.cb());
      vi.advanceTimersByTime(64);
    });
    expect(screen.getByText("⌄ Scroll for more")).toBeTruthy();
  });

  it("Large text multiplies the lane row height", () => {
    const std = render(<BoardView view={boardView(21)} mainW={1216} vh={864} onSelectPerson={() => {}} />);
    const stdH = parseFloat(std.container.querySelector<HTMLElement>(".board-row")!.style.height);
    cleanup();
    settings.value = { text_size: "large" };
    const big = render(<BoardView view={boardView(21)} mainW={1216} vh={864} onSelectPerson={() => {}} />);
    expect(parseFloat(big.container.querySelector<HTMLElement>(".board-row")!.style.height)).toBeCloseTo(stdH * 1.25, 5);
  });
});
