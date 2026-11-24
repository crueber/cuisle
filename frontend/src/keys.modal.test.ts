// WP-T9 tests: global keys are suspended while the ticket modal is open (ux-spec §14.10: "While the modal is open, R never
// starts a board resync"). The modal's own document listener handles J/K, R, ...; the window-level handler must not also act.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi, type Api } from "./api";
import { handleGlobalKey } from "./keys";
import { route } from "./state/route";
import { closeTicket, openTicket, ticketSession } from "./state/ticket";
import { keyLegendOpen, showToast, toasts } from "./state/ui";

const key = (k: string, init: KeyboardEventInit = {}) => new KeyboardEvent("keydown", { key: k, bubbles: true, ...init });

let start: ReturnType<typeof vi.fn>;
beforeEach(() => {
  start = vi.fn(async () => 1);
  setApi({ sync: { start }, app: { setUIState: vi.fn(async () => undefined) } } as unknown as Api);
  route.value = { view: "standup", standupId: 1, person: "ana" };
  keyLegendOpen.value = false;
  ticketSession.value = null;
});
afterEach(() => {
  closeTicket();
  setApi(wailsApi);
});

describe("global keys with the ticket modal open", () => {
  it("R starts a resync with no modal (control)", () => {
    expect(handleGlobalKey(key("r"))).toBe(true);
    expect(start).toHaveBeenCalledTimes(1);
  });

  it.each(["r", "R", "ArrowLeft", "ArrowRight", "b", "1", ",", "/", "?", "Escape"])("%s is not handled while the modal is open", (k) => {
    openTicket(1, 4, [2, 4, 8]);
    expect(handleGlobalKey(key(k))).toBe(false);
    expect(start).not.toHaveBeenCalled();
    expect(keyLegendOpen.value).toBe(false);
    expect(route.value).toEqual({ view: "standup", standupId: 1, person: "ana" });
  });

  it("Ctrl/Cmd+Z does not run an Undo toast behind the modal, and does once it is closed", () => {
    const undo = vi.fn();
    toasts.value = [];
    showToast({ message: "Deleted", action: { label: "Undo", run: undo }, timeoutMs: 10_000 });
    openTicket(1, 4, [2, 4, 8]);
    expect(handleGlobalKey(key("z", { ctrlKey: true }))).toBe(false);
    expect(handleGlobalKey(key("z", { metaKey: true }))).toBe(false);
    expect(undo).not.toHaveBeenCalled();
    closeTicket();
    expect(handleGlobalKey(key("z", { ctrlKey: true }))).toBe(true);
    expect(undo).toHaveBeenCalledTimes(1);
  });

  it("the keys work again once the modal is closed", () => {
    openTicket(1, 4, [2, 4, 8]);
    closeTicket();
    expect(handleGlobalKey(key("r"))).toBe(true);
    expect(start).toHaveBeenCalledTimes(1);
  });
});
