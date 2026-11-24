// WP-T7 tests: TicketModal, part 1 (open, header, modes, back stack, comments wiring). Companions:
// TicketModal.keys.test.tsx (keyboard, Esc unwinding), TicketModal.writes.test.tsx (writes, Refresh, live update,
// lightbox), TicketModal.geometry.test.tsx (modalGeometry). Spec: ux-spec §14.1–14.3, §14.10; architecture §4.9;
// AGENTS invariant 2 ("opening the modal makes zero network calls").
//
// SPEC-NOTE: TicketModal is rendered with the REAL state/ticket.ts and the REAL TicketHeader / FieldRail / StatusCombo /
//   Lightbox over createMockApi({ handBuiltTickets: true }); only TicketContent (WP-T8) is replaced by a double (`vi.mock("./TicketContent")`):
//   TicketModal must `import { TicketContent } from "./TicketContent"` and render it with the frozen TicketContentProps
//   (detail, geometry, ctx, updated, onReload, comments). The double exposes those props as data-* attributes and
//   buttons, see `ContentDouble` below. (If WP-T8 has not merged when WP-T7 is implemented, the implementer needs the
//   file to exist for the import to resolve: that is an orchestration matter, not a test one.)
// SPEC-NOTE: DOM HOOKS of TicketModal: role="dialog" aria-modal="true" with an accessible name that contains the key
//   (the lightbox is a second dialog named "Image viewer"). The modal calls `loadTicket(standupId, issueId)` of
//   state/ticket whenever its issueId prop changes and renders the Header, the content, and ONE FieldRail:
//   layout "column" (data-layout="column") normally, or layout "strip" (data-layout="strip", no column rail) when
//   geometry.narrow. The keyboard hints live in an element data-part="hints"; in tall mode (vh >= 800) it sits inside
//   a footer element data-part="footer"; in short mode there is NO footer and a button named /keyboard shortcuts/i
//   (the "?" popover) toggles the data-part="hints" element in and out.
// SPEC-NOTE: the back button's key is looked up from the cached StandupView rows (`views`) by issue id; a failed load shows
//   an element with role="alert" containing the error message, and the header's Close button stays usable.
// SPEC-NOTE: comment order: the modal feeds CommentsProps.order from app setting `ticket.comment_order` (default
//   "newest", ux-spec/architecture §6.4) and `onOrder(o)` persists it with api.app.setAppSetting("ticket.comment_order", o)
//   and then reflects it. `newSince` is detail.newSince; `pending` comes from the state; sending goes through
//   `sendComment` (one addComment call, a failure leaves a failed bubble, nothing retries).
// SPEC-NOTE: the composer text is autosaved through the state's draft pipeline: 400 ms after the last change (timing pinned
//   in state/ticket.draft.test.ts), immediately on blur (Esc blurs it) and on unmount.
// SPEC-NOTE: the parent crumb: a parent with issueId > 0 (synced) opens in the modal through props.onOpenLinked(issueId);
//   a parent with issueId 0 opens the browser through api.app.openIssue(parent.key) and does not call onOpenLinked. Any
//   other URL a link wants to open (ctx.onOpenUrl) has no frozen facade method besides app.openIssue(key), so only the
//   ticket's own URL (needs-screen transitions, Open in Jira) is pinned at modal level.
// SPEC-NOTE: TIMING OF THE HARNESS: `settle()` (in this and the sibling files) lets the load finish inside act and then
//   waits for the content; it assumes the modal paints within one macrotask of mounting against the mock api. Comboboxes
//   mounted by a render that happens outside act run their first effect (which syncs the input text to the committed
//   label) up to ~100 ms late, so a test that types the instant a late-painted modal appears would see its text
//   overwritten; the tests avoid this by settling first, and an implementation must not need more than one macrotask.
import { act, fireEvent, render, screen, waitFor } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../../api/errors";
import { setEventBus } from "../../api/events";
import { setApi } from "../../api/index";
import { createMockApi, type MockApi } from "../../api/mock";
import { views } from "../../state/standup";
import { backTicket, closeTicket, openLinkedTicket, openTicket, resetTicketState, stepToTicket, ticketSession } from "../../state/ticket";
import { TicketModal } from "./TicketModal";
import type { TicketContentProps, TicketModalProps } from "./props";

