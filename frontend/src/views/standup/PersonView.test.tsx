// WP-24 tests: PersonView (ux-spec §5, §10, AGENTS invariants 1 and 11, testing.md §4 golden density cases).
// PersonView is rendered with the REAL TicketRow, PulseTrack, TrackBackdrop, MoreBar and Legend (all WP-24) and the real
// layout functions (WP-13).
//
// SPEC-NOTE: DOM HOOKS used by this file (see PulseTrack.test.tsx / TicketRow.test.tsx for the track and row hooks):
//   - Title row:  <h1> = person.name; an element with the text "<active> active · <done> done"; when the density step is
//                 not "roomy", an element whose text is DensitySpec.label ("compact rows, side headings", ...).
//   - Group:      role="group" with aria-label = LaneInfo.label (printed unchanged), data-heading="stacked"|"side";
//                 inside it an <h2> whose text is "<label> <count>" (e.g. "QA 3"), then the TicketRows in order.
//   - Axis row:   an element with the text "last <pulseDays> days", and one [data-axis-label] per labelled day whose text
//                 is AxisDay.label. Every day is labelled when a day is >= 18px wide, else Mondays only.
//   - Backdrop:   [data-weekend] (one per weekend day) and [data-today-line="3"], see TrackBackdrop.test.tsx.
//   - More bar:   text "Scroll for more" + summary / "End of list" (MoreBar.test.tsx).
//   - Rows:       a TicketRow button's name contains its key; "ticket rows" below means buttons whose text includes "MOB-".
//
// SPEC-NOTE: the golden window sizes are expressed as `vh` with the mockup formula H = vh - 64 - padTop - titleH - 30 - 10:
//   vh 1080 → H 900, vh 900 → 720, vh 720 → 558, vh 640 → 478 (ux-spec §5.5 table). The 20-row fixture (QA 3, In Review 3,
//   In Progress 5, To Do 3, Done 6) gives exactly the documented outcomes.
// SPEC-NOTE: jsdom has no layout, so tests install scroll metrics (scrollHeight/clientHeight/scrollTop on every element)
//   and fire `scroll` on every element in the rendered tree; the component must (re)compute the more bar on scroll,
//   throttled to one animation frame (the tests advance fake timers by 64ms). Metrics are consistent with the golden
//   H (clientHeight = H; scrollHeight larger only when the density says it scrolls).
// SPEC-NOTE: the more bar is not shown unless the list scrolls; "End of list" appears at scrollTop + clientHeight >=
//   scrollHeight - 6 (ux-spec §5.6).
// SPEC-NOTE: a group with no tickets is not rendered even if the caller passes one ("only lanes that have tickets").
// SPEC-NOTE: lane labels pass through untouched: the fixture uses the mockup's "In Review" / "In Progress" / "To Do"
//   (bindings comments say "Review"/"In progress"/"To do"); the golden summary "2 To Do · 6 Done below" uses "To Do".
// SPEC-NOTE: PersonView opens issues itself through the api facade (api().app.openIssue(key)); it has no onOpen prop.
// SPEC-NOTE: keyCh passed to every row is the longest key in the WHOLE view (view.tickets), not just this person's.
import { act, fireEvent, render, screen, within } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi } from "../../api";
import type { Api } from "../../api";
import type { AxisDay, LaneInfo, Pulse, SpeakerView, StandupView, TicketView, ViewOptions } from "../../api/types";
import type { TicketGroup } from "../../state/standup";
import { PersonView } from "./PersonView";
import type { PersonViewProps } from "./props";

// ───────────────────────── fixture ─────────────────────────
const AXIS: AxisDay[] = Array.from({ length: 21 }, (_, i) => {
  const d = new Date(Date.UTC(2026, 10, 3 + i)); // Tue 3 Nov .. Mon 23 Nov; Mondays 9, 16, 23
  const dow = d.getUTCDay();
  return { date: d.toISOString().slice(0, 10), label: String(d.getUTCDate()), weekend: dow === 0 || dow === 6, monday: dow === 1 };
});

const L = (lane: string, label: string, limitDays: number): LaneInfo => ({ lane, label, limitDays, columns: [label] });
const LANE_QA = L("qa", "QA", 3);
const LANE_REVIEW = L("review", "In Review", 2);
const LANE_PROG = L("prog", "In Progress", 5);
const LANE_TODO = L("todo", "To Do", 10);
const LANE_DONE = L("done", "Done", 0);
const LANES = [LANE_QA, LANE_REVIEW, LANE_PROG, LANE_TODO, LANE_DONE];

