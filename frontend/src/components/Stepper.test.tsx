// Stepper: "− value +" (ux-spec §9.3).
//
// SPEC-NOTE (frozen props say nothing about roles, so this is the contract the tests set):
//   - the value element is role="spinbutton" named by `label`, with aria-valuenow/min/max and aria-valuetext
//     (zeroLabel at 0, else `${value}${suffix}`); it takes focus and handles ArrowUp/ArrowDown.
//   - two buttons named /decrease/i and /increase/i, disabled at min and max respectively.
// SPEC-NOTE: Changes are clamped to [min, max]. A step that would leave the range clamps to the bound (value 3, step 5,
//   Decrease -> 0). A no-op at a bound (already at max, Increase/ArrowUp) does NOT call onChange.
// SPEC-NOTE: The visible text shows `${value}${suffix}` (optional space tolerated), or zeroLabel instead of the number
//   when value is 0 and zeroLabel is given. zeroLabel is not shown when value is not 0.

import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { useState } from "preact/hooks";
import { Stepper } from "./Stepper";
import type { StepperProps } from "./props";

function props(over: Partial<StepperProps> = {}): StepperProps {
  return { id: "st", label: "Stale after", value: 5, min: 0, max: 10, onChange: () => {}, ...over };
}
const spin = () => screen.getByRole("spinbutton", { name: "Stale after" });
const dec = () => screen.getByRole("button", { name: /decrease/i }) as HTMLButtonElement;
const inc = () => screen.getByRole("button", { name: /increase/i }) as HTMLButtonElement;

function Harness(p: { init: number; over?: Partial<StepperProps>; onChange?: (v: number) => void }) {
  const [v, setV] = useState(p.init);
  return (
    <Stepper
      {...props(p.over)}
      value={v}
      onChange={(n) => {
        setV(n);
        p.onChange?.(n);
      }}
    />
  );
}

describe("Stepper", () => {
  it("exposes a spinbutton with aria-valuenow, aria-valuemin and aria-valuemax", () => {
    render(<Stepper {...props({ value: 5, min: 1, max: 9 })} />);
    expect(spin().getAttribute("aria-valuenow")).toBe("5");
    expect(spin().getAttribute("aria-valuemin")).toBe("1");
    expect(spin().getAttribute("aria-valuemax")).toBe("9");
  });

  it("shows the value with its suffix", () => {
    const { container } = render(<Stepper {...props({ value: 7, suffix: "d" })} />);
    expect(spin()).toBeTruthy();
    expect(container.textContent).toMatch(/\b7\s?d\b/);
    expect(spin().getAttribute("aria-valuetext")).toMatch(/^7\s?d$/);
  });

  it("shows the value alone without a suffix", () => {
    const { container } = render(<Stepper {...props({ value: 7 })} />);
    expect(spin()).toBeTruthy();
    expect(container.textContent).toContain("7");
    expect(spin().getAttribute("aria-valuenow")).toBe("7");
  });

  it("shows zeroLabel instead of the number at 0", () => {
    const { container } = render(<Stepper {...props({ value: 0, suffix: "d", zeroLabel: "no limit" })} />);
    expect(spin()).toBeTruthy();
    expect(container.textContent).toContain("no limit");
    expect(spin().getAttribute("aria-valuetext")).toBe("no limit");
    expect(spin().getAttribute("aria-valuenow")).toBe("0");
  });

  it("does not show zeroLabel when the value is not 0", () => {
    const { container } = render(<Stepper {...props({ value: 3, suffix: "d", zeroLabel: "no limit" })} />);
    expect(spin()).toBeTruthy();
    expect(container.textContent).not.toContain("no limit");
  });

  it("increases and decreases by 1 with the buttons", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness init={5} onChange={onChange} />);
    await user.click(inc());
    expect(onChange).toHaveBeenLastCalledWith(6);
    await user.click(dec());
    await user.click(dec());
    expect(onChange).toHaveBeenLastCalledWith(4);
    expect(onChange).toHaveBeenCalledTimes(3);
    expect(spin().getAttribute("aria-valuenow")).toBe("4");
  });

  it("honours a custom step", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness init={10} over={{ min: 0, max: 30, step: 5 }} onChange={onChange} />);
    await user.click(inc());
    expect(onChange).toHaveBeenLastCalledWith(15);
    await user.click(dec());
    await user.click(dec());
    expect(onChange).toHaveBeenLastCalledWith(5);
  });

  it("clamps a step that would pass the minimum or maximum to the bound", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { rerender } = render(<Stepper {...props({ value: 3, step: 5, onChange })} />);
    await user.click(dec());
    expect(onChange).toHaveBeenLastCalledWith(0);
    rerender(<Stepper {...props({ value: 8, step: 5, onChange })} />);
    await user.click(inc());
    expect(onChange).toHaveBeenLastCalledWith(10);
  });

  it("disables Decrease at min and Increase at max, and does not call onChange there", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { rerender } = render(<Stepper {...props({ value: 0, onChange })} />);
    expect(dec().disabled).toBe(true);
    expect(inc().disabled).toBe(false);
    await user.click(dec());
    rerender(<Stepper {...props({ value: 10, onChange })} />);
    expect(inc().disabled).toBe(true);
    expect(dec().disabled).toBe(false);
    await user.click(inc());
    expect(onChange).not.toHaveBeenCalled();
  });

  it("steps with ArrowUp and ArrowDown on the focused spinbutton", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness init={5} over={{ step: 2 }} onChange={onChange} />);
    spin().focus();
    expect(document.activeElement).toBe(spin());
    await user.keyboard("{ArrowUp}");
    expect(onChange).toHaveBeenLastCalledWith(7);
    await user.keyboard("{ArrowDown}{ArrowDown}");
    expect(onChange).toHaveBeenLastCalledWith(3);
    expect(onChange).toHaveBeenCalledTimes(3);
  });

  it("ignores ArrowUp at max and ArrowDown at min", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { rerender } = render(<Stepper {...props({ value: 10, onChange })} />);
    spin().focus();
    await user.keyboard("{ArrowUp}");
    rerender(<Stepper {...props({ value: 0, onChange })} />);
    spin().focus();
    await user.keyboard("{ArrowDown}");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("has no axe violations (middle, zero with zeroLabel, bounds)", async () => {
    const { container, rerender } = render(<Stepper {...props({ suffix: "d" })} />);
    expect(spin()).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
    rerender(<Stepper {...props({ value: 0, zeroLabel: "no limit" })} />);
    expect(spin()).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
    rerender(<Stepper {...props({ value: 10 })} />);
    expect(spin()).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
  });
});
