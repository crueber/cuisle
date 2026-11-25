/* eslint-disable @typescript-eslint/no-explicit-any */
// Configure > Pulse section (ux-spec §9.4 Pulse; architecture §5.3 "Refreshing the view after a save";
// jira-sync §2.11 Degradation).
//
// SPEC-NOTE (approach): state functions mocked via vi.mock("state/config"); Segmented/Switch/Stepper replaced by
//   fakes; PulseTrack and TrackBackdrop (WP-24) replaced by recording fakes so the preview's inputs can be asserted
//   (see Source.test.tsx for the rationale). state/standup `views` and state/app `connection` are real signals,
//   seeded directly.
// SPEC-NOTE (lane limits live here): "Status limits" are four Steppers in this section (ux-spec §9.4 + the approved
//   mockup), NOT in Person view (the orchestrator brief said otherwise). One stepper per WALK lane (todo/prog/review/
//   qa; the "done" lane has none) in config.lanes order (walk order), labelled with the lane label (e.g. /QA/i,
//   /In progress/i), value = lane.limitDays, min 0, max 99, zeroLabel "no limit" (or "none"). onChange(n) ->
//   setLaneLimit(laneId, n) (immediate, not saveField).
// SPEC-NOTE: Days shown is a Segmented labelled /days/i with values "7" | "14" | "21" | "28" -> saveField("pulseDays", n)
//   with n a NUMBER. Marks are Switches labelled /commit/i, /\bPRs?\b|pull request/i, /comment/i ->
//   saveField("showCommits" | "showPRs" | "showComments", bool). Weekends is a Segmented labelled /weekend/i with a
//   "Shade" and a "Plain" option (found by label, values are free) -> saveField("shadeWeekends", true | false).
// SPEC-NOTE: the live preview renders ONE <PulseTrack> (views/standup/PulseTrack) for the ticket of
//   views.get(config.id) whose key === view.previewKey, passing that ticket's `pulse`, days = config.pulseDays, a
//   positive pixel width, and options {showCommits, showPRs, showComments} taken from the CONFIG (not the view), so it
//   updates instantly. When there is no cached view, or previewKey is empty / not among the tickets, a static sample
//   Pulse is passed instead (any Pulse whose `label` differs from every ticket label). If a <TrackBackdrop> is
//   rendered, its shadeWeekends equals config.shadeWeekends.
// SPEC-NOTE: the dev-status warning is driven by state/app `connection.value.devActivity === "unavailable"` only. The
//   one-line text is "Commit and PR marks unavailable — Jira's development info isn't reachable for this site." and is
//   absent for "ok" and "unknown".


import { act, render, screen } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Connection, LaneInfo, Pulse, StandupConfig, StandupView, TicketView } from "../../../api/types";
import { connection } from "../../../state/app";
import { views } from "../../../state/standup";
import { PulseSection } from "./Pulse";

const reg = vi.hoisted(() => {
  type P = any;
  const m = new Map<string, Map<string, P>>();
  const keys = (kind: string) => [...(m.get(kind)?.keys() ?? [])];
  return {
    rec(kind: string, p: P) {
      let k = m.get(kind);
      if (!k) m.set(kind, (k = new Map()));
      k.set(String(p.label), p);
    },
    clear() { m.clear(); },
    labels: keys,
    has(kind: string, re: RegExp): boolean { return keys(kind).some((l) => re.test(l)); },
    find(kind: string, re: RegExp): P {
      const hit = [...(m.get(kind)?.entries() ?? [])].find(([l]) => re.test(l));
      if (!hit) throw new Error(`no ${kind} labelled ${re} (have: ${keys(kind).join(" | ") || "none"})`);
      return hit[1];
    },
  };
});


