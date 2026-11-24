// WP-25 tests: BoardView, the whole-board screen body (ux-spec §6, §10, AGENTS invariants 1 and 11, testing.md §4).
// PulseTrack, TrackBackdrop and Legend (WP-24) and DoneList and BacklogList (the sibling list components) are replaced by
// doubles that expose their props, so this file checks BoardView's own job: title line, lane balancing, lane rows and
// headers, axis rows, and what it hands to the lists and the track. Track internals live in PulseTrack.test.tsx.
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - Title:      <h1> "Whole board". The subtitle line is the PARENT element of the element whose own text is
//                 "<n> unassigned" (that element carries data-alert="true"); the line's normalised text is the whole line.
//   - Columns:    [data-column="1"] and [data-column="2"] containers (only "1" when there is a single column), in DOM
//                 order; lanes sit inside, in lane order.
//   - Lane:       role="group" with aria-label = SpeakerView.name ("Unassigned" for the unassigned lane).
//   - Lane header: the first <button> inside the lane: its text contains the name and "<active> active · <done> done";
//                 the Unassigned header instead has an element with the text "<n> need an owner" and data-alert="true".
//                 Pressing it calls onSelectPerson(speaker.id) ("unassigned" for the unassigned lane).
//   - Lane rows:  the other <button>s in the lane (text contains the key); each contains a track (PulseTrack with lane=true
//                 and days = view.pulseDays) and a days chip "<n>d" (min 1d, data-alert like the person view). Clicking
//                 calls openTicketFromRow(key) (the ticket modal opens; WP-T9).
//   - Axis row:   per column, an element with the text "last 21 days" and an element with the text "today".
//
// SPEC-NOTE: the subtitle line, with counts taken from view.counts, reads exactly (separators " · ", whitespace collapsed)
//     scrum:  "<sprint.name> · <sprint.endsLabel> · 40 active · 4 unassigned · 15 done · 40 backlog"
//     kanban: "Kanban · continuous flow · 40 active · 4 unassigned · 15 done · 40 backlog"
//   (ux-spec §6.1 gives the scrum line and "Kanban shows 'Kanban · continuous flow'"; product-spec F4 says the kanban line
//   keeps the counts.)
// SPEC-NOTE: lane order is hand-computed from ux-spec §6.2: Unassigned (if it has tickets) is pinned first in column 1
//   whatever its position in view.speakers; then speakers in view order (included ones, then excluded-with-tickets), each
//   into the column with the smaller height so far, ties to the left; height = 30 + rows x 23 + 8. The fixture gives
//   column 1: Unassigned, Silas, Otto, Jonas, Zed; column 2: Iris, Noor, Sam, Wren (derivation in the fixture comment).
// SPEC-NOTE: columns = 2 at mainW >= 1150, else 1 (mainW 1656 → 2, 1000 → 1). Lane row height is 23, or 20 when the
//   tallest column at 23 would exceed the available height (observed as the `rowH` the lane tracks receive: vh 1080 → 23,
//   vh 480 → 20).
// SPEC-NOTE: lane rows list only ACTIVE tickets, grouped by view.lanes order (walk order) then board rank (the order of
//   view.tickets); Done tickets appear only in the Done list.
// SPEC-NOTE: DoneList gets every ticket with lane "done", in view.tickets order, label = options.doneLabel, speakers =
//   view.speakers, cols = 2 when mainW - 40 >= 1300, else 1; BacklogList gets view.backlog,
//   view.backlogTotal, rows = view.backlog.length (0 hides it) and cols 3 / 2 / 1. mainW 1656 → done 2, backlog 3;
//   mainW 1180 → done 1, backlog 2; mainW 800 → done 1, backlog 1 (values chosen where the §6.3 thresholds are unambiguous).
// SPEC-NOTE: Legend gets mode full at mainW >= 1560, short at >= 1300, else button; a TrackBackdrop (todayLine=2,
//   shadeWeekends from the view options, the view's axis) is drawn behind the lanes.
// SPEC-NOTE: the more bar for the board is not asserted here (its summary depends on measured section positions).
import { fireEvent, render, screen, within } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi } from "../../api";
import type { Api } from "../../api";
import type { AxisDay, BacklogItem, LaneInfo, SpeakerView, StandupView, TicketView } from "../../api/types";
import { BoardView } from "./BoardView";
import type { BoardViewProps } from "./props";

