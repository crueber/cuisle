// WP-T7 tests: ticket modal state, part 2 (optimistic light writes and comments). Spec: ux-spec §14.6 "Saving",
// architecture §3.2 "Light writes", §4.9 "Optimistic writes", testing §4 "Optimistic write state machine",
// jira-sync §3.1 (writes are never retried; 429 → rate_limited{retryInMs}).
//
// SPEC-NOTE: optimistic means SYNCHRONOUS: the detail and the cached StandupView row change before the first await,
//   and `ticketWrites[field]` is {phase: "saving"} at once (the "saving…" TEXT appears after 250 ms in FieldRail).
// SPEC-NOTE: success replaces the detail with the call's authoritative TicketDetail AND replaces the cached row in every
//   cached view of that issue's standup with that detail's `row`. Failure restores both exactly (deep-equal to before),
//   sets {phase: "failed", reason}, makes exactly ONE facade call (no timer ever re-sends) and resolves, not rejects.
// SPEC-NOTE: reason strings: code write_rejected → "Jira rejected the change: <AppError.message>" (ux-spec §14.6);
//   rate_limited with retryInMs 6000 → "Jira is rate-limiting · try again in 6s" (jira-sync §3.1, whole seconds, rounded
//   up); any other code → a reason that contains the AppError message.
// SPEC-NOTE: the optimistic rail assignee only has to carry the right accountId (name and initials come from the cached
//   view's speakers when known; not asserted). Unassign is accountId "" → row.assignee "" and no rail users.
// SPEC-NOTE: a write result that arrives after the user stepped to another issue updates the cached view row of the
//   issue it belongs to but never replaces the visible detail of the other issue.
// SPEC-NOTE: retryWrite(field) re-sends the last FAILED write of that field exactly once (an explicit user action);
//   with nothing failed it makes no call.
// SPEC-NOTE: comments (ux-spec §14.5, testing §4): sendComment trims nothing but ignores whitespace-only text (no call,
//   no pending); it shows a pending bubble {state: "sending"} at once; success drops it and clears the draft with the new
//   detail; failure keeps the bubble {state: "failed", reason: "Not sent · <message>"}, saves the text with
//   TicketService.SaveDraft so it survives a restart, and never retries by itself.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../api/errors";
import { setEventBus } from "../api/events";
import { setApi } from "../api/index";
import { createMockApi, type MockApi } from "../api/mock";
import type { CommentInput, TicketDetail, TicketView, TransitionOption } from "../api/types";
import { views } from "./standup";
import * as S from "./ticket";

const clone = <X>(x: X): X => JSON.parse(JSON.stringify(x)) as X;
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const URL_1421 = "https://example.atlassian.net/browse/MOB-1421";
const IN_REVIEW: TransitionOption = { id: "31", name: "In Review", toStatusName: "In Review", toLane: "review", needsScreen: false, url: URL_1421 };

let mock: MockApi;
beforeEach(async () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(2026, 10, 23, 9, 58, 0));
  S.resetTicketState();
  mock = createMockApi();
  setApi(mock);
  setEventBus(mock.bus);
  views.value = new Map([[1, clone(await mock.standup.view(1))]]);
  await S.loadTicket(1, 107);
  mock.resetCalls();
});
afterEach(() => {
  S.resetTicketState();
  vi.useRealTimers();
});

const detail = (): TicketDetail => S.ticketDetail.value as TicketDetail;
const viewRow = (id = 107): TicketView => views.value.get(1)!.tickets.find((t) => t.id === id)!;
const railRow = (d: TicketDetail, fieldId: string) => d.rail.flatMap((g) => g.rows).find((r) => r.fieldId === fieldId)!;
/** Everything the user can see change: the detail and the row behind the modal. */
const snapshot = () => ({ detail: clone(detail()), row: clone(viewRow()) });
/** A server answer: the open detail with a patch, so tests can tell "authoritative" from "optimistic". */
function answer(patch: (d: TicketDetail) => void): TicketDetail {
  const d = clone(detail());
  patch(d);
  return d;
}
const noRetry = async (spy: { mock: { calls: unknown[] } }, calls = 1) => {
  await vi.advanceTimersByTimeAsync(120_000);
  expect(spy.mock.calls.length).toBe(calls);
};

