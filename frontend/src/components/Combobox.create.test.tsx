// Shared Combobox: the optional create affordance (onCreate), used by Configure › Status mapping to name a new lane.
// The create option is the last option of the listbox, offered only for typed text that names no option exactly.
import { render, screen, act } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Combobox } from "./Combobox";
import type { ComboboxProps } from "./props";

interface Lane {
  lane: string;
  label: string;
}
const LANES: Lane[] = [
  { lane: "todo", label: "To Do" },
  { lane: "prog", label: "In Progress" },
  { lane: "qa", label: "QA" },
];

function props(over: Partial<ComboboxProps<Lane>> = {}): ComboboxProps<Lane> {
  return {
    id: "lane",
    label: "Lane for Design",
    value: LANES[0]!,
    getKey: (o) => o.lane,
    getLabel: (o) => o.label,
    options: LANES,
    onSelect: vi.fn(),
    onCreate: vi.fn(),
    createLabel: (t) => `Create lane “${t}”`,
    ...over,
  };
}

const input = () => screen.getByRole("combobox", { name: "Lane for Design" }) as HTMLInputElement;
const options = () => screen.queryAllByRole("option").map((o) => o.textContent);

beforeEach(() => {
  (Element.prototype as unknown as { scrollIntoView: unknown }).scrollIntoView = vi.fn();
});

describe("Combobox create option", () => {
  it("offers Create for a new name after the matches, and Enter on it creates (trimmed)", async () => {
    const user = userEvent.setup();
    const p = props();
    render(<Combobox<Lane> {...p} />);
    await user.clear(input());
    await user.type(input(), "  Design ");
    expect(options()).toEqual(["Create lane “Design”"]);
    await user.keyboard("{Enter}");
    expect(p.onCreate).toHaveBeenCalledWith("Design");
    expect(p.onSelect).not.toHaveBeenCalled();
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("is the last option, reachable with the arrows, and clickable", async () => {
    const user = userEvent.setup();
    const p = props();
    render(<Combobox<Lane> {...p} />);
    await user.clear(input());
    await user.type(input(), "Q");
    expect(options()).toEqual(["QA", "Create lane “Q”"]);
    await user.keyboard("{ArrowDown}");
    expect(input().getAttribute("aria-activedescendant")).toBe("lane-opt-1");
    await user.keyboard("{Enter}");
    expect(p.onCreate).toHaveBeenCalledWith("Q");

    expect(input().value).toBe("To Do"); // after a create the field shows its committed value again
    await user.clear(input());
    await user.type(input(), "Review");
    await act(async () => {
      screen.getByRole("option", { name: "Create lane “Review”" }).click();
    });
    expect(p.onCreate).toHaveBeenLastCalledWith("Review");
  });

  it("is not offered for an existing name (any case), for blank text, or without onCreate", async () => {
    const user = userEvent.setup();
    const { unmount } = render(<Combobox<Lane> {...props()} />);
    await user.clear(input());
    await user.type(input(), "qa");
    expect(options()).toEqual(["QA"]);
    await user.clear(input());
    await user.type(input(), "   ");
    expect(options().some((o) => o?.startsWith("Create"))).toBe(false);
    unmount();

    render(<Combobox<Lane> {...props({ onCreate: undefined })} />);
    await user.clear(input());
    await user.type(input(), "Design");
    expect(options()).toEqual([]);
    expect(screen.getByText("No matches")).toBeTruthy();
  });

  it("has no axe violations with the create option open", async () => {
    const user = userEvent.setup();
    const { container } = render(<Combobox<Lane> {...props()} />);
    await user.clear(input());
    await user.type(input(), "Design");
    const res = await axe.run(container, { rules: { region: { enabled: false } } });
    expect(res.violations.map((v) => v.id)).toEqual([]);
  });
});
