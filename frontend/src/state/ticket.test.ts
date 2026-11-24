// WP-T7 tests: ticket modal state, part 1 (state/ticket.ts: session, stepping, back stack, loading, live updates).
// Companions: ticket.writes.test.ts, ticket.refresh.test.ts, ticket.draft.test.ts. Spec: ux-spec §14.1, §14.9,
// architecture §4.9, testing §4 "Ticket modal (Vitest)".
//
// SPEC-NOTE: THE EXPORTS OF state/ticket.ts ARE THE TEST AUTHOR'S INVENTION (route.ts is frozen and has no
//   `route.ticket`, although architecture §4.9 mentions one). The surface pinned by these four files is the stub
//   `state/ticket.ts` committed with the tests: ticketSession, openTicket, closeTicket, stepTicket, stepToTicket,
//   openLinkedTicket, backTicket, ticketPosition, ticketDetail, ticketError, ticketWrites, ticketRefresh, ticketUpdated,
//   ticketPending, ticketDraft, loadTicket, reloadTicket, resetTicketState, applyTransition, applyAssign, applyFlag,
//   retryWrite, loadTransitions, loadAssignees, refreshTicket, editDraft, flushDraft, sendComment, retryComment,
//   editComment, discardComment. WP-T9 wires rows and keys to them; WP-T7's TicketModal reads them.
// SPEC-NOTE: openTicket / stepping / the back stack are pure session changes: they make NO facade call. The detail is
//   loaded by `loadTicket(standupId, issueId)` (the modal calls it whenever its issueId prop changes); that makes the
//   issue "active" and calls only TicketService.Get. Neighbour prefetch (architecture §4.9) is allowed but not asserted:
//   tests only require that every facade call made while opening is `ticket.get`.
// SPEC-NOTE: stepping is relative to the open issue's index in `steps` (so after an inline-card hop the index of the
//   linked issue is used when it is in `steps`; when it is not, stepTicket is a no-op returning false). It stops at both
//   ends (no wrap) and clears the back stack. The speaker never changes (state/standup is untouched).
// SPEC-NOTE: `ticket:updated` handling follows ux-spec §14.9 over the brief's looser "live update": an event with
//   cause "sync" for the active issue only records the changed part names in `ticketUpdated` (union, first-seen order;
//   an empty `changed` records nothing). The content is NOT re-fetched and the detail is NOT replaced until
//   `reloadTicket()`. cause "write" (our own edit, already applied from the call's result) and events for other issues
//   are ignored. Switching issue clears the list.
// SPEC-NOTE: the people combobox of the field rail loads through `ticket.searchMentions(standupId, query)` (local people
//   first, architecture §3.2); `loadTransitions` calls `ticket.transitions(issueId)`.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../api/errors";
import { setEventBus } from "../api/events";
import { setApi } from "../api/index";
import { createMockApi, type MockApi } from "../api/mock";
import type { TicketDetail } from "../api/types";
import { views } from "./standup";
import * as S from "./ticket";

const STEPS = Array.from({ length: 20 }, (_, i) => 101 + i); // MOB-1390 … MOB-1497; 107 is MOB-1421 (7 / 20)

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}
const clone = <X>(x: X): X => JSON.parse(JSON.stringify(x)) as X;

let mock: MockApi;
beforeEach(async () => {
  S.resetTicketState();
  mock = createMockApi();
  setApi(mock);
  setEventBus(mock.bus);
  views.value = new Map([[1, clone(await mock.standup.view(1))]]);
  mock.resetCalls();
});
afterEach(() => {
  S.resetTicketState();
  vi.useRealTimers();
});

const session = () => S.ticketSession.value;

describe("session: open, close", () => {
  it("openTicket_SetsTheSessionWithAnEmptyStack_AndMakesNoFacadeCall", () => {
    S.openTicket(1, 107, STEPS);
    expect(session()).toEqual({ standupId: 1, issueId: 107, steps: STEPS, stack: [] });
    expect(mock.calls).toEqual([]);
  });

  it("closeTicket_ClearsTheSession", () => {
    S.openTicket(1, 107, STEPS);
    expect(session()?.issueId).toBe(107);
    S.closeTicket();
    expect(session()).toBeNull();
  });
});

