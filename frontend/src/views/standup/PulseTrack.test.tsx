// WP-24 tests: PulseTrack (ux-spec §7).
//
// SPEC-NOTE: DOM HOOKS used by this file (the shared vocabulary; every other hook in the standup tests is documented the same way):
//   - Track root:   role="img", aria-label = pulse.label, inline style `width: <width>px; height: <rowH>px`,
//                   custom property --days = days.
//   - Segment:      [data-lane="<lane>"] inside the root, in pulse.segs order, with custom properties
//                   --x = seg.from and --w = seg.to - seg.from (DAY units, unitless numbers).
//   - Blocked span: [data-blocked] inside the root, --x = span.from and --w = span.to - span.from + 1 (inclusive span).
//   - Mark:         [data-mark="commit"|"pr"|"comment"] inside the root, --x = the day index.
//                   A commit mark also carries data-level="1"|"2"|"3" (1 commit → 1, 2–3 → 2, 4+ → 3).
//
// SPEC-NOTE: ux-spec §7 gives CSS (`left: calc(var(--x) / var(--days) * 100%)`) but not the DOM. The hooks above are the
//   smallest contract that lets a unit test check geometry in day units without a layout engine.
// SPEC-NOTE: `done` (muted history, segments only) and `lane` (thin board track, no marks) are checked by behaviour:
//   the same pulse with marks renders marks as a person row and none as a done or lane track.
// SPEC-NOTE: `devActivity="unavailable"` hides commit and PR marks only; comment marks stay (jira-sync: only dev-status
//   data is missing). Whether "unknown" hides them is not specified and not tested.
// SPEC-NOTE: when one day has both commits and PR activity, only the PR diamond is drawn for that day (ux-spec §7); the
//   same day's comment tick is a separate mark and stays.
import { render } from "@testing-library/preact";
import axe from "axe-core";
import { describe, expect, it } from "vitest";
import type { Pulse } from "../../api/types";
import { PulseTrack } from "./PulseTrack";
import type { PulseTrackProps } from "./props";

const OPTS: PulseTrackProps["options"] = { showCommits: true, showPRs: true, showComments: true, devActivity: "ok" };

// Testing-library queries are not extended with jest-dom here; read custom properties off the style attribute.
function cssVar(el: Element, name: string): number {
  const style = el.getAttribute("style") ?? "";
  const m = new RegExp(`${name}\\s*:\\s*(-?[0-9.]+)`).exec(style);
  if (!m) throw new Error(`${name} not set on <${el.tagName.toLowerCase()} style="${style}">`);
  return parseFloat(m[1]!);
}

function track(container: Element): HTMLElement {
  const el = container.querySelector<HTMLElement>('[role="img"]');
  if (!el) throw new Error("no role=img track rendered");
  return el;
}

function marks(root: Element, kind: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(`[data-mark="${kind}"]`));
}

const HISTORY: Pulse = {
  // testing.md §4: history [todo 5, prog 6, review 3], N = 21 → segments start at day 7, widths 5, 6, 3, last ends at 21.
  segs: [
    { lane: "todo", from: 7, to: 12 },
    { lane: "prog", from: 12, to: 18 },
    { lane: "review", from: 18, to: 21 },
  ],
  marks: [],
  blocked: [],
  label: "In Review 3 days, previously In Progress 6 days",
};

const BUSY: Pulse = {
  segs: [{ lane: "prog", from: 0, to: 21 }],
  marks: [
    { day: 3, commits: 1, prs: 0, comments: 0 },
    { day: 6, commits: 3, prs: 0, comments: 0 },
    { day: 9, commits: 5, prs: 0, comments: 0 },
    { day: 12, commits: 0, prs: 2, comments: 0 },
    { day: 15, commits: 4, prs: 1, comments: 2 }, // both: PR diamond only (plus the comment tick)
    { day: 18, commits: 0, prs: 0, comments: 3 },
  ],
  blocked: [{ from: 10, to: 12 }],
  label: "In Progress 21 days; 13 commits, 3 PR updates, 5 comments in 21 days; blocked 3 days",
};