vi.mock("../../standup/PulseTrack", async () => {
  const { h } = await import("preact");
  return {
    PulseTrack: (p: any) => {
      reg.rec("PulseTrack", { ...p, label: "track" });
      return h("div", { role: "img", "aria-label": p.pulse?.label ?? "", "data-testid": "pulse-track" });
    },
  };
});
vi.mock("../../standup/TrackBackdrop", async () => {
  const { h } = await import("preact");
  return {
    TrackBackdrop: (p: any) => {
      reg.rec("TrackBackdrop", { ...p, label: "backdrop" });
      return h("div", { "data-testid": "track-backdrop" });
    },
  };
});
const st = vi.hoisted(() => ({
  loadConfig: vi.fn(async () => {}), saveField: vi.fn(), setSpace: vi.fn(), setBoard: vi.fn(), setColumnLane: vi.fn(),
  setWalkOrder: vi.fn(), setLaneLimit: vi.fn(), setSpeakerIncluded: vi.fn(), moveSpeaker: vi.fn(), sortSpeakers: vi.fn(),
  addSpeaker: vi.fn(), removeSpeaker: vi.fn(), validateFilter: vi.fn(), createStandup: vi.fn(async () => {}), duplicateStandup: vi.fn(async () => {}),
  copySettingsFrom: vi.fn(async () => {}), deleteStandup: vi.fn(async () => {}), moveStandup: vi.fn(async () => {}), leaveConfigure: vi.fn(async () => {}),
}));
vi.mock("../../../state/config", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../state/config")>()),
  ...st,
}));

vi.mock("../../../components/Segmented", async () => {
  const { h } = await import("preact");
  return {
    Segmented: (p: any) => {
      reg.rec("Segmented", p);
      return h("div", { role: "radiogroup", "aria-label": p.label }, p.options.map((o: any) =>
        h("button", { type: "button", role: "radio", "aria-checked": String(o.value === p.value), disabled: !!o.disabled, onClick: () => p.onChange(o.value) }, o.label)));
    },
  };
});

vi.mock("../../../components/Switch", async () => {
  const { h } = await import("preact");
  return {
    Switch: (p: any) => {
      reg.rec("Switch", p);
      return h("button", { type: "button", role: "switch", "aria-label": p.label, "aria-checked": String(p.checked), disabled: p.disabled, onClick: () => p.onChange(!p.checked) });
    },
  };
});

vi.mock("../../../components/Stepper", async () => {
  const { h } = await import("preact");
  return {
    Stepper: (p: any) => {
      reg.rec("Stepper", p);
      return h("input", { role: "spinbutton", "aria-label": p.label, value: p.value, readOnly: true });
    },
  };
});

function mkConfig(over: Partial<StandupConfig> = {}): StandupConfig {
  return {
    id: 1, rev: 3, position: 0, name: "Saffron Mobile",
    space: { id: "10001", key: "MOB", name: "Saffron Mobile" },
    board: { id: "42", name: "MOB board", type: "scrum" },
    scope: "sprint", extraFilter: "", filterCheck: null,
    columns: [
      { columnName: "Icebox", statusNames: ["Open"], lane: "backlog", auto: true, order: 0 },
      { columnName: "Ready", statusNames: ["Selected for Development"], lane: "todo", auto: true, order: 0 },
      { columnName: "Doing", statusNames: ["In Progress"], lane: "prog", auto: true, order: 0 },
      { columnName: "Code Review", statusNames: ["In Review", "Peer Review"], lane: "review", auto: false, order: 0 },
      { columnName: "Verify", statusNames: ["Testing"], lane: "qa", auto: true, order: 0 },
      { columnName: "Closed", statusNames: ["Done"], lane: "done", auto: true, order: 0 },
    ],
    lanes: [
      { lane: "qa", label: "QA", defaultLabel: "QA", kind: "working", custom: false, hue: 0, limitDays: 3, columns: ["Verify"] },
      { lane: "review", label: "Review", defaultLabel: "In Review", kind: "working", custom: false, hue: 0, limitDays: 2, columns: ["Code Review"] },
      { lane: "prog", label: "In progress", defaultLabel: "In Progress", kind: "working", custom: false, hue: 0, limitDays: 5, columns: ["Doing"] },
      { lane: "todo", label: "To do", defaultLabel: "To Do", kind: "working", custom: false, hue: 0, limitDays: 0, columns: ["Ready"] },
      { lane: "done", label: "Done", defaultLabel: "Done", kind: "done", custom: false, hue: 0, limitDays: 0, columns: ["Closed"] },
    ],
    laneOptions: [], walkLeftToRight: [],
    unmappedStatuses: [],
    rowsMode: "fit",
    speakers: [
      { accountId: "u-dana", name: "Dana Whitaker", initials: "DW", position: 0, included: true, manual: false, active: 3, note: "" },
      { accountId: "u-iris", name: "Iris Calloway", initials: "IC", position: 1, included: true, manual: false, active: 2, note: "" },
      { accountId: "u-amara", name: "Amara Osei", initials: "AO", position: 2, included: false, manual: false, active: 0, note: "no tickets right now" },
      { accountId: "u-lee", name: "Lee Tran", initials: "LT", position: 3, included: true, manual: true, active: 1, note: "" },
    ],
    unassignedPosition: "last", reshuffleDaily: false,
    pulseDays: 14, showCommits: true, showPRs: true, showComments: false, shadeWeekends: true,
    doneWindow: "sprint", backlogRows: 8, needsSync: false, draft: false,
    ...over,
  };
}

