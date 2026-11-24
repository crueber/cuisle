// WP-T7 tests: TicketModal, part 3 (light writes, Refresh, live update, lightbox). Spec: ux-spec §14.6 "Saving", §14.7,
// §14.9, §14.12; architecture §3.2 "Light writes", §4.9; testing §4 "Optimistic write state machine".
//
// SPEC-NOTE: same harness and TicketContent double as TicketModal.test.tsx (see its notes). Writes are driven through the
//   real rail controls: every write makes exactly ONE facade call, is shown optimistically before the call settles (also on
//   the cached StandupView row behind the modal), and on failure reverts both, shows the reason in the field's alert with
//   Retry, and never re-sends by itself (the call count is asserted right after the alert appears; the state tests
//   assert that no timer re-sends later). Retry re-sends once.
// SPEC-NOTE: choosing a needs-screen transition ("Done") opens the ticket in Jira via api.app.openIssue(key) and makes no
//   transition call; the issue URL of a TransitionOption is the ticket's own, so it maps to the key.
// SPEC-NOTE: the Refresh button is wired to state/ticket's refresh state: a click calls api.ticket.refresh(standupId,
//   issueId) once, spins (aria-busy) while pending, then shows "refreshed H:mm · just this ticket" with a disabled button
//   whose text carries the 10 s countdown ("10s"); a `cooldown` error shows the countdown from retryInMs; `rate_limited`
//   shows "Jira is rate-limiting · try again in 6s" (inside the "Couldn't refresh · …" meta); `offline` shows
//   "Couldn't refresh · offline". Whole-second values only; the countdown ticking itself is covered in ticket.refresh.test.ts.
// SPEC-NOTE: `ticket:updated` (ux-spec §14.9): cause "sync" for the open issue passes the changed part names to
//   TicketContent (`updated`) without refetching; `onReload` refetches once and clears them; cause "write" and events for
//   other issues change nothing.
// SPEC-NOTE: the lightbox shows the ticket's IMAGES (attachments with image === true, attachment order), opens at the
//   image the content asked for (ctx.onOpenImage(id)), ensures a non-"full" image through api.ticket.ensureMedia on
//   open/step, "Open file" calls api.ticket.openAttachment(id), and closing it leaves the modal open. ctx.onOpenFile(id)
//   calls api.ticket.openAttachment(id); ctx.onLoadImage(id) calls api.ticket.ensureMedia(id).
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../../api/errors";
import { setEventBus } from "../../api/events";
import { setApi } from "../../api/index";
import { createMockApi, type MockApi } from "../../api/mock";
import type { TicketDetail, TicketView } from "../../api/types";
import { views } from "../../state/standup";
import { resetTicketState, ticketSession } from "../../state/ticket";
import { TicketModal } from "./TicketModal";
import type { TicketContentProps, TicketModalProps } from "./props";

vi.mock("./TicketContent", async () => {
  const { h } = await import("preact");
  return {
    TicketContent: (p: TicketContentProps) =>
      h(
        "div",
        { "data-testid": "content", "data-key": p.detail.row.key, "data-updated": p.updated.join(",") },
        h("button", { onClick: () => p.ctx.onOpenImage(201) }, "open image 201"),
        h("button", { onClick: () => p.ctx.onOpenImage(202) }, "open image 202"),
        h("button", { onClick: () => p.ctx.onOpenFile(203) }, "open file 203"),
        h("button", { onClick: () => p.ctx.onLoadImage(202) }, "load image 202"),
        h("button", { onClick: p.onReload }, "reload"),
      ),
  };
});

const clone = <X,>(x: X): X => JSON.parse(JSON.stringify(x)) as X;
const STEPS = Array.from({ length: 20 }, (_, i) => 101 + i);
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** Lets the load finish INSIDE act (the mock settles on microtasks), so the render and the first effects of the freshly
 *  mounted Comboboxes have run before anyone types. */
async function settle() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
  await screen.findByTestId("content");
}