function renderTrack(over: Partial<PulseTrackProps> = {}) {
  return render(<PulseTrack pulse={BUSY} days={21} width={440} rowH={46} options={OPTS} {...over} />);
}

describe("PulseTrack", () => {
  it("renders a role=img root labelled with the Go-supplied pulse label", () => {
    const { container } = renderTrack({ pulse: HISTORY });
    const root = track(container);
    expect(root.getAttribute("aria-label")).toBe(HISTORY.label);
  });

  it("sizes the root to width x rowH pixels", () => {
    const { container } = renderTrack({ pulse: HISTORY, width: 600, rowH: 38 });
    const root = track(container);
    expect(root.style.width).toBe("600px");
    expect(root.style.height).toBe("38px");
  });

  it("multiplies its height by the scale the caller gives it (the standup screen's text size × zoom), else by the text scale only", async () => {
    // F15: the ticket modal's header and Configure's preview use PulseTrack with no scale, so a standup's zoom never moves them
    const { seedZooms } = await import("../../state/zoom");
    const { route } = await import("../../state/route");
    route.value = { view: "standup", standupId: 1, person: null };
    seedZooms({ "1": 150 });
    try {
      expect(track(renderTrack({ pulse: HISTORY, rowH: 40, scale: 1.5 }).container).style.height).toBe("60px");
      expect(track(renderTrack({ pulse: HISTORY, rowH: 40 }).container).style.height).toBe("40px");
    } finally {
      seedZooms({});
    }
  });

  it("exposes the day count to CSS as --days", () => {
    const { container } = renderTrack({ pulse: HISTORY, days: 28 });
    expect(cssVar(track(container), "--days")).toBe(28);
  });

  it("positions segments in day units: history [todo 5, prog 6, review 3] starts at day 7 and ends at day 21", () => {
    const { container } = renderTrack({ pulse: HISTORY });
    const segs = Array.from(track(container).querySelectorAll<HTMLElement>("[data-lane]"));
    expect(segs.map((s) => s.getAttribute("data-lane"))).toEqual(["todo", "prog", "review"]);
    expect(segs.map((s) => cssVar(s, "--x"))).toEqual([7, 12, 18]);
    expect(segs.map((s) => cssVar(s, "--w"))).toEqual([5, 6, 3]);
    const last = segs[2]!;
    expect(cssVar(last, "--x") + cssVar(last, "--w")).toBe(21);
  });

  it("keeps a segment that starts at the left edge at x = 0", () => {
    const pulse: Pulse = { segs: [{ lane: "qa", from: 0, to: 4 }], marks: [], blocked: [], label: "QA 4 days" };
    const { container } = renderTrack({ pulse });
    const seg = track(container).querySelector<HTMLElement>('[data-lane="qa"]');
    expect(seg).not.toBeNull();
    expect(cssVar(seg!, "--x")).toBe(0);
    expect(cssVar(seg!, "--w")).toBe(4);
  });

  it("draws blocked spans as inclusive ranges (to - from + 1 days)", () => {
    const { container } = renderTrack();
    const blocked = Array.from(track(container).querySelectorAll<HTMLElement>("[data-blocked]"));
    expect(blocked).toHaveLength(1);
    expect(cssVar(blocked[0]!, "--x")).toBe(10);
    expect(cssVar(blocked[0]!, "--w")).toBe(3);
  });

  it("draws no blocked overlay when nothing is blocked", () => {
    const { container } = renderTrack({ pulse: HISTORY });
    expect(track(container).querySelectorAll("[data-lane]")).toHaveLength(3); // anchor: the track rendered
    expect(track(container).querySelectorAll("[data-blocked]")).toHaveLength(0);
  });

  it("draws a commit tick per day with a level from the commit count (1, 2-3, 4+)", () => {
    const { container } = renderTrack();
    const ticks = marks(track(container), "commit");
    const byDay = new Map(ticks.map((t) => [cssVar(t, "--x"), t.getAttribute("data-level")]));
    expect(byDay.get(3)).toBe("1");
    expect(byDay.get(6)).toBe("2");
    expect(byDay.get(9)).toBe("3");
  });

  it("draws a PR diamond on a PR-only day", () => {
    const { container } = renderTrack();
    expect(marks(track(container), "pr").map((m) => cssVar(m, "--x"))).toContain(12);
  });

  it("draws the PR diamond instead of the commit tick on a day with both", () => {
    const { container } = renderTrack();
    const root = track(container);
    expect(marks(root, "pr").map((m) => cssVar(m, "--x"))).toContain(15);
    expect(marks(root, "commit").map((m) => cssVar(m, "--x"))).not.toContain(15);
    expect(marks(root, "commit").map((m) => cssVar(m, "--x"))).toEqual([3, 6, 9]); // anchor: other commit days remain
  });

  it("draws comment ticks on every day with comments, including a day that also has a PR", () => {
    const { container } = renderTrack();
    expect(marks(track(container), "comment").map((m) => cssVar(m, "--x"))).toEqual([15, 18]);
  });

  it("hides commit ticks when showCommits is off, keeping PR and comment marks", () => {
    const { container } = renderTrack({ options: { ...OPTS, showCommits: false } });
    const root = track(container);
    expect(marks(root, "pr").length).toBeGreaterThan(0);
    expect(marks(root, "comment").length).toBeGreaterThan(0);
    expect(marks(root, "commit")).toHaveLength(0);
  });

  it("hides PR diamonds when showPRs is off, keeping commit and comment marks", () => {
    const { container } = renderTrack({ options: { ...OPTS, showPRs: false } });
    const root = track(container);
    expect(marks(root, "commit").length).toBeGreaterThan(0);
    expect(marks(root, "comment").length).toBeGreaterThan(0);
    expect(marks(root, "pr")).toHaveLength(0);
  });

  it("hides comment ticks when showComments is off, keeping commit and PR marks", () => {
    const { container } = renderTrack({ options: { ...OPTS, showComments: false } });
    const root = track(container);
    expect(marks(root, "commit").length).toBeGreaterThan(0);
    expect(marks(root, "pr").length).toBeGreaterThan(0);
    expect(marks(root, "comment")).toHaveLength(0);
  });

  it("hides commit and PR marks when dev activity is unavailable, keeping comments", () => {
    const { container } = renderTrack({ options: { ...OPTS, devActivity: "unavailable" } });
    const root = track(container);
    expect(marks(root, "comment").length).toBeGreaterThan(0);
    expect(marks(root, "commit")).toHaveLength(0);
    expect(marks(root, "pr")).toHaveLength(0);
  });

  it("draws no marks on a done track but still draws its segments", () => {
    const person = renderTrack();
    expect(marks(track(person.container), "commit").length).toBeGreaterThan(0); // anchor: this pulse has marks
    person.unmount();
    const { container } = renderTrack({ done: true });
    const root = track(container);
    expect(root.querySelectorAll("[data-lane]").length).toBe(1);
    expect(root.querySelectorAll("[data-mark]")).toHaveLength(0);
  });

  it("draws no marks on a board lane track but still draws segments and blocked spans", () => {
    const { container } = renderTrack({ lane: true, rowH: 23 });
    const root = track(container);
    expect(root.querySelectorAll("[data-lane]").length).toBe(1);
    expect(root.querySelectorAll("[data-blocked]")).toHaveLength(1);
    expect(root.querySelectorAll("[data-mark]")).toHaveLength(0);
  });

  it("has no inline colours: lane colours come from data-lane and tokens", () => {
    const { container } = renderTrack();
    const styled = Array.from(container.querySelectorAll("[style]"));
    expect(styled.length).toBeGreaterThan(0); // anchor: the root carries inline size
    for (const el of styled) {
      expect(el.getAttribute("style")).not.toMatch(/#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(/i);
    }
  });

  it("has no axe violations", async () => {
    const { container } = renderTrack();
    expect(track(container).getAttribute("aria-label")).toBe(BUSY.label); // anchor
    const res = await axe.run(container, { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } });
    expect(res.violations.map((v) => v.id)).toEqual([]);
  });
});
