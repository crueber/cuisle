// WP-15 tests: transient UI state (state/ui.ts): toasts and banners.
//
// SPEC-NOTE: a toast removes itself after its own `timeoutMs` (undo toasts use 10 000); `dismissToast` removes it
//   at once. A timeoutMs <= 0 is not asserted. Banners never expire on their own (they are dismissed explicitly or
//   by the code that raised them).
// SPEC-NOTE: ids are unique and increasing within toasts and, separately, within banners (the two id spaces are
//   not compared). Deduplicating banners is the raiser's job (see sync.test.ts), not showBanner's.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

async function load() {
  vi.resetModules();
  return import("./ui");
}
let U: Awaited<ReturnType<typeof load>>;
beforeEach(async () => {
  vi.useFakeTimers();
  U = await load();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("toasts", () => {
  it("showToast_adds_the_toast_with_a_fresh_id_and_returns_it", () => {
    const id = U.showToast({ message: "Saved", timeoutMs: 3_000 });
    expect(U.toasts.value).toHaveLength(1);
    expect(U.toasts.value[0]).toMatchObject({ id, message: "Saved", timeoutMs: 3_000 });
  });

  it("gives_each_toast_a_unique_id_and_keeps_them_in_order", () => {
    const a = U.showToast({ message: "A", timeoutMs: 3_000 });
    const b = U.showToast({ message: "B", timeoutMs: 3_000 });
    expect(a).not.toBe(b);
    expect(U.toasts.value.map((t) => t.message)).toEqual(["A", "B"]);
  });

  it("keeps_the_action_on_the_toast", () => {
    const run = vi.fn();
    U.showToast({ message: "Deleted “Web & Checkout”", action: { label: "Undo", run }, timeoutMs: 10_000 });
    U.toasts.value[0]!.action!.run();
    expect(U.toasts.value[0]!.action!.label).toBe("Undo");
    expect(run).toHaveBeenCalledTimes(1);
  });

  it("removes_a_toast_after_its_timeout_and_not_before", async () => {
    U.showToast({ message: "Deleted", timeoutMs: 10_000 });
    await vi.advanceTimersByTimeAsync(9_999);
    expect(U.toasts.value).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(U.toasts.value).toHaveLength(0);
  });

  it("times_each_toast_from_its_own_creation", async () => {
    U.showToast({ message: "first", timeoutMs: 1_000 });
    await vi.advanceTimersByTimeAsync(600);
    U.showToast({ message: "second", timeoutMs: 1_000 });
    await vi.advanceTimersByTimeAsync(400); // t = 1000: first expires
    expect(U.toasts.value.map((t) => t.message)).toEqual(["second"]);
    await vi.advanceTimersByTimeAsync(600); // t = 1600
    expect(U.toasts.value).toHaveLength(0);
  });

  it("dismissToast_removes_only_that_toast", () => {
    const a = U.showToast({ message: "A", timeoutMs: 10_000 });
    U.showToast({ message: "B", timeoutMs: 10_000 });
    U.dismissToast(a);
    expect(U.toasts.value.map((t) => t.message)).toEqual(["B"]);
  });

  it("dismissToast_ignores_an_unknown_id", () => {
    U.showToast({ message: "A", timeoutMs: 10_000 });
    expect(() => U.dismissToast(9_999)).not.toThrow();
    expect(U.toasts.value).toHaveLength(1);
  });
});

describe("banners", () => {
  it("showBanner_adds_the_banner_with_a_fresh_id_and_returns_it", () => {
    const id = U.showBanner({ kind: "offline", message: "Offline — showing data from 3 h ago", dismissible: true });
    expect(U.banners.value).toHaveLength(1);
    expect(U.banners.value[0]).toMatchObject({ id, kind: "offline", dismissible: true });
  });

  it("gives_each_banner_a_unique_id", () => {
    const a = U.showBanner({ kind: "offline", message: "A", dismissible: true });
    const b = U.showBanner({ kind: "auth_invalid", message: "B", dismissible: false });
    expect(a).not.toBe(b);
    expect(U.banners.value.map((x) => x.kind)).toEqual(["offline", "auth_invalid"]);
  });

  it("keeps_the_action_on_the_banner", () => {
    const run = vi.fn();
    U.showBanner({ kind: "auth_missing", message: "Connect to Jira to sync", action: { label: "Connect", run }, dismissible: false });
    U.banners.value[0]!.action!.run();
    expect(run).toHaveBeenCalledTimes(1);
  });

  it("does_not_expire_on_its_own", async () => {
    U.showBanner({ kind: "error", message: "Something went wrong", dismissible: true });
    await vi.advanceTimersByTimeAsync(10 * 60_000);
    expect(U.banners.value).toHaveLength(1);
  });

  it("dismissBanner_removes_only_that_banner", () => {
    const a = U.showBanner({ kind: "offline", message: "A", dismissible: true });
    U.showBanner({ kind: "recovered", message: "B", dismissible: true });
    U.dismissBanner(a);
    expect(U.banners.value.map((x) => x.kind)).toEqual(["recovered"]);
  });

  it("dismissBanner_ignores_an_unknown_id", () => {
    U.showBanner({ kind: "offline", message: "A", dismissible: true });
    expect(() => U.dismissBanner(9_999)).not.toThrow();
    expect(U.banners.value).toHaveLength(1);
  });
});
