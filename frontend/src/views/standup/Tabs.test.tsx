// WP-23 tests: Tabs, the header standup tabs (ux-spec §4).
//
// The width MEASUREMENT is not simulated (jsdom has no layout, so every measured width is 0). Instead layout/tabs.ts
// `tabMode` and `visibleTabs` are replaced by controllable doubles (visibleTabs implements the documented rule), and the
// shared Combobox by a double (see StandupSwitcher.test.tsx). Tab-mode thresholds are tested in layout/tabs.test.ts.
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - Root:     <nav aria-label="Standups">.
//   - Tab:      a <button> per visible standup, aria-pressed="true" on the active one and "false" on the rest (the approved
//               reference snapshots use aria-pressed). It contains the number box (index + 1 in `tabs`, i.e. its position
//               among CONFIGURED standups), and in full mode the name and the Jira key.
//   - Key mode: the tab shows number + key only; its `title` and aria-label contain the full name.
//   - Add:      a <button> named exactly "Add a standup" in every mode.
//   - More:     with more than 7 tabs in full/key mode, a button named /+N more/ (N = hidden count). Opening it makes every
//               hidden standup's name reachable (a menu), and picking one calls onSelect(id).
//   - Menu:     in "menu" mode no tab button exists; the StandupSwitcher renders (Combobox variant "menu", hint "1–N to switch").
//
// SPEC-NOTE: Tabs receives CONFIGURED standups only; drafts never reach it (Header filters them, see Header.test.tsx).
// SPEC-NOTE: Tabs must ask tabMode(measures, availW, ...) with the availW prop as the 2nd argument, and measures must be
//   `{full:number, key:number}` entries for only the visible tabs (at most 7).
// SPEC-NOTE: clicking the active tab calls onSelect(activeId) or nothing; both are acceptable and not tested.
import { fireEvent, render, screen, within } from "@testing-library/preact";
import axe from "axe-core";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { StandupTab } from "../../api/types";
import * as layoutTabs from "../../layout/tabs";
import { Tabs } from "./Tabs";
import type { TabsProps } from "./props";

const mode = vi.hoisted(() => ({ value: "full" as "full" | "key" | "menu" }));

vi.mock("../../layout/tabs", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../layout/tabs")>();
  return {
    ...actual,
    tabMode: vi.fn(() => mode.value),
    // The documented rule (ux-spec §4): more than max → first max, the active one swapped into the last visible slot.
    visibleTabs: vi.fn((count: number, activeIndex: number, max = 7) => {
      if (count <= max) return { indices: Array.from({ length: count }, (_, i) => i), more: 0 };
      const indices = Array.from({ length: max }, (_, i) => i);
      if (activeIndex >= max) indices[max - 1] = activeIndex;
      return { indices, more: count - max };
    }),
  };
});

vi.mock("../../components/Combobox", async () => {
  const { h } = await import("preact");
  const { useState } = await import("preact/hooks");
  type P = {
    label: string;
    variant?: string;
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
      { "data-testid": "combobox", "data-variant": p.variant ?? "field" },
      h("button", { type: "button", "aria-haspopup": "listbox", "aria-expanded": String(open), onClick: () => setOpen(!open) }, p.menuLabel as never),
      open
        ? h(
            "ul",
            { role: "listbox", "aria-label": p.label },
            (p.options ?? []).map((o) => h("li", { key: p.getKey(o), role: "option" }, h("button", { type: "button", onClick: () => { p.onSelect(o); setOpen(false); } }, p.getLabel(o)))),
          )
        : null,
    );
  }
  return { Combobox };
});

const mk = (i: number, name: string, spaceKey: string): StandupTab => ({
  id: 100 + i,
  rev: 1,
  name,
  spaceKey,
  position: i,
  activeCount: 10,
  speakerCount: 5,
  configured: true,
  needsSync: false,
  lastSyncedAt: 0,
});
const FIVE: StandupTab[] = [
  mk(0, "Saffron Mobile", "MOB"),
  mk(1, "Web & Checkout", "WEB"),
  mk(2, "Recipe Data", "DATA"),
  mk(3, "Growth", "GRO"),
  mk(4, "Platform", "PLAT"),
];
const NINE: StandupTab[] = Array.from({ length: 9 }, (_, i) => mk(i, `Team ${i + 1}`, `K${i + 1}`));

