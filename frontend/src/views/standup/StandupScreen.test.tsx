// WP-23 tests: StandupScreen, the frame that wires Header, Rail and the person/board views (ux-spec §2, §3, §5, §6;
// architecture §4.3, §4.5). Header, Rail, PersonView and BoardView are replaced by doubles that expose their props, so
// this file only tests the screen's own wiring and layout maths (the children have their own tests; PersonView and
// BoardView belong to other work packages). State is the REAL signals (route, views, standups) and real state functions.
//
// SPEC-NOTE: vw and vh are `window.innerWidth` / `window.innerHeight` (unscaled CSS px at Standard text size), re-read on
//   `resize` (the tests advance fake timers by 64ms so a throttled handler settles). Large (TV) text scaling is not
//   covered here: how the screen learns the text scale is not specified beyond "unscale(px, scale)".
// SPEC-NOTE: props the screen computes for its children, hand-computed from ux-spec §2.2, §2.3 and §3:
//     vw 1920 → rail full  (208),  padding.x 32 → mainW 1920 − 208 − 64 = 1648
//     vw 1440 → rail full  (208),  padding.x 20 → mainW 1440 − 208 − 40 = 1192
//     vw 1280 → rail compact (76), padding.x 20 → mainW 1280 −  76 − 40 = 1164
//     vw 1024 → rail compact (76), padding.x 20 → mainW 1024 −  76 − 40 =  908
//   PersonView and BoardView get that mainW, and `vh` = window.innerHeight (the full window height; the view subtracts
//   the header, padding, title and axis itself, per the §5.5 formula). Header gets vw = innerWidth; Rail gets mode.
// SPEC-NOTE: the selected person comes from route.person: an accountId or "unassigned" → PersonView for that speaker
//   (also an excluded speaker who has tickets, since clicking their board lane opens their person view); null, or an id
//   that is not a speaker of this standup, → BoardView ("keeps the person only if they speak there, otherwise whole board").
// SPEC-NOTE: PersonView gets `groups` = the person's tickets grouped by view.lanes (walk order, Done last, empty groups
//   dropped); `view` is the cached StandupView; Rail gets view.speakers unchanged (the rail does its own Included filter).
// SPEC-NOTE: switching speaker (rail click, lane title click) never calls the Go api (architecture §4.3): the fake api's
//   standup.view must not be called.
// SPEC-NOTE: the screen's content area is a <main> landmark holding the person or board view.
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi } from "../../api";
import type { Api } from "../../api";
import type { LaneInfo, SpeakerView, StandupTab, StandupView, TicketView } from "../../api/types";
import { standups } from "../../state/app";
import { route } from "../../state/route";
import { views } from "../../state/standup";
import { syncStatus } from "../../state/sync";
import { StandupScreen } from "./StandupScreen";

vi.mock("./Header", async () => {
  const { h } = await import("preact");
  return { Header: (p: { vw: number }) => h("header", { "data-testid": "header", "data-vw": String(p.vw) }, "header-double") };
});
vi.mock("./Rail", async () => {
  const { h } = await import("preact");
  type P = { mode: string; speakers: { id: string }[]; selected: string | null; onSelect: (p: string | null) => void };
  return {
    Rail: (p: P) =>
      h(
        "nav",
        { "aria-label": "Speakers", "data-testid": "rail", "data-mode": p.mode, "data-speakers": p.speakers.map((s) => s.id).join(","), "data-selected": p.selected ?? "" },
        h("button", { type: "button", onClick: () => p.onSelect(null) }, "rail-pick-whole-board"),
        h("button", { type: "button", onClick: () => p.onSelect("p2") }, "rail-pick-p2"),
      ),
  };
});
vi.mock("./PersonView", async () => {
  const { h } = await import("preact");
  type P = { person: { id: string }; groups: { lane: { label: string }; tickets: unknown[] }[]; mainW: number; vh: number; view: { standupId: number } };
  return {
    PersonView: (p: P) =>
      h("div", {
        "data-testid": "person-view",
        "data-person": p.person.id,
        "data-groups": p.groups.map((g) => `${g.lane.label}:${g.tickets.length}`).join("|"),
        "data-mainw": String(p.mainW),
        "data-vh": String(p.vh),
        "data-standup": String(p.view.standupId),
      }),
  };
});
vi.mock("./BoardView", async () => {
  const { h } = await import("preact");
  type P = { view: { standupId: number }; mainW: number; vh: number; onSelectPerson: (p: string) => void };
  return {
    BoardView: (p: P) =>
      h(
        "div",
        { "data-testid": "board-view", "data-standup": String(p.view.standupId), "data-mainw": String(p.mainW), "data-vh": String(p.vh) },
        h("button", { type: "button", onClick: () => p.onSelectPerson("p2") }, "board-pick-p2"),
      ),
  };
});