vi.mock("./PulseTrack", async () => {
  const { h } = await import("preact");
  type P = { pulse: { label: string }; days: number; rowH: number; lane?: boolean; done?: boolean };
  return {
    PulseTrack: (p: P) =>
      h("div", { role: "img", "aria-label": p.pulse.label, "data-testid": "track", "data-lane-flag": String(!!p.lane), "data-days": String(p.days), "data-rowh": String(p.rowH) }),
  };
});
vi.mock("./TrackBackdrop", async () => {
  const { h } = await import("preact");
  type P = { axis: unknown[]; todayLine: number; shadeWeekends: boolean };
  return {
    TrackBackdrop: (p: P) => h("div", { "aria-hidden": "true", "data-testid": "backdrop", "data-today": String(p.todayLine), "data-shade": String(p.shadeWeekends), "data-days": String(p.axis.length) }),
  };
});
vi.mock("./Legend", async () => {
  const { h } = await import("preact");
  type P = { mode: string; lanes: { lane: string }[] };
  return { Legend: (p: P) => h("div", { "data-testid": "legend", "data-mode": p.mode, "data-lanes": p.lanes.map((l) => l.lane).join(",") }) };
});
vi.mock("./DoneList", async () => {
  const { h } = await import("preact");
  type P = { tickets: { key: string }[]; label: string; cols: number; speakers: { id: string }[] };
  return {
    DoneList: (p: P) =>
      h("div", { "data-testid": "done-list", "data-keys": p.tickets.map((t) => t.key).join(","), "data-label": p.label, "data-cols": String(p.cols), "data-speakers": p.speakers.map((s) => s.id).join(",") }),
  };
});
vi.mock("./BacklogList", async () => {
  const { h } = await import("preact");
  type P = { items: { key: string }[]; total: number; rows: number; cols: number };
  return {
    BacklogList: (p: P) =>
      h("div", { "data-testid": "backlog-list", "data-keys": p.items.map((i) => i.key).join(","), "data-total": String(p.total), "data-rows": String(p.rows), "data-cols": String(p.cols) }),
  };
});

// ───────────────────────── fixture ─────────────────────────
const AXIS: AxisDay[] = Array.from({ length: 21 }, (_, i) => {
  const d = new Date(Date.UTC(2026, 10, 3 + i));
  const dow = d.getUTCDay();
  return { date: d.toISOString().slice(0, 10), label: String(d.getUTCDate()), weekend: dow === 0 || dow === 6, monday: dow === 1 };
});
const L = (lane: string, label: string): LaneInfo => ({ lane, label, limitDays: 0, columns: [label] });
const LANES = [L("qa", "QA"), L("review", "In Review"), L("prog", "In Progress"), L("todo", "To Do"), L("done", "Done")];

let counter = 0;
const tk = (lane: string, assignee: string, over: Partial<TicketView> = {}): TicketView => {
  counter += 1;
  const key = `MOB-${1000 + counter}`;
  return {
    id: counter, sourceId: String(counter), key, summary: `Summary of ${key}`, url: "", type: "story", assignee, lane, statusName: lane, daysInLane: 2,
    overLimit: false, blocked: false, doneAt: lane === "done" ? 1_763_800_000_000 : 0, pulse: { segs: [{ lane: "prog", from: 0, to: 21 }], marks: [], blocked: [], label: `pulse of ${key}` }, ...over,
  };
};
const sp = (id: string, name: string, active: number, done: number, over: Partial<SpeakerView> = {}): SpeakerView => ({
  id, name, first: name.split(" ")[0]!, initials: name.split(" ").map((w) => w[0]).join(""), unassigned: false, included: true, active, done, ...over,
});

