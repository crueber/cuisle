// WP-T7 tests: ticket modal state, part 3 (Refresh this ticket). Spec: ux-spec §14.12 (button states, 10 s per-issue
// cooldown, "refreshing…", failure, "waiting for Jira (rate limit) · 6s"), §14.13, jira-sync §3.1 ("writes do not
// retry … the UI says 'Jira is rate-limiting · try again in 6s'"), architecture §3.2 (Refresh → cooldown{retryInMs}).
//
// SPEC-NOTE: `ticketRefresh` is the RefreshState (frozen shape in props.ts) of the ACTIVE issue. refreshTicket() sets
//   {phase: "refreshing"} synchronously, calls TicketService.Refresh(standupId, issueId) once (a call while one is
//   pending, or during a cooldown, is ignored locally and makes no facade call), and on success replaces the detail,
//   clears `ticketUpdated`, and sets {phase: "done", at: Date.now(), cooldownMs: 10000}.
// SPEC-NOTE: the COUNTDOWN LIVES IN THE STATE (a 1 s interval; not network polling): cooldownMs is recomputed from a
//   per-issue "cooldown until" instant, so tests only look at whole-second multiples. The instant is per issue and is
//   kept when stepping away, so stepping back shows the remaining time, not 0 and not a reset.
// SPEC-NOTE: error mapping. Go's `cooldown` error (retryInMs) is expected, not a failure: {phase: "idle", cooldownMs:
//   retryInMs}. `rate_limited` → {phase: "failed", reason: "Jira is rate-limiting · try again in 6s", cooldownMs:
//   retryInMs} (the brief's copy; the button also stays disabled for that long). `offline` → {phase: "failed", reason:
//   "offline"}; any other error → reason = AppError.message (the header prints "Couldn't refresh · <reason>").
//   A failure keeps the old detail, sets no cooldown, and allows another explicit Refresh.
// SPEC-NOTE: the frozen RefreshState has a "waiting" phase (queued behind the limiter). The only signal the frontend has
//   is the `sync:throttled` event, so: while a refresh is pending and `sync:throttled {reason: "rate_limited",
//   resumeAt}` arrives, the phase becomes "waiting" with waitMs = resumeAt − now (counting down per second); the
//   phase returns to "done"/"failed" when the call settles. A `near_limit` event (resumeAt 0) changes nothing, and so does
//   an event while no refresh is pending.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../api/errors";
import { setEventBus } from "../api/events";
import { setApi } from "../api/index";
import { createMockApi, type MockApi } from "../api/mock";
import type { TicketDetail } from "../api/types";
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

const NOW = new Date(2026, 10, 23, 9, 58, 0).getTime();
let mock: MockApi;
beforeEach(async () => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
  S.resetTicketState();
  mock = createMockApi();
  setApi(mock);
  setEventBus(mock.bus);
  await S.loadTicket(1, 107);
  mock.resetCalls();
});
afterEach(() => {
  S.resetTicketState();
  vi.useRealTimers();
});

const detail = (): TicketDetail => S.ticketDetail.value as TicketDetail;
const refreshed = (summary: string): TicketDetail => {
  const d = clone(detail());
  d.row.summary = summary;
  return d;
};

