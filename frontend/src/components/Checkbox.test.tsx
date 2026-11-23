// Checkbox: a real role=checkbox (ux-spec §9.3, 24px).
//
// SPEC-NOTE: "Real" role=checkbox means either a native input[type=checkbox] (its `checked` property is the truth) or an
//   element with role=checkbox and aria-checked. The tests accept both; they only read state through that helper.
// SPEC-NOTE: A disabled checkbox exposes `disabled` or aria-disabled="true" and never calls onChange.

import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { Checkbox } from "./Checkbox";

const cb = () => screen.getByRole("checkbox", { name: "Show commits" });
const isChecked = (el: HTMLElement) => (el instanceof HTMLInputElement ? el.checked : el.getAttribute("aria-checked") === "true");
const isDisabled = (el: HTMLElement) => (el as HTMLInputElement).disabled === true || el.getAttribute("aria-disabled") === "true";

describe("Checkbox", () => {
  it("renders role=checkbox named by label and mirrors checked", () => {
    const { rerender } = render(<Checkbox id="c" label="Show commits" checked={false} onChange={() => {}} />);
    expect(isChecked(cb())).toBe(false);
    rerender(<Checkbox id="c" label="Show commits" checked={true} onChange={() => {}} />);
    expect(isChecked(cb())).toBe(true);
  });

  it("calls onChange with the inverted value on click", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { rerender } = render(<Checkbox id="c" label="Show commits" checked={false} onChange={onChange} />);
    await user.click(cb());
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenLastCalledWith(true);
    rerender(<Checkbox id="c" label="Show commits" checked={true} onChange={onChange} />);
    await user.click(cb());
    expect(onChange).toHaveBeenLastCalledWith(false);
  });

  it("toggles when the label text is clicked", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Checkbox id="c" label="Show commits" checked={false} onChange={onChange} />);
    expect(cb()).toBeTruthy();
    await user.click(screen.getByText("Show commits"));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("toggles with the Space key when focused", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Checkbox id="c" label="Show commits" checked={false} onChange={onChange} />);
    cb().focus();
    expect(document.activeElement).toBe(cb());
    await user.keyboard(" ");
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("does not call onChange when disabled", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Checkbox id="c" label="Show commits" checked={false} onChange={onChange} disabled />);
    expect(isDisabled(cb())).toBe(true);
    await user.click(cb());
    expect(onChange).not.toHaveBeenCalled();
  });

  it("has no axe violations (checked, unchecked, disabled)", async () => {
    const { container, rerender } = render(<Checkbox id="c" label="Show commits" checked={false} onChange={() => {}} />);
    expect(cb()).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
    rerender(<Checkbox id="c" label="Show commits" checked={true} onChange={() => {}} disabled />);
    expect(cb()).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
  });
});