// ───────────────────────── fixture ─────────────────────────
const L = (lane: string, label: string): LaneInfo => ({ lane, label, defaultLabel: "", kind: lane === "done" ? "done" : "working", custom: false, hue: 0, limitDays: 0, columns: [label] });
const LANES = [L("qa", "QA"), L("review", "In Review"), L("prog", "In Progress"), L("todo", "To Do"), L("done", "Done")];
const sp = (id: string, name: string, initials: string, active: number, over: Partial<SpeakerView> = {}): SpeakerView => ({
  id, name, first: name.split(" ")[0]!, initials, unassigned: false, included: true, active, done: 0, ...over,
});
const PRIYA = sp("p1", "Iris Calloway", "IC", 5);
const MARCUS = sp("p2", "Silas Marchetti", "SM", 1);
const ELENA = sp("p4", "Otto Diaz", "OD", 1, { included: false });
const UNASSIGNED: SpeakerView = { id: "unassigned", name: "Unassigned", first: "Unassigned", initials: "··", unassigned: true, included: true, active: 1, done: 0 };

let n = 0;
const tk = (lane: string, assignee: string): TicketView => {
  n += 1;
  return {
    id: n, sourceId: String(n), key: `MOB-${n}`, summary: `Summary ${n}`, url: "", type: "story", assignee, lane, statusName: lane, daysInLane: 2,
    overLimit: false, blocked: false, doneAt: lane === "done" ? 1 : 0, pulse: { segs: [], marks: [], blocked: [], label: `pulse ${n}` },
  };
};

function makeView(): StandupView {
  n = 0;
  // Given in a scrambled order on purpose: grouping must follow view.lanes, not ticket order.
  const tickets = [
    tk("done", "p1"), tk("prog", "p1"), tk("qa", "p1"), tk("prog", "p1"), tk("review", "p1"), tk("qa", "p1"),
    tk("prog", "p2"), tk("todo", ""), tk("prog", "p4"),
  ];
  return {
    standupId: 1, rev: 1, generatedAt: 1, today: "2026-11-23", asOf: 1, live: true, name: "Saffron Mobile", spaceKey: "MOB", boardName: "Board", boardType: "scrum",
    scope: "sprint", sprint: null, pulseDays: 21, axis: [], lanes: LANES, speakers: [PRIYA, MARCUS, UNASSIGNED, ELENA], tickets, backlog: [], backlogTotal: 0,
    counts: { active: 8, unassigned: 1, done: 1, backlog: 0 },
    options: { rowsMode: "fit", showCommits: true, showPRs: true, showComments: true, shadeWeekends: true, doneWindow: "sprint", doneLabel: "this sprint", devActivity: "ok" },
    sync: { standupId: 1, runId: 0, state: "idle", mode: "", phase: "", done: 0, total: 0, lastSuccessAt: 0, lastAttemptAt: 0, lastStatus: "", warnings: [], errorCode: "", errorMessage: "", needsSync: false },
    warnings: [], previewKey: "", lastSyncedAt: 0,
  };
}

const TAB: StandupTab = { id: 1, rev: 1, name: "Saffron Mobile", spaceKey: "MOB", position: 0, activeCount: 8, speakerCount: 3, configured: true, needsSync: false, lastSyncedAt: 0 };

