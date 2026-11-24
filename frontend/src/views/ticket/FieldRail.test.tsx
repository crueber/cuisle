// WP-T7 tests: FieldRail (ux-spec §14.6 field rail, §14.2 narrow strip and stacked labels; props frozen in props.ts).
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - Root: the first element child of the render container; data-layout="column|strip" mirrors the `layout` prop and
//     data-stacked="true|false" mirrors geometry.stackedLabels.
//   - Groups: one element per RailGroup with data-group="<group.id>" in the order given (state, details, custom,
//     development, dates), holding a heading (any level) whose text is group.label exactly ("State", "Custom fields", …;
//     CSS makes it caps). Layout "strip" renders ONLY the state group.
//   - Rows: an element per RailRow with data-field="<row.fieldId>" containing the sentence-case label text exactly as
//     given ("Fix version", never upper-cased in the DOM) and the value. No row for a field Go folded into notSet.
//   - "Not set": ONE line per group with notSet entries, text "Not set: A · B" (labels joined by " · "); none when empty.
//   - Value rendering (kind table, ux-spec §14.6): chips are elements with class `chip` (labels, components, versions,
//     options); every row with `mono: true` has a descendant with data-mono="true", and ONLY those rows do; users show initials and name (one
//     per line for `users`); a `url` row is an <a> with the urlLabel as text, title = the full url, and a click calls
//     onOpenUrl(url) and is default-prevented (the webview never navigates); `time` shows an element with
//     role="progressbar" and aria-valuenow = round(progress × 100); `paragraph` renders through AdfDoc inside the row
//     (an `.adf` element with data-size="field"); a `number` row without text shows the number.
//   - Status and Assignee are the shared Combobox (`.cbx`, role="combobox" named "Status" / "Assignee", no <select>).
//     StatusCombo gets lane = detail.row.lane and statusName from the status row's text. An empty assignee row renders the
//     combobox with the value "Unassigned" inside an element data-unassigned="true" (the dashed-red control).
//   - Assignee list: opening it calls loadAssignees(query, signal); the options are an "Unassigned" entry first, then the
//     users returned (display name as the label). Choosing a user calls onAssign(accountId); "Unassigned" calls onAssign("").
//   - Status list: opening it calls loadTransitions once; a normal option calls onTransition(option); a needs-screen
//     option calls onOpenUrl(option.url) and NOT onTransition (props.ts: "needsScreen options open option.url instead").
//   - Flag: role="switch" named /flag/i whose aria-checked mirrors detail.row.blocked; click calls onFlag(!checked). When
//     flagged the Flag row shows "BLOCKED" and the row's `sub` ("3d").
//   - Disabling: the three controls are disabled when !detail.can.<transition|assign|flag>, when `offline`, and while their
//     write is saving (writes[field].phase === "saving").
//   - Write state: "saving…" appears 250 ms after phase "saving" begins (not before); phase "failed" shows the reason in
//     an element with role="alert" inside that field's row, with a button named /retry/i that calls onRetry(field).
//   - openToken: a CHANGE of the token opens the matching combobox (status: list + one loadTransitions call; assignee:
//     aria-expanded="true"); re-rendering with the same token does not re-open it.
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import mob1421 from "../../api/fixtures/ticket_mob1421.json";
import mob1429 from "../../api/fixtures/ticket_mob1429.json";
import type { TicketDetail, TransitionList, TransitionOption, UserOption } from "../../api/types";
import type { AdfContext } from "../../components/adf/props";
import { FieldRail } from "./FieldRail";
import type { EditField, FieldRailProps, ModalGeometry, WriteState } from "./props";

const clone = <X,>(x: X): X => JSON.parse(JSON.stringify(x)) as X;
const D1421: TicketDetail = mob1421;
const D1429: TicketDetail = mob1429;

