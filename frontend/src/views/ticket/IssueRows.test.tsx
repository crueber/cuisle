// WP-T8: IssueRows, the Subtasks and Linked-issues sections (ux-spec §14.4 "Subtasks and links"; props.ts IssueRowsProps).
//
// SPEC-NOTE (DOM): IssueRows renders up to two sections, each <section data-section="subtasks"> / "links" (the
//   TicketContent jump targets), each with an h2 head:
//     subtasks: "SUBTASKS <done>/<total>"  (done = the subtasksDone prop, total = subtasks.length)
//     links:    "LINKS <n>"                (n = total rows across all groups)
//   case-insensitive. A section with no rows is not rendered; with neither, nothing is rendered.
// SPEC-NOTE: the subtasks live in a <ul> named "Subtasks"; each link group is a <ul> whose accessible name is the
//   group label ("IS BLOCKED BY", "BLOCKS", ...) and which is preceded by that label as visible micro caps. Groups
//   keep the order they arrive in. Rows are <li>.
// SPEC-NOTE (row): each <li> holds ONE <button> whose accessible name contains the key and the summary; the button
//   shows the key, summary, status name (the lozenge text) and the assignee initials (nothing when ""). The <li>
//   carries data-done="true" only for done rows (muted and struck through by CSS; it is a list row, not a task
//   list, so no checkbox). Lozenge: the status element carries data-lane="<statusLozenge>" (tokens only, invariant 11).
// SPEC-NOTE: a click calls onOpen(row) once, with the row object it was given, whether or not the row is synced:
//   the parent decides modal (issueId > 0) versus browser (issueId 0 -> row.url). IssueRows itself never navigates and
//   makes no api calls. The modal/browser split is asserted at the TicketContent level.
import { fireEvent, render, screen, within } from "@testing-library/preact";
import { describe, expect, it, vi } from "vitest";
import type { IssueRow, LinkGroup } from "../../api/types";
import { IssueRows } from "./IssueRows";

function row(over: Partial<IssueRow> & { key: string }): IssueRow {
  return {
    issueId: 0,
    summary: `Summary of ${over.key}`,
    typeKind: "subtask",
    statusName: "To Do",
    statusLozenge: "todo",
    assigneeInitials: "",
    url: `https://example.atlassian.net/browse/${over.key}`,
    done: false,
    ...over,
  };
}

const SUBS: IssueRow[] = [
  row({ key: "MOB-1422", statusName: "Done", statusLozenge: "done", done: true, assigneeInitials: "WK" }),
  row({ key: "MOB-1423", statusName: "Done", statusLozenge: "done", done: true, assigneeInitials: "IC" }),
  row({ key: "MOB-1424", statusName: "Done", statusLozenge: "done", done: true }),
  row({ key: "MOB-1425", statusName: "In Progress", statusLozenge: "prog", assigneeInitials: "JR" }),
  row({ key: "MOB-1426", statusName: "To Do", statusLozenge: "todo" }),
];
const GROUPS: LinkGroup[] = [
  { label: "IS BLOCKED BY", rows: [row({ key: "PLAT-444", issueId: 0, typeKind: "bug" })] },
  { label: "BLOCKS", rows: [row({ key: "MOB-1436", issueId: 111, typeKind: "story", statusName: "In Review", statusLozenge: "review" }), row({ key: "MOB-1437", issueId: 112, typeKind: "task" })] },
];

function mount(over: Partial<{ subtasks: IssueRow[]; subtasksDone: number; linkGroups: LinkGroup[] }> = {}) {
  const onOpen = vi.fn();
  const view = render(<IssueRows subtasks={SUBS} subtasksDone={3} linkGroups={GROUPS} onOpen={onOpen} {...over} />);
  return { onOpen, ...view };
}

