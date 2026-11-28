// WP-T7 tests: TicketModal, part 2 (the modal's own keyboard map and Esc unwinding). Spec: ux-spec §14.1 "Esc unwinds
// one layer at a time", §14.10, testing §4 "Stepping order" and "Esc unwinding".
//
// SPEC-NOTE: the modal handles its keys ITSELF (global keys.ts belongs to WP-T9 and is suspended while the modal is
//   open). Every key is dispatched here on an element INSIDE the modal (the dialog, or the focused control) and bubbles
//   to the modal's handler, wherever it attaches (the dialog element or document). Keys with Ctrl/Meta/Alt are ignored.
//   Keys are ignored when the event target is an input, textarea, contenteditable or role=combobox (so typing "j" in the
//   Status filter or the composer never steps and Backspace never goes back).
// SPEC-NOTE: key map under test (ux-spec §14.10): J / → next and K / ← previous (stop at the ends, call props.onStep with
//   the neighbour id from props.steps; case-insensitive); C bumps the composer focusToken by exactly 1; S / A open the Status
//   / Assignee combobox (S loads transitions once); F toggles the flag; R refreshes this ticket (api.ticket.refresh once);
//   O opens the ticket in Jira (api.app.openIssue(key)); E toggles expand-all (ctx.expandAll {open, token}: first press
//   open=true, second open=false, token +1 each); Backspace calls onBack only when props.stack is non-empty.
// SPEC-NOTE: Esc unwinds one layer per press, innermost first: an open combobox (its own Escape handler closes it and
//   preventDefaults; the modal must then NOT also close) → an open lightbox (closes only the lightbox) → the focused
//   composer (blur, which flushes the draft; focus stays inside the modal) → the modal (props.onClose, once). While the
//   lightbox is open ←/→ step its images, not the tickets.
import { act, fireEvent, render, screen, waitFor } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setEventBus } from "../../api/events";
import { setApi } from "../../api/index";
import { createMockApi, type MockApi } from "../../api/mock";
import { views } from "../../state/standup";
import { resetTicketState, ticketDetail, ticketSession } from "../../state/ticket";
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
          "data-expand": p.ctx.expandAll ? `${p.ctx.expandAll.open}:${p.ctx.expandAll.token}` : "",
          "data-focus-token": String(p.comments.composer.focusToken),
        },
        h("button", { onClick: () => p.ctx.onOpenImage(201) }, "open image 201"),
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
const STEPS = Array.from({ length: 20 }, (_, i) => 101 + i);

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
const dialog = () => screen.getByRole("dialog", { name: /MOB-\d+/ });
const press = (key: string, init: KeyboardEventInit = {}, target: Element = dialog()) => fireEvent.keyDown(target, { key, ...init });
const statusInput = () => screen.getByRole("combobox", { name: "Status" }) as HTMLInputElement;
const assigneeInput = () => screen.getByRole("combobox", { name: "Assignee" }) as HTMLInputElement;
const tokenOf = () => Number(screen.getByTestId("content").getAttribute("data-focus-token"));

describe("stepping keys", () => {
  it.each([["j"], ["J"], ["ArrowRight"]])("%s_StepsToTheNextTicket", async (key) => {
    const { p } = await mount();
    press(key);
    expect(p.onStep).toHaveBeenCalledTimes(1);
    expect(p.onStep).toHaveBeenCalledWith(108);
  });

  it.each([["k"], ["K"], ["ArrowLeft"]])("%s_StepsToThePreviousTicket", async (key) => {
    const { p } = await mount();
    press(key);
    expect(p.onStep).toHaveBeenCalledTimes(1);
    expect(p.onStep).toHaveBeenCalledWith(106);
  });

  it("stopsAtTheFirstTicket_KeepsGoingForward", async () => {
    const { p } = await mount({ issueId: 101 });
    press("k");
    press("ArrowLeft");
    expect(p.onStep).not.toHaveBeenCalled();
    press("j");
    expect(p.onStep).toHaveBeenCalledWith(102);
  });

  it("stopsAtTheLastTicket_KeepsGoingBack", async () => {
    const { p } = await mount({ issueId: 120 });
    press("j");
    press("ArrowRight");
    expect(p.onStep).not.toHaveBeenCalled();
    press("k");
    expect(p.onStep).toHaveBeenCalledWith(119);
  });

  it("ignoresKeysWithModifiers", async () => {
    const { p } = await mount();
    press("j", { ctrlKey: true });
    press("j", { metaKey: true });
    press("ArrowRight", { altKey: true });
    expect(p.onStep).not.toHaveBeenCalled();
    press("j"); // control
    expect(p.onStep).toHaveBeenCalledTimes(1);
  });

  it("doesNotStepWhileTypingInTheComposer", async () => {
    const { p } = await mount();
    const box = screen.getByLabelText("Comment");
    press("j", {}, box);
    press("ArrowRight", {}, box);
    press("k", {}, box);
    expect(p.onStep).not.toHaveBeenCalled();
    press("j"); // control: the same key on the dialog steps
    expect(p.onStep).toHaveBeenCalledWith(108);
  });

  it("doesNotStepWhileTypingInTheStatusFilter", async () => {
    const { p } = await mount();
    press("s");
    await screen.findByRole("listbox");
    press("j", {}, statusInput());
    press("ArrowLeft", {}, statusInput());
    expect(p.onStep).not.toHaveBeenCalled();
    expect(screen.getByRole("listbox")).toBeTruthy();
  });
});