const OPTIONS: ViewOptions = {
  rowsMode: "fit",
  showCommits: true,
  showPRs: true,
  showComments: true,
  shadeWeekends: true,
  doneWindow: "sprint",
  doneLabel: "this sprint",
  devActivity: "ok",
};

function pulse(key: string): Pulse {
  return {
    segs: [
      { lane: "todo", from: 0, to: 9 },
      { lane: "prog", from: 9, to: 21 },
    ],
    marks: [
      { day: 12, commits: 3, prs: 1, comments: 0 },
      { day: 16, commits: 2, prs: 0, comments: 1 },
    ],
    blocked: [],
    label: `pulse of ${key}`,
  };
}

function ticket(key: string, lane: string, assignee: string, over: Partial<TicketView> = {}): TicketView {
  return {
    id: Number(key.replace(/\D/g, "")),
    sourceId: key.replace(/\D/g, ""),
    key,
    summary: `Summary of ${key}`,
    url: `https://example.atlassian.net/browse/${key}`,
    type: "story",
    assignee,
    lane,
    statusName: lane,
    daysInLane: 2,
    overLimit: false,
    blocked: false,
    doneAt: lane === "done" ? 1_763_800_000_000 : 0,
    pulse: pulse(key),
    ...over,
  };
}

const keys = (prefix: number, n: number) => Array.from({ length: n }, (_, i) => `MOB-${prefix + i}`);
const QA_KEYS = ["MOB-1390", "MOB-1399", "MOB-1407"];
const REVIEW_KEYS = ["MOB-1411", "MOB-1413", "MOB-1414"];
const PROG_KEYS = keys(1420, 5);
const TODO_KEYS = keys(1430, 3);
const DONE_KEYS = keys(1440, 6);

const PRIYA: SpeakerView = { id: "p1", name: "Iris Calloway", first: "Iris", initials: "IC", unassigned: false, included: true, active: 14, done: 6 };
const MARCUS: SpeakerView = { id: "p2", name: "Silas Marchetti", first: "Silas", initials: "SM", unassigned: false, included: true, active: 1, done: 0 };
const UNASSIGNED: SpeakerView = { id: "unassigned", name: "Unassigned", first: "Unassigned", initials: "··", unassigned: true, included: true, active: 4, done: 0 };

function group(lane: LaneInfo, laneKeys: string[], assignee = "p1", over: (k: string, i: number) => Partial<TicketView> = () => ({})): TicketGroup {
  return { lane, tickets: laneKeys.map((k, i) => ticket(k, lane.lane, assignee, over(k, i))) };
}

function irisGroups(): TicketGroup[] {
  return [
    group(LANE_QA, QA_KEYS, "p1", (k) => (k === "MOB-1407" ? { daysInLane: 9, overLimit: true } : {})),
    group(LANE_REVIEW, REVIEW_KEYS, "p1", (k) => (k === "MOB-1414" ? { blocked: true, daysInLane: 4 } : {})),
    group(LANE_PROG, PROG_KEYS),
    group(LANE_TODO, TODO_KEYS),
    group(LANE_DONE, DONE_KEYS),
  ];
}

function viewFor(groups: TicketGroup[], over: Partial<StandupView> = {}, extra: TicketView[] = []): StandupView {
  const tickets = [...groups.flatMap((g) => g.tickets), ...extra];
  return {
    standupId: 1,
    rev: 1,
    generatedAt: 1_763_900_000_000,
    today: "2026-11-23",
    asOf: 1_763_900_000_000,
    live: true,
    name: "Saffron Mobile",
    spaceKey: "MOB",
    boardName: "MOB board",
    boardType: "scrum",
    scope: "sprint",
    sprint: null,
    pulseDays: 21,
    axis: AXIS,
    lanes: LANES,
    speakers: [PRIYA, MARCUS, UNASSIGNED],
    tickets,
    backlog: [],
    backlogTotal: 0,
    counts: { active: 14, unassigned: 4, done: 6, backlog: 0 },
    options: OPTIONS,
    sync: {
      standupId: 1, runId: 0, state: "idle", mode: "", phase: "", done: 0, total: 0, lastSuccessAt: 0, lastAttemptAt: 0,
      lastStatus: "", warnings: [], errorCode: "", errorMessage: "", needsSync: false,
    },
    warnings: [],
    previewKey: "",
    lastSyncedAt: 0,
    ...over,
  };
}

