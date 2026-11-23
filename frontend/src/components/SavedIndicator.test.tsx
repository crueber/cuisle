// SavedIndicator (ux-spec §9.1, architecture §5.3).
//
// SPEC-NOTE: The 250 ms "Saving…" delay lives in state/save.ts, not here: this component renders the state it is given.
//   state="saving" therefore shows "Saving…" immediately, and the component uses no timers.
// SPEC-NOTE: The role="status" element is ALWAYS rendered, including for "idle" (empty text), and it is the same DOM
//   node across state changes, so screen readers announce the transitions (live-region rule).
// SPEC-NOTE: "failed" shows the text "Not saved" plus a button named "Retry" that calls onRetry. Only "failed" renders a
//   button. Exact punctuation between "Not saved" and Retry (the "·") is not asserted.
// SPEC-NOTE: Text per state: saving "Saving…" (U+2026), saved "Saved", settled "All changes saved", idle "".

import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { SavedIndicator } from "./SavedIndicator";

const status = () => screen.getByRole("status");
const text = () => status().textContent?.trim();

describe("SavedIndicator", () => {
  it("renders an empty status region when idle", () => {
    render(<SavedIndicator state="idle" onRetry={() => {}} />);
    expect(status()).toBeTruthy();
    expect(text()).toBe("");
    expect(screen.queryByRole("button")).toBeNull();
  });

  it.each([
    ["saving", "Saving…"],
    ["saved", "Saved"],
    ["settled", "All changes saved"],
  ] as const)("renders %s as %s with no Retry button", (state, expected) => {
    render(<SavedIndicator state={state} onRetry={() => {}} />);
    expect(text()).toBe(expected);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("renders failed as 'Not saved' with a Retry button that calls onRetry", async () => {
    const user = userEvent.setup();
    const onRetry = vi.fn();
    render(<SavedIndicator state="failed" onRetry={onRetry} />);
    expect(text()).toContain("Not saved");
    const retry = screen.getByRole("button", { name: "Retry" });
    expect(onRetry).not.toHaveBeenCalled();
    await user.click(retry);
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("keeps the same status node across state changes and updates its text", () => {
    const { rerender } = render(<SavedIndicator state="idle" onRetry={() => {}} />);
    const node = status();
    rerender(<SavedIndicator state="saving" onRetry={() => {}} />);
    expect(status()).toBe(node);
    expect(text()).toBe("Saving…");
    rerender(<SavedIndicator state="saved" onRetry={() => {}} />);
    expect(status()).toBe(node);
    expect(text()).toBe("Saved");
    rerender(<SavedIndicator state="settled" onRetry={() => {}} />);
    expect(status()).toBe(node);
    expect(text()).toBe("All changes saved");
    rerender(<SavedIndicator state="failed" onRetry={() => {}} />);
    expect(status()).toBe(node);
    expect(text()).toContain("Not saved");
  });

  it("drops the Retry button again when the state recovers", () => {
    const { rerender } = render(<SavedIndicator state="failed" onRetry={() => {}} />);
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();
    rerender(<SavedIndicator state="saved" onRetry={() => {}} />);
    expect(screen.queryByRole("button")).toBeNull();
    expect(text()).toBe("Saved");
  });

  it.each(["idle", "saving", "saved", "settled", "failed"] as const)("has no axe violations in state %s", async (state) => {
    const { container } = render(<SavedIndicator state={state} onRetry={() => {}} />);
    expect(status()).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
  });
});