const WIDE: ModalGeometry = { modalW: 1320, modalH: 1000, contentW: 880, railW: 432, short: false, narrow: false, stackedLabels: false };
const STACKED: ModalGeometry = { modalW: 1000, modalH: 616, contentW: 700, railW: 292, short: true, narrow: false, stackedLabels: true };
const IDLE: WriteState = { phase: "idle", reason: "" };
const URL_1421 = "https://example.atlassian.net/browse/MOB-1421";
const opt = (id: string, name: string, toLane: string, needsScreen = false): TransitionOption => ({ id, name, toStatusName: name, toLane, needsScreen, url: URL_1421 });
const REVIEW = opt("31", "In Review", "review");
const DONE = opt("51", "Done", "done", true);
const TRANSITIONS: TransitionList = { items: [opt("11", "To Do", "todo"), DONE, REVIEW, opt("41", "QA", "qa")], fetchedAt: 1795424400000, offline: false };
const WREN: UserOption = { accountId: "wren", displayName: "Wren Kowalski", initials: "WK", onBoard: true };
const SILAS: UserOption = { accountId: "silas", displayName: "Silas Marchetti", initials: "SM", onBoard: true };

function ctx(): AdfContext {
  return {
    onOpenIssue: vi.fn(), onOpenUrl: vi.fn(), onOpenImage: vi.fn(), onOpenFile: vi.fn(), onLoadImage: vi.fn(),
    issueUrl: URL_1421, theme: "light",
  };
}
function props(over: Partial<FieldRailProps> = {}): FieldRailProps {
  return {
    detail: clone(D1421),
    geometry: WIDE,
    layout: "column",
    writes: { status: IDLE, assignee: IDLE, flag: IDLE },
    offline: false,
    openToken: null,
    ctx: ctx(),
    loadTransitions: vi.fn(async () => TRANSITIONS),
    loadAssignees: vi.fn(async () => [WREN, SILAS]),
    onTransition: vi.fn(),
    onAssign: vi.fn(),
    onFlag: vi.fn(),
    onRetry: vi.fn(),
    onOpenUrl: vi.fn(),
    ...over,
  };
}
function renderRail(over: Partial<FieldRailProps> = {}) {
  const p = props(over);
  const view = render(<FieldRail {...p} />);
  return { ...view, container: view.container as HTMLElement, p, root: view.container.firstElementChild as HTMLElement };
}
const row = (container: HTMLElement, fieldId: string) => container.querySelector(`[data-field="${fieldId}"]`) as HTMLElement;
const statusInput = () => screen.getByRole("combobox", { name: "Status" }) as HTMLInputElement;
const assigneeInput = () => screen.getByRole("combobox", { name: "Assignee" }) as HTMLInputElement;
const flagSwitch = () => screen.getByRole("switch", { name: /flag/i }) as HTMLButtonElement;

describe("groups, labels and the Not set line", () => {
  it("rendersTheGroupsInSpecOrder_EachWithItsHeading", () => {
    const { container } = renderRail();
    const groups = [...container.querySelectorAll("[data-group]")];
    expect(groups.map((g) => g.getAttribute("data-group"))).toEqual(["state", "details", "custom", "development", "dates"]);
    const heads = ["State", "Details", "Custom fields", "Development", "Dates"];
    groups.forEach((g, i) => expect(within(g as HTMLElement).getByRole("heading", { name: heads[i]! })).toBeTruthy());
  });

  it("saysThatEditsGoToJira_InTheStateGroup", () => {
    const { container } = renderRail();
    const state = container.querySelector('[data-group="state"]') as HTMLElement;
    expect(within(state).getByText("edits go to Jira")).toBeTruthy();
  });

  it("keepsLabelsInSentenceCase_AsGiven", () => {
    const { container } = renderRail();
    expect(row(container, "fixVersions").textContent).toContain("Fix version");
    expect(within(row(container, "fixVersions")).getByText("Fix version")).toBeTruthy();
    expect(within(row(container, "customfield_10101")).getByText("User impact")).toBeTruthy();
  });

  it("endsAGroupWithOneNotSetLine_AndOmitsItWhenNothingIsMissing", () => {
    const { container } = renderRail();
    const details = container.querySelector('[data-group="details"]') as HTMLElement;
    expect(within(details).getAllByText(/^Not set:/)).toHaveLength(1);
    expect(within(details).getByText("Not set: Environment · Original estimate")).toBeTruthy();
    const state = container.querySelector('[data-group="state"]') as HTMLElement;
    expect(within(state).queryByText(/Not set/)).toBeNull();
    expect(within(state).getByRole("heading", { name: "State" })).toBeTruthy();
  });

  it("neverRendersARowForAFieldThatWasFoldedIntoNotSet", () => {
    const { container } = renderRail();
    expect(row(container, "environment")).toBeNull();
    expect(row(container, "priority")).not.toBeNull();
  });
});