// The other person's ticket makes the longest key in the view (10 chars) longer than any of Iris's (8).
const OTHER = ticket("MOB-123456", "prog", "p2");

function props(over: Partial<PersonViewProps> = {}): PersonViewProps {
  const groups = over.groups ?? irisGroups();
  return { view: viewFor(groups, {}, [OTHER]), person: PRIYA, groups, mainW: 1656, vh: 1080, ...over };
}

// ───────────────────────── harness ─────────────────────────
let openIssue: ReturnType<typeof vi.fn>;

beforeEach(() => {
  openIssue = vi.fn(async () => {});
  setApi({ app: { openIssue } } as unknown as Api);
});
afterEach(() => {
  setApi(wailsApi);
  restoreScroll();
  vi.useRealTimers();
});

const metrics = { scrollHeight: 0, clientHeight: 0, scrollTop: 0 };
function installScroll(m: Partial<typeof metrics>) {
  Object.assign(metrics, { scrollHeight: 0, clientHeight: 0, scrollTop: 0 }, m);
  Object.defineProperty(HTMLElement.prototype, "scrollHeight", { configurable: true, get: () => metrics.scrollHeight });
  Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => metrics.clientHeight });
  Object.defineProperty(HTMLElement.prototype, "scrollTop", { configurable: true, get: () => metrics.scrollTop, set: (v: number) => void (metrics.scrollTop = v) });
}
function restoreScroll() {
  for (const k of ["scrollHeight", "clientHeight", "scrollTop"]) delete (HTMLElement.prototype as unknown as Record<string, unknown>)[k];
}
/** Golden heights: [vh, H, scrolls]. */
function scrollFor(vh: number, H: number, scrolls: boolean) {
  void vh;
  installScroll({ clientHeight: H, scrollHeight: scrolls ? 2000 : H, scrollTop: 0 });
}
async function scrollTo(container: Element, top: number) {
  metrics.scrollTop = top;
  for (const el of Array.from(container.querySelectorAll("*"))) fireEvent.scroll(el);
  fireEvent.scroll(container);
  await act(async () => {
    vi.advanceTimersByTime(64);
  });
}
function useFrameTimers() {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "requestAnimationFrame", "cancelAnimationFrame", "Date"] });
}

const ticketRows = () => screen.queryAllByRole("button").filter((b) => (b.textContent ?? "").includes("MOB-"));
const rowKey = (b: HTMLElement) => /MOB-\d+/.exec(b.textContent ?? "")?.[0] ?? "";
function rowFor(key: string): HTMLElement {
  const found = ticketRows().find((b) => rowKey(b) === key);
  expect(found, `a ticket row for ${key}`).toBeDefined();
  return found as HTMLElement;
}
const track = (key: string) => screen.getByRole("img", { name: `pulse of ${key}` });
const groupEl = (name: string) => screen.getByRole("group", { name });
const LANE_LABELS = ["QA", "In Review", "In Progress", "To Do", "Done"];
const laneGroups = () => screen.queryAllByRole("group").filter((g) => LANE_LABELS.includes(g.getAttribute("aria-label") ?? ""));
const tracksShown = () => screen.queryAllByRole("img").filter((i) => (i.getAttribute("aria-label") ?? "").startsWith("pulse of "));
const AXE = { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } };

// ───────────────────────── tests ─────────────────────────
describe("PersonView title row", () => {
  it("shows the person's name as the h1 and the active/done counts", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props()} />);
    expect(screen.getByRole("heading", { level: 1, name: "Iris Calloway" })).toBeTruthy();
    expect(screen.getByText("14 active · 6 done")).toBeTruthy();
  });

  it("titles the unassigned person view 'Unassigned' with the same rows", () => {
    scrollFor(1080, 900, false);
    const groups = [group(LANE_PROG, keys(1500, 2), ""), group(LANE_TODO, keys(1510, 2), "")];
    render(<PersonView {...props({ groups, person: UNASSIGNED })} />);
    expect(screen.getByRole("heading", { level: 1, name: "Unassigned" })).toBeTruthy();
    expect(screen.getByText("4 active · 0 done")).toBeTruthy();
    expect(ticketRows().map(rowKey)).toEqual([...keys(1500, 2), ...keys(1510, 2)]);
  });

  it("names the 21-day window in the axis row", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props()} />);
    expect(screen.getByText("last 21 days")).toBeTruthy();
  });
});