function renderTabs(over: Partial<TabsProps> = {}) {
  const onSelect = vi.fn();
  const onAdd = vi.fn();
  const props: TabsProps = { tabs: FIVE, activeId: 100, availW: 1234, onSelect, onAdd, ...over };
  return { onSelect, onAdd, ...render(<Tabs {...props} />) };
}

const nav = () => screen.getByRole("navigation", { name: "Standups" });
const tabButtons = () => within(nav()).queryAllByRole("button").filter((b) => b.hasAttribute("aria-pressed"));
const AXE = { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } };

beforeEach(() => {
  mode.value = "full";
  vi.mocked(layoutTabs.tabMode).mockClear();
});

describe("Tabs in full mode", () => {
  it("shows one tab per standup with number, name and key", () => {
    renderTabs();
    const tabs = tabButtons();
    expect(tabs).toHaveLength(5);
    const first = tabs[0]!;
    expect(within(first).getByText("1")).toBeTruthy();
    expect(within(first).getByText("Saffron Mobile")).toBeTruthy();
    expect(within(first).getByText("MOB")).toBeTruthy();
    expect(within(tabs[4]!).getByText("5")).toBeTruthy();
    expect(within(tabs[4]!).getByText("Platform")).toBeTruthy();
  });

  it("marks only the active tab as pressed", () => {
    renderTabs({ activeId: 102 });
    expect(tabButtons().map((b) => b.getAttribute("aria-pressed"))).toEqual(["false", "false", "true", "false", "false"]);
  });

  it("selects an inactive tab by id", () => {
    const { onSelect } = renderTabs();
    fireEvent.click(screen.getByRole("button", { name: /Web & Checkout/ }));
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(101);
  });

  it("has an Add a standup button that calls onAdd", () => {
    const { onAdd } = renderTabs();
    fireEvent.click(screen.getByRole("button", { name: "Add a standup" }));
    expect(onAdd).toHaveBeenCalledTimes(1);
  });

  it("asks tabMode with the availW prop and one measurement per visible tab", () => {
    renderTabs({ availW: 987 });
    expect(tabButtons()).toHaveLength(5); // anchor
    const calls = vi.mocked(layoutTabs.tabMode).mock.calls;
    expect(calls.length).toBeGreaterThan(0);
    expect(calls.some((c) => c[1] === 987)).toBe(true);
    const measures = calls.map((c) => c[0]).find((m) => m.length === 5);
    expect(measures).toBeDefined();
    for (const m of measures!) {
      expect(typeof m.full).toBe("number");
      expect(typeof m.key).toBe("number");
    }
  });

  it("has no axe violations", async () => {
    const { container } = renderTabs();
    expect(tabButtons()).toHaveLength(5); // anchor
    expect((await axe.run(container, AXE)).violations.map((v) => v.id)).toEqual([]);
  });
});

describe("Tabs in key mode", () => {
  beforeEach(() => {
    mode.value = "key";
  });

  it("shows number and key only, with the full name in title and aria-label", () => {
    renderTabs();
    const tabs = tabButtons();
    expect(tabs).toHaveLength(5);
    expect(within(tabs[1]!).getByText("2")).toBeTruthy();
    expect(within(tabs[1]!).getByText("WEB")).toBeTruthy();
    expect(screen.queryByText("Web & Checkout")).toBeNull();
    expect(tabs[1]!.getAttribute("title")).toContain("Web & Checkout");
    expect(tabs[1]!.getAttribute("aria-label")).toContain("Web & Checkout");
    expect(screen.getByRole("button", { name: /Web & Checkout/ })).toBe(tabs[1]);
  });

  it("marks the active tab pressed and selects another by id", () => {
    const { onSelect } = renderTabs({ activeId: 104 });
    expect(tabButtons().map((b) => b.getAttribute("aria-pressed"))).toEqual(["false", "false", "false", "false", "true"]);
    fireEvent.click(screen.getByRole("button", { name: /Recipe Data/ }));
    expect(onSelect).toHaveBeenCalledWith(102);
  });

  it("keeps the Add a standup button", () => {
    const { onAdd } = renderTabs();
    expect(tabButtons()).toHaveLength(5); // anchor
    fireEvent.click(screen.getByRole("button", { name: "Add a standup" }));
    expect(onAdd).toHaveBeenCalledTimes(1);
  });

  it("has no axe violations", async () => {
    const { container } = renderTabs();
    expect(tabButtons()).toHaveLength(5); // anchor
    expect((await axe.run(container, AXE)).violations.map((v) => v.id)).toEqual([]);
  });
});