describe("parent row", () => {
  it("opensTheParentWhenClicked", () => {
    const onOpenParent = vi.fn();
    const { container } = renderRail({ onOpenParent });
    const btn = within(row(container, "parent")).getByRole("button", { name: /MOB-1200/ });
    fireEvent.click(btn);
    expect(onOpenParent).toHaveBeenCalledTimes(1);
  });

  it("isPlainTextWithoutAHandler", () => {
    const { container } = renderRail({ onOpenParent: undefined });
    expect(within(row(container, "parent")).queryByRole("button")).toBeNull();
  });
});

describe("value rendering by kind", () => {
  const CONTAINS: [string, string[]][] = [
    ["reporter", ["Reporter", "SM", "Silas Marchetti"]],
    ["priority", ["Priority", "High"]],
    ["parent", ["Parent", "MOB-1200", "Recipe units v2"]],
    ["sprint", ["Sprint", "MOB Sprint 47", "ends 1 Dec"]],
    ["customfield_10016", ["Points", "5"]],
    ["customfield_10102", ["Rollout", "20 %"]],
    ["customfield_10104", ["Region", "North America › United States"]],
    ["customfield_10105", ["QA owner", "WK", "Wren Kowalski"]],
    ["customfield_10106", ["Reviewers", "Jonas Reyes", "Noor Abadi"]],
    ["customfield_10107", ["Team", "Saffron Mobile"]],
    ["customfield_10108", ["Approvers", "mobile-leads", "design"]],
    ["customfield_10110", ["Experiment", "units-v2-us-only"]],
    ["customfield_10113", ["Owning project", "Saffron Mobile (MOB)"]],
    ["customfield_10114", ["Legacy rank", '{"lexo":"0|i0001r:"}']],
    ["dev.branch", ["Branch", "feature/MOB-1421-unit-toggle"]],
    ["dev.commits", ["7 commits", "last 2h"]],
    ["dev.pr.418", [["#", "418"].join(""), "OPEN", "Unit preference API", "mob-web"]], // joined: the tokens-only lint rule mistakes the literal "#418" for a hex colour
    ["watches", ["Watchers", "4"]],
  ];
  it.each(CONTAINS)("renders_%s", (fieldId, parts) => {
    const { container } = renderRail();
    const r = row(container, fieldId);
    expect(r).not.toBeNull();
    for (const part of parts) expect(r.textContent).toContain(part);
  });

  it.each([
    ["labels", ["units", "recipes", "beta-us"]],
    ["components", ["Recipe page", "Shopping list"]],
    ["fixVersions", ["2026.12"]],
    ["customfield_10103", ["iOS", "Android"]],
  ])("drawsChipsFor_%s", (fieldId, texts) => {
    const { container } = renderRail();
    const chips = [...row(container, fieldId).querySelectorAll(".chip")].map((c) => c.textContent?.trim());
    expect(chips).toEqual(texts);
  });

  it("drawsThePullRequestRepositoryAsMutedSecondaryText_AndOmitsItWhenThereIsNone", () => {
    const { container } = renderRail();
    const sub = row(container, "dev.pr.418").querySelector(".fr-pr .fr-sub");
    expect(sub?.textContent).toBe("mob-web");
    expect(row(container, "dev.pr.418").querySelector(".fr-pr > span:not(.fr-pr-state):not(.fr-sub)")?.textContent).toBe("Unit preference API");
    // rows synced before the title was recorded carry the repository as their text and no secondary
    const legacy = structuredClone(D1421);
    const pr = legacy.rail.flatMap((g) => g.rows).find((r) => r.fieldId === "dev.pr.418");
    if (!pr) throw new Error("no PR row in the fixture");
    pr.text = "mob-web";
    pr.sub = "";
    const { container: c2 } = renderRail({ detail: legacy });
    expect(row(c2, "dev.pr.418").querySelector(".fr-sub")).toBeNull();
    expect(row(c2, "dev.pr.418").textContent).toContain("mob-web");
  });

  it("marksEveryMonoRowAndNoOther", () => {
    const { container } = renderRail();
    const rows = D1421.rail.flatMap((g) => g.rows).filter((r) => r.edit === "");
    expect(rows.some((r) => r.mono)).toBe(true);
    for (const r of rows) {
      const hasMono = row(container, r.fieldId).querySelector('[data-mono="true"]') !== null;
      expect([r.fieldId, hasMono]).toEqual([r.fieldId, r.mono]);
    }
  });

  it("drawsTheTimeTrackingBar_AsAProgressbar", () => {
    const { container } = renderRail();
    const bar = within(row(container, "timetracking")).getByRole("progressbar");
    expect(row(container, "timetracking").textContent).toContain("14h of 24h");
    expect(bar.getAttribute("aria-valuenow")).toBe("58");
  });

  it("rendersAUrlAsALinkWithAFullUrlTitle_AndOpensItWithoutNavigating", () => {
    const { container, p } = renderRail();
    const link = within(row(container, "customfield_10109")).getByRole("link", { name: "docs.example.com › units-v2" });
    expect(link.getAttribute("title")).toBe("https://docs.example.com/recipes/units-v2");
    const notPrevented = fireEvent.click(link);
    expect(notPrevented).toBe(false);
    expect(p.onOpenUrl).toHaveBeenCalledTimes(1);
    expect(p.onOpenUrl).toHaveBeenCalledWith("https://docs.example.com/recipes/units-v2");
  });

  it("rendersAParagraphFieldThroughAdfDoc", () => {
    const { container } = renderRail();
    const doc = row(container, "customfield_10112").querySelector(".adf");
    expect(doc).not.toBeNull();
    expect(doc!.getAttribute("data-size")).toBe("field");
  });
});