const SYNCED: Pulse = { segs: [{ lane: "prog", from: 0, to: 5 }] as any, marks: [], blocked: [], label: "DEV-12 in progress for 6 days" };
const OTHER: Pulse = { segs: [], marks: [], blocked: [], label: "DEV-99 in review for 1 day" };

function ticket(key: string, pulse: Pulse): TicketView {
  return { id: 1, sourceId: "10", key, summary: "x", url: "", type: "story", assignee: "u-dana", lane: "prog", statusName: "In Progress", daysInLane: 6, overLimit: false, blocked: false, doneAt: 0, pulse };
}
function mkView(over: Partial<StandupView> = {}): StandupView {
  return {
    standupId: 1, rev: 3, generatedAt: 0, today: "2026-11-23", asOf: 0, live: true, name: "Saffron Mobile", spaceKey: "MOB", boardName: "MOB board",
    boardType: "scrum", scope: "sprint", sprint: null, pulseDays: 14, axis: [], lanes: [], speakers: [],
    tickets: [ticket("DEV-99", OTHER), ticket("DEV-12", SYNCED)], backlog: [], backlogTotal: 0,
    counts: { active: 2, unassigned: 0, done: 0, backlog: 0 },
    options: { rowsMode: "fit", showCommits: true, showPRs: true, showComments: false, shadeWeekends: true, doneWindow: "sprint", doneLabel: "", devActivity: "ok" },
    sync: { standupId: 1, runId: 0, state: "idle", mode: "", phase: "", done: 0, total: 0, lastSuccessAt: 0, lastAttemptAt: 0, lastStatus: "", warnings: [], errorCode: "", errorMessage: "", needsSync: false },
    warnings: [], previewKey: "DEV-12", lastSyncedAt: 1,
    ...over,
  } as StandupView;
}
const conn = (devActivity: string): Connection => ({ state: "ok", source: "jira", baseUrl: "https://acme.atlassian.net", account: "me@acme.com", displayName: "Me", devActivity, capabilities: {} as any });
const trackProps = () => reg.find("PulseTrack", /^track$/);

beforeEach(() => {
  reg.clear();
  Object.values(st).forEach((f) => f.mockClear());
  views.value = new Map();
  connection.value = conn("ok");
});

describe("PulseSection: days shown", () => {
  it("offers 7 / 14 / 21 / 28 with the stored value", () => {
    render(<PulseSection config={mkConfig({ pulseDays: 21 })} columns={3} />);
    const seg = reg.find("Segmented", /days/i);
    expect(seg.value).toBe("21");
    expect(seg.options.map((o: any) => o.value)).toEqual(["7", "14", "21", "28"]);
  });

  it.each([7, 14, 21, 28])("autosaves %i days as a number immediately", (n) => {
    render(<PulseSection config={mkConfig({ pulseDays: n === 7 ? 14 : 7 })} columns={3} />);
    act(() => reg.find("Segmented", /days/i).onChange(String(n)));
    expect(st.saveField).toHaveBeenCalledTimes(1);
    expect(st.saveField).toHaveBeenCalledWith("pulseDays", n);
  });
});

