// F15 keys (ux-spec §8, §16.2): + / = zoom in, − / _ zoom out, 0 resets; ⌘/Ctrl with them do the same and are always
// swallowed (preventDefault) so the webview never applies its own page zoom. They are ignored while typing and when the
// ticket modal or the palette is open, and 0 never collides with the 1-9 standup digits.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi, type Api } from "./api";
import { handleGlobalKey, installGlobalKeys } from "./keys";
import { closePalette, openPalette } from "./state/palette";
import { route } from "./state/route";
import { closeTicket, openTicket, ticketSession } from "./state/ticket";
import { seedZooms, zoomOf } from "./state/zoom";

const key = (k: string, init: KeyboardEventInit = {}, target?: Element) => {
  const e = new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true, ...init });
  if (target) Object.defineProperty(e, "target", { value: target });
  return e;
};

let setUIState: ReturnType<typeof vi.fn>;
beforeEach(() => {
  setUIState = vi.fn(async () => undefined);
  setApi({ app: { setUIState } } as unknown as Api);
  seedZooms({});
  route.value = { view: "standup", standupId: 1, person: null };
  ticketSession.value = null;
  closePalette();
});
afterEach(() => {
  closeTicket();
  closePalette();
  seedZooms({});
  setApi(wailsApi);
});

describe("plain keys", () => {
  it("+ and = zoom in, - _ and the minus sign zoom out, 0 resets", () => {
    expect(handleGlobalKey(key("+", { shiftKey: true }))).toBe(true);
    expect(zoomOf(1)).toBe(110);
    expect(handleGlobalKey(key("="))).toBe(true);
    expect(zoomOf(1)).toBe(125);
    expect(handleGlobalKey(key("-"))).toBe(true);
    expect(zoomOf(1)).toBe(110);
    expect(handleGlobalKey(key("_", { shiftKey: true }))).toBe(true);
    expect(zoomOf(1)).toBe(100);
    expect(handleGlobalKey(key("−"))).toBe(true);
    expect(zoomOf(1)).toBe(90);
    expect(handleGlobalKey(key("0"))).toBe(true);
    expect(zoomOf(1)).toBe(100);
  });

  it("persists each change with the standup id", () => {
    handleGlobalKey(key("="));
    expect(setUIState).toHaveBeenLastCalledWith({ standupId: 1, zoom: 110 });
  });

  it("works on the person view too, for the same standup", () => {
    route.value = { view: "standup", standupId: 1, person: "acc-1" };
    handleGlobalKey(key("="));
    expect(zoomOf(1)).toBe(110);
  });

  it("is handled (true) at the ends of the range, so the key is still swallowed", () => {
    seedZooms({ "1": 200 });
    expect(handleGlobalKey(key("="))).toBe(true);
    expect(zoomOf(1)).toBe(200);
  });

  it("does not touch the 1-9 standup digits: 0 is free", () => {
    const e = key("0");
    expect(handleGlobalKey(e)).toBe(true);
    expect(zoomOf(1)).toBe(100);
    expect(setUIState).not.toHaveBeenCalled(); // already 100: nothing to save, and no standup switch either
  });

  it("is ignored while typing in a field", () => {
    const input = document.createElement("input");
    document.body.append(input);
    expect(handleGlobalKey(key("=", {}, input))).toBe(false);
    expect(zoomOf(1)).toBe(100);
    input.remove();
  });

  it("is ignored off the standup screen", () => {
    route.value = { view: "configure", standupId: 1 };
    expect(handleGlobalKey(key("="))).toBe(false);
    expect(zoomOf(1)).toBe(100);
  });

  it("is ignored while the ticket modal or the palette is open", () => {
    openTicket(1, 4, [4]);
    expect(handleGlobalKey(key("="))).toBe(false);
    closeTicket();
    openPalette();
    expect(handleGlobalKey(key("="))).toBe(false);
    expect(zoomOf(1)).toBe(100);
  });
});

describe("⌘ / Ctrl with + = − 0", () => {
  it.each([
    ["=", { metaKey: true }, 110],
    ["+", { metaKey: true, shiftKey: true }, 110],
    ["=", { ctrlKey: true }, 110],
    ["-", { metaKey: true }, 90],
    ["-", { ctrlKey: true }, 90],
  ])("%s with %j sets %i%%", (k, mods, want) => {
    expect(handleGlobalKey(key(k, mods))).toBe(true);
    expect(zoomOf(1)).toBe(want);
  });

  it("⌘0 resets", () => {
    seedZooms({ "1": 150 });
    expect(handleGlobalKey(key("0", { metaKey: true }))).toBe(true);
    expect(zoomOf(1)).toBe(100);
  });

  it("is prevented through the installed listener, so the webview never page-zooms", () => {
    const off = installGlobalKeys(window);
    for (const [k, mods] of [["=", { metaKey: true }], ["-", { ctrlKey: true }], ["0", { metaKey: true }], ["+", { ctrlKey: true, shiftKey: true }]] as const) {
      const e = key(k, mods);
      window.dispatchEvent(e);
      expect(e.defaultPrevented, `${k} ${JSON.stringify(mods)}`).toBe(true);
    }
    off();
  });

  it("is swallowed even where it does nothing (Configure, a field, the modal, the palette), still no page zoom", () => {
    route.value = { view: "configure", standupId: 1 };
    expect(handleGlobalKey(key("=", { metaKey: true }))).toBe(true);
    route.value = { view: "standup", standupId: 1, person: null };
    const input = document.createElement("input");
    document.body.append(input);
    expect(handleGlobalKey(key("=", { ctrlKey: true }, input))).toBe(true);
    input.remove();
    openTicket(1, 4, [4]);
    expect(handleGlobalKey(key("-", { metaKey: true }))).toBe(true);
    closeTicket();
    openPalette();
    expect(handleGlobalKey(key("0", { metaKey: true }))).toBe(true);
    expect(zoomOf(1)).toBe(100); // none of those zoomed
  });

  it("Alt combinations are not zoom keys", () => {
    expect(handleGlobalKey(key("=", { altKey: true, metaKey: true }))).toBe(false);
    expect(zoomOf(1)).toBe(100);
  });

  it("does not disturb Cmd+K or Cmd+Z", () => {
    expect(handleGlobalKey(key("k", { metaKey: true }))).toBe(true);
    closePalette();
    expect(zoomOf(1)).toBe(100);
  });
});
