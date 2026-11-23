// Configure rail: StandupList (ux-spec §9.1 Left rail, §4 drafts, §9.3 ordered lists; product-spec F2/F7).
//
// SPEC-NOTE (approach): state/config is mocked via vi.mock (real signals kept); moveStandup is the only state call
//   StandupList makes itself (props.ts has no move callback, so reorder is wired inside the component). No shared
//   component is mocked: the rail only needs plain elements.
// SPEC-NOTE: each standup is an `li` (role listitem) in document order = the `standups` prop order. Selecting = clicking
//   anywhere on the item's name area (onSelect(id)). The selected item carries aria-current="true" on the item or on
//   an element inside it, and no other item does.
// SPEC-NOTE: item text: the name, and "‹spaceKey› · ‹n› people" ("1 person" for one). A tab with configured === false
//   shows a tag with the exact text "DRAFT"; configured tabs do not. needsSync tabs show text matching /needs sync/i.
// SPEC-NOTE: up/down are buttons inside the item, named /\bup\b/i and /\bdown\b/i, disabled on the first / last item,
//   and clicking one calls moveStandup(id, toPosition) with toPosition = the 0-based target index in the list (current
//   index -1 / +1) and does NOT call onSelect.
// SPEC-NOTE: the header reads "STANDUPS · N" (N = every item, drafts included; case-insensitive match), the footer
//   "Order here = tab order and the 1–N shortcuts.", and "+ New standup" is a button that calls onAdd once.

import { act, render, screen, within } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { StandupTab } from "../../api/types";
import { StandupList } from "./StandupList";

const st = vi.hoisted(() => ({
  loadConfig: vi.fn(async () => {}), saveField: vi.fn(), setSpace: vi.fn(), setBoard: vi.fn(), setColumnLane: vi.fn(),
  setWalkOrder: vi.fn(), setLaneLimit: vi.fn(), setSpeakerIncluded: vi.fn(), moveSpeaker: vi.fn(), sortSpeakers: vi.fn(),
  addSpeaker: vi.fn(), removeSpeaker: vi.fn(), validateFilter: vi.fn(), createStandup: vi.fn(async () => {}), duplicateStandup: vi.fn(async () => {}),
  copySettingsFrom: vi.fn(async () => {}), deleteStandup: vi.fn(async () => {}), moveStandup: vi.fn(async () => {}), leaveConfigure: vi.fn(async () => {}),
}));
vi.mock("../../state/config", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../state/config")>()),
  ...st,
}));

function tab(id: number, name: string, over: Partial<StandupTab> = {}): StandupTab {
  return { id, rev: 1, name, spaceKey: "MOB", position: id - 1, activeCount: 12, speakerCount: 7, configured: true, needsSync: false, lastSyncedAt: 0, ...over };
}
const TABS: StandupTab[] = [
  tab(1, "Saffron Mobile", { spaceKey: "MOB", speakerCount: 7 }),
  tab(2, "Payments", { spaceKey: "PAY", speakerCount: 1, needsSync: true }),
  tab(3, "Fresh idea", { spaceKey: "", speakerCount: 0, configured: false }),
];

const item = (name: string): HTMLElement => {
  const el = screen.getByText(name).closest('li, [role="listitem"]');
  if (!el) throw new Error(`no rail item for ${name}`);
  return el as HTMLElement;
};
const up = (name: string) => within(item(name)).getByRole("button", { name: /\bup\b/i }) as HTMLButtonElement;
const down = (name: string) => within(item(name)).getByRole("button", { name: /\bdown\b/i }) as HTMLButtonElement;

function renderList(over: Partial<{ selectedId: number | null; onSelect: (id: number) => void; onAdd: () => void; standups: StandupTab[] }> = {}) {
  const onSelect = over.onSelect ?? vi.fn();
  const onAdd = over.onAdd ?? vi.fn();
  render(<StandupList standups={over.standups ?? TABS} selectedId={over.selectedId === undefined ? 1 : over.selectedId} onSelect={onSelect} onAdd={onAdd} />);
  return { onSelect, onAdd };
}

beforeEach(() => {
  Object.values(st).forEach((f) => f.mockClear());
});