describe("PersonView groups", () => {
  it("renders groups in the order given: walk order, Done last, with lane labels unchanged", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props()} />);
    expect(laneGroups().map((g) => g.getAttribute("aria-label"))).toEqual(["QA", "In Review", "In Progress", "To Do", "Done"]);
  });

  it("heads each group with its label and ticket count", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props()} />);
    const expected: [string, string][] = [["QA", "3"], ["In Review", "3"], ["In Progress", "5"], ["To Do", "3"], ["Done", "6"]];
    for (const [label, n] of expected) {
      const heading = within(groupEl(label)).getByRole("heading", { level: 2 });
      expect((heading.textContent ?? "").replace(/\s+/g, " ").trim().replace(/ /g, "")).toBe(`${label.replace(/ /g, "")}${n}`);
    }
  });

  it("keeps each group's tickets in order inside that group", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props()} />);
    const inGroup = (label: string) => within(groupEl(label)).queryAllByRole("button").map(rowKey);
    expect(inGroup("QA")).toEqual(QA_KEYS);
    expect(inGroup("In Review")).toEqual(REVIEW_KEYS);
    expect(inGroup("In Progress")).toEqual(PROG_KEYS);
    expect(inGroup("To Do")).toEqual(TODO_KEYS);
    expect(inGroup("Done")).toEqual(DONE_KEYS);
  });

  it("does not render a group that has no tickets", () => {
    scrollFor(1080, 900, false);
    const groups = [group(LANE_QA, QA_KEYS), { lane: LANE_REVIEW, tickets: [] }, group(LANE_DONE, DONE_KEYS)];
    render(<PersonView {...props({ groups })} />);
    expect(laneGroups().map((g) => g.getAttribute("aria-label"))).toEqual(["QA", "Done"]);
  });

  it("prints a renamed lane exactly as supplied", () => {
    scrollFor(1080, 900, false);
    const groups = [group({ ...LANE_PROG, label: "Doing" }, PROG_KEYS)];
    render(<PersonView {...props({ groups })} />);
    expect(groupEl("Doing")).toBeTruthy();
  });

  it("shows the empty state when the person has no tickets", () => {
    scrollFor(1080, 900, false);
    const none: SpeakerView = { ...MARCUS, active: 0, done: 0 };
    render(<PersonView {...props({ groups: [], person: none })} />);
    expect(screen.getByRole("heading", { level: 1, name: "Silas Marchetti" })).toBeTruthy();
    expect(screen.getByText("Nothing on the board for this person.")).toBeTruthy();
    expect(ticketRows()).toHaveLength(0);
    expect(screen.queryByText(/Scroll for more/)).toBeNull();
  });
});