// Speakers in speaking order, then an excluded speaker who has tickets, then Unassigned LAST in the array on purpose.
//   rows:  Iris 4, Silas 4, Noor 4, Otto 3, Sam 4, Jonas 3, Wren 4, Zed 2 (excluded), Unassigned 4
//   height = 38 + 23 x rows (30 + 8 + rows x 23):  4 rows → 130, 3 → 107, 2 → 84
//   Unassigned → col1 (130). Iris: col1 130 vs col2 0 → col2 (130). Silas: tie → col1 (260). Noor: col2 (260).
//   Otto: tie → col1 (367). Sam: col2 260 < 367 → col2 (390). Jonas: col1 367 < 390 → col1 (474).
//   Wren: col2 390 < 474 → col2 (520). Zed: col1 474 < 520 → col1 (558).
const PRIYA = sp("p1", "Iris Calloway", 4, 2);
const MARCUS = sp("p2", "Silas Marchetti", 4, 2);
const JONAH = sp("p3", "Noor Abadi", 4, 1);
const ELENA = sp("p4", "Otto Diaz", 3, 1);
const SAM = sp("p5", "Amara Osei", 4, 1);
const TESS = sp("p6", "Jonas Lund", 3, 1);
const RAVI = sp("p7", "Wren Kowalski", 4, 1);
const ZED = sp("p8", "Zed Zhang", 2, 0, { included: false });
const UNASSIGNED: SpeakerView = { id: "unassigned", name: "Unassigned", first: "Unassigned", initials: "··", unassigned: true, included: true, active: 4, done: 0 };
const SPEAKERS = [PRIYA, MARCUS, JONAH, ELENA, SAM, TESS, RAVI, ZED, UNASSIGNED];

function build(): { view: StandupView; silasRowOrder: string[]; doneKeys: string[]; activeCount: number } {
  counter = 0;
  const tickets: TicketView[] = [];
  const silas = [tk("todo", "p2"), tk("qa", "p2"), tk("prog", "p2", { blocked: true, daysInLane: 0 }), tk("qa", "p2", { overLimit: true, daysInLane: 5 })];
  const silasRowOrder = [silas[1]!.key, silas[3]!.key, silas[2]!.key, silas[0]!.key]; // lanes qa, qa, prog, todo
  for (const s of SPEAKERS) {
    if (s.id === "p2") {
      tickets.push(...silas);
    } else {
      for (let i = 0; i < s.active; i++) tickets.push(tk(i % 2 ? "todo" : "prog", s.id === "unassigned" ? "" : s.id));
    }
  }
  const doneKeys: string[] = [];
  for (const s of SPEAKERS) for (let i = 0; i < s.done; i++) { const t = tk("done", s.id); tickets.push(t); doneKeys.push(t.key); }
  const orphan = tk("done", ""); // done with no assignee
  tickets.push(orphan);
  doneKeys.push(orphan.key);
  const backlog: BacklogItem[] = Array.from({ length: 12 }, (_, i) => ({ issueId: 5000 + i, key: `MOB-${5000 + i}`, summary: `Backlog ${i}`, type: "story", url: "" }));
  const view: StandupView = {
    standupId: 1, rev: 1, generatedAt: 1, today: "2026-11-23", asOf: 1, live: true, name: "Saffron Mobile", spaceKey: "MOB", boardName: "Board", boardType: "scrum", scope: "sprint",
    sprint: { id: "42", name: "Sprint 42", startAt: 1, endAt: 2, daysLeft: 0, endsLabel: "ends today" },
    pulseDays: 21, axis: AXIS, lanes: LANES, speakers: SPEAKERS, tickets, backlog, backlogTotal: 40,
    counts: { active: 40, unassigned: 4, done: 15, backlog: 40 },
    options: { rowsMode: "fit", showCommits: true, showPRs: true, showComments: true, shadeWeekends: true, doneWindow: "sprint", doneLabel: "this sprint", devActivity: "ok" },
    sync: { standupId: 1, runId: 0, state: "idle", mode: "", phase: "", done: 0, total: 0, lastSuccessAt: 0, lastAttemptAt: 0, lastStatus: "", warnings: [], errorCode: "", errorMessage: "", needsSync: false },
    warnings: [], previewKey: "", lastSyncedAt: 0,
  };
  return { view, silasRowOrder, doneKeys, activeCount: tickets.filter((t) => t.lane !== "done").length };
}

