// F14 keys: ⌘K / Ctrl+K opens the palette everywhere (also over the ticket modal and in a field), T opens it when no field
// has focus, and nothing else acts while it is open (ux-spec §8, §15.1).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi, type Api } from "./api";
import { handleGlobalKey } from "./keys";
import { paletteOpen, closePalette } from "./state/palette";
import { route } from "./state/route";
import { closeTicket, openTicket, ticketSession } from "./state/ticket";

const key = (k: string, init: KeyboardEventInit = {}, target?: Element) => {
  const e = new KeyboardEvent("keydown", { key: k, bubbles: true, ...init });
  if (target) Object.defineProperty(e, "target", { value: target });
  return e;
};

let start: ReturnType<typeof vi.fn>;
beforeEach(() => {
  start = vi.fn(async () => 1);
  setApi({ sync: { start }, app: { setUIState: vi.fn(async () => undefined) } } as unknown as Api);
  route.value = { view: "standup", standupId: 1, person: null };
  ticketSession.value = null;
  closePalette();
});
afterEach(() => {
  closeTicket();
  closePalette();
  setApi(wailsApi);
});

describe("opening the palette", () => {
  it("Ctrl+K and Cmd+K open it, and press again closes it", () => {
    expect(handleGlobalKey(key("k", { ctrlKey: true }))).toBe(true);
    expect(paletteOpen.value).toEqual({ mode: "" });
    expect(handleGlobalKey(key("K", { metaKey: true }))).toBe(true);
    expect(paletteOpen.value).toBeNull();
    expect(handleGlobalKey(key("k", { metaKey: true }))).toBe(true);
    expect(paletteOpen.value).not.toBeNull();
  });

  it("opens over Configure and over the ticket modal", () => {
    route.value = { view: "configure", standupId: 1 };
    handleGlobalKey(key("k", { ctrlKey: true }));
    expect(paletteOpen.value).not.toBeNull();
    closePalette();
    route.value = { view: "standup", standupId: 1, person: null };
    openTicket(1, 4, [4]);
    expect(handleGlobalKey(key("k", { ctrlKey: true }))).toBe(true);
    expect(paletteOpen.value).not.toBeNull();
  });

  it("opens from inside a field too (the shortcut wins over the field)", () => {
    const input = document.createElement("input");
    document.body.append(input);
    expect(handleGlobalKey(key("k", { ctrlKey: true }, input))).toBe(true);
    expect(paletteOpen.value).not.toBeNull();
    input.remove();
  });

  it("does not open on the Connect screen", () => {
    route.value = { view: "connect" };
    handleGlobalKey(key("k", { ctrlKey: true }));
    expect(paletteOpen.value).toBeNull();
  });

  it("T opens it when no field has focus, on the standup and on Configure", () => {
    expect(handleGlobalKey(key("t"))).toBe(true);
    expect(paletteOpen.value).not.toBeNull();
    closePalette();
    route.value = { view: "configure", standupId: 1 };
    expect(handleGlobalKey(key("T"))).toBe(true);
    expect(paletteOpen.value).not.toBeNull();
  });

  it("T types into a field instead, and does nothing behind the ticket modal", () => {
    const input = document.createElement("input");
    document.body.append(input);
    expect(handleGlobalKey(key("t", {}, input))).toBe(false);
    input.remove();
    openTicket(1, 4, [4]);
    expect(handleGlobalKey(key("t"))).toBe(false);
    expect(paletteOpen.value).toBeNull();
  });
});

describe("keys while it is open", () => {
  it("no other global key acts behind the palette", () => {
    handleGlobalKey(key("k", { ctrlKey: true }));
    for (const k of ["r", "b", "g", "ArrowLeft", "1", ",", "?", "Escape", "t"]) expect(handleGlobalKey(key(k)), k).toBe(false);
    expect(start).not.toHaveBeenCalled();
    expect(route.value).toEqual({ view: "standup", standupId: 1, person: null });
  });
});