let mock: MockApi;
async function mount(over: Partial<TicketModalProps> = {}) {
  mock = createMockApi({ handBuiltTickets: true });
  setApi(mock);
  setEventBus(mock.bus);
  views.value = new Map([[1, clone(await mock.standup.view(1))]]);
  mock.resetCalls();
  const p: TicketModalProps = {
    standupId: 1, issueId: 107, steps: STEPS, stack: [], vw: 1920, vh: 1080,
    onStep: vi.fn(), onOpenLinked: vi.fn(), onBack: vi.fn(), onClose: vi.fn(),
    ...over,
  };
  const view = render(<TicketModal {...p} />);
  await settle();
  return { p, ...view };
}
beforeEach(() => resetTicketState());
afterEach(() => {
  resetTicketState();
  ticketSession.value = null;
});
const statusInput = () => screen.getByRole("combobox", { name: "Status" }) as HTMLInputElement;
const flag = () => screen.getByRole("switch", { name: /flag/i }) as HTMLButtonElement;
const viewRow = (): TicketView => views.value.get(1)!.tickets.find((t) => t.id === 107)!;
const rowOf = (fieldId: string) => document.querySelector(`[data-field="${fieldId}"]`) as HTMLElement;
const refreshBtn = () => screen.getByRole("button", { name: /refresh/i }) as HTMLButtonElement;
const IN_REVIEW = { name: /In Review/ };