// ───────────────────────── harness ─────────────────────────
const rowOpen = vi.hoisted(() => ({ fn: (_key: string): unknown => undefined }));
vi.mock("../../state/ticketSteps", () => ({ openTicketFromRow: (key: string) => rowOpen.fn(key) }));
let openIssue: ReturnType<typeof vi.fn>;
beforeEach(() => {
  openIssue = vi.fn(async () => {});
  rowOpen.fn = openIssue as unknown as (key: string) => unknown; // rows open the ticket modal (WP-T9); the opener is replaced by this spy
  setApi({ app: { openIssue } } as unknown as Api);
});
afterEach(() => setApi(wailsApi));

function renderBoard(over: Partial<BoardViewProps> = {}, viewOver: Partial<StandupView> = {}) {
  const fx = build();
  const onSelectPerson = vi.fn();
  const view = { ...fx.view, ...viewOver };
  const utils = render(<BoardView view={view} mainW={1656} vh={1080} onSelectPerson={onSelectPerson} {...over} />);
  return { ...fx, view, onSelectPerson, ...utils };
}

const norm = (s: string | null) => (s ?? "").replace(/\s+/g, " ").trim();
const lane = (name: string) => screen.getByRole("group", { name });
const laneRows = (name: string) => within(lane(name)).queryAllByRole("button").filter((b) => (b.textContent ?? "").includes("MOB-"));
const keyOf = (b: HTMLElement) => /MOB-\d+/.exec(b.textContent ?? "")?.[0] ?? "";
const columnsEls = (c: Element) => Array.from(c.querySelectorAll<HTMLElement>("[data-column]"));
function colAt(c: Element, i: number): HTMLElement {
  const el = columnsEls(c)[i];
  expect(el, `board column #${i + 1}`).toBeDefined();
  return el as HTMLElement;
}
function headerOf(name: string): HTMLElement {
  const h = within(lane(name)).queryAllByRole("button")[0];
  expect(h, `header button of lane ${name}`).toBeDefined();
  return h as HTMLElement;
}
function rowAt(name: string, i: number): HTMLElement {
  const r = laneRows(name)[i];
  expect(r, `row ${i} of lane ${name}`).toBeDefined();
  return r as HTMLElement;
}
const lanesIn = (col: HTMLElement) => within(col).queryAllByRole("group").map((g) => g.getAttribute("aria-label"));
const AXE = { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } };

