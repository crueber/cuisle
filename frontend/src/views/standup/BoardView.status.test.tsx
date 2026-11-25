// F13 tests: BoardView grouped by status (ux-spec §6.5). The shared doubles from BoardView.test.tsx stand in for the
// track, backdrop, legend, Done and Backlog lists; the Segmented, the section headers and the rows are real.
//
// SPEC-NOTE: DOM HOOKS: a status section is role="group" with aria-label = the lane label (inside [data-column]);
//   its header is [data-sec-head=<lane>]; rows are the <button>s inside it that contain "MOB-"; the assignee cell is
//   .board-assignee (title = full name, ".board-assignee--none" with "··" for an unassigned ticket). Done and Backlog sit
//   inside the columns, wrapped in [data-sec=done|backlog], not in the .board-bottom row of the assignee grouping.
import { fireEvent, render, screen, within } from "@testing-library/preact";
import axe from "axe-core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi } from "../../api";
import type { Api } from "../../api";
import type { AxisDay, BacklogItem, LaneInfo, SpeakerView, StandupView, TicketView } from "../../api/types";
import { boardGroupOf, seedBoardGroups } from "../../state/standup";
import { BoardView } from "./BoardView";
import type { BoardViewProps } from "./props";

vi.mock("./PulseTrack", async () => {
  const { h } = await import("preact");
  return { PulseTrack: (p: { width: number }) => h("div", { "data-testid": "track", "data-width": String(p.width) }) };
});
vi.mock("./TrackBackdrop", async () => {
  const { h } = await import("preact");
  return { TrackBackdrop: () => h("div", { "aria-hidden": "true" }) };
});
vi.mock("./Legend", async () => {
  const { h } = await import("preact");
  return { Legend: () => h("div", { "data-testid": "legend" }) };
});
vi.mock("./DoneList", async () => {
  const { h } = await import("preact");
  return { DoneList: (p: { tickets: unknown[]; cols: number }) => h("div", { "data-testid": "done-list", "data-n": String(p.tickets.length), "data-cols": String(p.cols) }) };
});
vi.mock("./BacklogList", async () => {
  const { h } = await import("preact");
  return { BacklogList: (p: { items: unknown[]; cols: number }) => h("div", { "data-testid": "backlog-list", "data-n": String(p.items.length), "data-cols": String(p.cols) }) };
});
const rowOpen = vi.hoisted(() => ({ fn: (_key: string): unknown => undefined }));
vi.mock("../../state/ticketSteps", () => ({ openTicketFromRow: (key: string) => rowOpen.fn(key) }));

const AXIS: AxisDay[] = Array.from({ length: 21 }, (_, i) => {
  const d = new Date(Date.UTC(2026, 10, 3 + i));
  const dow = d.getUTCDay();
  return { date: d.toISOString().slice(0, 10), label: String(d.getUTCDate()), weekend: dow === 0 || dow === 6, monday: dow === 1 };
});
const L = (lane: string, label: string, limitDays = 0): LaneInfo => ({ lane, label, defaultLabel: "", kind: lane === "done" ? "done" : "working", custom: false, hue: 0, limitDays, columns: [label] });
const LANES = [L("qa", "QA", 3), L("review", "In Review", 2), L("prog", "In Progress", 5), L("todo", "To Do", 0), L("done", "Done")];

let counter = 0;
const tk = (lane: string, assignee: string, over: Partial<TicketView> = {}): TicketView => {
  counter += 1;
  const key = `MOB-${1000 + counter}`;
  return {
    id: counter, sourceId: String(counter), key, summary: `Summary of ${key}`, url: "", type: "story", assignee, lane, statusName: lane, daysInLane: 2,
    overLimit: false, blocked: false, doneAt: lane === "done" ? 1_763_800_000_000 : 0, pulse: { segs: [], marks: [], blocked: [], label: `pulse of ${key}` }, ...over,
  };
};
const sp = (id: string, name: string, over: Partial<SpeakerView> = {}): SpeakerView => ({
  id, name, first: name.split(" ")[0]!, initials: name.split(" ").map((w) => w[0]).join(""), unassigned: false, included: true, active: 1, done: 0, ...over,
});
const SPEAKERS = [sp("p1", "Iris Calloway"), sp("p2", "Silas Marchetti"), sp("unassigned", "Unassigned", { unassigned: true, initials: "··" })];

