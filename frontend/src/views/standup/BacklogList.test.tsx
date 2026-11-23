// WP-25 tests: BacklogList, the Backlog section of the whole board (ux-spec §6.3, §10; product-spec F4).
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - Heading: an <h2> whose text starts with "Backlog" (caps by CSS), plus an element whose own text is
//              "top <rows> by rank" (rows = the configured count prop).
//   - Rows:    one <button> per item in the given order; text contains the key and the summary. Click or Enter calls
//              api().app.openIssue(key); BacklogList has no onOpen prop.
//   - Footnote: an element whose own text is exactly
//              "Showing the top <rows> as set in Configure · <total - items.length> more ranked below in Jira", shown only
//              when total > items.length.
//   - Hidden:  rows = 0 renders nothing at all.
//
// SPEC-NOTE: total = -1 means "unknown" (Counts.backlog could not be fetched). With an unknown total the footnote is
//   omitted (we cannot say how many are below); the list and heading still render. Chosen to avoid inventing a number.
// SPEC-NOTE: `rows` is the configured N (0-50) and drives the heading and the "hidden when 0" rule; the items shown are
//   `items` as given (never truncated or padded by the component).
import { fireEvent, render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, wailsApi } from "../../api";
import type { Api } from "../../api";
import type { BacklogItem } from "../../api/types";
import { BacklogList } from "./BacklogList";
import type { BacklogListProps } from "./props";

const item = (n: number): BacklogItem => ({ key: `MOB-${1500 + n}`, summary: `Backlog summary ${n}`, type: "story", url: `https://example.atlassian.net/browse/MOB-${1500 + n}` });
const TWELVE = Array.from({ length: 12 }, (_, i) => item(i));

function renderBacklog(over: Partial<BacklogListProps> = {}) {
  return render(<BacklogList items={TWELVE} total={40} rows={12} cols={3} {...over} />);
}

let openIssue: ReturnType<typeof vi.fn>;
beforeEach(() => {
  openIssue = vi.fn(async () => {});
  setApi({ app: { openIssue } } as unknown as Api);
});
afterEach(() => setApi(wailsApi));

const rows = () => screen.queryAllByRole("button");
function rowAt(i: number): HTMLElement {
  const r = rows()[i];
  expect(r, `row ${i}`).toBeDefined();
  return r as HTMLElement;
}
const AXE = { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } };
const FOOT = (rowsN: number, more: number) => `Showing the top ${rowsN} as set in Configure · ${more} more ranked below in Jira`;

describe("BacklogList", () => {
  it("has a Backlog heading and 'top 12 by rank'", () => {
    renderBacklog();
    expect(screen.getByRole("heading", { level: 2, name: /^Backlog/ })).toBeTruthy();
    expect(screen.getByText("top 12 by rank")).toBeTruthy();
  });

  it("follows the configured row count in the subheading", () => {
    renderBacklog({ rows: 5, items: TWELVE.slice(0, 5), total: 5 });
    expect(screen.getByText("top 5 by rank")).toBeTruthy();
  });

  it("lists every item in rank order with key and summary", () => {
    renderBacklog();
    const list = rows();
    expect(list).toHaveLength(12);
    expect(list.map((b) => /MOB-\d+/.exec(b.textContent ?? "")?.[0])).toEqual(TWELVE.map((i) => i.key));
    expect(list[3]!.textContent).toContain("Backlog summary 3");
  });

  it("shows the footnote with how many more are ranked below in Jira", () => {
    renderBacklog({ total: 40 });
    expect(screen.getByText(FOOT(12, 28))).toBeTruthy();
  });

  it("omits the footnote when everything is shown", () => {
    renderBacklog({ total: 12 });
    expect(rows()).toHaveLength(12); // anchor
    expect(screen.queryByText(/Showing the top/)).toBeNull();
  });

  it("omits the footnote when the total is unknown (-1) but still lists the items", () => {
    renderBacklog({ total: -1 });
    expect(rows()).toHaveLength(12); // anchor
    expect(screen.queryByText(/Showing the top/)).toBeNull();
  });

  it("is hidden entirely when backlog rows is 0", () => {
    const visible = renderBacklog();
    expect(screen.getByRole("heading", { level: 2, name: /^Backlog/ })).toBeTruthy(); // positive variant
    visible.unmount();
    const { container } = renderBacklog({ rows: 0, items: [], total: 40 });
    expect(container.textContent).toBe("");
    expect(screen.queryByRole("heading")).toBeNull();
  });

  it("opens the issue on click", () => {
    renderBacklog();
    fireEvent.click(rowAt(2));
    expect(openIssue).toHaveBeenCalledTimes(1);
    expect(openIssue).toHaveBeenCalledWith("MOB-1502");
  });

  it("opens the issue once on Enter", async () => {
    renderBacklog();
    const user = userEvent.setup();
    rowAt(0).focus();
    await user.keyboard("{Enter}");
    expect(openIssue).toHaveBeenCalledTimes(1);
    expect(openIssue).toHaveBeenCalledWith("MOB-1500");
  });

  it("uses no inline colours", () => {
    const { container } = renderBacklog();
    expect(screen.getByRole("heading", { level: 2, name: /^Backlog/ })).toBeTruthy(); // anchor
    for (const el of Array.from(container.querySelectorAll("[style]"))) {
      expect(el.getAttribute("style")).not.toMatch(/#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(/i);
    }
  });

  it.each([1, 2, 3])("has no axe violations with %i columns", async (cols) => {
    const { container } = renderBacklog({ cols });
    expect(rows()).toHaveLength(12); // anchor
    expect((await axe.run(container, AXE)).violations.map((v) => v.id)).toEqual([]);
  });
});
