// WP-T7 tests: ticket modal state, part 4 (comment draft autosave). Spec: architecture §4.9 "Draft autosave"
// (§5.3 pipeline, key draft:<issueId>, 400 ms debounce, flush on blur / close / pagehide, does not drive the Configure
// "Saved" indicator) and ux-spec §14.5 (autosaved per issue).
//
// SPEC-NOTE: `editDraft(input)` updates `ticketDraft` synchronously and (re)starts a 400 ms timer; the timer fires
//   TicketService.SaveDraft(issueId, lastInput) once. An empty text is saved too (Go deletes the stored draft).
// SPEC-NOTE: `flushDraft()` saves a pending change at once and cancels its timer; with nothing pending it makes no
//   call. Loading another issue, `closeTicket()` and the window's `pagehide` event all flush the draft of the issue it
//   belonged to (saved under THAT issue's id), so a draft is never lost or filed under the wrong ticket.
// SPEC-NOTE: the draft pipeline never touches `saveState` of state/save.ts (the Configure indicator).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setEventBus } from "../api/events";
import { setApi } from "../api/index";
import { createMockApi, type MockApi } from "../api/mock";
import type { CommentInput } from "../api/types";
import { saveState } from "./save";
import * as S from "./ticket";

const draft = (text: string): CommentInput => ({ text, mentions: [] });

let mock: MockApi;
beforeEach(async () => {
  vi.useFakeTimers();
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

describe("editDraft: 400 ms debounce", () => {
  it("editDraft_UpdatesTheSignalAtOnce_AndSavesOnlyAfter400msOfQuiet", async () => {
    const save = vi.spyOn(mock.ticket, "saveDraft");
    S.editDraft(draft("hello"));
    expect(S.ticketDraft.value).toEqual(draft("hello"));
    await vi.advanceTimersByTimeAsync(399);
    expect(save).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith(107, draft("hello"));
  });

  it("editDraft_RapidEdits_SaveOnceWithTheLastValue_400msAfterTheLastEdit", async () => {
    const save = vi.spyOn(mock.ticket, "saveDraft");
    S.editDraft(draft("h"));
    await vi.advanceTimersByTimeAsync(300);
    S.editDraft(draft("he"));
    await vi.advanceTimersByTimeAsync(300);
    S.editDraft(draft("hel"));
    await vi.advanceTimersByTimeAsync(399);
    expect(save).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith(107, draft("hel"));
  });

  it("editDraft_EmptyText_IsSavedToo_SoTheStoredDraftIsDeleted", async () => {
    const save = vi.spyOn(mock.ticket, "saveDraft");
    S.editDraft(draft("x"));
    await vi.advanceTimersByTimeAsync(400);
    S.editDraft(draft(""));
    await vi.advanceTimersByTimeAsync(400);
    expect(save).toHaveBeenCalledTimes(2);
    expect(save).toHaveBeenLastCalledWith(107, draft(""));
  });

  it("editDraft_DoesNotDriveTheConfigureSavedIndicator", async () => {
    const save = vi.spyOn(mock.ticket, "saveDraft");
    S.editDraft(draft("hello"));
    await vi.advanceTimersByTimeAsync(400);
    expect(save).toHaveBeenCalledTimes(1);
    expect(saveState.value).toBe("idle");
  });
});

describe("flushDraft and the other flush points", () => {
  it("flushDraft_SavesAtOnce_ThenHasNothingLeftToSave", async () => {
    const save = vi.spyOn(mock.ticket, "saveDraft");
    S.editDraft(draft("hello"));
    await S.flushDraft();
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith(107, draft("hello"));
    await S.flushDraft();
    await vi.advanceTimersByTimeAsync(1000);
    expect(save).toHaveBeenCalledTimes(1); // neither the second flush nor the cancelled timer saved again
  });

  it("flushDraft_WithNothingPending_MakesNoCall", async () => {
    const save = vi.spyOn(mock.ticket, "saveDraft");
    await S.flushDraft();
    expect(save).not.toHaveBeenCalled();
    S.editDraft(draft("a"));
    await S.flushDraft();
    expect(save).toHaveBeenCalledTimes(1); // control: the spy is live
  });

  it("loadTicket_OfAnotherIssue_FlushesTheOldDraftUnderTheOldIssueId", async () => {
    const save = vi.spyOn(mock.ticket, "saveDraft");
    S.editDraft(draft("for 107"));
    await S.loadTicket(1, 108);
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith(107, draft("for 107"));
    await vi.advanceTimersByTimeAsync(1000);
    expect(save).toHaveBeenCalledTimes(1);
  });

  it("closeTicket_FlushesTheDraft", async () => {
    const save = vi.spyOn(mock.ticket, "saveDraft");
    S.openTicket(1, 107, [107, 108]);
    S.editDraft(draft("unsent thought"));
    S.closeTicket();
    await vi.advanceTimersByTimeAsync(0);
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith(107, draft("unsent thought"));
  });

  it("pagehide_FlushesThePendingDraft", async () => {
    const save = vi.spyOn(mock.ticket, "saveDraft");
    S.editDraft(draft("closing the window"));
    window.dispatchEvent(new Event("pagehide"));
    await vi.advanceTimersByTimeAsync(0);
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith(107, draft("closing the window"));
  });
});