function build(tickets: TicketView[], over: Partial<StandupView> = {}): StandupView {
  const backlog: BacklogItem[] = Array.from({ length: 3 }, (_, i) => ({ issueId: 5000 + i, key: `MOB-${5000 + i}`, summary: `Backlog ${i}`, type: "story", url: "" }));
  return {
    standupId: 1, rev: 1, generatedAt: 1, today: "2026-11-23", asOf: 1, live: true, name: "Saffron Mobile", spaceKey: "MOB", boardName: "Board", boardType: "scrum", scope: "sprint",
    sprint: { id: "42", name: "Sprint 42", startAt: 1, endAt: 2, daysLeft: 0, endsLabel: "ends today" },
    pulseDays: 21, axis: AXIS, lanes: LANES, speakers: SPEAKERS, tickets, backlog, backlogTotal: 3,
    counts: { active: tickets.filter((t) => t.lane !== "done").length, unassigned: 1, done: tickets.filter((t) => t.lane === "done").length, backlog: 3 },
    options: { rowsMode: "fit", showCommits: true, showPRs: true, showComments: true, shadeWeekends: true, doneWindow: "sprint", doneLabel: "this sprint", devActivity: "ok" },
    sync: { standupId: 1, runId: 0, state: "idle", mode: "", phase: "", done: 0, total: 0, lastSuccessAt: 0, lastAttemptAt: 0, lastStatus: "", warnings: [], errorCode: "", errorMessage: "", needsSync: false },
    warnings: [], previewKey: "", lastSyncedAt: 0, ...over,
  } as StandupView;
}

function sample(): TicketView[] {
  counter = 0;
  return [
    tk("todo", "p1", { daysInLane: 1 }),
    tk("qa", "p2", { daysInLane: 2 }),
    tk("qa", "p1", { daysInLane: 9, overLimit: true, blocked: true }),
    tk("qa", "", { daysInLane: 4, overLimit: true }),
    tk("qa", "p2", { daysInLane: 5 }),
    tk("prog", "p1", { daysInLane: 3 }),
    tk("prog", "p2", { daysInLane: 3 }),
    tk("done", "p1"),
    tk("done", "p2"),
  ];
}

let setUIState: ReturnType<typeof vi.fn>;
beforeEach(() => {
  setUIState = vi.fn(async () => undefined);
  setApi({ app: { setUIState, openIssue: vi.fn() } } as unknown as Api);
  rowOpen.fn = vi.fn();
  seedBoardGroups({ "1": "status" });
});
afterEach(() => {
  seedBoardGroups({});
  setApi(wailsApi);
});

function renderBoard(over: Partial<BoardViewProps> = {}, view = build(sample())) {
  return { view, ...render(<BoardView view={view} mainW={1656} vh={1080} vw={1920} onSelectPerson={vi.fn()} {...over} />) };
}
const section = (name: string) => screen.getByRole("group", { name });
const rowsOf = (name: string) => within(section(name)).getAllByRole("button").map((b) => /MOB-\d+/.exec(b.textContent ?? "")?.[0] ?? "");
const norm = (s: string | null) => (s ?? "").replace(/\s+/g, " ").trim();
const colAt = (c: Element, i: number) => c.querySelectorAll<HTMLElement>("[data-column]")[i] as HTMLElement;
const sectionsIn = (col: HTMLElement) => Array.from(col.querySelectorAll<HTMLElement>("[data-sec]")).map((e) => e.dataset.sec === "lane" ? e.dataset.secLabel : e.dataset.sec);

