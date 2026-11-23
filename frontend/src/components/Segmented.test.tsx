// Segmented: a radiogroup (ux-spec §9.3) with roving selection.
//
// SPEC-NOTE: Arrow keys (Right/Down = next, Left/Up = previous) move focus AND select (onChange fires), as in the APG
//   radio group; they wrap at the ends and skip disabled options. Only the checked radio is in the tab order
//   (tabindex 0, the others -1).
// SPEC-NOTE: A disabled option exposes `disabled` or aria-disabled="true", and clicking it never calls onChange.

import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { useState } from "preact/hooks";
import { Segmented } from "./Segmented";
import type { SegmentedOption } from "./props";

type V = "a" | "b" | "c";
const OPTS: SegmentedOption<V>[] = [
  { value: "a", label: "Alpha" },
  { value: "b", label: "Beta" },
  { value: "c", label: "Gamma" },
];

function Harness(p: { init?: V; options?: SegmentedOption<V>[]; onChange?: (v: V) => void }) {
  const [v, setV] = useState<V>(p.init ?? "a");
  return (
    <Segmented<V>
      id="seg"
      label="Greek"
      value={v}
      options={p.options ?? OPTS}
      onChange={(n) => {
        setV(n);
        p.onChange?.(n);
      }}
    />
  );
}

const radio = (name: string) => screen.getByRole("radio", { name });
const isDisabled = (el: HTMLElement) => (el as HTMLButtonElement).disabled === true || el.getAttribute("aria-disabled") === "true";

describe("Segmented", () => {
  it("renders a radiogroup named by label with one radio per option", () => {
    render(<Segmented<V> id="seg" label="Greek" value="b" options={OPTS} onChange={() => {}} />);
    expect(screen.getByRole("radiogroup", { name: "Greek" })).toBeTruthy();
    expect(screen.getAllByRole("radio").map((r) => r.textContent?.trim())).toEqual(["Alpha", "Beta", "Gamma"]);
  });

  it("marks only the current value as checked", () => {
    render(<Segmented<V> id="seg" label="Greek" value="b" options={OPTS} onChange={() => {}} />);
    expect(radio("Alpha").getAttribute("aria-checked")).toBe("false");
    expect(radio("Beta").getAttribute("aria-checked")).toBe("true");
    expect(radio("Gamma").getAttribute("aria-checked")).toBe("false");
  });

  it("puts only the checked radio in the tab order", () => {
    render(<Segmented<V> id="seg" label="Greek" value="b" options={OPTS} onChange={() => {}} />);
    expect(radio("Beta").getAttribute("tabindex")).toBe("0");
    expect(radio("Alpha").getAttribute("tabindex")).toBe("-1");
    expect(radio("Gamma").getAttribute("tabindex")).toBe("-1");
  });

  it("calls onChange with the option value on click", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    await user.click(radio("Gamma"));
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith("c");
    expect(radio("Gamma").getAttribute("aria-checked")).toBe("true");
  });

  it("selects the next radio and moves focus with ArrowRight, and the previous with ArrowLeft", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    radio("Alpha").focus();
    await user.keyboard("{ArrowRight}");
    expect(onChange).toHaveBeenLastCalledWith("b");
    expect(document.activeElement).toBe(radio("Beta"));
    expect(radio("Beta").getAttribute("aria-checked")).toBe("true");
    await user.keyboard("{ArrowLeft}");
    expect(onChange).toHaveBeenLastCalledWith("a");
    expect(document.activeElement).toBe(radio("Alpha"));
    expect(onChange).toHaveBeenCalledTimes(2);
  });

  it("also moves with ArrowDown and ArrowUp", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness init="b" onChange={onChange} />);
    radio("Beta").focus();
    await user.keyboard("{ArrowDown}");
    expect(onChange).toHaveBeenLastCalledWith("c");
    await user.keyboard("{ArrowUp}");
    expect(onChange).toHaveBeenLastCalledWith("b");
  });

  it("wraps from the last radio to the first and from the first to the last", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness init="c" onChange={onChange} />);
    radio("Gamma").focus();
    await user.keyboard("{ArrowRight}");
    expect(onChange).toHaveBeenLastCalledWith("a");
    expect(document.activeElement).toBe(radio("Alpha"));
    await user.keyboard("{ArrowLeft}");
    expect(onChange).toHaveBeenLastCalledWith("c");
    expect(document.activeElement).toBe(radio("Gamma"));
  });

  it("skips disabled options when arrowing", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const options: SegmentedOption<V>[] = [OPTS[0]!, { ...OPTS[1]!, disabled: true }, OPTS[2]!];
    render(<Harness options={options} onChange={onChange} />);
    radio("Alpha").focus();
    await user.keyboard("{ArrowRight}");
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith("c");
  });

  it("does not call onChange when a disabled option is clicked", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const options: SegmentedOption<V>[] = [OPTS[0]!, { ...OPTS[1]!, disabled: true }, OPTS[2]!];
    render(<Harness options={options} onChange={onChange} />);
    expect(isDisabled(radio("Beta"))).toBe(true);
    await user.click(radio("Beta"));
    expect(onChange).not.toHaveBeenCalled();
  });

  it("has no axe violations", async () => {
    const { container } = render(<Segmented<V> id="seg" label="Greek" value="b" options={OPTS} onChange={() => {}} />);
    expect(screen.getByRole("radiogroup")).toBeTruthy();
    const results = await axe.run(container);
    expect(results.violations.map((v) => v.id)).toEqual([]);
  });
});