describe("the State controls", () => {
  it("usesTheSharedComboboxForStatusAndAssignee_NeverANativeSelect", () => {
    const { container } = renderRail();
    expect(statusInput().value).toBe("In Progress");
    expect(assigneeInput().value).toBe("Iris Calloway");
    expect(statusInput().closest(".cbx")).not.toBeNull();
    expect(assigneeInput().closest(".cbx")).not.toBeNull();
    expect(container.querySelector("select")).toBeNull();
  });

  it("showsAnEmptyAssigneeAsTheUnassignedControl", () => {
    const detail = clone(D1421);
    detail.row.assignee = "";
    for (const g of detail.rail) for (const r of g.rows) if (r.fieldId === "assignee") r.users = [];
    const { container } = renderRail({ detail });
    expect(assigneeInput().value).toBe("Unassigned");
    expect(row(container, "assignee").querySelector('[data-unassigned="true"]')).not.toBeNull();
  });

  it("assigneeOptions_StartWithUnassigned_ThenTheLoadedUsers_AndChoosingCallsOnAssign", async () => {
    const { p } = renderRail();
    const user = userEvent.setup();
    await user.click(assigneeInput());
    await screen.findByRole("listbox");
    await waitFor(() => expect(screen.getAllByRole("option").length).toBe(3));
    expect(screen.getAllByRole("option").map((o) => o.textContent?.trim())).toEqual([
      expect.stringContaining("Unassigned"), expect.stringContaining("Wren Kowalski"), expect.stringContaining("Silas Marchetti"),
    ]);
    expect(p.loadAssignees).toHaveBeenCalledWith("", expect.any(AbortSignal));
    await user.click(screen.getByRole("option", { name: /Wren Kowalski/ }));
    expect(p.onAssign).toHaveBeenCalledTimes(1);
    expect(p.onAssign).toHaveBeenCalledWith("wren");
  });

  it("choosingUnassigned_CallsOnAssignWithAnEmptyId", async () => {
    const { p } = renderRail();
    const user = userEvent.setup();
    await user.click(assigneeInput());
    await user.click(await screen.findByRole("option", { name: /Unassigned/ }));
    expect(p.onAssign).toHaveBeenCalledTimes(1);
    expect(p.onAssign).toHaveBeenCalledWith("");
  });

  it("typingInTheAssigneeCombobox_SearchesPeopleWithTheQuery", async () => {
    const { p } = renderRail();
    const user = userEvent.setup();
    await user.clear(assigneeInput());
    await user.type(assigneeInput(), "wr");
    await waitFor(() => expect(p.loadAssignees).toHaveBeenCalledWith("wr", expect.any(AbortSignal)));
  });

  it("opensTheStatusListAndLoadsTransitionsOnlyThen", async () => {
    const { p } = renderRail();
    expect(p.loadTransitions).not.toHaveBeenCalled();
    await userEvent.setup().click(statusInput());
    await screen.findByRole("listbox");
    await waitFor(() => expect(p.loadTransitions).toHaveBeenCalledTimes(1));
  });

  it("aNormalTransition_CallsOnTransition_AndANeedsScreenOneOpensJiraInstead", async () => {
    const { p } = renderRail();
    const user = userEvent.setup();
    await user.click(statusInput());
    await user.click(await screen.findByRole("option", { name: /In Review/ }));
    expect(p.onTransition).toHaveBeenCalledTimes(1);
    expect(p.onTransition).toHaveBeenCalledWith(REVIEW);
    expect(p.onOpenUrl).not.toHaveBeenCalled();

    await user.click(statusInput());
    await user.click(await screen.findByRole("option", { name: /Done/ }));
    expect(p.onOpenUrl).toHaveBeenCalledTimes(1);
    expect(p.onOpenUrl).toHaveBeenCalledWith(URL_1421);
    expect(p.onTransition).toHaveBeenCalledTimes(1);
  });

  it("flagSwitch_ReflectsBlocked_AndTogglesThroughOnFlag", () => {
    const off = renderRail();
    expect(flagSwitch().getAttribute("aria-checked")).toBe("false");
    fireEvent.click(flagSwitch());
    expect(off.p.onFlag).toHaveBeenCalledTimes(1);
    expect(off.p.onFlag).toHaveBeenCalledWith(true);
    off.unmount();

    const on = renderRail({ detail: clone(D1429) });
    expect(flagSwitch().getAttribute("aria-checked")).toBe("true");
    fireEvent.click(flagSwitch());
    expect(on.p.onFlag).toHaveBeenCalledWith(false);
  });

  it("showsTheBlockedLozengeAndDaysOnlyWhenFlagged", () => {
    const on = renderRail({ detail: clone(D1429) });
    expect(row(on.container, "flagged").textContent).toContain("BLOCKED");
    expect(row(on.container, "flagged").textContent).toContain("3d");
    on.unmount();
    const off = renderRail();
    expect(flagSwitch()).toBeTruthy();
    expect(row(off.container, "flagged").textContent).not.toContain("BLOCKED");
  });
});

