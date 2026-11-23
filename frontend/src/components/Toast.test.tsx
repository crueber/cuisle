// Toast: message, optional action, dismiss.
//
// SPEC-NOTE: The toast is a role="status" region containing the message. The dismiss control is a button whose
//   accessible name matches /dismiss|close/i; the action is a button named exactly by action.label. The component has
//   no auto-dismiss timer; the caller decides when to unmount it.
// SPEC-NOTE: Whether running the action also dismisses is not specified; the tests only assert run() was called.

import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { Toast } from "./Toast";

describe("Toast", () => {
  it("shows the message inside a status region", () => {
    render(<Toast message="Standup deleted" onDismiss={() => {}} />);
    const status = screen.getByRole("status");
    expect(status.textContent).toContain("Standup deleted");
  });

  it("calls the action's run when the action button is clicked", async () => {
    const user = userEvent.setup();
    const run = vi.fn();
    render(<Toast message="Standup archived" action={{ label: "Undo", run }} onDismiss={() => {}} />);
    await user.click(screen.getByRole("button", { name: "Undo" }));
    expect(run).toHaveBeenCalledTimes(1);
  });

  it("calls onDismiss from the dismiss button", async () => {
    const user = userEvent.setup();
    const onDismiss = vi.fn();
    render(<Toast message="Standup archived" onDismiss={onDismiss} />);
    await user.click(screen.getByRole("button", { name: /dismiss|close/i }));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("does not call onDismiss when the action is clicked", async () => {
    const user = userEvent.setup();
    const onDismiss = vi.fn();
    render(<Toast message="Standup archived" action={{ label: "Undo", run: () => {} }} onDismiss={onDismiss} />);
    await user.click(screen.getByRole("button", { name: "Undo" }));
    expect(onDismiss).not.toHaveBeenCalled();
  });

  it("renders only the dismiss button when there is no action", () => {
    render(<Toast message="Standup archived" onDismiss={() => {}} />);
    expect(screen.getAllByRole("button")).toHaveLength(1);
  });

  it("renders both action and dismiss buttons when an action is given", () => {
    render(<Toast message="Standup archived" action={{ label: "Undo", run: () => {} }} onDismiss={() => {}} />);
    expect(screen.getAllByRole("button")).toHaveLength(2);
  });

  it("has no axe violations (with and without action)", async () => {
    const { container, rerender } = render(<Toast message="Standup archived" onDismiss={() => {}} />);
    expect(screen.getByRole("status")).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
    rerender(<Toast message="Standup archived" action={{ label: "Undo", run: () => {} }} onDismiss={() => {}} />);
    expect(screen.getByRole("button", { name: "Undo" })).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
  });
});