vi.mock("./TicketContent", async () => {
  const { h } = await import("preact");
  return {
    TicketContent: (p: TicketContentProps) =>
      h(
        "div",
        {
          "data-testid": "content",
          "data-key": p.detail.row.key,
          "data-updated": p.updated.join(","),
          "data-short": String(p.geometry.short),
          "data-narrow": String(p.geometry.narrow),
          "data-order": p.comments.order,
          "data-new-since": String(p.comments.newSince),
          "data-expand": p.ctx.expandAll ? `${p.ctx.expandAll.open}:${p.ctx.expandAll.token}` : "",
          "data-focus-token": String(p.comments.composer.focusToken),
          "data-pending": p.comments.pending.map((x) => x.state).join(","),
          "data-pending-reason": p.comments.pending.map((x) => x.reason).join("|"),
        },
        h("button", { onClick: () => p.ctx.onOpenImage(201) }, "open image 201"),
        h("button", { onClick: () => p.ctx.onOpenImage(202) }, "open image 202"),
        h("button", { onClick: () => p.ctx.onOpenIssue(108) }, "open linked 108"),
        h("button", { onClick: () => p.ctx.onOpenFile(203) }, "open file 203"),
        h("button", { onClick: () => p.ctx.onLoadImage(202) }, "load image 202"),
        h("button", { onClick: p.onReload }, "reload"),
        h("button", { onClick: () => p.comments.composer.onSend({ text: "hello", mentions: [] }) }, "send comment"),
        h("button", { onClick: () => p.comments.onOrder("oldest") }, "order oldest"),
        h("button", { onClick: () => p.comments.onRetry(p.comments.pending[0]!.localId) }, "retry pending"),
        h("textarea", {
          "aria-label": "Comment",
          value: p.comments.composer.draft.text,
          onInput: (e: Event) => p.comments.composer.onChange({ text: (e.currentTarget as HTMLTextAreaElement).value, mentions: [] }),
          onBlur: () => p.comments.composer.onBlur(),
        }),
      ),
  };
});

const clone = <X,>(x: X): X => JSON.parse(JSON.stringify(x)) as X;
const STEPS = Array.from({ length: 20 }, (_, i) => 101 + i); // 107 = MOB-1421 (7 / 20), 109 = MOB-1429 (9 / 20)

/** Lets the load finish INSIDE act (the mock settles on microtasks), so the render and the first effects of the freshly
 *  mounted Comboboxes have run before anyone types. */
async function settle() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
  await screen.findByTestId("content");
}

let mock: MockApi;
async function mount(over: Partial<TicketModalProps> = {}, wait = true) {
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
  if (wait) {
    await settle();
  }
  return { p, ...view };
}
beforeEach(() => resetTicketState());
afterEach(() => {
  resetTicketState();
  ticketSession.value = null;
});
const dialog = () => screen.getByRole("dialog", { name: /MOB-\d+/ });
const getCalls = () => mock.calls.filter((c) => c.method === "ticket.get");