// ───────────────────────── harness ─────────────────────────
const realSize = { w: window.innerWidth, h: window.innerHeight };
function setWindow(w: number, h: number) {
  Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: w });
  Object.defineProperty(window, "innerHeight", { configurable: true, writable: true, value: h });
}
let viewCall: ReturnType<typeof vi.fn>;

/** Every facade method is a vi.fn resolving undefined, so real state functions never hit "x is not a function". */
function makeFakeApi(over: Record<string, Record<string, (...a: never[]) => unknown>>): Api {
  const fake: Record<string, Record<string, ReturnType<typeof vi.fn>>> = {};
  for (const [svc, methods] of Object.entries(wailsApi)) {
    fake[svc] = {};
    for (const m of Object.keys(methods)) fake[svc]![m] = vi.fn(async () => undefined);
  }
  for (const [svc, methods] of Object.entries(over)) for (const [m, impl] of Object.entries(methods)) fake[svc]![m] = vi.fn(impl as never);
  return fake as unknown as Api;
}

function mount(person: string | null, w = 1920, h = 1080) {
  setWindow(w, h);
  standups.value = [TAB];
  views.value = new Map([[1, makeView()]]);
  route.value = { view: "standup", standupId: 1, person };
  return render(<StandupScreen />);
}

beforeEach(() => {
  vi.useFakeTimers();
  viewCall = vi.fn(async () => makeView());
  setApi(
    makeFakeApi({
      standup: { view: viewCall as never, list: (async () => [TAB]) as never },
      sync: { start: (async () => 1) as never, status: (async () => []) as never },
    }),
  );
});
afterEach(() => {
  cleanup(); // unmount BEFORE resetting signals so the screen never re-renders against emptied state
  vi.clearAllTimers();
  vi.useRealTimers();
  setApi(wailsApi);
  setWindow(realSize.w, realSize.h);
  standups.value = [];
  views.value = new Map();
  syncStatus.value = new Map();
  route.value = { view: "connect" };
});

const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(64); });
const personView = () => screen.getByTestId("person-view");
const attr = (el: HTMLElement, name: string) => el.getAttribute(name);

describe("StandupScreen first-sync notice", () => {
  it("explains an empty, never-synced standup and how to sync it", () => {
    mount(null);
    expect(screen.getByText(/Not synced yet/)).toBeTruthy();
  });

  it("shows first-sync progress while that sync runs", () => {
    syncStatus.value = new Map([[1, { standupId: 1, state: "running", phase: "issues", done: 3, total: 9 } as never]]);
    mount(null);
    expect(screen.getByText(/First sync in progress \(issues 3\/9\)/)).toBeTruthy();
  });

  it("shows nothing once the standup has synced", () => {
    standups.value = [TAB];
    views.value = new Map([[1, { ...makeView(), lastSyncedAt: Date.now() }]]);
    route.value = { view: "standup", standupId: 1, person: null };
    render(<StandupScreen />);
    expect(screen.queryByText(/synced yet|First sync/)).toBeNull();
  });
});

describe("StandupScreen chooses the person or board view from the route", () => {
  it("shows the person view for a speaker, with their tickets grouped in walk order and Done last", () => {
    mount("p1");
    expect(attr(personView(), "data-person")).toBe("p1");
    expect(attr(personView(), "data-groups")).toBe("QA:2|In Review:1|In Progress:2|Done:1"); // empty To Do dropped
    expect(attr(personView(), "data-standup")).toBe("1");
    expect(screen.queryByTestId("board-view")).toBeNull();
  });

  it("shows the whole board when no person is selected", () => {
    mount(null);
    expect(attr(screen.getByTestId("board-view"), "data-standup")).toBe("1");
    expect(screen.queryByTestId("person-view")).toBeNull();
  });

  it("shows the Unassigned person view for 'unassigned'", () => {
    mount("unassigned");
    expect(attr(personView(), "data-person")).toBe("unassigned");
    expect(attr(personView(), "data-groups")).toBe("To Do:1");
  });

  it("opens the person view of an excluded speaker who has tickets", () => {
    mount("p4");
    expect(attr(personView(), "data-person")).toBe("p4");
    expect(attr(personView(), "data-groups")).toBe("In Progress:1");
  });

  it("falls back to the whole board when the person does not speak in this standup", () => {
    mount("ghost");
    expect(screen.getByTestId("board-view")).toBeTruthy();
    expect(screen.queryByTestId("person-view")).toBeNull();
  });

  it("puts the content in a main landmark", () => {
    mount("p1");
    const main = screen.getByRole("main");
    expect(within(main).getByTestId("person-view")).toBeTruthy();
  });
});