describe("applyTransition: optimistic status", () => {
  it("applyTransition_AppliesTheLaneToTheDetailAndTheRowBehindTheModal_BeforeAnyAwait", () => {
    const d = deferred<TicketDetail>();
    const spy = vi.spyOn(mock.ticket, "transition").mockReturnValue(d.promise);
    void S.applyTransition(IN_REVIEW);
    expect(spy).toHaveBeenCalledWith(1, 107, "31");
    expect(detail().row.lane).toBe("review");
    expect(detail().row.statusName).toBe("In Review");
    expect(railRow(detail(), "status").text).toBe("In Review");
    expect(viewRow().lane).toBe("review");
    expect(viewRow().statusName).toBe("In Review");
    expect(S.ticketWrites.value.status.phase).toBe("saving");
  });

  it("applyTransition_Success_ReplacesTheDetailAndTheRowWithTheAuthoritativeResult", async () => {
    mock.bus.emit("ticket:updated", { issueId: 107, key: "MOB-1421", standupIds: [1], cause: "sync", changed: ["description"] });
    expect(S.ticketUpdated.value).toEqual(["description"]);
    const result = answer((x) => {
      x.row.lane = "qa";
      x.row.statusName = "QA";
      x.row.summary = "Server copy";
    });
    vi.spyOn(mock.ticket, "transition").mockResolvedValue(result);
    await S.applyTransition(IN_REVIEW);
    expect(detail().row.summary).toBe("Server copy");
    expect(detail().row.lane).toBe("qa");
    expect(viewRow().lane).toBe("qa");
    expect(viewRow().statusName).toBe("QA");
    expect(S.ticketUpdated.value).toEqual([]);
    expect(S.ticketWrites.value.status).toEqual({ phase: "idle", reason: "" });
  });

  it("applyTransition_Failure_RevertsBothAndShowsTheReason_WithExactlyOneCall", async () => {
    const before = snapshot();
    const spy = vi
      .spyOn(mock.ticket, "transition")
      .mockRejectedValue(new AppError("write_rejected", "Resolution is required"));
    await expect(S.applyTransition(IN_REVIEW)).resolves.toBeUndefined();
    expect(detail()).toEqual(before.detail);
    expect(viewRow()).toEqual(before.row);
    expect(S.ticketWrites.value.status).toEqual({ phase: "failed", reason: "Jira rejected the change: Resolution is required" });
    await noRetry(spy);
  });

  it("applyTransition_RateLimited_SaysWhenToTryAgain_AndDoesNotRetry", async () => {
    const before = snapshot();
    const spy = vi
      .spyOn(mock.ticket, "transition")
      .mockRejectedValue(new AppError("rate_limited", "Jira is busy", "", true, "", 6000));
    await S.applyTransition(IN_REVIEW);
    expect(S.ticketWrites.value.status).toEqual({ phase: "failed", reason: "Jira is rate-limiting · try again in 6s" });
    expect(detail()).toEqual(before.detail);
    await noRetry(spy);
  });

  it("applyTransition_OtherErrors_KeepTheirMessageInTheReason", async () => {
    vi.spyOn(mock.ticket, "transition").mockRejectedValue(new AppError("offline", "Can't reach Jira.", "", true));
    await S.applyTransition(IN_REVIEW);
    expect(S.ticketWrites.value.status.phase).toBe("failed");
    expect(S.ticketWrites.value.status.reason).toContain("Can't reach Jira.");
  });

  it("retryWrite_ResendsTheFailedTransitionOnce_AndSucceedsThisTime", async () => {
    const spy = vi.spyOn(mock.ticket, "transition");
    spy.mockRejectedValueOnce(new AppError("write_rejected", "Resolution is required"));
    await S.applyTransition(IN_REVIEW);
    expect(spy).toHaveBeenCalledTimes(1);
    spy.mockResolvedValueOnce(answer((x) => (x.row.statusName = "In Review")));
    await S.retryWrite("status");
    expect(spy).toHaveBeenCalledTimes(2);
    expect(spy).toHaveBeenLastCalledWith(1, 107, "31");
    expect(S.ticketWrites.value.status.phase).toBe("idle");
  });

  it("retryWrite_WithNothingFailed_MakesNoCall", async () => {
    const spy = vi.spyOn(mock.ticket, "assign");
    await S.retryWrite("assignee");
    expect(spy).not.toHaveBeenCalled();
    await S.applyAssign("silas"); // control: the same spy is live
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("applyTransition_ResultAfterSteppingAway_UpdatesItsRow_ButNotTheVisibleDetail", async () => {
    const d = deferred<TicketDetail>();
    vi.spyOn(mock.ticket, "transition").mockReturnValue(d.promise);
    const pending = S.applyTransition(IN_REVIEW);
    const result = answer((x) => (x.row.lane = "qa")); // the server's answer for 107, built while 107 is still open
    await S.loadTicket(1, 108);
    expect(detail().issueId).toBe(108);
    d.resolve(result);
    await pending;
    expect(detail().issueId).toBe(108);
    expect(detail().row.key).toBe("MOB-1428");
    expect(viewRow(107).lane).toBe("qa");
  });
});

describe("applyAssign: optimistic assignee", () => {
  it("applyAssign_ChangesTheAssigneeOfTheDetailAndTheRow_BeforeAnyAwait", () => {
    const d = deferred<TicketDetail>();
    const spy = vi.spyOn(mock.ticket, "assign").mockReturnValue(d.promise);
    void S.applyAssign("silas");
    expect(spy).toHaveBeenCalledWith(1, 107, "silas");
    expect(detail().row.assignee).toBe("silas");
    expect(railRow(detail(), "assignee").users.map((u) => u.accountId)).toEqual(["silas"]);
    expect(viewRow().assignee).toBe("silas");
    expect(S.ticketWrites.value.assignee.phase).toBe("saving");
  });

  it("applyAssign_EmptyAccountId_Unassigns", () => {
    const d = deferred<TicketDetail>();
    const spy = vi.spyOn(mock.ticket, "assign").mockReturnValue(d.promise);
    void S.applyAssign("");
    expect(spy).toHaveBeenCalledWith(1, 107, "");
    expect(detail().row.assignee).toBe("");
    expect(railRow(detail(), "assignee").users).toEqual([]);
    expect(viewRow().assignee).toBe("");
  });

  it("applyAssign_Failure_RevertsBoth_ShowsTheReason_AndMakesOneCall", async () => {
    const before = snapshot();
    const spy = vi.spyOn(mock.ticket, "assign").mockRejectedValue(new AppError("permission_denied", "You can't assign this issue."));
    await S.applyAssign("silas");
    expect(detail()).toEqual(before.detail);
    expect(viewRow()).toEqual(before.row);
    expect(S.ticketWrites.value.assignee.phase).toBe("failed");
    expect(S.ticketWrites.value.assignee.reason).toContain("You can't assign this issue.");
    expect(S.ticketWrites.value.status.phase).toBe("idle"); // other fields are untouched
    await noRetry(spy);
  });

  it("applyAssign_Success_TakesTheAuthoritativeDetail", async () => {
    vi.spyOn(mock.ticket, "assign").mockResolvedValue(answer((x) => (x.row.assignee = "silas")));
    await S.applyAssign("silas");
    expect(detail().row.assignee).toBe("silas");
    expect(S.ticketWrites.value.assignee).toEqual({ phase: "idle", reason: "" });
  });
});

describe("applyFlag: optimistic flag", () => {
  it("applyFlag_TurnsBlockedOnInTheDetailAndTheRow_BeforeAnyAwait", () => {
    const d = deferred<TicketDetail>();
    const spy = vi.spyOn(mock.ticket, "setFlag").mockReturnValue(d.promise);
    void S.applyFlag(true);
    expect(spy).toHaveBeenCalledWith(1, 107, true);
    expect(detail().row.blocked).toBe(true);
    expect(railRow(detail(), "flagged").text).toBe("Flagged");
    expect(viewRow().blocked).toBe(true);
    expect(S.ticketWrites.value.flag.phase).toBe("saving");
  });

  it("applyFlag_Failure_RevertsBoth_WithExactlyOneCall", async () => {
    const before = snapshot();
    const spy = vi.spyOn(mock.ticket, "setFlag").mockRejectedValue(new AppError("write_rejected", "Flagging is disabled for this project"));
    await S.applyFlag(true);
    expect(detail()).toEqual(before.detail);
    expect(viewRow()).toEqual(before.row);
    expect(S.ticketWrites.value.flag).toEqual({ phase: "failed", reason: "Jira rejected the change: Flagging is disabled for this project" });
    await noRetry(spy);
  });

  it("applyFlag_Off_ClearsBlockedOnAFlaggedTicket", async () => {
    await S.loadTicket(1, 109); // MOB-1429 is flagged
    expect(detail().row.blocked).toBe(true);
    const d = deferred<TicketDetail>();
    const spy = vi.spyOn(mock.ticket, "setFlag").mockReturnValue(d.promise);
    void S.applyFlag(false);
    expect(spy).toHaveBeenCalledWith(1, 109, false);
    expect(detail().row.blocked).toBe(false);
    expect(railRow(detail(), "flagged").text).toBe("Not flagged");
    expect(viewRow(109).blocked).toBe(false);
  });
});

describe("comments: pending, failed, retry, edit, discard", () => {
  const input: CommentInput = { text: "Unblocked once #421 merges.", mentions: [] };

  it("sendComment_ShowsASendingBubbleAtOnce_AndCallsAddCommentOnce", () => {
    const d = deferred<TicketDetail>();
    const spy = vi.spyOn(mock.ticket, "addComment").mockReturnValue(d.promise);
    void S.sendComment(input);
    expect(spy).toHaveBeenCalledWith(1, 107, input);
    expect(S.ticketPending.value).toHaveLength(1);
    expect(S.ticketPending.value[0]).toMatchObject({ input, state: "sending", reason: "", createdAt: Date.now() });
    expect(S.ticketPending.value[0]!.localId).not.toBe("");
  });

  it("sendComment_Success_DropsTheBubble_ClearsTheDraft_AndTakesTheNewDetail", async () => {
    const saveDraft = vi.spyOn(mock.ticket, "saveDraft");
    S.editDraft(input);
    vi.spyOn(mock.ticket, "addComment").mockResolvedValue(answer((x) => (x.row.summary = "After comment")));
    await S.sendComment(input);
    expect(S.ticketPending.value).toEqual([]);
    expect(detail().row.summary).toBe("After comment");
    expect(S.ticketDraft.value.text).toBe("");
    await vi.advanceTimersByTimeAsync(1000);
    // a pending debounced save of the sent text must not resurrect it
    expect(saveDraft.mock.calls.every((c) => c[1].text === "")).toBe(true);
  });

  it("sendComment_SuccessWithAFailedRefresh_KeepsTheBubbleAsSent_UntilALaterDetailCarriesIt", async () => {
    const warned = answer((x) => {
      x.warnings = ["refresh_failed"];
    });
    vi.spyOn(mock.ticket, "addComment").mockResolvedValue(warned);
    await S.sendComment(input);
    expect(S.ticketPending.value).toHaveLength(1);
    expect(S.ticketPending.value[0]).toMatchObject({ state: "sent", reason: "", input });
    expect(S.ticketDraft.value.text).toBe("");
    // a Reload that still lacks it keeps the bubble
    vi.spyOn(mock.ticket, "get").mockResolvedValue(clone(detail()));
    await S.reloadTicket();
    expect(S.ticketPending.value).toHaveLength(1);
    // a Refresh whose detail carries the comment removes it
    const withComment = answer((x) => {
      x.comments = [...x.comments, { ...clone(x.comments[0]!), id: "new", body: { c: [{ t: "p", c: [{ t: "text", x: input.text }] }] } as never }];
    });
    vi.spyOn(mock.ticket, "refresh").mockResolvedValue(withComment);
    await S.refreshTicket();
    expect(S.ticketPending.value).toEqual([]);
    expect(detail().comments.some((c) => c.id === "new")).toBe(true);
  });

  it("sendComment_SuccessWithARefreshWarningButTheCommentPresent_DropsTheBubble", async () => {
    const both = answer((x) => {
      x.warnings = ["refresh_failed"];
      x.comments = [...x.comments, { ...clone(x.comments[0]!), id: "new", body: { c: [{ t: "p", c: [{ t: "text", x: input.text }] }] } as never }];
    });
    vi.spyOn(mock.ticket, "addComment").mockResolvedValue(both);
    await S.sendComment(input);
    expect(S.ticketPending.value).toEqual([]);
  });

  it("sendComment_Failure_KeepsAFailedBubble_SavesTheText_AndNeverRetries", async () => {
    const before = snapshot();
    const saveDraft = vi.spyOn(mock.ticket, "saveDraft");
    const spy = vi.spyOn(mock.ticket, "addComment").mockRejectedValue(new AppError("offline", "Couldn't reach Jira (offline).", "", true));
    await S.sendComment(input);
    const p = S.ticketPending.value[0]!;
    expect(S.ticketPending.value).toHaveLength(1);
    expect(p.state).toBe("failed");
    expect(p.reason).toBe("Couldn't reach Jira (offline)."); // only the cause: Comments.tsx writes "Not sent · " once
    expect(detail()).toEqual(before.detail);
    await noRetry(spy);
    expect(saveDraft).toHaveBeenCalledWith(107, input);
  });

  it("sendComment_WhitespaceOnly_MakesNoCallAndNoBubble", async () => {
    const spy = vi.spyOn(mock.ticket, "addComment");
    await S.sendComment({ text: "  \n ", mentions: [] });
    expect(spy).not.toHaveBeenCalled();
    expect(S.ticketPending.value).toEqual([]);
    await S.sendComment(input); // control
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("retryComment_SendsAgain_ShowsSendingAtOnce_AndRemovesTheBubbleOnSuccess", async () => {
    const spy = vi.spyOn(mock.ticket, "addComment");
    spy.mockRejectedValueOnce(new AppError("offline", "offline", "", true));
    await S.sendComment(input);
    const id = S.ticketPending.value[0]!.localId;
    const d = deferred<TicketDetail>();
    spy.mockReturnValueOnce(d.promise);
    const retrying = S.retryComment(id);
    expect(S.ticketPending.value[0]).toMatchObject({ localId: id, state: "sending" });
    d.resolve(answer(() => undefined));
    await retrying;
    expect(spy).toHaveBeenCalledTimes(2);
    expect(spy).toHaveBeenLastCalledWith(1, 107, input);
    expect(S.ticketPending.value).toEqual([]);
  });

  it("editComment_PutsTheTextBackInTheDraft_AndDropsTheBubble", async () => {
    vi.spyOn(mock.ticket, "addComment").mockRejectedValue(new AppError("offline", "offline", "", true));
    await S.sendComment(input);
    S.editComment(S.ticketPending.value[0]!.localId);
    expect(S.ticketPending.value).toEqual([]);
    expect(S.ticketDraft.value).toEqual(input);
  });

  it("discardComment_DropsTheBubble_AndDeletesTheStoredDraft", async () => {
    const saveDraft = vi.spyOn(mock.ticket, "saveDraft");
    vi.spyOn(mock.ticket, "addComment").mockRejectedValue(new AppError("offline", "offline", "", true));
    await S.sendComment(input);
    S.discardComment(S.ticketPending.value[0]!.localId);
    expect(S.ticketPending.value).toEqual([]);
    expect(S.ticketDraft.value.text).toBe("");
    await vi.advanceTimersByTimeAsync(400);
    const last = saveDraft.mock.calls.at(-1)!;
    expect(last[0]).toBe(107);
    expect(last[1].text).toBe("");
  });
});
