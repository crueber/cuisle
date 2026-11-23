// Switch: role=switch (ux-spec §9.3, 44x24).
//
// SPEC-NOTE: A disabled switch exposes `disabled` or aria-disabled="true" and never calls onChange.
// SPEC-NOTE: `description` is rendered as text and linked from the switch through aria-describedby.

import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { Switch } from "./Switch";

const isDisabled = (el: HTMLElement) => (el as HTMLButtonElement).disabled === true || el.getAttribute("aria-disabled") === "true";
const sw = () => screen.getByRole("switch", { name: "Sync on open" });

describe("Switch", () => {
  it("renders role=switch named by label with aria-checked mirroring the prop", () => {
    const { rerender } = render(<Switch id="s" label="Sync on open" checked={false} onChange={() => {}} />);
    expect(sw().getAttribute("aria-checked")).toBe("false");
    rerender(<Switch id="s" label="Sync on open" checked={true} onChange={() => {}} />);
    expect(sw().getAttribute("aria-checked")).toBe("true");
  });

  it("calls onChange with the inverted value on click", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { rerender } = render(<Switch id="s" label="Sync on open" checked={false} onChange={onChange} />);
    await user.click(sw());
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenLastCalledWith(true);
    rerender(<Switch id="s" label="Sync on open" checked={true} onChange={onChange} />);
    await user.click(sw());
    expect(onChange).toHaveBeenCalledTimes(2);
    expect(onChange).toHaveBeenLastCalledWith(false);
  });

  it("toggles with the Space key when focused", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Switch id="s" label="Sync on open" checked={true} onChange={onChange} />);
    sw().focus();
    expect(document.activeElement).toBe(sw());
    await user.keyboard(" ");
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith(false);
  });

  it("does not call onChange when disabled", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Switch id="s" label="Sync on open" checked={false} onChange={onChange} disabled />);
    expect(isDisabled(sw())).toBe(true);
    await user.click(sw());
    expect(onChange).not.toHaveBeenCalled();
  });

  it("renders the description and links it with aria-describedby", () => {
    render(<Switch id="s" label="Sync on open" checked={false} onChange={() => {}} description="Resync when data is stale" />);
    expect(screen.getByText("Resync when data is stale")).toBeTruthy();
    const ids = (sw().getAttribute("aria-describedby") ?? "").split(/\s+/).filter(Boolean);
    const text = ids.map((id) => document.getElementById(id)?.textContent ?? "").join(" ");
    expect(text).toContain("Resync when data is stale");
  });

  it("has no axe violations (on, off, disabled, with description)", async () => {
    const { container, rerender } = render(
      <Switch id="s" label="Sync on open" checked={false} onChange={() => {}} description="Resync when data is stale" />,
    );
    expect(sw()).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
    rerender(<Switch id="s" label="Sync on open" checked={true} onChange={() => {}} disabled />);
    expect(sw()).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
  });
});