describe("session: stepping (J/K, ←/→, stepper)", () => {
  it("stepTicket_MovesToTheNeighbourInStepsOrder", () => {
    S.openTicket(1, 107, STEPS);
    expect(S.stepTicket(1)).toBe(true);
    expect(session()?.issueId).toBe(108);
    expect(S.stepTicket(1)).toBe(true);
    expect(session()?.issueId).toBe(109);
    expect(S.stepTicket(-1)).toBe(true);
    expect(S.stepTicket(-1)).toBe(true);
    expect(session()?.issueId).toBe(107);
  });

  it("stepTicket_StopsAtBothEnds_WithoutWrapping", () => {
    S.openTicket(1, 101, STEPS);
    expect(S.stepTicket(-1)).toBe(false);
    expect(session()?.issueId).toBe(101);
    expect(S.stepTicket(1)).toBe(true); // a real move in the same test, so this is not vacuous
    expect(session()?.issueId).toBe(102);

    S.openTicket(1, 120, STEPS);
    expect(S.stepTicket(1)).toBe(false);
    expect(session()?.issueId).toBe(120);
    expect(S.stepTicket(-1)).toBe(true);
    expect(session()?.issueId).toBe(119);
  });

  it("stepTicket_FollowsTheGivenOrder_NotNumericOrder", () => {
    const order = [115, 101, 109, 103]; // e.g. walk order then Done: arbitrary ids
    S.openTicket(1, 101, order);
    S.stepTicket(1);
    expect(session()?.issueId).toBe(109);
    S.stepTicket(1);
    expect(session()?.issueId).toBe(103);
    expect(S.stepTicket(1)).toBe(false);
    S.stepTicket(-1);
    S.stepTicket(-1);
    S.stepTicket(-1);
    expect(session()?.issueId).toBe(115);
  });

  it("stepTicket_IsANoOp_WhenTheOpenIssueIsNotInSteps", () => {
    S.openTicket(1, 999, STEPS);
    expect(S.stepTicket(1)).toBe(false);
    expect(S.stepTicket(-1)).toBe(false);
    expect(session()).toEqual({ standupId: 1, issueId: 999, steps: STEPS, stack: [] });
    S.openTicket(1, 101, STEPS);
    expect(S.stepTicket(1)).toBe(true); // control: the same call works on a listed issue
  });

  it("stepTicket_ClearsTheBackStack_AndNeverChangesSteps", () => {
    S.openTicket(1, 107, STEPS);
    S.openLinkedTicket(108);
    expect(session()?.stack).toEqual([107]);
    expect(S.stepTicket(1)).toBe(true);
    expect(session()).toEqual({ standupId: 1, issueId: 109, steps: STEPS, stack: [] });
  });

  it("stepToTicket_OpensAnIssueInSteps_AndClearsTheStack", () => {
    S.openTicket(1, 107, STEPS);
    S.openLinkedTicket(108);
    expect(S.stepToTicket(112)).toBe(true);
    expect(session()?.issueId).toBe(112);
    expect(session()?.stack).toEqual([]);
  });

  it("stepToTicket_RejectsAnIssueThatIsNotInSteps", () => {
    S.openTicket(1, 107, STEPS);
    expect(S.stepToTicket(5000)).toBe(false);
    expect(session()?.issueId).toBe(107);
    expect(S.stepToTicket(108)).toBe(true); // control
  });

  it("ticketPosition_ReportsOneBasedIndexAndTotal", () => {
    S.openTicket(1, 107, STEPS);
    expect(S.ticketPosition(session())).toEqual({ index: 7, total: 20 });
    S.stepTicket(1);
    expect(S.ticketPosition(session())).toEqual({ index: 8, total: 20 });
    S.openTicket(1, 999, STEPS);
    expect(S.ticketPosition(session())).toEqual({ index: 0, total: 20 });
    expect(S.ticketPosition(null)).toEqual({ index: 0, total: 0 });
  });
});

