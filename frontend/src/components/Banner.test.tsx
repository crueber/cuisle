// Banner: a persistent notice per kind (architecture §4.5 banners, ux-spec §12 copy).
//
// SPEC-NOTE: The caller supplies the message; the component never invents copy. The banner root carries
//   data-kind="<kind>" (the styling hook, like data-lane; colour is never the only signal because the message is text),
//   and the message sits inside a role="alert" or role="status" live region. Which of the two a given kind uses is NOT
//   asserted.
// SPEC-NOTE: The action is a button named exactly action.label; the dismiss control is a button named /dismiss|close/i
//   and is rendered only when onDismiss is given. With neither, there are no buttons.

import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { Banner } from "./Banner";
import type { BannerProps } from "./props";

const KINDS: BannerProps["kind"][] = ["offline", "auth_missing", "auth_invalid", "recovered", "error"];
const region = () => document.querySelector('[role="alert"], [role="status"]') as HTMLElement | null;

describe("Banner", () => {
  it.each(KINDS)("renders the message for kind %s in a live region carrying data-kind", (kind) => {
    const { container } = render(<Banner kind={kind} message={`Message for ${kind}`} />);
    expect(screen.getByText(`Message for ${kind}`)).toBeTruthy();
    expect(region()?.textContent).toContain(`Message for ${kind}`);
    expect(container.querySelector(`[data-kind="${kind}"]`)).not.toBeNull();
  });

  it("uses a distinct data-kind for each kind", () => {
    const seen = new Set<string>();
    for (const kind of KINDS) {
      const { container, unmount } = render(<Banner kind={kind} message="m" />);
      const el = container.querySelector("[data-kind]");
      expect(el).not.toBeNull();
      seen.add(el?.getAttribute("data-kind") ?? "");
      unmount();
    }
    expect(seen.size).toBe(KINDS.length);
  });

  it("renders no buttons without an action or onDismiss", () => {
    render(<Banner kind="offline" message="You are offline" />);
    expect(screen.getByText("You are offline")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("calls the action's run from its button", async () => {
    const user = userEvent.setup();
    const run = vi.fn();
    render(<Banner kind="auth_invalid" message="Token rejected" action={{ label: "Reconnect", run }} />);
    await user.click(screen.getByRole("button", { name: "Reconnect" }));
    expect(run).toHaveBeenCalledTimes(1);
  });

  it("renders a dismiss button only when onDismiss is given, and calls it", async () => {
    const user = userEvent.setup();
    const onDismiss = vi.fn();
    const { rerender } = render(<Banner kind="recovered" message="Back online" />);
    expect(screen.getByText("Back online")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /dismiss|close/i })).toBeNull();
    rerender(<Banner kind="recovered" message="Back online" onDismiss={onDismiss} />);
    await user.click(screen.getByRole("button", { name: /dismiss|close/i }));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("renders both the action and the dismiss button together", () => {
    render(<Banner kind="error" message="Sync failed" action={{ label: "Retry", run: () => {} }} onDismiss={() => {}} />);
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();
    expect(screen.getByRole("button", { name: /dismiss|close/i })).toBeTruthy();
  });

  it.each(KINDS)("has no axe violations for kind %s (with action and dismiss)", async (kind) => {
    const { container } = render(
      <Banner kind={kind} message={`Message for ${kind}`} action={{ label: "Fix it", run: () => {} }} onDismiss={() => {}} />,
    );
    expect(screen.getByText(`Message for ${kind}`)).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
  });
});
