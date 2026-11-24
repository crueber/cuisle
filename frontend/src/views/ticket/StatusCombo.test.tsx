// WP-T7 tests: StatusCombo (ux-spec §14.6 "Status combobox, open state"; props frozen in props.ts; AGENTS invariant 4).
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - The control is the SHARED Combobox: a `.cbx` wrapper holding an input with role="combobox" and accessible name
//     /status/i whose value is statusName; no native <select> anywhere.
//   - A lane swatch for the current status: an element with data-lane="<lane>" outside the popup.
//   - Options are the Combobox's role="option" rows. Each row's content has `→ <name>` and a swatch with
//     data-lane="<option.toLane>". Order: every normal transition first, then every needs-screen one, each group in the
//     order `load` returned them. A needs-screen row contains an element with data-needs-screen="true" holding the text
//     "screen" and "↗ Jira"; the FIRST needs-screen row also carries data-divider="true" on an element in its content
//     (the Combobox list is flat, so the "divider below" lives inside that row's content; the 2px rule is CSS).
//   - Choosing a normal row calls onSelect(option) and nothing else; choosing a needs-screen row calls
//     onOpenInJira(option.url) and does NOT call onSelect; the field then shows the CURRENT status again (not the target).
//   - `load(signal)` is called once each time the list opens (0 calls while closed, none per keystroke: typing filters the
//     loaded list locally). The `open` prop is a controlled open (the S key): true opens the list and loads; false closes
//     it (the load may start a moment after the list appears, so tests wait for it). onOpenChange(true|false) is reported for every open / close the user causes (click, Esc, choosing a row).
//   - offline (prop) or the loaded list's own `offline: true` flag: the list can still be opened and shows the text
//     "Offline · open in Jira"; choosing a row never calls onSelect. `disabled` and `saving` make the input disabled
//     (nothing opens, no load). A rejected load shows the Combobox's "Couldn't load results." text.
import { render, screen, waitFor, within } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { TransitionList, TransitionOption } from "../../api/types";
import { StatusCombo } from "./StatusCombo";
import type { StatusComboProps } from "./props";

const URL_1421 = "https://example.atlassian.net/browse/MOB-1421";
const opt = (id: string, name: string, toLane: string, needsScreen = false): TransitionOption => ({
  id, name, toStatusName: name, toLane, needsScreen, url: URL_1421,
});
// Deliberately interleaved: the component must put the needs-screen ones below the rest.
const TODO = opt("11", "To Do", "todo");
const DONE = opt("51", "Done", "done", true);
const REVIEW = opt("31", "In Review", "review");
const QA = opt("41", "QA", "qa");
const WONT = opt("61", "Won't Do", "done", true);
const LIST: TransitionList = { items: [TODO, DONE, REVIEW, QA, WONT], fetchedAt: 1795424400000, offline: false };

function props(over: Partial<StatusComboProps> = {}): StatusComboProps {
  return {
    id: "ticket-status",
    statusName: "In Progress",
    lane: "prog",
    disabled: false,
    offline: false,
    saving: false,
    load: vi.fn(async () => LIST),
    onSelect: vi.fn(),
    onOpenInJira: vi.fn(),
    onOpenChange: vi.fn(),
    ...over,
  };
}
const input = () => screen.getByRole("combobox", { name: /status/i }) as HTMLInputElement;
const optionNames = () => screen.getAllByRole("option").map((o) => (o.textContent ?? "").replace(/\s+/g, " ").trim());
async function openByClick(p: StatusComboProps) {
  const user = userEvent.setup();
  const view = render(<StatusCombo {...p} />);
  await user.click(input());
  await screen.findByRole("listbox");
  return { user, ...view };
}

describe("the control is the shared Combobox", () => {
  it("rendersAComboboxWithTheCurrentStatusAndItsLaneSwatch_AndNoNativeSelect", () => {
    const { container } = render(<StatusCombo {...props()} />);
    expect(container.querySelector(".cbx")).not.toBeNull();
    expect(input().value).toBe("In Progress");
    expect(container.querySelector('[data-lane="prog"]')).not.toBeNull();
    expect(container.querySelector("select")).toBeNull();
  });

  it("doesNotLoadTransitionsUntilItOpens", async () => {
    const p = props();
    render(<StatusCombo {...p} />);
    expect(input().value).toBe("In Progress");
    expect(p.load).not.toHaveBeenCalled();
    await userEvent.setup().click(input());
    await screen.findByRole("listbox");
    await waitFor(() => expect(p.load).toHaveBeenCalledTimes(1));
    expect(p.load).toHaveBeenCalledWith(expect.any(AbortSignal));
  });
});