describe("opening a ticket", () => {
  it("callsOnlyTicketGet_ForTheOpenedTicket_AndPaintsTheHeader", async () => {
    await mount();
    expect(mock.calls.length).toBeGreaterThan(0);
    expect(mock.calls.every((c) => c.method === "ticket.get")).toBe(true);
    expect(getCalls().some((c) => c.args[0] === 1 && c.args[1] === 107)).toBe(true);
    expect(dialog().getAttribute("aria-modal")).toBe("true");
    expect(screen.getByText("MOB-1421")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Unit toggle between metric and US cups" })).toBeTruthy();
    expect((document.querySelector(".th") as HTMLElement).textContent).not.toContain("MOB-1200"); // no parent crumb in the header
    expect(screen.queryByText("7 / 20")).toBeNull(); // no stepper counter
    expect(screen.getByRole("button", { name: /refresh/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /open in jira/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Close" })).toBeTruthy();
    expect(screen.getByText(/^\u26A1\uFE0E\d{1,2}:\d{2}$/)).toBeTruthy();
  });

  it("opensAnotherTicket", async () => {
    await mount({ issueId: 109 });
    expect(screen.getByText("MOB-1429")).toBeTruthy();
    expect(screen.getByTestId("content").getAttribute("data-key")).toBe("MOB-1429");
  });

  it("showsTheRailWithTheStateControls_AndFetchesNoTransitionsUntilAsked", async () => {
    await mount();
    expect(screen.getByRole("combobox", { name: "Status" })).toBeTruthy();
    expect(screen.getByRole("combobox", { name: "Assignee" })).toBeTruthy();
    expect(screen.getByRole("switch", { name: /flag/i })).toBeTruthy();
    expect(mock.calls.every((c) => c.method === "ticket.get")).toBe(true);
  });

  it("showsTheErrorAndKeepsCloseUsable_WhenGetFails", async () => {
    mock = createMockApi({ handBuiltTickets: true });
    vi.spyOn(mock.ticket, "get").mockRejectedValue(new AppError("not_found", "Ticket not found."));
    setApi(mock);
    setEventBus(mock.bus);
    const onClose = vi.fn();
    render(<TicketModal standupId={1} issueId={4242} steps={STEPS} stack={[]} vw={1920} vh={1080} onStep={vi.fn()} onOpenLinked={vi.fn()} onBack={vi.fn()} onClose={onClose} />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Ticket not found.");
    expect(screen.queryByTestId("content")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe("header actions", () => {
  it("hasNoStepperButtons_SteppingIsKeyboardOnly", async () => {
    const { p } = await mount();
    expect(screen.queryByRole("button", { name: "Next ticket" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Previous ticket" })).toBeNull();
    fireEvent.keyDown(dialog(), { key: "j" });
    expect(p.onStep).toHaveBeenLastCalledWith(108);
    fireEvent.keyDown(dialog(), { key: "k" });
    expect(p.onStep).toHaveBeenLastCalledWith(106);
    expect(p.onStep).toHaveBeenCalledTimes(2);
    expect(screen.getByText("step")).toBeTruthy(); // the footer hint keeps J/K discoverable
  });

  it("closeAndOpenInJiraWork", async () => {
    const { p } = await mount();
    const openIssue = vi.spyOn(mock.app, "openIssue");
    fireEvent.click(screen.getByRole("button", { name: /open in jira/i }));
    expect(openIssue).toHaveBeenCalledTimes(1);
    expect(openIssue).toHaveBeenCalledWith("MOB-1421");
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(p.onClose).toHaveBeenCalledTimes(1);
  });
});

describe("inline cards and the back stack", () => {
  it("anInlineCardToASyncedIssue_CallsOnOpenLinked", async () => {
    const { p } = await mount();
    fireEvent.click(screen.getByText("open linked 108"));
    expect(p.onOpenLinked).toHaveBeenCalledTimes(1);
    expect(p.onOpenLinked).toHaveBeenCalledWith(108);
  });

  it("showsTheBackButtonWithThePreviousKey_AndGoesBack", async () => {
    const { p } = await mount({ issueId: 108, stack: [107] });
    expect(screen.getByTestId("content").getAttribute("data-key")).toBe("MOB-1428");
    fireEvent.click(screen.getByRole("button", { name: /MOB-1421/ }));
    expect(p.onBack).toHaveBeenCalledTimes(1);
  });

  it("hasNoBackButtonWithAnEmptyStack", async () => {
    await mount({ issueId: 108, stack: [] });
    expect(screen.getByText("MOB-1428")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /MOB-1421/ })).toBeNull();
  });
});

describe("modes: the footer, the ? popover, the strip", () => {
  const HINTS = /J\s*K\s*step\s*C\s*comment\s*S\s*status\s*A\s*assignee\s*F\s*flag\s*R\s*refresh\s*O\s*open in Jira\s*Esc\s*close/;
  const hints = (root: HTMLElement) => root.querySelector('[data-part="hints"]') as HTMLElement | null;

  it("tallWindow_ShowsTheKeyboardFooter", async () => {
    await mount({ vw: 1920, vh: 1080 });
    const footer = document.querySelector('[data-part="footer"]') as HTMLElement;
    expect(footer).not.toBeNull();
    expect(hints(footer)?.textContent?.replace(/\s+/g, " ")).toMatch(HINTS);
  });

  it("shortWindow_HidesTheFooter_AndKeepsTheHintsInAQuestionMarkPopover", async () => {
    await mount({ vw: 1280, vh: 720 });
    expect(screen.getByText("MOB-1421")).toBeTruthy();
    expect(document.querySelector('[data-part="footer"]')).toBeNull();
    expect(hints(document.body)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /keyboard shortcuts/i }));
    expect(hints(document.body)?.textContent?.replace(/\s+/g, " ")).toMatch(HINTS);
    fireEvent.click(screen.getByRole("button", { name: /keyboard shortcuts/i }));
    expect(hints(document.body)).toBeNull();
  });

  it("theShortFlagReachesTheContent", async () => {
    const tall = await mount({ vh: 1080 });
    expect(screen.getByTestId("content").getAttribute("data-short")).toBe("false");
    tall.unmount();
    await mount({ vh: 799, vw: 1920 });
    expect(screen.getByTestId("content").getAttribute("data-short")).toBe("true");
  });

  it("at1024TheRailLabelsStack_AndAt1920TheyDoNot", async () => {
    const small = await mount({ vw: 1024, vh: 640 });
    const rail = document.querySelector('[data-layout="column"]') as HTMLElement;
    expect(rail).not.toBeNull();
    expect(rail.getAttribute("data-stacked")).toBe("true");
    small.unmount();
    await mount({ vw: 1920, vh: 1080 });
    expect((document.querySelector('[data-layout="column"]') as HTMLElement).getAttribute("data-stacked")).toBe("false");
  });

  it("underTheNarrowBreakpoint_TheStateGroupBecomesAStripAndThereIsNoColumnRail", async () => {
    const narrow = await mount({ vw: 819, vh: 512 });
    expect(document.querySelector('[data-layout="strip"]')).not.toBeNull();
    expect(document.querySelector('[data-layout="column"]')).toBeNull();
    expect(screen.getByTestId("content").getAttribute("data-narrow")).toBe("true");
    narrow.unmount();
    await mount({ vw: 1920, vh: 1080 });
    expect(document.querySelector('[data-layout="column"]')).not.toBeNull();
    expect(document.querySelector('[data-layout="strip"]')).toBeNull();
  });
});

describe("comments wiring", () => {
  it("startsNewestFirst_PassesNewSince_AndPersistsTheOrderToggle", async () => {
    await mount();
    const content = () => screen.getByTestId("content");
    expect(content().getAttribute("data-order")).toBe("newest");
    expect(content().getAttribute("data-new-since")).toBe("1795132800000");
    const set = vi.spyOn(mock.app, "setAppSetting");
    fireEvent.click(screen.getByText("order oldest"));
    expect(set).toHaveBeenCalledTimes(1);
    expect(set).toHaveBeenCalledWith("ticket.comment_order", "oldest");
    await waitFor(() => expect(content().getAttribute("data-order")).toBe("oldest"));
  });

  it("aFailedSendLeavesAFailedBubble_WithOneCall_AndRetryResendsOnce", async () => {
    await mount();
    const add = vi.spyOn(mock.ticket, "addComment").mockRejectedValue(new AppError("offline", "Couldn't reach Jira (offline).", "", true));
    fireEvent.click(screen.getByText("send comment"));
    expect(add).toHaveBeenCalledTimes(1);
    expect(add).toHaveBeenCalledWith(1, 107, { text: "hello", mentions: [] });
    await waitFor(() => expect(screen.getByTestId("content").getAttribute("data-pending")).toBe("failed"));
    expect(screen.getByTestId("content").getAttribute("data-pending-reason")).toMatch(/^Couldn't reach Jira/);
    expect(add).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByText("retry pending"));
    expect(add).toHaveBeenCalledTimes(2);
  });

  it("savesTheComposerTextWhenTheComposerLosesFocus", async () => {
    await mount(); // the 400 ms debounce itself is pinned with fake timers in state/ticket.draft.test.ts
    const save = vi.spyOn(mock.ticket, "saveDraft");
    const box = screen.getByLabelText("Comment");
    fireEvent.input(box, { target: { value: "half a thought" } });
    expect(save).not.toHaveBeenCalled();
    fireEvent.blur(box);
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1), { timeout: 300 });
    expect(save).toHaveBeenCalledWith(107, { text: "half a thought", mentions: [] });
  });

  it("flushesTheDraftWhenTheModalUnmounts_WithoutWaitingForTheDebounce", async () => {
    const { unmount } = await mount();
    const save = vi.spyOn(mock.ticket, "saveDraft");
    fireEvent.input(screen.getByLabelText("Comment"), { target: { value: "bye" } });
    unmount();
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1), { timeout: 300 });
    expect(save).toHaveBeenCalledWith(107, { text: "bye", mentions: [] });
  });
});

describe("end to end with the session state: stepping, links, back, close", () => {
  function Harness() {
    const s = ticketSession.value;
    if (!s) return <div>closed</div>;
    return (
      <TicketModal
        standupId={s.standupId} issueId={s.issueId} steps={s.steps} stack={s.stack} vw={1920} vh={1080}
        onStep={stepToTicket} onOpenLinked={openLinkedTicket} onBack={backTicket} onClose={closeTicket}
      />
    );
  }
  async function start() {
    mock = createMockApi({ handBuiltTickets: true });
    setApi(mock);
    setEventBus(mock.bus);
    views.value = new Map([[1, clone(await mock.standup.view(1))]]);
    openTicket(1, 107, STEPS);
    render(<Harness />);
    await screen.findByTestId("content");
  }
  const key = () => screen.getByTestId("content").getAttribute("data-key");

  it("stepsWithTheKeys_SwappingTheContent", async () => {
    await start();
    expect(key()).toBe("MOB-1421");
    fireEvent.keyDown(dialogByKey("MOB-1421"), { key: "j" });
    await waitFor(() => expect(key()).toBe("MOB-1428"));
    fireEvent.keyDown(dialogByKey("MOB-1428"), { key: "k" });
    await waitFor(() => expect(key()).toBe("MOB-1421"));
  });

  it("opensALinkedIssueWithABackStack_BackspaceReturns_EscClosesTheModal", async () => {
    await start();
    fireEvent.click(screen.getByText("open linked 108"));
    await waitFor(() => expect(key()).toBe("MOB-1428"));
    expect(screen.getByRole("button", { name: /MOB-1421/ })).toBeTruthy();
    fireEvent.keyDown(dialogByKey("MOB-1428"), { key: "Backspace" });
    await waitFor(() => expect(key()).toBe("MOB-1421"));
    expect(screen.queryByRole("button", { name: /MOB-1428/ })).toBeNull();
    fireEvent.keyDown(dialogByKey("MOB-1421"), { key: "Escape" });
    await waitFor(() => expect(screen.getByText("closed")).toBeTruthy());
    expect(ticketSession.value).toBeNull();
  });
});

function dialogByKey(key: string): HTMLElement {
  return screen.getByRole("dialog", { name: new RegExp(key) });
}

describe("the rail's parent row", () => {
  it("opensTheParentInTheModalWhenItIsSynced", async () => {
    // MOB-1200 (issueId 900) is a synced issue in the fixtures: it opens as a linked ticket (back stack).
    const { p } = await mount();
    fireEvent.click(screen.getByRole("button", { name: /MOB-1200/ }));
    expect(p.onOpenLinked).toHaveBeenCalledTimes(1);
    expect(p.onOpenLinked).toHaveBeenCalledWith(900);
  });

  it("opensTheParentInTheBrowserWhenItIsNotSynced", async () => {
    mock = createMockApi({ handBuiltTickets: true });
    const detail = clone(await mock.ticket.get(1, 107));
    detail.parent = { issueId: 0, key: "MOB-1200", summary: "Recipe units v2", typeKind: "epic" };
    vi.spyOn(mock.ticket, "get").mockResolvedValue(detail);
    const openIssue = vi.spyOn(mock.app, "openIssue");
    setApi(mock);
    setEventBus(mock.bus);
    const onOpenLinked = vi.fn();
    render(<TicketModal standupId={1} issueId={107} steps={STEPS} stack={[]} vw={1920} vh={1080} onStep={vi.fn()} onOpenLinked={onOpenLinked} onBack={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: /MOB-1200/ }));
    expect(openIssue).toHaveBeenCalledTimes(1);
    expect(openIssue).toHaveBeenCalledWith("MOB-1200");
    expect(onOpenLinked).not.toHaveBeenCalled();
  });
});

describe("the mask", () => {
  it("aClickOnTheMaskClosesTheModal_AClickInsideDoesNot", async () => {
    const { p } = await mount();
    const scrim = document.querySelector(".tm-scrim") as HTMLElement;
    const dlg = screen.getByRole("dialog");
    fireEvent.mouseDown(dlg);
    fireEvent.click(dlg);
    expect(p.onClose).not.toHaveBeenCalled();
    fireEvent.mouseDown(scrim);
    fireEvent.click(scrim);
    expect(p.onClose).toHaveBeenCalledTimes(1);
  });

  it("aDragThatStartsInsideAndEndsOnTheMaskDoesNotClose", async () => {
    const { p } = await mount();
    const scrim = document.querySelector(".tm-scrim") as HTMLElement;
    fireEvent.mouseDown(screen.getByRole("dialog"));
    fireEvent.click(scrim); // a text selection released over the mask
    expect(p.onClose).not.toHaveBeenCalled();
  });
});