describe("PersonView density ladder (testing.md §4 golden cases)", () => {
  const ROW_H = (): number[] => ticketRows().map((b) => parseInt(b.style.height, 10));

  it("1920x1080 (H 900): compact-side, 38px rows, side headings, tag shown, no scroll", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props({ vh: 1080 })} />);
    expect(screen.getByText("compact rows, side headings")).toBeTruthy();
    expect(ticketRows()).toHaveLength(20);
    expect(new Set(ROW_H())).toEqual(new Set([38]));
    for (const g of laneGroups()) expect(g.getAttribute("data-heading")).toBe("side");
    expect(screen.queryByText(/Scroll for more/)).toBeNull();
    expect(screen.queryByText(/End of list/)).toBeNull();
  });

  it("1440x900 (H 720): tight-side, 34px rows, no scroll", () => {
    scrollFor(900, 720, false);
    render(<PersonView {...props({ vh: 900, mainW: 1192 })} />);
    expect(screen.getByText("tight rows, side headings")).toBeTruthy();
    expect(ticketRows()).toHaveLength(20);
    expect(new Set(ROW_H())).toEqual(new Set([34]));
    for (const g of laneGroups()) expect(g.getAttribute("data-heading")).toBe("side");
    expect(screen.queryByText(/Scroll for more/)).toBeNull();
  });

  it("1280x720 (H 558): tight-side and scrolls, the more bar says what is below", () => {
    scrollFor(720, 558, true);
    render(<PersonView {...props({ vh: 720, mainW: 1164 })} />);
    expect(screen.getByText("tight rows, side headings")).toBeTruthy();
    expect(new Set(ROW_H())).toEqual(new Set([34]));
    expect(screen.getByText(/Scroll for more/)).toBeTruthy();
    expect(screen.getByText("6 Done below")).toBeTruthy(); // fold 522: only the Done rows end past it
  });

  it("1024x640 (H 478): tight-side and scrolls; more bar reads '2 To Do · 6 Done below'", () => {
    scrollFor(640, 478, true);
    render(<PersonView {...props({ vh: 640, mainW: 908 })} />);
    expect(screen.getByText("tight rows, side headings")).toBeTruthy();
    expect(screen.getByText(/Scroll for more/)).toBeTruthy();
    expect(screen.getByText("2 To Do · 6 Done below")).toBeTruthy();
  });

  it("Rows set to Always roomy (H 900): roomy step, 46px rows, stacked headings, no tag, scrolls", () => {
    scrollFor(1080, 900, true);
    const groups = irisGroups();
    const view = viewFor(groups, { options: { ...OPTIONS, rowsMode: "roomy" } }, [OTHER]);
    render(<PersonView {...props({ view, groups, vh: 1080 })} />);
    expect(new Set(ROW_H())).toEqual(new Set([46]));
    for (const g of laneGroups()) expect(g.getAttribute("data-heading")).toBe("stacked");
    expect(screen.queryByText(/\brows\b/i)).toBeNull(); // step 1 shows no density tag
    expect(screen.getByText(/Scroll for more/)).toBeTruthy();
  });
});

describe("PersonView never hides tickets (AGENTS invariant 1)", () => {
  it("renders every one of the 20 tickets exactly once at 1024x640", () => {
    scrollFor(640, 478, true);
    render(<PersonView {...props({ vh: 640, mainW: 908 })} />);
    const shown = ticketRows().map(rowKey);
    expect(shown).toHaveLength(20);
    expect(new Set(shown)).toEqual(new Set([...QA_KEYS, ...REVIEW_KEYS, ...PROG_KEYS, ...TODO_KEYS, ...DONE_KEYS]));
  });

  it("renders all 60 tickets of a heavily loaded person when the list must scroll", () => {
    scrollFor(640, 478, true);
    const groups = [
      group(LANE_PROG, keys(2000, 20)),
      group(LANE_TODO, keys(2100, 20)),
      group(LANE_DONE, keys(2200, 20)),
    ];
    render(<PersonView {...props({ groups, vh: 640, mainW: 908 })} />);
    expect(ticketRows()).toHaveLength(60);
    expect(screen.getByText(/Scroll for more/)).toBeTruthy();
    expect(within(groupEl("Done")).queryAllByRole("button")).toHaveLength(20);
  });

  it("renders every done ticket as a row, never as a count or chip", () => {
    scrollFor(640, 478, true);
    render(<PersonView {...props({ vh: 640, mainW: 908 })} />);
    expect(within(groupEl("Done")).queryAllByRole("button").map(rowKey)).toEqual(DONE_KEYS);
  });
});

describe("PersonView more bar scroll behaviour", () => {
  beforeEach(() => useFrameTimers());

  it("recomputes the summary on scroll and shows 'End of list' at the end", async () => {
    scrollFor(640, 478, true);
    const { container } = render(<PersonView {...props({ vh: 640, mainW: 908 })} />);
    expect(screen.getByText("2 To Do · 6 Done below")).toBeTruthy();

    await scrollTo(container, 100); // everything shifts up 100px; only the Done rows end past the 442px fold
    expect(screen.getByText("6 Done below")).toBeTruthy();
    expect(screen.queryByText("2 To Do · 6 Done below")).toBeNull();

    await scrollTo(container, 2000 - 478); // scrollTop + clientHeight >= scrollHeight - 6
    expect(screen.getByText(/End of list/)).toBeTruthy();
    expect(screen.getByText("every ticket is shown above")).toBeTruthy();
    expect(screen.queryByText(/Scroll for more/)).toBeNull();

    await scrollTo(container, 0);
    expect(screen.getByText(/Scroll for more/)).toBeTruthy();
    expect(screen.getByText("2 To Do · 6 Done below")).toBeTruthy();
  });

  it("treats scrollTop + clientHeight within 6px of scrollHeight as the end", async () => {
    scrollFor(640, 478, true);
    const { container } = render(<PersonView {...props({ vh: 640, mainW: 908 })} />);
    await scrollTo(container, 2000 - 478 - 6);
    expect(screen.getByText(/End of list/)).toBeTruthy();
    await scrollTo(container, 2000 - 478 - 7);
    expect(screen.getByText(/Scroll for more/)).toBeTruthy();
  });
});