describe("BoardView title line", () => {
  it("is titled 'Whole board'", () => {
    renderBoard();
    expect(screen.getByRole("heading", { level: 1, name: "Whole board" })).toBeTruthy();
  });

  it("reads the scrum line with the unassigned count in alert text", () => {
    renderBoard();
    const alert = screen.getByText("4 unassigned");
    expect(alert.getAttribute("data-alert")).toBe("true");
    expect(norm(alert.parentElement!.textContent)).toBe("Sprint 42 · ends today · 40 active · 4 unassigned · 15 done · 40 backlog");
  });

  it("passes the sprint end label through", () => {
    const fx = build();
    renderBoard({}, { sprint: { ...fx.view.sprint!, endsLabel: "ends in 3 days" } });
    expect(norm(screen.getByText("4 unassigned").parentElement!.textContent)).toBe("Sprint 42 · ends in 3 days · 40 active · 4 unassigned · 15 done · 40 backlog");
  });

  it("reads 'Kanban · continuous flow' for a kanban board", () => {
    renderBoard({}, { boardType: "kanban", scope: "board", sprint: null });
    expect(norm(screen.getByText("4 unassigned").parentElement!.textContent)).toBe("Kanban · continuous flow · 40 active · 4 unassigned · 15 done · 40 backlog");
  });

  it("hands the Legend the walk lanes and a width-based mode (full / short / button)", () => {
    const full = renderBoard({ mainW: 1656 });
    expect(screen.getByTestId("legend").getAttribute("data-mode")).toBe("full");
    expect(screen.getByTestId("legend").getAttribute("data-lanes")).toContain("qa,review,prog,todo");
    full.unmount();
    const short = renderBoard({ mainW: 1400 });
    expect(screen.getByTestId("legend").getAttribute("data-mode")).toBe("short");
    short.unmount();
    renderBoard({ mainW: 1000 });
    expect(screen.getByTestId("legend").getAttribute("data-mode")).toBe("button");
  });
});

describe("BoardView lane columns and balancing", () => {
  it("uses two columns at mainW 1656 and one at 1000", () => {
    const wide = renderBoard({ mainW: 1656 });
    expect(columnsEls(wide.container).map((c) => c.getAttribute("data-column"))).toEqual(["1", "2"]);
    wide.unmount();
    const narrow = renderBoard({ mainW: 1000 });
    expect(columnsEls(narrow.container).map((c) => c.getAttribute("data-column"))).toEqual(["1"]);
  });

  it("pins Unassigned first in column 1 and balances lanes by height in speaking order (ties left)", () => {
    const { container } = renderBoard({ mainW: 1656 });
    expect(lanesIn(colAt(container, 0))).toEqual(["Unassigned", "Silas Marchetti", "Otto Diaz", "Jonas Lund", "Zed Zhang"]);
    expect(lanesIn(colAt(container, 1))).toEqual(["Iris Calloway", "Noor Abadi", "Amara Osei", "Wren Kowalski"]);
  });

  it("puts every lane in the single column in order when there is one column", () => {
    const { container } = renderBoard({ mainW: 1000 });
    expect(lanesIn(colAt(container, 0))).toEqual(["Unassigned", "Iris Calloway", "Silas Marchetti", "Noor Abadi", "Otto Diaz", "Amara Osei", "Jonas Lund", "Wren Kowalski", "Zed Zhang"]);
  });

  it("shows an excluded speaker who has tickets, after the included ones", () => {
    const { container } = renderBoard({ mainW: 1000 });
    const names = lanesIn(colAt(container, 0));
    expect(names.indexOf("Zed Zhang")).toBeGreaterThan(names.indexOf("Wren Kowalski"));
    expect(names).toContain("Zed Zhang");
  });

  it("draws one axis row per column: 'last 21 days' and 'today'", () => {
    const wide = renderBoard({ mainW: 1656 });
    expect(screen.getAllByText("last 21 days")).toHaveLength(2);
    expect(screen.getAllByText("today")).toHaveLength(2);
    wide.unmount();
    renderBoard({ mainW: 1000 });
    expect(screen.getAllByText("last 21 days")).toHaveLength(1);
    expect(screen.getAllByText("today")).toHaveLength(1);
  });

  it("uses 23px lane rows when the lanes fit and 20px when the tallest column would not", () => {
    const roomy = renderBoard({ vh: 1080 });
    const tracks = screen.getAllByTestId("track");
    expect(tracks.length).toBeGreaterThan(0);
    expect(new Set(tracks.map((t) => t.getAttribute("data-rowh")))).toEqual(new Set(["23"]));
    roomy.unmount();
    renderBoard({ vh: 480 });
    expect(new Set(screen.getAllByTestId("track").map((t) => t.getAttribute("data-rowh")))).toEqual(new Set(["20"]));
  });
});

