// WP-38F: Ctrl/Cmd+Z runs a visible Undo toast (architecture §5.3) and nothing else.
import { beforeEach, describe, expect, it, vi } from "vitest";
import { handleGlobalKey } from "./keys";
import { route } from "./state/route";
import { showToast, toasts } from "./state/ui";

const key = (init: KeyboardEventInit, target?: Element) => {
  const e = new KeyboardEvent("keydown", { key: "z", bubbles: true, ...init });
  if (target) Object.defineProperty(e, "target", { value: target });
  return e;
};

beforeEach(() => {
  toasts.value = [];
  route.value = { view: "standup", standupId: 1, person: null };
});

describe("Ctrl/Cmd+Z", () => {
  it.each([{ ctrlKey: true }, { metaKey: true }])("runs the newest Undo toast (%o) once", (mod) => {
    const run = vi.fn();
    showToast({ message: "Removed X", action: { label: "Undo", run }, timeoutMs: 10_000 });
    expect(handleGlobalKey(key(mod))).toBe(true);
    expect(run).toHaveBeenCalledTimes(1);
    expect(toasts.value).toHaveLength(0);
    expect(handleGlobalKey(key(mod))).toBe(false);
    expect(run).toHaveBeenCalledTimes(1);
  });

  it("is not handled without an Undo toast", () => {
    expect(handleGlobalKey(key({ ctrlKey: true }))).toBe(false);
  });

  it("leaves native text undo alone inside inputs", () => {
    const run = vi.fn();
    showToast({ message: "Removed X", action: { label: "Undo", run }, timeoutMs: 10_000 });
    const input = document.createElement("input");
    expect(handleGlobalKey(key({ ctrlKey: true }, input))).toBe(false);
    expect(run).not.toHaveBeenCalled();
  });

  it("ignores Shift+Ctrl+Z (redo) and Alt combinations", () => {
    const run = vi.fn();
    showToast({ message: "Removed X", action: { label: "Undo", run }, timeoutMs: 10_000 });
    expect(handleGlobalKey(key({ ctrlKey: true, shiftKey: true }))).toBe(false);
    expect(handleGlobalKey(key({ ctrlKey: true, altKey: true }))).toBe(false);
    expect(run).not.toHaveBeenCalled();
  });
});