describe("PulseSection: marks and weekends", () => {
  it("renders the three mark switches mirroring config", () => {
    render(<PulseSection config={mkConfig({ showCommits: true, showPRs: false, showComments: true })} columns={3} />);
    expect(reg.find("Switch", /commit/i).checked).toBe(true);
    expect(reg.find("Switch", /\bPRs?\b|pull request/i).checked).toBe(false);
    expect(reg.find("Switch", /comment/i).checked).toBe(true);
  });

  it.each([
    [/commit/i, "showCommits", true, false],
    [/\bPRs?\b|pull request/i, "showPRs", true, false],
    [/comment/i, "showComments", false, true],
  ] as const)("toggling %s autosaves %s immediately", (re, field, current, next) => {
    render(<PulseSection config={mkConfig({ [field]: current })} columns={3} />);
    act(() => reg.find("Switch", re).onChange(next));
    expect(st.saveField).toHaveBeenCalledTimes(1);
    expect(st.saveField).toHaveBeenCalledWith(field, next);
  });

  it("offers Shade / Plain for weekends, selected by config.shadeWeekends", () => {
    render(<PulseSection config={mkConfig({ shadeWeekends: true })} columns={3} />);
    const seg = reg.find("Segmented", /weekend/i);
    const shade = seg.options.find((o: any) => /shade/i.test(o.label));
    const plain = seg.options.find((o: any) => /plain/i.test(o.label));
    expect(seg.options).toHaveLength(2);
    expect(seg.value).toBe(shade.value);
    expect(plain.value).not.toBe(shade.value);
  });

  it("selecting Plain / Shade autosaves shadeWeekends false / true", () => {
    render(<PulseSection config={mkConfig({ shadeWeekends: true })} columns={3} />);
    const seg = reg.find("Segmented", /weekend/i);
    act(() => seg.onChange(seg.options.find((o: any) => /plain/i.test(o.label)).value));
    expect(st.saveField).toHaveBeenLastCalledWith("shadeWeekends", false);
    act(() => seg.onChange(seg.options.find((o: any) => /shade/i.test(o.label)).value));
    expect(st.saveField).toHaveBeenLastCalledWith("shadeWeekends", true);
    expect(st.saveField).toHaveBeenCalledTimes(2);
  });
});

describe("PulseSection: status limits", () => {
  it("renders one stepper per walk lane in walk order, none for Done", () => {
    render(<PulseSection config={mkConfig()} columns={3} />);
    const labels = reg.labels("Stepper");
    expect(labels).toHaveLength(4);
    ["QA", "Review", "In progress", "To do"].forEach((l, i) => expect(labels[i]?.toLowerCase()).toContain(l.toLowerCase()));
    expect(reg.has("Stepper", /^done/i)).toBe(false);
  });

  it("each stepper shows the lane's limit with range 0 to 99 and a zero label", () => {
    render(<PulseSection config={mkConfig()} columns={3} />);
    const expected: [RegExp, number][] = [[/qa/i, 3], [/review/i, 2], [/in progress/i, 5], [/to do/i, 0]];
    for (const [re, days] of expected) {
      const s = reg.find("Stepper", re);
      expect(s.value).toBe(days);
      expect(s.min).toBe(0);
      expect(s.max).toBe(99);
      expect(s.zeroLabel).toMatch(/no limit|none/i);
    }
  });

  it.each([
    [/qa/i, "qa", 4],
    [/review/i, "review", 0],
    [/in progress/i, "prog", 12],
    [/to do/i, "todo", 99],
  ] as const)("changing the %s stepper calls setLaneLimit('%s', %i)", (re, lane, n) => {
    render(<PulseSection config={mkConfig()} columns={3} />);
    act(() => reg.find("Stepper", re).onChange(n));
    expect(st.setLaneLimit).toHaveBeenCalledTimes(1);
    expect(st.setLaneLimit).toHaveBeenCalledWith(lane, n);
    expect(st.saveField).not.toHaveBeenCalled();
  });

  it("has a limit for a lane the user named, in its walk position", () => {
    const base = mkConfig().lanes;
    const design: LaneInfo = { lane: "c1", label: "Design", defaultLabel: "", kind: "working", custom: true, hue: 1, limitDays: 4, columns: ["Design"] };
    render(<PulseSection config={mkConfig({ lanes: [base[0]!, design, ...base.slice(1)] })} columns={3} />);
    expect(reg.labels("Stepper")).toHaveLength(5);
    expect(reg.labels("Stepper")[1]?.toLowerCase()).toContain("design");
    const s = reg.find("Stepper", /design/i);
    expect(s.value).toBe(4);
    act(() => s.onChange(9));
    expect(st.setLaneLimit).toHaveBeenCalledWith("c1", 9);
  });

  it("follows a different walk order", () => {
    const lanes = mkConfig().lanes.slice().reverse().filter((l) => l.lane !== "done");
    render(<PulseSection config={mkConfig({ lanes: [...lanes, mkConfig().lanes[4]!] })} columns={3} />);
    expect(reg.labels("Stepper")[0]?.toLowerCase()).toContain("to do");
    expect(reg.labels("Stepper")[3]?.toLowerCase()).toContain("qa");
  });
});