describe("the Assignee | Status switch", () => {
  it("is a labelled Segmented in the title row with the saved choice selected, and a G hint", () => {
    const { container } = renderBoard();
    const group = screen.getByRole("group", { name: "Group the board by" });
    expect(within(group).getAllByRole("button").map((r) => r.textContent)).toEqual(["Assignee", "Status"]);
    expect(screen.getByRole("button", { name: "Status" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: "Assignee" }).getAttribute("aria-pressed")).toBe("false");
    const head = container.querySelector(".board-head") as HTMLElement;
    expect(head.contains(group)).toBe(true);
    expect(norm(head.querySelector(".board-group-kbd")!.textContent)).toBe("G");
    // the switch (with its G hint) comes right before the legend
    expect(head.querySelector(".board-group")!.nextElementSibling).toBe(head.querySelector("[data-testid=legend]"));
    expect(head.querySelector(".board-group")!.lastElementChild).toBe(head.querySelector(".board-group-kbd"));
  });

  it("selecting a segment regroups the board and persists the choice for this standup at once", () => {
    seedBoardGroups({});
    const { container } = renderBoard();
    expect(container.querySelector(".board-sec")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Status" }));
    expect(boardGroupOf(1)).toBe("status");
    expect(setUIState).toHaveBeenCalledWith({ standupId: 1, boardGroup: "status" });
    expect(container.querySelector(".board-sec")).not.toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Assignee" }));
    expect(setUIState).toHaveBeenLastCalledWith({ standupId: 1, boardGroup: "assignee" });
    expect(container.querySelector(".board-sec")).toBeNull();
    expect(container.querySelector(".board-lane-head")).not.toBeNull();
  });

  it("assignee mode keeps today's board: lanes by person, Done and Backlog below, no assignee column", () => {
    seedBoardGroups({ "1": "assignee" });
    const { container } = renderBoard();
    expect(container.querySelector(".board-assignee")).toBeNull();
    expect(container.querySelector(".board-bottom")).not.toBeNull();
    expect(container.querySelector("[data-sec]")).toBeNull();
    expect(screen.getByRole("group", { name: "Iris Calloway" })).toBeTruthy();
  });
});

describe("status sections", () => {
  it("makes one section per lane in walk order, cut once into two columns, with Done and Backlog after them", () => {
    const { container } = renderBoard();
    const c1 = sectionsIn(colAt(container, 0));
    const c2 = sectionsIn(colAt(container, 1));
    expect([...c1, ...c2]).toEqual(["QA", "In Progress", "To Do", "done", "backlog"].filter((s) => s !== "In Review"));
    // nothing is dropped and nothing is repeated
    expect(container.querySelectorAll(".board-sec").length).toBe(3);
  });

  it("leaves out a lane with no active ticket (In Review here)", () => {
    renderBoard();
    expect(screen.queryByRole("group", { name: "In Review" })).toBeNull();
  });

  it("sorts rows over-limit first, then days in lane descending, then board rank", () => {
    renderBoard();
    // keys: 1001 todo; 1002 qa 2d; 1003 qa 9d over; 1004 qa 4d over; 1005 qa 5d
    expect(rowsOf("QA")).toEqual(["MOB-1003", "MOB-1004", "MOB-1005", "MOB-1002"]);
  });

  it("lists every active ticket exactly once (nothing hidden)", () => {
    const { container } = renderBoard();
    const keys = Array.from(container.querySelectorAll(".board-sec .board-row-key")).map((e) => e.textContent);
    expect(keys.length).toBe(7);
    expect(new Set(keys).size).toBe(7);
  });

  it("stacks every section into one column below mainW 1150", () => {
    const { container } = renderBoard({ mainW: 1000, vw: 1200 });
    expect(container.querySelectorAll("[data-column]").length).toBe(1);
    expect(sectionsIn(colAt(container, 0))).toEqual(["QA", "In Progress", "To Do", "done", "backlog"]);
  });

  it("puts Done and Backlog in the columns as two-column grids, not in the bottom row", () => {
    const { container } = renderBoard();
    expect(container.querySelector(".board-bottom")).toBeNull();
    expect(screen.getByTestId("done-list").dataset.cols).toBe("2");
    expect(screen.getByTestId("backlog-list").dataset.cols).toBe("2");
    expect(screen.getByTestId("done-list").dataset.n).toBe("2");
  });

  it("has an axis row in each column", () => {
    const { container } = renderBoard();
    for (const col of [colAt(container, 0), colAt(container, 1)]) expect(norm(col.querySelector(".board-axis-label")!.textContent)).toBe("last 21 days");
  });

  it("follows the lanes the view declares (custom lane ids and labels need no change here)", () => {
    counter = 0;
    const lanes = [L("spike", "Spike", 4), L("prog", "In Progress", 5), L("done", "Done")];
    const view = build([tk("spike", "p1"), tk("prog", "p2"), tk("spike", "")], { lanes });
    const { container } = renderBoard({}, view);
    expect(Array.from(container.querySelectorAll(".board-sec-name")).map((e) => e.textContent)).toEqual(["Spike", "In Progress"]);
    expect(container.querySelector('[data-sec-head="spike"] [data-lane="spike"]')).not.toBeNull(); // the swatch colour comes from data-lane
  });
});

describe("status section header", () => {
  // The header is a flex row of spans; read it cell by cell, as it is laid out.
  const head = (lane: string) => norm(Array.from(document.querySelector(`[data-sec-head="${lane}"]`)!.children).map((c) => c.textContent).join(" "));

  it("shows the name, ticket count, non-zero alert counts and the limit at full width", () => {
    renderBoard();
    expect(head("qa")).toBe("QA 4 tickets · 2 over limit · 1 blocked · 1 unassigned limit 3d · longest first");
  });

  it("says nothing for a zero count and omits the limit text when the lane has no limit", () => {
    renderBoard();
    expect(head("todo")).toBe("To Do 1 ticket");
    expect(head("prog")).toBe("In Progress 2 tickets limit 5d · longest first");
  });

  it("shortens below 1360 (compact rail): the bare count, 'over', and 'limit 3d'", () => {
    renderBoard({ vw: 1280, mainW: 1164 });
    expect(head("qa")).toBe("QA 4 · 2 over · 1 blocked · 1 unassigned limit 3d");
    expect(head("todo")).toBe("To Do 1");
  });

  it("gives way before it would run out of the column: first 'longest first' goes, then the short wording", () => {
    // 1440 wide (mainW 1192, two 576px columns): QA's four-part header does not fit in full, fits without "longest first".
    renderBoard({ mainW: 1192, vw: 1440 });
    expect(head("qa")).toBe("QA 4 tickets · 2 over limit · 1 blocked · 1 unassigned limit 3d");
    // a narrower column still: the short wording ("4", "over") like below 1360
    document.body.innerHTML = "";
    renderBoard({ mainW: 1150, vw: 1400 });
    expect(head("qa")).toMatch(/^QA 4 /);
    expect(head("qa")).not.toContain("longest first");
  });

  it("colours the alert counts with the alert token (data-alert) and carries the lane swatch", () => {
    renderBoard();
    const h = document.querySelector('[data-sec-head="qa"]') as HTMLElement;
    expect(Array.from(h.querySelectorAll("[data-alert=true]")).map((e) => norm(e.textContent))).toEqual(["· 2 over limit", "· 1 blocked", "· 1 unassigned"]);
    expect(h.querySelector('.board-sec-swatch[data-lane="qa"]')).not.toBeNull();
  });
});

describe("status rows", () => {
  it("add an assignee column with the initials and the full name in the title", () => {
    renderBoard();
    const row = within(section("QA")).getAllByRole("button")[0]!; // MOB-1003, Iris
    const cell = row.querySelector(".board-assignee") as HTMLElement;
    expect(cell.textContent).toBe("IC");
    expect(cell.getAttribute("title")).toBe("Iris Calloway");
    // the cells run icon, key, title, assignee, track, days chip
    expect(Array.from(row.children).map((c) => (c as HTMLElement).className).join("|")).toMatch(/board-row-key.*board-row-title.*board-assignee.*board-days/);
    expect(row.querySelector("[data-testid=track]")!.compareDocumentPosition(cell) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy();
  });

  it("show a dashed '··' box with an accessible name for an unassigned ticket", () => {
    renderBoard();
    const none = within(section("QA")).getAllByRole("img", { name: "Unassigned" });
    expect(none).toHaveLength(1);
    expect(none[0]!.textContent).toBe("··");
    expect(none[0]!.className).toContain("board-assignee--none");
  });

  it("show BLOCKED after the title, and only in this mode", () => {
    renderBoard();
    const row = within(section("QA")).getAllByRole("button")[0]!;
    const title = row.querySelector(".board-row-title") as HTMLElement;
    expect(title.lastElementChild?.textContent).toBe("BLOCKED");
    expect(title.firstElementChild?.textContent).toBe("Summary of MOB-1003");
    expect(within(section("QA")).getAllByText("BLOCKED")).toHaveLength(1);
  });

  it("narrow the track by the assignee column (30px + one gap) compared with the assignee grouping", () => {
    const widthOf = () => Number((document.querySelector("[data-testid=track]") as HTMLElement).dataset.width);
    const { unmount } = renderBoard();
    const status = widthOf();
    unmount();
    seedBoardGroups({ "1": "assignee" });
    renderBoard();
    expect(widthOf() - status).toBe(30 + 8);
  });

  it("open the ticket modal on click, like every board row", () => {
    renderBoard();
    fireEvent.click(within(section("QA")).getAllByRole("button")[0]!);
    expect(rowOpen.fn).toHaveBeenCalledWith("MOB-1003");
  });
});

describe("accessibility", () => {
  it("has no axe violations in status mode (switch as a labelled group of aria-pressed buttons)", async () => {
    const { container } = renderBoard();
    const res = await axe.run(container, { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } });
    expect(res.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(" ")}`)).toEqual([]);
  });
});