describe("session: inline-card back stack", () => {
  it("openLinkedTicket_PushesTheCurrentIssue_AndBackPopsIt", () => {
    S.openTicket(1, 107, STEPS);
    S.openLinkedTicket(108);
    expect(session()).toMatchObject({ issueId: 108, stack: [107] });
    S.openLinkedTicket(111);
    expect(session()).toMatchObject({ issueId: 111, stack: [107, 108] });
    expect(S.backTicket()).toBe(true);
    expect(session()).toMatchObject({ issueId: 108, stack: [107] });
    expect(S.backTicket()).toBe(true);
    expect(session()).toMatchObject({ issueId: 107, stack: [] });
    expect(S.backTicket()).toBe(false);
    expect(session()?.issueId).toBe(107);
  });

  it("openLinkedTicket_KeepsTheStepListOfTheViewBehindIt", () => {
    S.openTicket(1, 107, STEPS);
    S.openLinkedTicket(9999); // a synced issue that is not on the board
    expect(session()?.steps).toEqual(STEPS);
    expect(session()?.issueId).toBe(9999);
  });
});

describe("loadTicket: opening makes zero network calls", () => {
  it("loadTicket_CallsOnlyTicketGet_AndPublishesTheDetail", async () => {
    await S.loadTicket(1, 107);
    expect(S.ticketDetail.value?.issueId).toBe(107);
    expect(S.ticketDetail.value?.row.key).toBe("MOB-1421");
    expect(mock.calls.length).toBeGreaterThan(0);
    expect(mock.calls.every((c) => c.method === "ticket.get")).toBe(true);
    expect(mock.calls.some((c) => c.method === "ticket.get" && c.args[0] === 1 && c.args[1] === 107)).toBe(true);
    expect(S.ticketError.value).toBeNull();
  });

  it("loadTicket_DoesNotFetchTransitionsOrMedia_UntilSomethingAsksForThem", async () => {
    await S.loadTicket(1, 107);
    const methods = mock.calls.map((c) => c.method);
    expect(methods).toContain("ticket.get");
    for (const m of ["ticket.transitions", "ticket.refresh", "ticket.ensureMedia", "ticket.searchMentions", "app.openIssue"]) {
      expect(methods).not.toContain(m);
    }
  });

  it("loadTicket_SeedsTheDraftFromTheDetail", async () => {
    const detail = clone(await mock.ticket.get(1, 107));
    detail.draft = { text: "half-written reply", mentions: [] };
    vi.spyOn(mock.ticket, "get").mockResolvedValue(detail);
    await S.loadTicket(1, 107);
    expect(S.ticketDraft.value.text).toBe("half-written reply");
  });

  it("loadTicket_PublishesTheErrorInsteadOfRejecting_WhenGetFails", async () => {
    vi.spyOn(mock.ticket, "get").mockRejectedValue(new AppError("not_found", "Ticket not found."));
    await expect(S.loadTicket(1, 4242)).resolves.toBeUndefined();
    expect(S.ticketDetail.value).toBeNull();
    expect(S.ticketError.value?.code).toBe("not_found");
  });

  it("loadTicket_DropsASlowEarlierLoad_WhenALaterIssueWasRequested", async () => {
    const slow = deferred<TicketDetail>();
    const real = vi.spyOn(mock.ticket, "get");
    const detail108 = clone(await mock.ticket.get(1, 108));
    const detail109 = clone(await mock.ticket.get(1, 109));
    real.mockImplementation((_standupId, issueId) => (issueId === 107 ? slow.promise : Promise.resolve(detail108)));
    const first = S.loadTicket(1, 107);
    await S.loadTicket(1, 108);
    expect(S.ticketDetail.value?.issueId).toBe(108);
    slow.resolve(detail109); // a stale answer for the first request: it must not appear
    await first;
    expect(S.ticketDetail.value?.issueId).toBe(108);
  });
});

