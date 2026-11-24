// WP-T9 tests: the ticket modal's step lists and the row-to-modal opener (testing §4 "Stepping order", ux-spec §14.1).
//   Person view: the person's tickets in walk order (view.lanes), board rank inside a lane, then Done.
//   Board: lanes top to bottom (Unassigned first, then speakers, each lane-by-lane), then Done, then the backlog.
//   Opening makes no facade call (except the browser fallback for a backlog row without a local issue).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi, type Api } from "../api";
import type { BacklogItem, SpeakerView, StandupView, TicketView } from "../api/types";
import { route } from "./route";
import { buildBoardLanes, views } from "./standup";
import { closeTicket, ticketSession } from "./ticket";
import { boardSteps, issueIdOfKey, openTicketFromRow, personSteps, stepsFor } from "./ticketSteps";

const LANES = ["qa", "review", "prog", "todo", "done"].map((lane) => ({ lane, label: lane, limitDays: 0, columns: [] }));

const ticket = (id: number, lane: string, assignee: string): TicketView =>
  ({ id, key: `MOB-${id}`, summary: `t${id}`, url: "", type: "story", assignee, lane, statusName: lane, daysInLane: 1, overLimit: false, blocked: false, doneAt: 0 }) as unknown as TicketView;
const speaker = (id: string, over: Partial<SpeakerView> = {}): SpeakerView =>
  ({ id, name: id, first: id, initials: id.slice(0, 2), unassigned: id === "unassigned", included: true, active: 0, done: 0, ...over }) as SpeakerView;
const backlog = (issueId: number): BacklogItem => ({ issueId, key: `MOB-${issueId}`, summary: "b", type: "task" }) as unknown as BacklogItem;

// Rank order in view.tickets: 1 (ana todo), 2 (ana qa), 3 (bo prog), 4 (ana prog), 5 (ana done), 6 (nobody todo), 7 (bo done), 8 (ana review)
const VIEW = {
  standupId: 1, rev: 1, name: "S", lanes: LANES, axis: [], warnings: [],
  speakers: [speaker("unassigned"), speaker("ana"), speaker("bo")],
  tickets: [ticket(1, "todo", "ana"), ticket(2, "qa", "ana"), ticket(3, "prog", "bo"), ticket(4, "prog", "ana"), ticket(5, "done", "ana"), ticket(6, "todo", ""), ticket(7, "done", "bo"), ticket(8, "review", "ana")],
  backlog: [backlog(90), backlog(0), backlog(91)],
} as unknown as StandupView;

let openIssue: ReturnType<typeof vi.fn>;
beforeEach(() => {
  openIssue = vi.fn(async () => undefined);
  setApi({ app: { openIssue } } as unknown as Api);
  views.value = new Map([[1, VIEW]]);
  route.value = { view: "standup", standupId: 1, person: null };
  ticketSession.value = null;
});
afterEach(() => {
  closeTicket();
  setApi(wailsApi);
});

describe("personSteps", () => {
  it("walks the lanes of view.lanes (QA first here) then Done, rank order inside a lane", () => {
    expect(personSteps(VIEW, "ana")).toEqual([2, 8, 4, 1, 5]);
    expect(personSteps(VIEW, "bo")).toEqual([3, 7]);
  });
  it("Unassigned is a person whose tickets have no assignee", () => {
    expect(personSteps(VIEW, "unassigned")).toEqual([6]);
  });
  it("a person with nothing has no steps", () => {
    expect(personSteps(VIEW, "nobody")).toEqual([]);
  });
  it("follows a custom walk order", () => {
    const flipped = { ...VIEW, lanes: [LANES[3]!, LANES[2]!, LANES[1]!, LANES[0]!, LANES[4]!] } as StandupView;
    expect(personSteps(flipped, "ana")).toEqual([1, 4, 8, 2, 5]);
  });
});

describe("boardSteps", () => {
  it("lists the lanes (Unassigned first), then every Done ticket, then the backlog items that have an id", () => {
    expect(buildBoardLanes(VIEW).map((l) => l.speaker.id)).toEqual(["unassigned", "ana", "bo"]);
    expect(boardSteps(VIEW)).toEqual([6, 2, 8, 4, 1, 3, 5, 7, 90, 91]);
  });
  it("never lists an issue twice", () => {
    const dup = { ...VIEW, backlog: [backlog(3), backlog(90)] } as StandupView;
    const steps = boardSteps(dup);
    expect(new Set(steps).size).toBe(steps.length);
  });
  it("stepsFor picks the person list or the board list", () => {
    expect(stepsFor(VIEW, "bo")).toEqual([3, 7]);
    expect(stepsFor(VIEW, null)).toEqual(boardSteps(VIEW));
  });
});

describe("openTicketFromRow", () => {
  it("opens the person's modal with the person's steps and calls nothing", () => {
    route.value = { view: "standup", standupId: 1, person: "ana" };
    openTicketFromRow("MOB-4");
    expect(ticketSession.value).toEqual({ standupId: 1, issueId: 4, steps: [2, 8, 4, 1, 5], stack: [] });
    expect(openIssue).not.toHaveBeenCalled();
  });
  it("opens from the whole board with the board's steps (Done and backlog rows included)", () => {
    openTicketFromRow("MOB-7");
    expect(ticketSession.value?.issueId).toBe(7);
    expect(ticketSession.value?.steps).toEqual(boardSteps(VIEW));
    openTicketFromRow("MOB-91");
    expect(ticketSession.value?.issueId).toBe(91);
  });
  it("a backlog row without a local issue falls back to the browser", () => {
    expect(issueIdOfKey(VIEW, "MOB-0")).toBe(0);
    openTicketFromRow("MOB-0");
    expect(ticketSession.value).toBeNull();
    expect(openIssue).toHaveBeenCalledWith("MOB-0");
  });
  it("does nothing off the standup screen", () => {
    route.value = { view: "configure", standupId: 1 };
    openTicketFromRow("MOB-4");
    expect(ticketSession.value).toBeNull();
  });
});