describe("StandupList: items", () => {
  it("renders one item per standup in order, including drafts", () => {
    renderList();
    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(3);
    expect(items[0]?.textContent).toContain("Saffron Mobile");
    expect(items[1]?.textContent).toContain("Payments");
    expect(items[2]?.textContent).toContain("Fresh idea");
  });

  it("shows the header count, the new-standup button and the order footnote", () => {
    renderList();
    expect(screen.getByText(/standups\s*·\s*3/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /new standup/i })).toBeTruthy();
    expect(screen.getByText(/tab order/i).textContent).toMatch(/shortcuts/i);
  });

  it("shows 'KEY · N people' (singular for one) under each name", () => {
    renderList();
    expect(item("Saffron Mobile").textContent).toContain("MOB · 7 people");
    expect(item("Payments").textContent).toContain("PAY · 1 person");
    expect(item("Payments").textContent).not.toContain("1 people");
  });

  it("marks drafts (no board yet) with a DRAFT tag and nothing else", () => {
    renderList();
    expect(screen.getAllByText("DRAFT")).toHaveLength(1);
    expect(within(item("Fresh idea")).getByText("DRAFT")).toBeTruthy();
    expect(within(item("Saffron Mobile")).queryByText("DRAFT")).toBeNull();
  });

  it("flags standups that need a sync", () => {
    renderList();
    expect(screen.getAllByText(/needs sync/i)).toHaveLength(1);
    expect(within(item("Payments")).getByText(/needs sync/i)).toBeTruthy();
  });

  it("marks only the selected item as current", () => {
    renderList({ selectedId: 2 });
    expect(screen.getAllByRole("listitem")).toHaveLength(3);
    const current = document.querySelectorAll('[aria-current="true"]');
    expect(current).toHaveLength(1);
    expect(item("Payments").contains(current[0] as Element)).toBe(true);
  });

  it("marks nothing as current when nothing is selected", () => {
    renderList({ selectedId: null });
    expect(screen.getAllByRole("listitem")).toHaveLength(3);
    expect(document.querySelectorAll('[aria-current="true"]')).toHaveLength(0);
  });
});

describe("StandupList: select and add", () => {
  it("clicking an item selects its standup", () => {
    const { onSelect } = renderList();
    act(() => { (screen.getByText("Payments") as HTMLElement).click(); });
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(2);
  });

  it("clicking a draft selects it too", () => {
    const { onSelect } = renderList();
    act(() => { (screen.getByText("Fresh idea") as HTMLElement).click(); });
    expect(onSelect).toHaveBeenCalledWith(3);
  });

  it("+ New standup calls onAdd once and selects nothing", () => {
    const { onAdd, onSelect } = renderList();
    act(() => { screen.getByRole("button", { name: /new standup/i }).click(); });
    expect(onAdd).toHaveBeenCalledTimes(1);
    expect(onSelect).not.toHaveBeenCalled();
  });
});

describe("StandupList: reorder", () => {
  it("moves a standup up to the previous index", () => {
    renderList();
    act(() => { up("Payments").click(); });
    expect(st.moveStandup).toHaveBeenCalledTimes(1);
    expect(st.moveStandup).toHaveBeenCalledWith(2, 0);
  });

  it("moves a standup down to the next index", () => {
    renderList();
    act(() => { down("Payments").click(); });
    expect(st.moveStandup).toHaveBeenCalledTimes(1);
    expect(st.moveStandup).toHaveBeenCalledWith(2, 2);
  });

  it("disables up on the first item and down on the last", () => {
    renderList();
    expect(up("Saffron Mobile").disabled).toBe(true);
    expect(down("Saffron Mobile").disabled).toBe(false);
    expect(up("Fresh idea").disabled).toBe(false);
    expect(down("Fresh idea").disabled).toBe(true);
  });

  it("reordering does not select the item", () => {
    const { onSelect } = renderList();
    act(() => { down("Saffron Mobile").click(); });
    expect(st.moveStandup).toHaveBeenCalledWith(1, 1);
    expect(onSelect).not.toHaveBeenCalled();
  });
});
