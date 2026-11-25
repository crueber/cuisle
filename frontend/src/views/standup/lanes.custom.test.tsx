// Lanes the user names on the standup screen (ux-spec §5, §7, §9.4): the header legend and the ? key legend list them
// with their configured labels, and every lane-coloured element (track segments, swatches) carries data-hue so the
// shared CSS paints it from the extended lane palette. Built-in lanes carry no data-hue.
import { render, screen } from "@testing-library/preact";
import { IssueRows } from "../ticket/IssueRows";
import { StatusSectionHeader } from "./StatusSectionHeader";
import { statusSections } from "../../layout/board";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { LaneInfo, StandupConfig, StandupView, TicketView, ViewOptions } from "../../api/types";
import { KeyLegend } from "../../components/KeyLegend";
import { config } from "../../state/config";
import { Legend } from "./Legend";
import { PulseTrack } from "./PulseTrack";

const L = (lane: string, label: string, hue = 0, kind = "working"): LaneInfo => ({
  lane, label, defaultLabel: hue ? "" : label, kind, custom: hue > 0, hue, limitDays: 2, columns: [],
});
// Walk order (right to left): QA, Code Review (renamed), Design (custom c2), In Progress, To Do; then Shipped (Done).
const LANES = [L("qa", "QA"), L("review", "Code Review"), L("c2", "Design", 2), L("prog", "In Progress"), L("todo", "To Do"), L("done", "Shipped", 0, "done")];
const OPTIONS: ViewOptions = {
  rowsMode: "fit", showCommits: true, showPRs: true, showComments: true, shadeWeekends: true, doneWindow: "sprint",
  doneLabel: "this sprint", devActivity: "ok",
};

beforeEach(() => {
  config.value = { id: 1, laneOptions: LANES } as unknown as StandupConfig;
});
afterEach(() => {
  config.value = null;
});

describe("custom lanes on the standup screen", () => {
  it("header legend: configured labels in board-flow order, custom swatch painted by hue", () => {
    const { container } = render(<Legend mode="full" options={OPTIONS} lanes={LANES} />);
    const items = Array.from(container.querySelectorAll(".legend-swatch[data-lane]"));
    expect(items.map((e) => e.getAttribute("data-lane"))).toEqual(["todo", "prog", "c2", "review", "qa"]);
    expect(screen.getByText("Design")).toBeTruthy();
    expect(screen.getByText("Code Review")).toBeTruthy();
    expect(container.querySelector('.legend-swatch[data-lane="c2"]')?.getAttribute("data-hue")).toBe("2");
    expect(container.querySelector('.legend-swatch[data-lane="qa"]')?.hasAttribute("data-hue")).toBe(false);
  });

  it("? key legend: lists the standup's lanes, custom ones included", () => {
    const { container } = render(
      <KeyLegend open onClose={() => {}} showCommits showPRs showComments devActivity="ok" lanes={LANES} />,
    );
    const names = Array.from(container.querySelectorAll(".keylegend-pulse .keylegend-text")).map((e) => e.textContent);
    expect(names.slice(0, 5)).toEqual(["To Do", "In Progress", "Design", "Code Review", "QA"]);
    expect(container.querySelector('.legend-swatch[data-lane="c2"]')?.getAttribute("data-hue")).toBe("2");
  });

  it("? key legend: falls back to the built-in lanes without a standup", () => {
    const { container } = render(<KeyLegend open onClose={() => {}} showCommits showPRs showComments devActivity="ok" />);
    const names = Array.from(container.querySelectorAll(".keylegend-pulse .keylegend-text")).map((e) => e.textContent);
    expect(names.slice(0, 4)).toEqual(["To Do", "In Progress", "In Review", "QA"]);
  });

  it("whole board, status mode: a custom lane is its own section in walk order, swatch painted by hue", () => {
    const tk = (id: number, lane: string) => ({ id, key: `K-${id}`, lane, assignee: "a", overLimit: false, blocked: false, daysInLane: id }) as unknown as TicketView;
    const view = { lanes: LANES, tickets: [tk(1, "todo"), tk(2, "c2"), tk(3, "c2"), tk(4, "qa"), tk(5, "done")] } as unknown as StandupView;
    const sections = statusSections(view);
    expect(sections.map((s) => [s.lane, s.label, s.tickets.length])).toEqual([["qa", "QA", 1], ["c2", "Design", 2], ["todo", "To Do", 1]]);
    const { container } = render(<StatusSectionHeader section={sections[1]!} compact={false} />);
    expect(screen.getByRole("heading", { name: "Design" })).toBeTruthy();
    expect(container.querySelector('.board-sec-swatch[data-lane="c2"]')?.getAttribute("data-hue")).toBe("2");
  });

  it("ticket modal: a subtask in a custom lane gets a lozenge painted by hue", () => {
    const sub = (key: string, lane: string) => ({
      issueId: 1, key, summary: "s", typeKind: "task", statusName: lane, statusLozenge: lane, assigneeInitials: "", url: "", done: false,
    });
    const { container } = render(
      <IssueRows subtasks={[sub("K-1", "c2"), sub("K-2", "prog")]} subtasksDone={0} linkGroups={[]} onOpen={() => {}} />,
    );
    expect(container.querySelector('.ir-status[data-lane="c2"]')?.getAttribute("data-hue")).toBe("2");
    expect(container.querySelector('.ir-status[data-lane="prog"]')?.hasAttribute("data-hue")).toBe(false);
  });

  it("pulse track: a custom-lane segment carries its hue, built-in segments do not", () => {
    const { container } = render(
      <PulseTrack
        pulse={{ label: "Design 3 days", segs: [{ lane: "prog", from: 0, to: 4 }, { lane: "c2", from: 4, to: 7 }], marks: [], blocked: [] }}
        days={7}
        width={280}
        rowH={46}
        options={{ showCommits: true, showPRs: true, showComments: true, devActivity: "ok" }}
      />,
    );
    const segs = Array.from(container.querySelectorAll(".pulse-seg"));
    expect(segs.map((s) => [s.getAttribute("data-lane"), s.getAttribute("data-hue")])).toEqual([["prog", null], ["c2", "2"]]);
  });
});