describe("Tabs in menu mode", () => {
  beforeEach(() => {
    mode.value = "menu";
  });

  it("replaces the tabs with the standup switcher", () => {
    renderTabs({ activeId: 101 });
    expect(screen.getByTestId("combobox").getAttribute("data-variant")).toBe("menu");
    expect(tabButtons()).toHaveLength(0);
    expect(screen.getByText(/1[–-]5 to switch/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Web & Checkout/ })).toBeTruthy();
  });

  it("switches through the menu", () => {
    const { onSelect } = renderTabs({ activeId: 100 });
    fireEvent.click(screen.getByRole("button", { name: /Saffron Mobile/ }));
    fireEvent.click(screen.getByRole("button", { name: /Growth/ }));
    expect(onSelect).toHaveBeenCalledWith(103);
  });

  it("keeps the Add a standup button", () => {
    const { onAdd } = renderTabs();
    expect(screen.getByTestId("combobox")).toBeTruthy(); // anchor
    fireEvent.click(screen.getByRole("button", { name: "Add a standup" }));
    expect(onAdd).toHaveBeenCalledTimes(1);
  });
});

describe("Tabs with more than 7 standups", () => {
  it("full mode shows the first 7 and '+2 more'", () => {
    renderTabs({ tabs: NINE, activeId: 102 });
    expect(tabButtons().map((b) => b.textContent)).toHaveLength(7);
    expect(screen.getByText("Team 7")).toBeTruthy();
    expect(screen.queryByText("Team 8")).toBeNull();
    expect(screen.queryByText("Team 9")).toBeNull();
    expect(screen.getByRole("button", { name: /\+2 more/ })).toBeTruthy();
  });

  it("swaps the active standup into the last visible slot when it is beyond the first 7", () => {
    renderTabs({ tabs: NINE, activeId: 108 });
    const tabs = tabButtons();
    expect(tabs).toHaveLength(7);
    const names = tabs.map((b) => /Team \d/.exec(b.textContent ?? "")?.[0]);
    expect(names).toEqual(["Team 1", "Team 2", "Team 3", "Team 4", "Team 5", "Team 6", "Team 9"]);
    expect(within(tabs[6]!).getByText("9")).toBeTruthy(); // number box keeps its real position
    expect(tabs[6]!.getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: /\+2 more/ })).toBeTruthy();
  });

  it("asks tabMode about only the visible tabs", () => {
    renderTabs({ tabs: NINE, activeId: 100 });
    expect(tabButtons()).toHaveLength(7); // anchor
    const lengths = vi.mocked(layoutTabs.tabMode).mock.calls.map((c) => c[0].length);
    expect(lengths.length).toBeGreaterThan(0);
    expect(Math.max(...lengths)).toBeLessThanOrEqual(7);
  });

  it("'+N more' opens a menu that reaches the hidden standups", () => {
    const { onSelect } = renderTabs({ tabs: NINE, activeId: 100 });
    expect(screen.queryByText(/Team 8/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /\+2 more/ }));
    fireEvent.click(screen.getByText(/Team 8/));
    expect(onSelect).toHaveBeenCalledWith(107);
  });

  it("key mode also caps at 7 with '+2 more'", () => {
    mode.value = "key";
    renderTabs({ tabs: NINE, activeId: 100 });
    expect(tabButtons()).toHaveLength(7);
    expect(screen.getByRole("button", { name: /\+2 more/ })).toBeTruthy();
  });

  it("shows no '+N more' at 7 standups or fewer", () => {
    renderTabs({ tabs: NINE.slice(0, 7), activeId: 100 });
    expect(tabButtons()).toHaveLength(7); // anchor
    expect(screen.queryByRole("button", { name: /more/ })).toBeNull();
  });
});