describe("loaders used by the field rail", () => {
  it("loadTransitions_CallsTicketTransitionsOnceForTheActiveIssue", async () => {
    await S.loadTicket(1, 107);
    mock.resetCalls();
    const list = await S.loadTransitions(new AbortController().signal);
    expect(list.items.map((o) => o.id)).toEqual(["11", "31", "41", "51", "61"]);
    expect(mock.calls.filter((c) => c.method === "ticket.transitions")).toEqual([{ method: "ticket.transitions", args: [107] }]);
  });

  it("loadAssignees_SearchesPeopleForTheActiveStandup", async () => {
    await S.loadTicket(1, 107);
    mock.resetCalls();
    const users = await S.loadAssignees("wr", new AbortController().signal);
    expect(users.map((u) => u.displayName)).toContain("Wren Kowalski");
    expect(mock.calls.filter((c) => c.method === "ticket.searchMentions")).toEqual([{ method: "ticket.searchMentions", args: [1, "wr"] }]);
  });
});

describe("live update: ticket:updated while the modal is open (ux-spec §14.9)", () => {
  const sync = (issueId: number, changed: string[], cause: "sync" | "write" = "sync") =>
    mock.bus.emit("ticket:updated", { issueId, key: "MOB-1421", standupIds: [1], cause, changed });

  it("syncEventForTheOpenIssue_RecordsTheChangedParts_WithoutRefetchingOrReflowing", async () => {
    await S.loadTicket(1, 107);
    const getSpy = vi.spyOn(mock.ticket, "get");
    const before = S.ticketDetail.value;
    sync(107, ["description", "comments"]);
    expect(S.ticketUpdated.value).toEqual(["description", "comments"]);
    expect(getSpy).not.toHaveBeenCalled();
    expect(S.ticketDetail.value).toBe(before);
  });

  it("syncEvents_AccumulateDistinctPartsInFirstSeenOrder", async () => {
    await S.loadTicket(1, 107);
    sync(107, ["description", "comments"]);
    sync(107, ["comments", "fields"]);
    expect(S.ticketUpdated.value).toEqual(["description", "comments", "fields"]);
  });

  it("writeEventsAndEventsForOtherIssues_AreIgnored", async () => {
    await S.loadTicket(1, 107);
    sync(107, ["status"], "write");
    sync(108, ["description"]);
    expect(S.ticketUpdated.value).toEqual([]);
    sync(107, ["attachments"]); // control: the subscription is alive
    expect(S.ticketUpdated.value).toEqual(["attachments"]);
  });

  it("anEventWithNoChangedParts_RecordsNothing", async () => {
    await S.loadTicket(1, 107);
    sync(107, []);
    expect(S.ticketUpdated.value).toEqual([]);
    sync(107, ["fields"]);
    expect(S.ticketUpdated.value).toEqual(["fields"]);
  });

  it("reloadTicket_FetchesOnce_ReplacesTheDetail_AndClearsTheBar", async () => {
    await S.loadTicket(1, 107);
    sync(107, ["description"]);
    const fresh = clone(S.ticketDetail.value as TicketDetail);
    fresh.row.summary = "Edited in Jira";
    const getSpy = vi.spyOn(mock.ticket, "get").mockResolvedValue(fresh);
    await S.reloadTicket();
    expect(getSpy).toHaveBeenCalledTimes(1);
    expect(getSpy).toHaveBeenCalledWith(1, 107);
    expect(S.ticketDetail.value?.row.summary).toBe("Edited in Jira");
    expect(S.ticketUpdated.value).toEqual([]);
  });

  it("loadingAnotherIssue_ClearsTheUpdatedBar", async () => {
    await S.loadTicket(1, 107);
    sync(107, ["description"]);
    expect(S.ticketUpdated.value).toEqual(["description"]);
    await S.loadTicket(1, 108);
    expect(S.ticketUpdated.value).toEqual([]);
    expect(S.ticketDetail.value?.issueId).toBe(108);
  });
});