describe("PersonView legend by width", () => {
  it("mainW 1656: full legend", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props({ mainW: 1656 })} />);
    expect(screen.getByRole("heading", { level: 1 })).toBeTruthy(); // anchor
    expect(screen.queryAllByText(/over limit/i).length).toBeGreaterThan(0);
    expect(screen.queryAllByText(/commit/i).length).toBeGreaterThan(0);
  });

  it("mainW 1400: short legend (lanes, blocked, over limit; no commit/PR/comment)", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props({ mainW: 1400 })} />);
    expect(screen.queryAllByText(/over limit/i).length).toBeGreaterThan(0);
    expect(screen.queryAllByText(/commit/i)).toHaveLength(0);
  });

  it("mainW 1000: a Key button instead of the legend", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props({ mainW: 1000 })} />);
    expect(screen.getByRole("button", { name: /key/i })).toBeTruthy();
    expect(screen.queryAllByText(/over limit/i)).toHaveLength(0);
  });
});

describe("PersonView pulse tracks", () => {
  it.each([
    [1656, 600],
    [1200, 440],
    [900, 300],
  ])("mainW %i gives %ipx tracks", (mainW, trackW) => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props({ mainW })} />);
    const tracks = tracksShown();
    expect(tracks).toHaveLength(20);
    for (const t of tracks) expect(t.style.width).toBe(`${trackW}px`);
  });

  it("sizes every key to the longest key in the whole view (10 chars here, not Iris's 8)", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props()} />);
    const key = screen.getByText("MOB-1390");
    expect(key.getAttribute("style") ?? "").toMatch(/10ch/);
    const another = screen.getByText("MOB-1445");
    expect(another.getAttribute("style") ?? "").toMatch(/10ch/);
  });

  it("draws marks on active rows but none on Done rows", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props()} />);
    expect(track("MOB-1390").querySelectorAll("[data-mark]").length).toBeGreaterThan(0);
    for (const k of DONE_KEYS) expect(track(k).querySelectorAll("[data-mark]")).toHaveLength(0);
  });

  it("honours the view's mark toggles: comments off", () => {
    scrollFor(1080, 900, false);
    const groups = irisGroups();
    const on = render(<PersonView {...props({ view: viewFor(groups, {}, [OTHER]), groups })} />);
    expect(on.container.querySelectorAll('[data-mark="comment"]').length).toBeGreaterThan(0);
    on.unmount();
    const view = viewFor(groups, { options: { ...OPTIONS, showComments: false } }, [OTHER]);
    const off = render(<PersonView {...props({ view, groups })} />);
    expect(tracksShown()).toHaveLength(20); // anchor
    expect(off.container.querySelectorAll('[data-mark="comment"]')).toHaveLength(0);
    expect(off.container.querySelectorAll('[data-mark="commit"]').length).toBeGreaterThan(0);
  });

  it("omits commit and PR marks when dev activity is unavailable", () => {
    scrollFor(1080, 900, false);
    const groups = irisGroups();
    const view = viewFor(groups, { options: { ...OPTIONS, devActivity: "unavailable" } }, [OTHER]);
    const { container } = render(<PersonView {...props({ view, groups })} />);
    expect(container.querySelectorAll('[data-mark="comment"]').length).toBeGreaterThan(0);
    expect(container.querySelectorAll('[data-mark="commit"]')).toHaveLength(0);
    expect(container.querySelectorAll('[data-mark="pr"]')).toHaveLength(0);
  });

  it("draws the 3px today line, and weekend shading only when enabled", () => {
    scrollFor(1080, 900, false);
    const on = render(<PersonView {...props()} />);
    const lines = on.container.querySelectorAll("[data-today-line]");
    expect(lines.length).toBeGreaterThan(0);
    for (const l of Array.from(lines)) expect(l.getAttribute("data-today-line")).toBe("3");
    expect(on.container.querySelectorAll("[data-weekend]").length).toBeGreaterThan(0);
    on.unmount();
    const groups = irisGroups();
    const view = viewFor(groups, { options: { ...OPTIONS, shadeWeekends: false } }, [OTHER]);
    const off = render(<PersonView {...props({ view, groups })} />);
    expect(off.container.querySelectorAll("[data-today-line]").length).toBeGreaterThan(0); // anchor
    expect(off.container.querySelectorAll("[data-weekend]")).toHaveLength(0);
  });
});