describe("PulseSection: live preview", () => {
  it("renders the previewKey ticket's pulse with the configured days and marks", () => {
    views.value = new Map([[1, mkView()]]);
    render(<PulseSection config={mkConfig({ pulseDays: 21, showCommits: false, showPRs: true, showComments: true })} columns={3} />);
    expect(screen.getAllByTestId("pulse-track")).toHaveLength(1);
    const p = trackProps();
    expect(p.pulse.label).toBe(SYNCED.label);
    expect(p.days).toBe(21);
    expect(p.width).toBeGreaterThan(0);
    expect(p.options.showCommits).toBe(false);
    expect(p.options.showPRs).toBe(true);
    expect(p.options.showComments).toBe(true);
  });

  it("updates instantly when the config changes (no view refetch needed)", () => {
    views.value = new Map([[1, mkView()]]);
    const { rerender } = render(<PulseSection config={mkConfig({ pulseDays: 14, showPRs: true })} columns={3} />);
    expect(trackProps().days).toBe(14);
    rerender(<PulseSection config={mkConfig({ pulseDays: 28, showPRs: false })} columns={3} />);
    expect(trackProps().days).toBe(28);
    expect(trackProps().options.showPRs).toBe(false);
  });

  it("passes shadeWeekends to the backdrop when one is rendered", () => {
    views.value = new Map([[1, mkView()]]);
    render(<PulseSection config={mkConfig({ shadeWeekends: false })} columns={3} />);
    expect(screen.getAllByTestId("pulse-track")).toHaveLength(1);
    if (reg.has("TrackBackdrop", /backdrop/)) expect(reg.find("TrackBackdrop", /backdrop/).shadeWeekends).toBe(false);
  });

  it("uses a static sample track when the standup has never synced", () => {
    views.value = new Map();
    render(<PulseSection config={mkConfig({ pulseDays: 7 })} columns={3} />);
    expect(screen.getAllByTestId("pulse-track")).toHaveLength(1);
    const p = trackProps();
    expect(p.days).toBe(7);
    expect(p.pulse).toBeTruthy();
    expect([SYNCED.label, OTHER.label]).not.toContain(p.pulse.label);
  });

  it("uses the static sample when previewKey is empty or not among the tickets", () => {
    views.value = new Map([[1, mkView({ previewKey: "" })]]);
    const a = render(<PulseSection config={mkConfig()} columns={3} />);
    expect(screen.getAllByTestId("pulse-track")).toHaveLength(1);
    expect([SYNCED.label, OTHER.label]).not.toContain(trackProps().pulse.label);
    a.unmount();
    reg.clear();
    views.value = new Map([[1, mkView({ previewKey: "GONE-1" })]]);
    render(<PulseSection config={mkConfig()} columns={3} />);
    expect(screen.getAllByTestId("pulse-track")).toHaveLength(1);
    expect([SYNCED.label, OTHER.label]).not.toContain(trackProps().pulse.label);
  });

  it("reads the view of THIS standup", () => {
    views.value = new Map([[2, mkView({ standupId: 2, previewKey: "DEV-99" })], [1, mkView({ previewKey: "DEV-12" })]]);
    render(<PulseSection config={mkConfig({ id: 2 })} columns={3} />);
    expect(trackProps().pulse.label).toBe(OTHER.label);
  });
});

describe("PulseSection: dev-status warning", () => {
  const WARNING = /Commit and PR marks unavailable — Jira.s development info isn.t reachable for this site\./;

  it("shows the one-line warning when dev activity is unavailable", () => {
    connection.value = conn("unavailable");
    render(<PulseSection config={mkConfig()} columns={3} />);
    expect(screen.getByText(WARNING)).toBeTruthy();
  });

  it.each(["ok", "unknown"])("shows no warning when dev activity is %s", (state) => {
    connection.value = conn(state);
    render(<PulseSection config={mkConfig()} columns={3} />);
    expect(screen.getByRole("heading", { name: /^Pulse/i })).toBeTruthy();
    expect(screen.queryByText(/marks unavailable/i)).toBeNull();
  });

  it("shows no warning when there is no connection yet", () => {
    connection.value = null;
    render(<PulseSection config={mkConfig()} columns={3} />);
    expect(screen.getByRole("heading", { name: /^Pulse/i })).toBeTruthy();
    expect(screen.queryByText(/marks unavailable/i)).toBeNull();
  });
});