describe("disabled controls", () => {
  it.each([
    ["transition", "status"],
    ["assign", "assignee"],
    ["flag", "flag"],
  ] as const)("disablesTheControlWhenTheSourceCannot_%s", (cap, field) => {
    const detail = clone(D1421);
    detail.can[cap] = false;
    renderRail({ detail });
    const controls = { status: statusInput(), assignee: assigneeInput(), flag: flagSwitch() };
    for (const [name, el] of Object.entries(controls)) expect([name, el.disabled]).toEqual([name, name === field]);
  });

  it("disablesAllThreeWhileOffline", () => {
    renderRail({ offline: true });
    expect(statusInput().disabled).toBe(true);
    expect(assigneeInput().disabled).toBe(true);
    expect(flagSwitch().disabled).toBe(true);
  });

  it.each(["status", "assignee", "flag"] as EditField[])("disablesTheControlWhileItsWriteIsSaving_%s", (field) => {
    const writes = { status: IDLE, assignee: IDLE, flag: IDLE, [field]: { phase: "saving", reason: "" } } as Record<EditField, WriteState>;
    renderRail({ writes });
    const controls = { status: statusInput(), assignee: assigneeInput(), flag: flagSwitch() };
    for (const [name, el] of Object.entries(controls)) expect([name, el.disabled]).toEqual([name, name === field]);
  });
});