describe("PersonView axis row", () => {
  const labels = (c: Element) => Array.from(c.querySelectorAll("[data-axis-label]")).map((e) => e.textContent);

  it("labels every day when a day is at least 18px wide (440px / 21 days)", () => {
    scrollFor(1080, 900, false);
    const { container } = render(<PersonView {...props({ mainW: 1200 })} />);
    expect(labels(container)).toEqual(AXIS.map((d) => d.label));
  });

  it("labels Mondays only when a day is narrower than 18px (300px / 21 days)", () => {
    scrollFor(1080, 900, false);
    const { container } = render(<PersonView {...props({ mainW: 900 })} />);
    expect(labels(container)).toEqual(["9", "16", "23"]);
  });
});

describe("PersonView opens issues", () => {
  it("clicking a row calls OpenIssue with its key", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props()} />);
    const row = rowFor("MOB-1399");
    fireEvent.click(row);
    expect(openIssue).toHaveBeenCalledTimes(1);
    expect(openIssue).toHaveBeenCalledWith("MOB-1399");
  });

  it("Enter on a focused row calls OpenIssue once", async () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props()} />);
    const user = userEvent.setup();
    const row = rowFor("MOB-1411");
    row.focus();
    await user.keyboard("{Enter}");
    expect(openIssue).toHaveBeenCalledTimes(1);
    expect(openIssue).toHaveBeenCalledWith("MOB-1411");
  });

  it("flags blocked and over-limit rows with BLOCKED and alert chips", () => {
    scrollFor(1080, 900, false);
    render(<PersonView {...props()} />);
    const blocked = rowFor("MOB-1414");
    expect(within(blocked).getByText("BLOCKED")).toBeTruthy();
    expect(within(blocked).getByText("4d").getAttribute("data-alert")).toBe("true");
    const over = rowFor("MOB-1407");
    expect(within(over).getByText("9d").getAttribute("data-alert")).toBe("true");
    expect(within(over).queryByText("BLOCKED")).toBeNull();
  });
});

describe("PersonView tokens and accessibility", () => {
  it("uses no inline colours anywhere", () => {
    scrollFor(1080, 900, false);
    const { container } = render(<PersonView {...props()} />);
    expect(screen.getByRole("heading", { level: 1 })).toBeTruthy(); // anchor
    const styled = Array.from(container.querySelectorAll("[style]"));
    expect(styled.length).toBeGreaterThan(0);
    for (const el of styled) expect(el.getAttribute("style")).not.toMatch(/#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(/i);
  });

  it("has no axe violations (fits, side headings)", async () => {
    scrollFor(1080, 900, false);
    const { container } = render(<PersonView {...props()} />);
    expect(screen.getByRole("heading", { level: 1 })).toBeTruthy(); // anchor
    expect((await axe.run(container, AXE)).violations.map((v) => v.id)).toEqual([]);
  });

  it("has no axe violations (scrolling, with the more bar and the Key button)", async () => {
    scrollFor(640, 478, true);
    const { container } = render(<PersonView {...props({ vh: 640, mainW: 908 })} />);
    expect(screen.getByText(/Scroll for more/)).toBeTruthy(); // anchor
    expect((await axe.run(container, AXE)).violations.map((v) => v.id)).toEqual([]);
  });

  it("has no axe violations (stacked headings, empty state)", async () => {
    scrollFor(1080, 900, true);
    const groups = irisGroups();
    const view = viewFor(groups, { options: { ...OPTIONS, rowsMode: "roomy" } }, [OTHER]);
    const roomy = render(<PersonView {...props({ view, groups })} />);
    expect(laneGroups()).toHaveLength(5); // anchor
    expect((await axe.run(roomy.container, AXE)).violations.map((v) => v.id)).toEqual([]);
    roomy.unmount();
    const empty = render(<PersonView {...props({ groups: [] })} />);
    expect(screen.getByText("Nothing on the board for this person.")).toBeTruthy();
    expect((await axe.run(empty.container, AXE)).violations.map((v) => v.id)).toEqual([]);
  });
});