describe("BoardView lanes", () => {
  it("heads a lane with the name and '<active> active · <done> done'", () => {
    renderBoard();
    const header = headerOf("Silas Marchetti");
    expect(header.textContent).toContain("Silas Marchetti");
    expect(norm(header.textContent)).toContain("4 active · 2 done");
  });

  it("heads the Unassigned lane with '4 need an owner' in alert text", () => {
    renderBoard();
    const header = headerOf("Unassigned");
    const text = within(header).getByText("4 need an owner");
    expect(text.getAttribute("data-alert")).toBe("true");
  });

  it("lists a lane's active tickets in walk-lane order, then rank", () => {
    const { silasRowOrder } = renderBoard();
    expect(laneRows("Silas Marchetti").map(keyOf)).toEqual(silasRowOrder);
  });

  it("gives every lane row a thin lane-mode track over the view's pulse window", () => {
    renderBoard();
    const row = rowAt("Silas Marchetti", 0);
    const track = within(row).getByTestId("track");
    expect(track.getAttribute("data-lane-flag")).toBe("true");
    expect(track.getAttribute("data-days")).toBe("21");
    for (const t of screen.getAllByTestId("track")) expect(t.getAttribute("data-lane-flag")).toBe("true");
  });

  it("shows days chips with a minimum of 1d and alert state for blocked or over-limit rows", () => {
    renderBoard();
    // Silas's rows in lane order: qa 2d, qa 5d over limit, prog blocked with daysInLane 0 (shown 1d), todo 2d.
    const chipOf = (i: number) => within(rowAt("Silas Marchetti", i)).getByText(/^\d+d$/);
    expect([0, 1, 2, 3].map((i) => chipOf(i).textContent)).toEqual(["2d", "5d", "1d", "2d"]);
    expect(chipOf(0).getAttribute("data-alert")).toBe("false");
    expect(chipOf(1).getAttribute("data-alert")).toBe("true"); // over limit
    expect(chipOf(2).getAttribute("data-alert")).toBe("true"); // blocked
  });

  it("selects a person from the lane header, including 'unassigned'", () => {
    const { onSelectPerson } = renderBoard();
    fireEvent.click(headerOf("Silas Marchetti"));
    expect(onSelectPerson).toHaveBeenLastCalledWith("p2");
    fireEvent.click(headerOf("Unassigned"));
    expect(onSelectPerson).toHaveBeenLastCalledWith("unassigned");
    fireEvent.click(headerOf("Zed Zhang"));
    expect(onSelectPerson).toHaveBeenLastCalledWith("p8");
    expect(onSelectPerson).toHaveBeenCalledTimes(3);
  });

  it("opens the issue when a lane row is clicked or activated with Enter", async () => {
    const { silasRowOrder } = renderBoard();
    fireEvent.click(rowAt("Silas Marchetti", 2));
    expect(openIssue).toHaveBeenCalledTimes(1);
    expect(openIssue).toHaveBeenLastCalledWith(silasRowOrder[2]);
    const user = userEvent.setup();
    rowAt("Silas Marchetti", 0).focus();
    await user.keyboard("{Enter}");
    expect(openIssue).toHaveBeenCalledTimes(2);
    expect(openIssue).toHaveBeenLastCalledWith(silasRowOrder[0]);
  });

  it("draws the 2px today line backdrop with the weekend setting from the view", () => {
    const on = renderBoard();
    const backdrops = screen.getAllByTestId("backdrop");
    expect(backdrops.length).toBeGreaterThan(0);
    for (const b of backdrops) {
      expect(b.getAttribute("data-today")).toBe("2");
      expect(b.getAttribute("data-shade")).toBe("true");
      expect(b.getAttribute("data-days")).toBe("21");
    }
    on.unmount();
    const fx = build();
    renderBoard({}, { options: { ...fx.view.options, shadeWeekends: false } });
    for (const b of screen.getAllByTestId("backdrop")) expect(b.getAttribute("data-shade")).toBe("false");
  });
});