describe("StandupScreen layout props", () => {
  it.each([
    [1920, 1080, "full", 1648],
    [1440, 900, "full", 1192],
    [1280, 720, "compact", 1164],
    [1024, 640, "compact", 908],
  ])("%ix%i: rail %s, mainW %i for the person view", (w, h, railMode, mainW) => {
    mount("p1", w, h);
    expect(attr(screen.getByTestId("header"), "data-vw")).toBe(String(w));
    expect(attr(screen.getByTestId("rail"), "data-mode")).toBe(railMode);
    expect(attr(personView(), "data-mainw")).toBe(String(mainW));
    expect(attr(personView(), "data-vh")).toBe(String(h));
  });

  it.each([
    [1920, 1080, 1648],
    [1280, 720, 1164],
  ])("%ix%i: the board view gets the same mainW (%i) and vh", (w, h, mainW) => {
    mount(null, w, h);
    const board = screen.getByTestId("board-view");
    expect(attr(board, "data-mainw")).toBe(String(mainW));
    expect(attr(board, "data-vh")).toBe(String(h));
  });

  it("re-reads the window size on resize", async () => {
    mount("p1", 1920, 1080);
    expect(attr(screen.getByTestId("rail"), "data-mode")).toBe("full");
    setWindow(1200, 800);
    window.dispatchEvent(new Event("resize"));
    await flush();
    expect(attr(screen.getByTestId("rail"), "data-mode")).toBe("compact");
    expect(attr(screen.getByTestId("header"), "data-vw")).toBe("1200");
    expect(attr(personView(), "data-mainw")).toBe(String(1200 - 76 - 40));
  });
});

describe("StandupScreen rail wiring", () => {
  it("gives the rail the view's speakers unchanged and the selected person", () => {
    mount("p1");
    expect(attr(screen.getByTestId("rail"), "data-speakers")).toBe("p1,p2,unassigned,p4");
    expect(attr(screen.getByTestId("rail"), "data-selected")).toBe("p1");
  });

  it("selects nothing in the rail for the whole board", () => {
    mount(null);
    expect(attr(screen.getByTestId("rail"), "data-selected")).toBe("");
    expect(attr(screen.getByTestId("rail"), "data-speakers")).toBe("p1,p2,unassigned,p4");
  });

  it("switches to another speaker without calling Go", async () => {
    mount("p1");
    fireEvent.click(screen.getByRole("button", { name: "rail-pick-p2" }));
    await flush();
    expect(attr(personView(), "data-person")).toBe("p2");
    expect(attr(personView(), "data-groups")).toBe("In Progress:1");
    expect(attr(screen.getByTestId("rail"), "data-selected")).toBe("p2");
    expect(viewCall).not.toHaveBeenCalled();
  });

  it("goes to the whole board from the rail's Whole board button", async () => {
    mount("p1");
    fireEvent.click(screen.getByRole("button", { name: "rail-pick-whole-board" }));
    await flush();
    expect(screen.getByTestId("board-view")).toBeTruthy();
    expect(screen.queryByTestId("person-view")).toBeNull();
    expect(attr(screen.getByTestId("rail"), "data-selected")).toBe("");
    expect(viewCall).not.toHaveBeenCalled();
  });

  it("opens a person view when a board lane title is picked", async () => {
    mount(null);
    fireEvent.click(screen.getByRole("button", { name: "board-pick-p2" }));
    await flush();
    expect(attr(personView(), "data-person")).toBe("p2");
    expect(screen.queryByTestId("board-view")).toBeNull();
    expect(route.value.view === "standup" && route.value.person).toBe("p2");
  });
});