describe("status: optimistic transition", () => {
  it("showsTheNewStatusAtOnce_AndTheRowBehindTheModal_ThenKeepsItOnSuccess", async () => {
    await mount();
    const d = deferred<TicketDetail>();
    const spy = vi.spyOn(mock.ticket, "transition").mockReturnValue(d.promise);
    const user = userEvent.setup();
    await user.click(statusInput());
    await user.click(await screen.findByRole("option", IN_REVIEW));
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith(1, 107, "31");
    expect(statusInput().value).toBe("In Review");
    expect(viewRow().lane).toBe("review");
    const result = clone((await mock.ticket.get(1, 107)) as TicketDetail);
    result.row.lane = "review";
    result.row.statusName = "In Review";
    for (const g of result.rail) for (const r of g.rows) if (r.fieldId === "status") r.text = "In Review";
    d.resolve(result);
    await waitFor(() => expect(statusInput().value).toBe("In Review"));
    expect(viewRow().lane).toBe("review");
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("revertsTheFieldAndTheRow_OnFailure_ShowsTheReasonWithRetry_AndSendsOnce", async () => {
    await mount();
    const spy = vi.spyOn(mock.ticket, "transition").mockRejectedValue(new AppError("write_rejected", "Resolution is required"));
    const user = userEvent.setup();
    await user.click(statusInput());
    await user.click(await screen.findByRole("option", IN_REVIEW));
    const alert = await within(rowOf("status")).findByRole("alert");
    expect(alert.textContent).toContain("Jira rejected the change: Resolution is required");
    await waitFor(() => expect(statusInput().value).toBe("In Progress"));
    expect(viewRow().lane).toBe("prog");
    expect(spy).toHaveBeenCalledTimes(1);

    spy.mockResolvedValueOnce(clone((await mock.ticket.get(1, 107)) as TicketDetail));
    fireEvent.click(within(alert).getByRole("button", { name: /retry/i }));
    expect(spy).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(within(rowOf("status")).queryByRole("alert")).toBeNull());
  });

  it("aNeedsScreenTransitionOpensJiraAndWritesNothing", async () => {
    await mount();
    const transition = vi.spyOn(mock.ticket, "transition");
    const openIssue = vi.spyOn(mock.app, "openIssue");
    const user = userEvent.setup();
    await user.click(statusInput());
    await user.click(await screen.findByRole("option", { name: /Done/ }));
    expect(openIssue).toHaveBeenCalledTimes(1);
    expect(openIssue).toHaveBeenCalledWith("MOB-1421");
    expect(transition).not.toHaveBeenCalled();
    await waitFor(() => expect(statusInput().value).toBe("In Progress")); // the field never keeps showing the unchosen target
  });
});

describe("assignee and flag: optimistic writes", () => {
  it("assigningShowsTheNewAssigneeOnTheRowAtOnce_AndRevertsOnFailureWithOneCall", async () => {
    await mount();
    const spy = vi.spyOn(mock.ticket, "assign").mockRejectedValue(new AppError("permission_denied", "You can't assign this issue."));
    const user = userEvent.setup();
    await user.clear(screen.getByRole("combobox", { name: "Assignee" }));
    await user.type(screen.getByRole("combobox", { name: "Assignee" }), "wr");
    await user.click(await screen.findByRole("option", { name: /Wren Kowalski/ }));
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith(1, 107, "wren");
    const alert = await within(rowOf("assignee")).findByRole("alert");
    expect(alert.textContent).toContain("You can't assign this issue.");
    expect(viewRow().assignee).toBe("iris");
    await waitFor(() => expect((screen.getByRole("combobox", { name: "Assignee" }) as HTMLInputElement).value).toBe("Iris Calloway"));
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("assigningShowsTheNewAssigneeOnTheRowBeforeTheCallSettles", async () => {
    await mount();
    const d = deferred<TicketDetail>();
    vi.spyOn(mock.ticket, "assign").mockReturnValue(d.promise);
    const user = userEvent.setup();
    await user.clear(screen.getByRole("combobox", { name: "Assignee" }));
    await user.type(screen.getByRole("combobox", { name: "Assignee" }), "wr");
    await user.click(await screen.findByRole("option", { name: /Wren Kowalski/ }));
    expect(viewRow().assignee).toBe("wren");
    d.reject(new AppError("write_rejected", "nope"));
    await waitFor(() => expect(viewRow().assignee).toBe("iris"));
  });

  it("theFlagSwitchTurnsOnAtOnce_AndRevertsWithAnAlertOnFailure_SendingOnce", async () => {
    await mount();
    const d = deferred<TicketDetail>();
    const spy = vi.spyOn(mock.ticket, "setFlag").mockReturnValue(d.promise);
    fireEvent.click(flag());
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith(1, 107, true);
    expect(flag().getAttribute("aria-checked")).toBe("true");
    expect(viewRow().blocked).toBe(true);
    expect(flag().disabled).toBe(true);
    d.reject(new AppError("write_rejected", "Flagging is disabled for this project"));
    const alert = await within(rowOf("flagged")).findByRole("alert");
    expect(alert.textContent).toContain("Jira rejected the change: Flagging is disabled for this project");
    expect(flag().getAttribute("aria-checked")).toBe("false");
    expect(viewRow().blocked).toBe(false);
    expect(spy).toHaveBeenCalledTimes(1);
  });
});

describe("Refresh this ticket", () => {
  it("clickingCallsRefreshOnce_SpinsWhilePending_ThenShowsRefreshedAndTheCooldown", async () => {
    await mount();
    const d = deferred<TicketDetail>();
    const spy = vi.spyOn(mock.ticket, "refresh").mockReturnValue(d.promise);
    fireEvent.click(refreshBtn());
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith(1, 107);
    expect(refreshBtn().disabled).toBe(true);
    expect(refreshBtn().getAttribute("aria-busy")).toBe("true");
    d.resolve(clone((await mock.ticket.get(1, 107)) as TicketDetail));
    expect(await screen.findByText(/refreshed \d{1,2}:\d{2} · just this ticket/)).toBeTruthy();
    expect(refreshBtn().disabled).toBe(true);
    expect(refreshBtn().textContent).toContain("10s");
    fireEvent.click(refreshBtn());
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("aCooldownErrorShowsTheCountdownFromRetryInMs", async () => {
    await mount();
    vi.spyOn(mock.ticket, "refresh").mockRejectedValue(new AppError("cooldown", "Refreshed a moment ago.", "", false, "", 8000));
    fireEvent.click(refreshBtn());
    await waitFor(() => expect(refreshBtn().textContent).toContain("8s"));
    expect(refreshBtn().disabled).toBe(true);
  });

  it("aRateLimitedErrorSaysWhenToTryAgain", async () => {
    await mount();
    vi.spyOn(mock.ticket, "refresh").mockRejectedValue(new AppError("rate_limited", "Jira is busy", "", true, "", 6000));
    fireEvent.click(refreshBtn());
    expect(await screen.findByText(/Jira is rate-limiting · try again in 6s/)).toBeTruthy();
    expect(refreshBtn().textContent).toContain("6s");
  });

  it("anOfflineFailureShowsCouldntRefreshOffline_AndAllowsAnotherPress", async () => {
    await mount();
    const spy = vi.spyOn(mock.ticket, "refresh").mockRejectedValue(new AppError("offline", "You appear to be offline.", "", true));
    fireEvent.click(refreshBtn());
    expect(await screen.findByText("Couldn't refresh · offline")).toBeTruthy();
    expect(refreshBtn().disabled).toBe(false);
    expect(spy).toHaveBeenCalledTimes(1);
  });
});

describe("live update (ticket:updated while open)", () => {
  const emit = (issueId: number, cause: "sync" | "write", changed: string[]) =>
    mock.bus.emit("ticket:updated", { issueId, key: "MOB-1421", standupIds: [1], cause, changed });
  const updated = () => screen.getByTestId("content").getAttribute("data-updated");
  const gets = () => mock.calls.filter((c) => c.method === "ticket.get" && c.args[1] === 107).length;

  it("aSyncEventNamesTheChangedParts_WithoutRefetching_AndReloadFetchesOnce", async () => {
    await mount();
    const before = gets();
    emit(107, "sync", ["description", "comments"]);
    await waitFor(() => expect(updated()).toBe("description,comments"));
    expect(gets()).toBe(before);
    fireEvent.click(screen.getByText("reload"));
    await waitFor(() => expect(updated()).toBe(""));
    expect(gets()).toBe(before + 1);
  });

  it("writeEventsAndOtherIssuesChangeNothing", async () => {
    await mount();
    emit(107, "write", ["status"]);
    emit(108, "sync", ["description"]);
    expect(updated()).toBe("");
    emit(107, "sync", ["fields"]); // control
    await waitFor(() => expect(updated()).toBe("fields"));
  });
});

describe("lightbox through the modal", () => {
  it("opensAtTheClickedImage_ShowsImagesOnly_AndEnsuresOnlyThumbOnlyOnes", async () => {
    await mount();
    const ensure = vi.spyOn(mock.ticket, "ensureMedia");
    fireEvent.click(screen.getByText("open image 201"));
    const box = await screen.findByRole("dialog", { name: "Image viewer" });
    expect(within(box).getByText("1 / 2")).toBeTruthy(); // 5 attachments, 2 images
    expect(within(box).getByRole("img", { name: "unit-toggle-v3.png" })).toBeTruthy();
    expect(ensure).not.toHaveBeenCalled();
    fireEvent.click(within(box).getByRole("button", { name: "Next image" }));
    await waitFor(() => expect(within(box).getByText("2 / 2")).toBeTruthy());
    expect(ensure).toHaveBeenCalledTimes(1);
    expect(ensure).toHaveBeenCalledWith(202);
  });

  it("opensDirectlyAtTheSecondImage", async () => {
    await mount();
    fireEvent.click(screen.getByText("open image 202"));
    const box = await screen.findByRole("dialog", { name: "Image viewer" });
    expect(within(box).getByText("2 / 2")).toBeTruthy();
    expect(within(box).getByRole("img", { name: "conversion-flow.png" })).toBeTruthy();
  });

  it("openFileGoesThroughOpenAttachment_AndClosingTheLightboxLeavesTheModalOpen", async () => {
    const { p } = await mount();
    const open = vi.spyOn(mock.ticket, "openAttachment");
    fireEvent.click(screen.getByText("open image 202"));
    const box = await screen.findByRole("dialog", { name: "Image viewer" });
    fireEvent.click(within(box).getByRole("button", { name: "Open file" }));
    expect(open).toHaveBeenCalledTimes(1);
    expect(open).toHaveBeenCalledWith(202);
    fireEvent.click(within(box).getByRole("button", { name: "Close image" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Image viewer" })).toBeNull());
    expect(screen.getByRole("dialog", { name: /MOB-1421/ })).toBeTruthy();
    expect(p.onClose).not.toHaveBeenCalled();
  });

  it("contentFileAndLoadImageClicksUseOpenAttachmentAndEnsureMedia", async () => {
    await mount();
    const open = vi.spyOn(mock.ticket, "openAttachment");
    const ensure = vi.spyOn(mock.ticket, "ensureMedia");
    fireEvent.click(screen.getByText("open file 203"));
    expect(open).toHaveBeenCalledWith(203);
    fireEvent.click(screen.getByText("load image 202"));
    expect(ensure).toHaveBeenCalledWith(202);
    expect(open).toHaveBeenCalledTimes(1);
    expect(ensure).toHaveBeenCalledTimes(1);
  });
});
