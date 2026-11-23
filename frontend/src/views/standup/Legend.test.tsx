// WP-24 tests: Legend (ux-spec §3 legend rule, §5.1, §7, §10).
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - Lane swatch: [data-lane="<lane>"], one per WALK lane (todo, prog, review, qa); the Done lane has no swatch, as in
//     the approved mockups (design/mockups/person-1920x1080.png shows To Do, In Progress, In Review, QA only).
//
// SPEC-NOTE: lane names are printed exactly as given in LaneInfo.label. The fixture uses the mockup's labels
//   ("To Do", "In Progress", "In Review", "QA", "Done"); the generated bindings comment says "To do" / "In progress" /
//   "Review", but the component must not re-case or rename anything.
// SPEC-NOTE: full = 4 lane swatches + blocked + commit + PR + comment + over limit; short = 4 lane swatches + blocked +
//   over limit (ux-spec §3); button = a single "? Key" button that opens the KeyLegend popover by setting the
//   `keyLegendOpen` signal (state/ui.ts). Entry text is matched case-insensitively: "blocked", "commit", "PR", "comment",
//   "over limit".
// SPEC-NOTE: commit and PR entries are hidden when their toggle is off or devActivity is "unavailable"; the comment
//   entry follows showComments only (the same rules as the track marks).
import { fireEvent, render, screen } from "@testing-library/preact";
import axe from "axe-core";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { LaneInfo, ViewOptions } from "../../api/types";
import { keyLegendOpen } from "../../state/ui";
import { Legend } from "./Legend";
import type { LegendProps } from "./props";

const LANES: LaneInfo[] = [
  { lane: "qa", label: "QA", limitDays: 3, columns: ["QA"] },
  { lane: "review", label: "In Review", limitDays: 2, columns: ["Review"] },
  { lane: "prog", label: "In Progress", limitDays: 5, columns: ["In Progress"] },
  { lane: "todo", label: "To Do", limitDays: 10, columns: ["To Do"] },
  { lane: "done", label: "Done", limitDays: 0, columns: ["Done"] },
];

const OPTIONS: ViewOptions = {
  rowsMode: "fit",
  showCommits: true,
  showPRs: true,
  showComments: true,
  shadeWeekends: true,
  doneWindow: "sprint",
  doneLabel: "this sprint",
  devActivity: "ok",
};

function renderLegend(over: Partial<LegendProps> = {}) {
  return render(<Legend mode="full" options={OPTIONS} lanes={LANES} {...over} />);
}

const has = (re: RegExp) => screen.queryAllByText(re).length > 0;

beforeEach(() => {
  keyLegendOpen.value = false;
});
afterEach(() => {
  keyLegendOpen.value = false;
});

describe("Legend full", () => {
  it("names the four walk lanes with swatches and not Done", () => {
    const { container } = renderLegend();
    for (const label of ["QA", "In Review", "In Progress", "To Do"]) expect(screen.getByText(label)).toBeTruthy();
    expect(screen.queryByText("Done")).toBeNull();
    const swatches = Array.from(container.querySelectorAll("[data-lane]")).map((e) => e.getAttribute("data-lane"));
    expect(swatches.sort()).toEqual(["prog", "qa", "review", "todo"]);
  });

  it("explains blocked, commit, PR, comment and over limit", () => {
    renderLegend();
    expect(has(/blocked/i)).toBe(true);
    expect(has(/commit/i)).toBe(true);
    expect(has(/^PR$/)).toBe(true);
    expect(has(/comment/i)).toBe(true);
    expect(has(/over limit/i)).toBe(true);
  });

  it("drops the commit entry when commits are off", () => {
    renderLegend({ options: { ...OPTIONS, showCommits: false } });
    expect(has(/over limit/i)).toBe(true); // anchor
    expect(has(/commit/i)).toBe(false);
    expect(has(/^PR$/)).toBe(true);
  });

  it("drops the PR entry when PRs are off", () => {
    renderLegend({ options: { ...OPTIONS, showPRs: false } });
    expect(has(/commit/i)).toBe(true); // anchor
    expect(has(/^PR$/)).toBe(false);
  });

  it("drops the comment entry when comments are off", () => {
    renderLegend({ options: { ...OPTIONS, showComments: false } });
    expect(has(/commit/i)).toBe(true); // anchor
    expect(has(/comment/i)).toBe(false);
  });

  it("drops commit and PR entries when dev activity is unavailable but keeps comment", () => {
    renderLegend({ options: { ...OPTIONS, devActivity: "unavailable" } });
    expect(has(/blocked/i)).toBe(true); // anchor
    expect(has(/commit/i)).toBe(false);
    expect(has(/^PR$/)).toBe(false);
    expect(has(/comment/i)).toBe(true);
  });

  it("prints lane labels exactly as supplied", () => {
    renderLegend({ lanes: LANES.map((l) => (l.lane === "prog" ? { ...l, label: "Doing It" } : l)) });
    expect(screen.getByText("Doing It")).toBeTruthy();
    expect(screen.queryByText("In Progress")).toBeNull();
  });

  it("has no axe violations", async () => {
    const { container } = renderLegend();
    expect(screen.getByText("QA")).toBeTruthy(); // anchor
    const res = await axe.run(container, { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } });
    expect(res.violations.map((v) => v.id)).toEqual([]);
  });
});

describe("Legend short", () => {
  it("shows the four lanes, blocked and over limit only", () => {
    const { container } = renderLegend({ mode: "short" });
    expect(container.querySelectorAll("[data-lane]")).toHaveLength(4);
    for (const label of ["QA", "In Review", "In Progress", "To Do"]) expect(screen.getByText(label)).toBeTruthy();
    expect(has(/blocked/i)).toBe(true);
    expect(has(/over limit/i)).toBe(true);
    expect(has(/commit/i)).toBe(false);
    expect(has(/^PR$/)).toBe(false);
    expect(has(/comment/i)).toBe(false);
  });
});

describe("Legend button", () => {
  it("shows a single Key button and no swatches", () => {
    const { container } = renderLegend({ mode: "button" });
    expect(screen.getByRole("button", { name: /key/i })).toBeTruthy();
    expect(container.querySelectorAll("[data-lane]")).toHaveLength(0);
    expect(has(/over limit/i)).toBe(false);
    expect(screen.queryByText("QA")).toBeNull();
  });

  it("opens the key popover (keyLegendOpen) when pressed", () => {
    renderLegend({ mode: "button" });
    expect(keyLegendOpen.value).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: /key/i }));
    expect(keyLegendOpen.value).toBe(true);
  });

  it("has no axe violations", async () => {
    const { container } = renderLegend({ mode: "button" });
    expect(screen.getByRole("button", { name: /key/i })).toBeTruthy(); // anchor
    const res = await axe.run(container, { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } });
    expect(res.violations.map((v) => v.id)).toEqual([]);
  });
});
