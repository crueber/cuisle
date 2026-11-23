// WP-23 tests: StandupSwitcher, the menu form of the standup tabs (ux-spec §4 "Menu").
// The shared Combobox (WP-11) is replaced by a double that exposes its props: a button showing `menuLabel` that toggles a
// listbox of every option (button per option, labelled with getLabel). This file checks how the switcher drives it.
//
// SPEC-NOTE: the switcher renders ONE Combobox with variant="menu" whose options are all tabs (type-to-filter is the
//   Combobox's job), whose value is the active tab, and whose menuLabel shows "[n] <name> <n>/<total> ▾" as text: the
//   number box, the active standup's name and its position out of the total (e.g. "2 Web & Checkout 2/5").
//   The switcher itself adds the hint "1–N to switch" (N = tab count), next to the menu.
// SPEC-NOTE: choosing an option calls onSelect(tab.id).
// SPEC-NOTE: getLabel(tab) contains the standup's name; getKey(tab) is unique per tab.
import { fireEvent, render, screen } from "@testing-library/preact";
import { describe, expect, it, vi } from "vitest";
import type { StandupTab } from "../../api/types";
import { StandupSwitcher } from "./StandupSwitcher";
import type { StandupSwitcherProps } from "./props";

vi.mock("../../components/Combobox", async () => {
  const { h } = await import("preact");
  const { useState } = await import("preact/hooks");
  type P = {
    id: string;
    label: string;
    variant?: string;
    value: unknown;
    options?: unknown[];
    getKey: (o: unknown) => string;
    getLabel: (o: unknown) => string;
    onSelect: (o: unknown) => void;
    menuLabel?: unknown;
  };
  function Combobox(p: P) {
    const [open, setOpen] = useState(false);
    return h(
      "div",
      { "data-testid": "combobox", "data-variant": p.variant ?? "field", "data-count": String((p.options ?? []).length), "data-value": p.value ? p.getKey(p.value) : "" },
      h("button", { type: "button", "aria-haspopup": "listbox", "aria-expanded": String(open), onClick: () => setOpen(!open) }, p.menuLabel as never),
      open
        ? h(
            "ul",
            { role: "listbox", "aria-label": p.label },
            (p.options ?? []).map((o) =>
              h("li", { key: p.getKey(o), role: "option" }, h("button", { type: "button", onClick: () => { p.onSelect(o); setOpen(false); } }, p.getLabel(o))),
            ),
          )
        : null,
    );
  }
  return { Combobox };
});

const NAMES: [string, string][] = [
  ["Saffron Mobile", "MOB"],
  ["Web & Checkout", "WEB"],
  ["Recipe Data", "DATA"],
  ["Growth", "GRO"],
  ["Platform", "PLAT"],
];
const TABS: StandupTab[] = NAMES.map(([name, spaceKey], i) => ({
  id: 10 + i,
  rev: 1,
  name,
  spaceKey,
  position: i,
  activeCount: 10,
  speakerCount: 5,
  configured: true,
  needsSync: false,
  lastSyncedAt: 0,
}));

function renderSwitcher(over: Partial<StandupSwitcherProps> = {}) {
  const onSelect = vi.fn();
  const props: StandupSwitcherProps = { tabs: TABS, activeId: 11, onSelect, ...over };
  return { onSelect, ...render(<StandupSwitcher {...props} />) };
}

describe("StandupSwitcher", () => {
  it("renders one menu-variant combobox over all standups", () => {
    renderSwitcher();
    const cb = screen.getByTestId("combobox");
    expect(cb.getAttribute("data-variant")).toBe("menu");
    expect(cb.getAttribute("data-count")).toBe("5");
    expect(cb.getAttribute("data-value")).not.toBe("");
  });

  it("labels the menu with the active number, name and position out of the total", () => {
    renderSwitcher({ activeId: 11 });
    const label = (screen.getByRole("button").textContent ?? "").replace(/\s+/g, " ");
    expect(label).toContain("Web & Checkout");
    expect(label).toContain("2/5");
    expect(label).toMatch(/\b2\b/);
  });

  it("tracks the active standup in the label", () => {
    const { rerender, onSelect } = renderSwitcher({ activeId: 10 });
    expect(screen.getByRole("button").textContent).toContain("1/5");
    expect(screen.getByRole("button").textContent).toContain("Saffron Mobile");
    rerender(<StandupSwitcher tabs={TABS} activeId={14} onSelect={onSelect} />);
    expect(screen.getByRole("button").textContent).toContain("Platform");
    expect(screen.getByRole("button").textContent).toContain("5/5");
  });

  it("shows the '1–N to switch' hint", () => {
    renderSwitcher();
    expect(screen.getByText(/1[–-]5 to switch/)).toBeTruthy();
  });

  it("lists every standup when opened and selects by id", () => {
    const { onSelect } = renderSwitcher({ activeId: 10 });
    fireEvent.click(screen.getByRole("button", { name: /Saffron Mobile/ }));
    expect(screen.getAllByRole("option")).toHaveLength(5);
    fireEvent.click(screen.getByRole("button", { name: /Recipe Data/ }));
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(12);
  });

  it("gives every option a distinct label", () => {
    renderSwitcher();
    fireEvent.click(screen.getByRole("button", { name: /Web & Checkout/ }));
    const labels = screen.getAllByRole("option").map((o) => o.textContent);
    expect(new Set(labels).size).toBe(5);
    for (const [name] of NAMES) expect(labels.some((l) => (l ?? "").includes(name))).toBe(true);
  });

  it("counts the hint from the number of tabs", () => {
    renderSwitcher({ tabs: TABS.slice(0, 3), activeId: 10 });
    expect(screen.getByText(/1[–-]3 to switch/)).toBeTruthy();
    expect(screen.getByRole("button").textContent).toContain("1/3");
  });
});