describe("BoardView never hides tickets (AGENTS invariant 1)", () => {
  it("renders every active ticket as a lane row even in a small, tight window", () => {
    const { activeCount, view } = renderBoard({ mainW: 800, vh: 400 });
    const rows = screen.getAllByRole("group").flatMap((g) => within(g).queryAllByRole("button").filter((b) => (b.textContent ?? "").includes("MOB-")));
    expect(rows).toHaveLength(activeCount);
    const expected = view.tickets.filter((t) => t.lane !== "done").map((t) => t.key).sort();
    expect(rows.map(keyOf).sort()).toEqual(expected);
  });

  it("hands every done ticket to the Done list, in view order, never a count", () => {
    const { doneKeys, view } = renderBoard({ mainW: 800, vh: 400 });
    const list = screen.getByTestId("done-list");
    expect(list.getAttribute("data-keys")).toBe(doneKeys.join(","));
    expect(doneKeys).toHaveLength(view.tickets.filter((t) => t.lane === "done").length);
    expect(list.getAttribute("data-label")).toBe("this sprint");
    expect(list.getAttribute("data-speakers")).toBe(SPEAKERS.map((s) => s.id).join(","));
  });
});

describe("BoardView Done and Backlog sections", () => {
  it.each([
    [1656, "2", "3"],
    [1180, "1", "2"],
    [800, "1", "1"],
  ])("mainW %i: Done in %s column(s), Backlog in %s", (mainW, doneCols, backlogCols) => {
    renderBoard({ mainW });
    expect(screen.getByTestId("done-list").getAttribute("data-cols")).toBe(doneCols);
    expect(screen.getByTestId("backlog-list").getAttribute("data-cols")).toBe(backlogCols);
  });

  it("gives the Backlog list the view's items, the unknown-aware total and the configured row count", () => {
    const { view } = renderBoard();
    const list = screen.getByTestId("backlog-list");
    expect(list.getAttribute("data-keys")).toBe(view.backlog.map((b) => b.key).join(","));
    expect(list.getAttribute("data-total")).toBe("40");
    expect(list.getAttribute("data-rows")).toBe("12");
  });

  it("passes a total of -1 through unchanged", () => {
    renderBoard({}, { backlogTotal: -1 });
    expect(screen.getByTestId("backlog-list").getAttribute("data-total")).toBe("-1");
  });

  it("leaves no Backlog content when the view has no backlog rows (0 hides the section)", () => {
    renderBoard({}, { backlog: [], backlogTotal: 0 });
    expect(screen.getByTestId("done-list")).toBeTruthy(); // anchor: the rest rendered
    const el = screen.queryByTestId("backlog-list");
    expect(el === null || el.getAttribute("data-rows") === "0").toBe(true);
  });
});

describe("BoardView tokens and accessibility", () => {
  it("uses no inline colours", () => {
    const { container } = renderBoard();
    expect(screen.getByRole("heading", { level: 1, name: "Whole board" })).toBeTruthy(); // anchor
    for (const el of Array.from(container.querySelectorAll("[style]"))) {
      expect(el.getAttribute("style")).not.toMatch(/#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(/i);
    }
  });

  it.each([1656, 800])("has no axe violations at mainW %i", async (mainW) => {
    const { container } = renderBoard({ mainW });
    expect(screen.getByRole("heading", { level: 1, name: "Whole board" })).toBeTruthy(); // anchor
    expect(screen.getAllByRole("group").length).toBeGreaterThan(0);
    expect((await axe.run(container, AXE)).violations.map((v) => v.id)).toEqual([]);
  });
});