describe("refreshTicket: the happy path", () => {
  it("refreshTicket_SpinsWhilePending_CallsRefreshOnce_AndIgnoresAPressWhilePending", async () => {
    const d = deferred<TicketDetail>();
    const spy = vi.spyOn(mock.ticket, "refresh").mockReturnValue(d.promise);
    void S.refreshTicket();
    expect(S.ticketRefresh.value.phase).toBe("refreshing");
    expect(spy).toHaveBeenCalledWith(1, 107);
    void S.refreshTicket();
    expect(spy).toHaveBeenCalledTimes(1);
    d.resolve(refreshed("x"));
    await vi.advanceTimersByTimeAsync(0);
  });

  it("refreshTicket_Success_ReplacesTheDetail_ClearsTheBar_AndStartsTheTenSecondCooldown", async () => {
    mock.bus.emit("ticket:updated", { issueId: 107, key: "MOB-1421", standupIds: [1], cause: "sync", changed: ["description"] });
    expect(S.ticketUpdated.value).toEqual(["description"]);
    vi.spyOn(mock.ticket, "refresh").mockResolvedValue(refreshed("Fresh from Jira"));
    await S.refreshTicket();
    expect(detail().row.summary).toBe("Fresh from Jira");
    expect(S.ticketUpdated.value).toEqual([]);
    expect(S.ticketRefresh.value).toEqual({ phase: "done", at: NOW, cooldownMs: 10_000, waitMs: 0, reason: "" });
  });

  it("refreshTicket_Cooldown_CountsDownEverySecond_ThenReachesZero", async () => {
    vi.spyOn(mock.ticket, "refresh").mockResolvedValue(refreshed("x"));
    await S.refreshTicket();
    expect(S.ticketRefresh.value.cooldownMs).toBe(10_000);
    await vi.advanceTimersByTimeAsync(2000);
    expect(S.ticketRefresh.value.cooldownMs).toBe(8000);
    await vi.advanceTimersByTimeAsync(7000);
    expect(S.ticketRefresh.value.cooldownMs).toBe(1000);
    await vi.advanceTimersByTimeAsync(1000);
    expect(S.ticketRefresh.value.cooldownMs).toBe(0);
    expect(S.ticketRefresh.value.phase).toBe("done");
    expect(S.ticketRefresh.value.at).toBe(NOW);
  });

  it("refreshTicket_DuringTheCooldown_IsIgnored_ThenWorksAgainAfterIt", async () => {
    const spy = vi.spyOn(mock.ticket, "refresh").mockResolvedValue(refreshed("x"));
    await S.refreshTicket();
    await vi.advanceTimersByTimeAsync(3000);
    await S.refreshTicket();
    expect(spy).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(7000);
    await S.refreshTicket();
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it("theCooldown_IsPerIssue_AndSurvivesSteppingAwayAndBack", async () => {
    vi.spyOn(mock.ticket, "refresh").mockResolvedValue(refreshed("x"));
    await S.refreshTicket();
    await vi.advanceTimersByTimeAsync(3000);
    await S.loadTicket(1, 108);
    expect(S.ticketRefresh.value).toMatchObject({ phase: "idle", cooldownMs: 0, at: 0 });
    await vi.advanceTimersByTimeAsync(2000);
    await S.loadTicket(1, 107);
    expect(S.ticketRefresh.value.cooldownMs).toBe(5000);
    await vi.advanceTimersByTimeAsync(5000);
    expect(S.ticketRefresh.value.cooldownMs).toBe(0);
  });
});

describe("refreshTicket: errors", () => {
  it("cooldownError_IsNotAFailure_ItJustCountsDownFromRetryInMs", async () => {
    vi.spyOn(mock.ticket, "refresh").mockRejectedValue(new AppError("cooldown", "Refreshed a moment ago.", "", false, "", 8000));
    await S.refreshTicket();
    expect(S.ticketRefresh.value).toMatchObject({ phase: "idle", cooldownMs: 8000, reason: "" });
    await vi.advanceTimersByTimeAsync(3000);
    expect(S.ticketRefresh.value.cooldownMs).toBe(5000);
    await vi.advanceTimersByTimeAsync(5000);
    expect(S.ticketRefresh.value.cooldownMs).toBe(0);
    expect(S.ticketRefresh.value.phase).toBe("idle");
  });

  it("rateLimitedError_SaysWhenToTryAgain_AndKeepsTheButtonDisabledThatLong", async () => {
    vi.spyOn(mock.ticket, "refresh").mockRejectedValue(new AppError("rate_limited", "Jira is busy", "", true, "", 6000));
    await S.refreshTicket();
    expect(S.ticketRefresh.value).toMatchObject({ phase: "failed", reason: "Jira is rate-limiting · try again in 6s", cooldownMs: 6000 });
    await vi.advanceTimersByTimeAsync(6000);
    expect(S.ticketRefresh.value.cooldownMs).toBe(0);
  });

  it("offlineError_FailsWithTheReasonOffline_KeepsTheDetail_AndAllowsAnotherPress", async () => {
    const before = clone(detail());
    const spy = vi.spyOn(mock.ticket, "refresh").mockRejectedValue(new AppError("offline", "You appear to be offline.", "", true));
    await S.refreshTicket();
    expect(S.ticketRefresh.value).toMatchObject({ phase: "failed", reason: "offline", cooldownMs: 0 });
    expect(detail()).toEqual(before);
    await vi.advanceTimersByTimeAsync(120_000);
    expect(spy).toHaveBeenCalledTimes(1); // nothing retried by itself
    spy.mockResolvedValueOnce(refreshed("x"));
    await S.refreshTicket();
    expect(spy).toHaveBeenCalledTimes(2);
    expect(S.ticketRefresh.value.phase).toBe("done");
  });

  it("otherErrors_UseTheirMessageAsTheReason", async () => {
    vi.spyOn(mock.ticket, "refresh").mockRejectedValue(new AppError("source_http", "Jira returned 500."));
    await S.refreshTicket();
    expect(S.ticketRefresh.value).toMatchObject({ phase: "failed", reason: "Jira returned 500." });
  });
});

describe("refreshTicket: waiting behind the rate limiter (sync:throttled)", () => {
  it("aRateLimitedThrottleEventWhilePending_ShowsWaitingWithTheRemainingTime", async () => {
    const d = deferred<TicketDetail>();
    vi.spyOn(mock.ticket, "refresh").mockReturnValue(d.promise);
    void S.refreshTicket();
    mock.bus.emit("sync:throttled", { resumeAt: NOW + 6000, reason: "rate_limited" });
    expect(S.ticketRefresh.value).toMatchObject({ phase: "waiting", waitMs: 6000 });
    await vi.advanceTimersByTimeAsync(2000);
    expect(S.ticketRefresh.value).toMatchObject({ phase: "waiting", waitMs: 4000 });
    d.resolve(refreshed("after the wait"));
    await vi.advanceTimersByTimeAsync(0);
    expect(S.ticketRefresh.value.phase).toBe("done");
    expect(S.ticketRefresh.value.waitMs).toBe(0);
    expect(detail().row.summary).toBe("after the wait");
  });

  it("nearLimitEventsAndEventsWhileIdle_ChangeNothing", async () => {
    mock.bus.emit("sync:throttled", { resumeAt: NOW + 6000, reason: "rate_limited" });
    expect(S.ticketRefresh.value.phase).toBe("idle");
    const d = deferred<TicketDetail>();
    vi.spyOn(mock.ticket, "refresh").mockReturnValue(d.promise);
    void S.refreshTicket();
    expect(S.ticketRefresh.value.phase).toBe("refreshing"); // control: refresh is pending
    mock.bus.emit("sync:throttled", { resumeAt: 0, reason: "near_limit" });
    expect(S.ticketRefresh.value.phase).toBe("refreshing");
    d.resolve(refreshed("x"));
    await vi.advanceTimersByTimeAsync(0);
  });
});