describe("the open list", () => {
  it("listsNormalTransitionsFirstThenTheNeedsScreenOnes", async () => {
    await openByClick(props());
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(5));
    const names = optionNames();
    expect(names[0]).toMatch(/→\s*To Do/);
    expect(names[1]).toMatch(/→\s*In Review/);
    expect(names[2]).toMatch(/→\s*QA/);
    expect(names[3]).toMatch(/→\s*Done/);
    expect(names[4]).toMatch(/→\s*Won't Do/);
  });

  it("marksNeedsScreenRowsAsMutedScreenJira_WithTheDividerOnTheFirstOne", async () => {
    await openByClick(props());
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(5));
    const rows = screen.getAllByRole("option");
    for (const normal of rows.slice(0, 3)) {
      expect(normal.querySelector('[data-needs-screen="true"]')).toBeNull();
      expect(normal.querySelector('[data-divider="true"]')).toBeNull();
    }
    for (const screenRow of rows.slice(3)) {
      const marker = screenRow.querySelector('[data-needs-screen="true"]') as HTMLElement;
      expect(marker).not.toBeNull();
      expect(marker.textContent).toContain("screen");
      expect(marker.textContent).toContain("↗ Jira");
    }
    expect(rows[3]!.querySelector('[data-divider="true"]')).not.toBeNull();
    expect(rows[4]!.querySelector('[data-divider="true"]')).toBeNull();
  });

  it("drawsALaneSwatchOnEveryRow", async () => {
    await openByClick(props());
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(5));
    const lanes = screen.getAllByRole("option").map((o) => o.querySelector("[data-lane]")?.getAttribute("data-lane"));
    expect(lanes).toEqual(["todo", "review", "qa", "done", "done"]);
  });

  it("filtersLocallyWhileTyping_WithoutLoadingAgain", async () => {
    const p = props();
    const { user } = await openByClick(p);
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(5));
    await user.clear(input()); // the field starts with the current status as its text
    await user.type(input(), "rev");
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(1));
    expect(optionNames()[0]).toMatch(/In Review/);
    expect(p.load).toHaveBeenCalledTimes(1);
  });

  it("loadsAgainEachTimeItOpens", async () => {
    const p = props();
    const { user } = await openByClick(p);
    await waitFor(() => expect(p.load).toHaveBeenCalledTimes(1));
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    await user.click(input());
    await screen.findByRole("listbox");
    await waitFor(() => expect(p.load).toHaveBeenCalledTimes(2));
  });

  it("showsTheComboboxLoadErrorWhenLoadRejects", async () => {
    await openByClick(props({ load: vi.fn(async () => Promise.reject(new Error("boom"))) }));
    expect(await screen.findByText(/couldn't load/i)).toBeTruthy();
  });
});

describe("choosing", () => {
  it("aNormalTransition_CallsOnSelectWithTheOptionOnly", async () => {
    const p = props();
    const { user } = await openByClick(p);
    await user.click(await screen.findByRole("option", { name: /In Review/ }));
    expect(p.onSelect).toHaveBeenCalledTimes(1);
    expect(p.onSelect).toHaveBeenCalledWith(REVIEW);
    expect(p.onOpenInJira).not.toHaveBeenCalled();
  });

  it("aNeedsScreenTransition_OpensJiraInsteadOfSelecting", async () => {
    const p = props();
    const { user } = await openByClick(p);
    await user.click(await screen.findByRole("option", { name: /Done/ }));
    expect(p.onOpenInJira).toHaveBeenCalledTimes(1);
    expect(p.onOpenInJira).toHaveBeenCalledWith(URL_1421);
    expect(p.onSelect).not.toHaveBeenCalled();
    await waitFor(() => expect(input().value).toBe("In Progress")); // the field goes back to the current status
  });

  it("keyboardHomeAndEnter_ChooseTheFirstNormalTransition", async () => {
    const p = props();
    const { user } = await openByClick(p);
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(5));
    await user.keyboard("{Home}{Enter}");
    expect(p.onSelect).toHaveBeenCalledTimes(1);
    expect(p.onSelect).toHaveBeenCalledWith(TODO);
  });
});

describe("controlled open (the S key) and onOpenChange", () => {
  it("openTrue_OpensTheListAndLoadsOnce_AndFalseClosesIt", async () => {
    const p = props({ open: false });
    const view = render(<StatusCombo {...p} />);
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(p.load).not.toHaveBeenCalled();
    view.rerender(<StatusCombo {...p} open={true} />);
    expect(await screen.findByRole("listbox")).toBeTruthy();
    await waitFor(() => expect(p.load).toHaveBeenCalledTimes(1));
    view.rerender(<StatusCombo {...p} open={false} />);
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
  });

  it("reportsEveryOpenAndCloseTheUserCauses", async () => {
    const p = props();
    const { user } = await openByClick(p);
    expect(p.onOpenChange).toHaveBeenLastCalledWith(true);
    await user.keyboard("{Escape}");
    expect(p.onOpenChange).toHaveBeenLastCalledWith(false);
    await user.click(input());
    await screen.findByRole("listbox");
    await user.click(await screen.findByRole("option", { name: /QA/ }));
    expect(p.onOpenChange).toHaveBeenLastCalledWith(false);
  });
});

describe("offline, disabled, saving", () => {
  it.each([
    ["the offline prop", { offline: true }, LIST],
    ["the loaded list being offline", {}, { ...LIST, offline: true }],
  ] as [string, Partial<StatusComboProps>, TransitionList][])("showsTheCachedListWithAnOfflineNote_%s", async (_n, over, list) => {
    const p = props({ ...over, load: vi.fn(async () => list) });
    const { user } = await openByClick(p);
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(5));
    expect(screen.getByText(/Offline · open in Jira/)).toBeTruthy();
    await user.click(within(screen.getByRole("listbox")).getByRole("option", { name: /In Review/ }));
    expect(p.onSelect).not.toHaveBeenCalled();
  });

  it("disabled_CannotBeOpened_AndNeverLoads", async () => {
    const p = props({ disabled: true });
    render(<StatusCombo {...p} />);
    expect(input().disabled).toBe(true);
    expect(input().value).toBe("In Progress");
    await userEvent.setup().click(input());
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(p.load).not.toHaveBeenCalled();
  });

  it("saving_DisablesTheInputWhileTheWriteIsInFlight", () => {
    render(<StatusCombo {...props({ saving: true })} />);
    expect(input().disabled).toBe(true);
    expect(input().value).toBe("In Progress");
  });
});