describe("write state: saving… after 250 ms, failed reason with Retry", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it.each(["status", "assignee", "flag"] as EditField[])("showsSavingOnlyAfter250ms_%s", (field) => {
    const writes = { status: IDLE, assignee: IDLE, flag: IDLE, [field]: { phase: "saving", reason: "" } } as Record<EditField, WriteState>;
    const { container } = renderRail({ writes });
    const fieldRow = row(container, field === "flag" ? "flagged" : field);
    expect(fieldRow).not.toBeNull();
    expect(within(fieldRow).queryByText("saving…")).toBeNull();
    act(() => void vi.advanceTimersByTime(249));
    expect(within(fieldRow).queryByText("saving…")).toBeNull();
    act(() => void vi.advanceTimersByTime(1));
    expect(within(fieldRow).getByText("saving…")).toBeTruthy();
  });

  it.each([
    ["status", "status"],
    ["assignee", "assignee"],
    ["flag", "flagged"],
  ] as [EditField, string][])("showsTheFailureReasonAsAnAlertWithRetry_%s", (field, fieldId) => {
    const reason = "Jira rejected the change: Resolution is required";
    const writes = { status: IDLE, assignee: IDLE, flag: IDLE, [field]: { phase: "failed", reason } } as Record<EditField, WriteState>;
    const { container, p } = renderRail({ writes });
    const alert = within(row(container, fieldId)).getByRole("alert");
    expect(alert.textContent).toContain(reason);
    fireEvent.click(within(alert).getByRole("button", { name: /retry/i }));
    expect(p.onRetry).toHaveBeenCalledTimes(1);
    expect(p.onRetry).toHaveBeenCalledWith(field);
    expect(screen.getAllByRole("alert")).toHaveLength(1); // the other fields show nothing
  });
});

describe("openToken (the S and A keys)", () => {
  it("aChangedStatusToken_OpensTheStatusList_AndTheSameTokenDoesNotReopenIt", async () => {
    const p = props();
    const view = render(<FieldRail {...p} openToken={null} />);
    expect(screen.queryByRole("listbox")).toBeNull();
    view.rerender(<FieldRail {...p} openToken={{ field: "status", token: 1 }} />);
    expect(await screen.findByRole("listbox")).toBeTruthy();
    await waitFor(() => expect(p.loadTransitions).toHaveBeenCalledTimes(1));

    await userEvent.setup().keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    view.rerender(<FieldRail {...p} openToken={{ field: "status", token: 1 }} />);
    expect(screen.queryByRole("listbox")).toBeNull();

    view.rerender(<FieldRail {...p} openToken={{ field: "status", token: 2 }} />);
    expect(await screen.findByRole("listbox")).toBeTruthy();
    await waitFor(() => expect(p.loadTransitions).toHaveBeenCalledTimes(2));
  });

  it("anAssigneeToken_OpensTheAssigneeCombobox", async () => {
    const p = props();
    const view = render(<FieldRail {...p} openToken={null} />);
    expect(assigneeInput().getAttribute("aria-expanded")).toBe("false");
    view.rerender(<FieldRail {...p} openToken={{ field: "assignee", token: 1 }} />);
    await waitFor(() => expect(assigneeInput().getAttribute("aria-expanded")).toBe("true"));
    expect(statusInput().getAttribute("aria-expanded")).toBe("false");
  });
});

describe("layouts", () => {
  it("columnLayout_RendersEveryGroup_AndMirrorsTheStackedFlag", () => {
    const wide = renderRail({ geometry: WIDE });
    expect(wide.root.getAttribute("data-layout")).toBe("column");
    expect(wide.root.getAttribute("data-stacked")).toBe("false");
    expect(wide.container.querySelectorAll("[data-group]")).toHaveLength(5);
    wide.unmount();

    const stacked = renderRail({ geometry: STACKED });
    expect(stacked.root.getAttribute("data-layout")).toBe("column");
    expect(stacked.root.getAttribute("data-stacked")).toBe("true");
    expect(stacked.container.querySelectorAll("[data-group]")).toHaveLength(5);
  });

  it("stripLayout_RendersOnlyTheStateGroupWithItsThreeControls", () => {
    const { root, container } = renderRail({ layout: "strip" });
    expect(root.getAttribute("data-layout")).toBe("strip");
    expect([...container.querySelectorAll("[data-group]")].map((g) => g.getAttribute("data-group"))).toEqual(["state"]);
    expect(statusInput()).toBeTruthy();
    expect(assigneeInput()).toBeTruthy();
    expect(flagSwitch()).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Details" })).toBeNull();
    expect(screen.queryByText("Reporter")).toBeNull();
  });
});