describe("action keys", () => {
  it("c_BumpsTheComposerFocusTokenByOne", async () => {
    await mount();
    const before = tokenOf();
    press("c");
    expect(tokenOf()).toBe(before + 1);
    press("C");
    expect(tokenOf()).toBe(before + 2);
  });

  it("s_OpensTheStatusList_AndFetchesTransitionsOnce", async () => {
    await mount();
    expect(mock.calls.filter((c) => c.method === "ticket.transitions")).toHaveLength(0);
    press("s");
    expect(await screen.findByRole("listbox")).toBeTruthy();
    await waitFor(() => expect(mock.calls.filter((c) => c.method === "ticket.transitions")).toEqual([{ method: "ticket.transitions", args: [107] }]));
    expect(await screen.findByRole("option", { name: /In Review/ })).toBeTruthy();
  });

  it("a_OpensTheAssigneeCombobox", async () => {
    await mount();
    expect(assigneeInput().getAttribute("aria-expanded")).toBe("false");
    press("a");
    await waitFor(() => expect(assigneeInput().getAttribute("aria-expanded")).toBe("true"));
    expect(statusInput().getAttribute("aria-expanded")).toBe("false");
  });

  it("sAndA_DoNotLatchOpenWhenFieldsLaterBecomeEnabled", async () => {
    mock = createMockApi({ handBuiltTickets: true });
    setApi(mock);
    setEventBus(mock.bus);
    views.value = new Map([[1, clone(await mock.standup.view(1))]]);
    const disabledDetail = clone(await mock.ticket.get(1, 107));
    disabledDetail.can.transition = false;
    disabledDetail.can.assign = false;
    const enabledDetail = clone(disabledDetail);
    enabledDetail.can.transition = true;
    enabledDetail.can.assign = true;
    vi.spyOn(mock.ticket, "get").mockResolvedValueOnce(disabledDetail);
    mock.resetCalls();
    render(
      <TicketModal
        standupId={1}
        issueId={107}
        steps={STEPS}
        stack={[]}
        vw={1920}
        vh={1080}
        onStep={vi.fn()}
        onOpenLinked={vi.fn()}
        onBack={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    await settle();
    // Press S and A while both fields are disabled: neither may latch open state.
    press("s");
    press("a");
    expect(screen.queryByRole("listbox")).toBeNull();
    // Re-enable both fields (e.g., reconnect or write completion): popups must remain closed and no fetch fires.
    act(() => {
      ticketDetail.value = enabledDetail;
    });
    await settle();
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(statusInput().getAttribute("aria-expanded")).toBe("false");
    expect(assigneeInput().getAttribute("aria-expanded")).toBe("false");
    expect(mock.calls.filter((c) => c.method === "ticket.transitions")).toHaveLength(0);
    expect(mock.calls.filter((c) => c.method === "ticket.searchUsers")).toHaveLength(0);
  });

  it("f_TogglesTheFlag_OptimisticallyThenBack", async () => {
    await mount();
    const flag = () => screen.getByRole("switch", { name: /flag/i });
    expect(flag().getAttribute("aria-checked")).toBe("false");
    const spy = vi.spyOn(mock.ticket, "setFlag");
    press("f");
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith(1, 107, true);
    await waitFor(() => expect(flag().getAttribute("aria-checked")).toBe("true"));
    await waitFor(() => expect((flag() as HTMLButtonElement).disabled).toBe(false));
    press("F");
    expect(spy).toHaveBeenCalledTimes(2);
    expect(spy).toHaveBeenLastCalledWith(1, 107, false);
  });

  it("r_RefreshesThisTicketOnce", async () => {
    await mount();
    const spy = vi.spyOn(mock.ticket, "refresh");
    press("r");
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith(1, 107);
    press("r"); // the cooldown (and the pending call) swallow a second press
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("o_OpensTheTicketInJira", async () => {
    await mount();
    const spy = vi.spyOn(mock.app, "openIssue");
    press("o");
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith("MOB-1421");
  });

  it("e_TogglesExpandAllWithAGrowingToken", async () => {
    await mount();
    const expand = () => screen.getByTestId("content").getAttribute("data-expand") ?? "";
    expect(expand()).toBe("");
    press("e");
    await waitFor(() => expect(expand()).toMatch(/^true:\d+$/));
    const first = Number(expand().split(":")[1]);
    press("E");
    await waitFor(() => expect(expand()).toMatch(/^false:\d+$/));
    expect(Number(expand().split(":")[1])).toBeGreaterThan(first);
  });

  it("backspace_GoesBackOnlyWithANonEmptyStack_AndNeverWhileTyping", async () => {
    const empty = await mount({ stack: [] });
    press("Backspace");
    expect(empty.p.onBack).not.toHaveBeenCalled();
    press("j"); // control: keys work on this dialog
    expect(empty.p.onStep).toHaveBeenCalledTimes(1);
    empty.unmount();

    const stacked = await mount({ stack: [105] });
    press("Backspace", {}, screen.getByLabelText("Comment"));
    expect(stacked.p.onBack).not.toHaveBeenCalled();
    press("Backspace");
    expect(stacked.p.onBack).toHaveBeenCalledTimes(1);
  });
});

describe("Esc unwinds one layer at a time", () => {
  it("withNoLayerOpen_EscClosesTheModalOnce", async () => {
    const { p } = await mount();
    press("Escape");
    expect(p.onClose).toHaveBeenCalledTimes(1);
  });

  it("anOpenCombobox_ClosesFirst_ThenTheModal", async () => {
    const { p } = await mount();
    press("s");
    await screen.findByRole("listbox");
    press("Escape", {}, statusInput());
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    expect(p.onClose).not.toHaveBeenCalled();
    press("Escape", {}, statusInput());
    expect(p.onClose).toHaveBeenCalledTimes(1);
  });

  it("anOpenLightbox_ClosesFirst_ThenTheModal", async () => {
    const { p } = await mount();
    fireEvent.click(screen.getByText("open image 201"));
    expect(screen.getByRole("dialog", { name: "Image viewer" })).toBeTruthy();
    press("Escape", {}, screen.getByRole("dialog", { name: "Image viewer" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Image viewer" })).toBeNull());
    expect(p.onClose).not.toHaveBeenCalled();
    press("Escape");
    expect(p.onClose).toHaveBeenCalledTimes(1);
  });

  it("theFocusedComposer_BlursFirstAndKeepsTheDraft_ThenTheModalCloses", async () => {
    const { p } = await mount();
    const save = vi.spyOn(mock.ticket, "saveDraft");
    const box = screen.getByLabelText("Comment") as HTMLTextAreaElement;
    box.focus();
    expect(document.activeElement).toBe(box);
    fireEvent.input(box, { target: { value: "keep me" } });
    press("Escape", {}, box);
    expect(document.activeElement).not.toBe(box);
    expect(dialog().contains(document.activeElement)).toBe(true);
    expect(p.onClose).not.toHaveBeenCalled();
    await waitFor(() => expect(save).toHaveBeenCalledWith(107, { text: "keep me", mentions: [] }), { timeout: 300 });
    expect((screen.getByLabelText("Comment") as HTMLTextAreaElement).value).toBe("keep me");
    press("Escape");
    expect(p.onClose).toHaveBeenCalledTimes(1);
  });

  it("arrowKeysStepTheLightboxImages_NotTheTickets_WhileItIsOpen", async () => {
    const { p } = await mount();
    fireEvent.click(screen.getByText("open image 201"));
    await screen.findByRole("dialog", { name: "Image viewer" });
    expect(screen.getByText("1 / 2")).toBeTruthy();
    press("ArrowRight", {}, screen.getByRole("dialog", { name: "Image viewer" }));
    await waitFor(() => expect(screen.getByText("2 / 2")).toBeTruthy());
    expect(p.onStep).not.toHaveBeenCalled();
    press("Escape", {}, screen.getByRole("dialog", { name: "Image viewer" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Image viewer" })).toBeNull());
    press("ArrowRight");
    expect(p.onStep).toHaveBeenCalledTimes(1);
    expect(p.onStep).toHaveBeenCalledWith(108);
  });
});