describe("IssueRows: subtasks", () => {
  it("heads the section SUBTASKS done/total using the subtasksDone prop", () => {
    const { container } = mount();
    const sec = container.querySelector('section[data-section="subtasks"]') as HTMLElement;
    expect(sec).not.toBeNull();
    expect(within(sec).getByRole("heading", { name: /^subtasks\s*3\/5$/i })).toBeTruthy();
  });

  it("uses the subtasksDone prop, not a recount, for the done figure", () => {
    mount({ subtasksDone: 4 });
    expect(screen.getByRole("heading", { name: /^subtasks\s*4\/5$/i })).toBeTruthy();
  });

  it("draws one row per subtask in order with key, summary, status and assignee initials", () => {
    mount();
    const list = screen.getByRole("list", { name: /^subtasks$/i });
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(5);
    expect(items.map((li) => li.textContent?.match(/MOB-\d+/)?.[0])).toEqual(["MOB-1422", "MOB-1423", "MOB-1424", "MOB-1425", "MOB-1426"]);
    const doing = items[3] as HTMLElement;
    expect(within(doing).getByText("MOB-1425")).toBeTruthy();
    expect(within(doing).getByText("Summary of MOB-1425")).toBeTruthy();
    expect(within(doing).getByText("In Progress")).toBeTruthy();
    expect(within(doing).getByText("JR")).toBeTruthy();
  });

  it("marks only the done subtasks with data-done=true and puts the status in a data-lane lozenge", () => {
    mount();
    const items = within(screen.getByRole("list", { name: /^subtasks$/i })).getAllByRole("listitem");
    expect(items.map((li) => li.getAttribute("data-done") === "true")).toEqual([true, true, true, false, false]);
    expect(within(items[0] as HTMLElement).getByText("Done").closest("[data-lane]")?.getAttribute("data-lane")).toBe("done");
    expect(within(items[3] as HTMLElement).getByText("In Progress").closest("[data-lane]")?.getAttribute("data-lane")).toBe("prog");
  });

  it("does not draw a checkbox (a list row, not a task list)", () => {
    mount();
    expect(screen.getByRole("list", { name: /^subtasks$/i })).toBeTruthy();
    expect(screen.queryAllByRole("checkbox")).toEqual([]);
  });

  it("calls onOpen with the clicked subtask row", () => {
    const { onOpen } = mount();
    fireEvent.click(screen.getByRole("button", { name: /MOB-1425/ }));
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onOpen.mock.calls[0]?.[0]).toBe(SUBS[3]);
  });

  it("calls onOpen for a row that is not synced (issueId 0) with the row, so the parent can open row.url", () => {
    const { onOpen } = mount();
    fireEvent.click(screen.getByRole("button", { name: /MOB-1422/ }));
    expect(onOpen).toHaveBeenCalledTimes(1);
    const arg = onOpen.mock.calls[0]?.[0] as IssueRow;
    expect(arg.issueId).toBe(0);
    expect(arg.url).toBe("https://example.atlassian.net/browse/MOB-1422");
  });
});

describe("IssueRows: linked issues", () => {
  it("heads the section LINKS with the total number of rows across groups", () => {
    const { container } = mount();
    const sec = container.querySelector('section[data-section="links"]') as HTMLElement;
    expect(sec).not.toBeNull();
    expect(within(sec).getByRole("heading", { name: /^links\s*3$/i })).toBeTruthy();
  });

  it("groups rows under their label, in the given order", () => {
    mount();
    const blockedBy = screen.getByRole("list", { name: /^is blocked by$/i });
    const blocks = screen.getByRole("list", { name: /^blocks$/i });
    expect(within(blockedBy).getAllByRole("listitem")).toHaveLength(1);
    expect(within(blockedBy).getByText("PLAT-444")).toBeTruthy();
    expect(within(blocks).getAllByRole("listitem")).toHaveLength(2);
    expect(within(blocks).getByText("MOB-1436")).toBeTruthy();
    expect(within(blocks).getByText("MOB-1437")).toBeTruthy();
    expect(blockedBy.compareDocumentPosition(blocks) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("shows each group's label as visible micro caps text", () => {
    const { container } = mount();
    const sec = container.querySelector('section[data-section="links"]') as HTMLElement;
    expect(within(sec).getByText(/^is blocked by$/i)).toBeTruthy();
    expect(within(sec).getByText(/^blocks$/i)).toBeTruthy();
  });

  it("calls onOpen with a synced link row (issueId > 0)", () => {
    const { onOpen } = mount();
    fireEvent.click(screen.getByRole("button", { name: /MOB-1436/ }));
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect((onOpen.mock.calls[0]?.[0] as IssueRow).issueId).toBe(111);
  });

  it("calls onOpen with an unsynced link row (issueId 0) too", () => {
    const { onOpen } = mount();
    fireEvent.click(screen.getByRole("button", { name: /PLAT-444/ }));
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect((onOpen.mock.calls[0]?.[0] as IssueRow).issueId).toBe(0);
  });
});

describe("IssueRows: empty sections are omitted", () => {
  it("renders no Subtasks section without subtasks, but keeps Links", () => {
    const { container } = mount({ subtasks: [], subtasksDone: 0 });
    expect(container.querySelector('section[data-section="links"]')).not.toBeNull();
    expect(container.querySelector('section[data-section="subtasks"]')).toBeNull();
    expect(screen.queryByRole("heading", { name: /subtasks/i })).toBeNull();
  });

  it("renders no Links section without link groups, but keeps Subtasks", () => {
    const { container } = mount({ linkGroups: [] });
    expect(container.querySelector('section[data-section="subtasks"]')).not.toBeNull();
    expect(container.querySelector('section[data-section="links"]')).toBeNull();
  });

  it("ignores a link group that has no rows", () => {
    mount({ linkGroups: [{ label: "RELATES TO", rows: [] }, ...GROUPS] });
    expect(screen.queryByRole("list", { name: /^relates to$/i })).toBeNull();
    expect(screen.getByRole("heading", { name: /^links\s*3$/i })).toBeTruthy();
  });

  it("renders nothing at all when both are empty", () => {
    const view = mount({ subtasks: [], subtasksDone: 0, linkGroups: [] });
    expect(view.container.querySelector("section")).toBeNull();
    expect(screen.queryByRole("heading")).toBeNull();
    view.rerender(<IssueRows subtasks={SUBS} subtasksDone={3} linkGroups={[]} onOpen={vi.fn()} />);
    expect(screen.getByRole("heading", { name: /^subtasks\s*3\/5$/i })).toBeTruthy(); // and they appear once present
  });
});
