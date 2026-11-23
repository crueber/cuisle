/* eslint-disable @typescript-eslint/no-explicit-any */
// Configure > Whole board section (ux-spec §9.4): Done window Segmented, Backlog rows Stepper.
//
// SPEC-NOTE (approach): section tests isolate the section from the state layer and the shared components.
//   - State functions are replaced with vi.fn via vi.mock("state/config") (real signals kept). Sections persist ONLY
//     through state/config.ts (props.ts: "mutations go through state/config.ts"), so the assertion is the call itself.
//     Text debounce lives in save.ts: text fields are asserted as saveField(field, value, { text: true }).
//   - Shared components (Combobox/Segmented/Switch/Stepper/Checkbox/TextField) are replaced by thin fakes that record
//     their props by `label` (registry `reg`). Control labels are matched case-insensitively against the ux-spec §9.4
//     field names (e.g. /done window/i, /backlog rows/i). Their real DOM is specified by src/components/*.test.tsx.
//   - Only calls with no state wrapper (connection.search*, sink.*) use a fake Api installed with setApi.
// SPEC-NOTE: Done window option values are the StandupConfig.doneWindow values "sprint" | "14d" | "yesterday";
//   "sprint" is disabled when the board type is "kanban" (no sprints). Backlog rows is a Stepper 0..50.
// SPEC-NOTE: the hint "top of the ranked backlog · 0 turns it off" is rendered as plain text next to the stepper.

import { act, render, screen } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { StandupConfig } from "../../../api/types";
import { WholeBoardSection } from "./WholeBoard";

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
    columns: [], lanes: [], unmappedStatuses: [],
    rowsMode: "fit", speakers: [], unassignedPosition: "last", reshuffleDaily: false,
    pulseDays: 14, showCommits: true, showPRs: true, showComments: false, shadeWeekends: true,
    doneWindow: "sprint", backlogRows: 8, needsSync: false, draft: false,
    ...over,
  };
}
const kanban = { id: "77", name: "Ops board", type: "kanban" };

beforeEach(() => {
  reg.clear();
  Object.values(st).forEach((f) => f.mockClear());
});

describe("WholeBoardSection", () => {
  it("shows the Done window segmented with Sprint / 14 days / Since yesterday and the stored value", () => {
    render(<WholeBoardSection config={mkConfig({ doneWindow: "14d" })} columns={3} />);
    const seg = reg.find("Segmented", /done window/i);
    expect(seg.value).toBe("14d");
    expect(seg.options.map((o: any) => o.value)).toEqual(["sprint", "14d", "yesterday"]);
    expect(seg.options.map((o: any) => o.label)).toEqual(["Sprint", "14 days", "Since yesterday"]);
    expect(seg.options.every((o: any) => !o.disabled)).toBe(true);
  });

  it.each([
    ["sprint", "14d"],
    ["14d", "yesterday"],
    ["yesterday", "sprint"],
  ] as const)("autosaves a Done window change from %s to %s immediately", (from, to) => {
    render(<WholeBoardSection config={mkConfig({ doneWindow: from })} columns={2} />);
    act(() => reg.find("Segmented", /done window/i).onChange(to));
    expect(st.saveField).toHaveBeenCalledTimes(1);
    expect(st.saveField).toHaveBeenCalledWith("doneWindow", to);
  });

  it("does not mark Done window as a text field (no debounce)", () => {
    render(<WholeBoardSection config={mkConfig()} columns={1} />);
    act(() => reg.find("Segmented", /done window/i).onChange("yesterday"));
    const opts = st.saveField.mock.calls[0]?.[2];
    expect(st.saveField).toHaveBeenCalledTimes(1);
    expect(opts?.text).toBeFalsy();
  });

  it("disables the Sprint option on a Kanban board and leaves the others enabled", () => {
    render(<WholeBoardSection config={mkConfig({ board: kanban, scope: "board", doneWindow: "14d" })} columns={3} />);
    const seg = reg.find("Segmented", /done window/i);
    const byValue = Object.fromEntries(seg.options.map((o: any) => [o.value, !!o.disabled]));
    expect(byValue).toEqual({ sprint: true, "14d": false, yesterday: false });
  });

  it("keeps Sprint enabled on a scrum board", () => {
    render(<WholeBoardSection config={mkConfig()} columns={3} />);
    const sprint = reg.find("Segmented", /done window/i).options.find((o: any) => o.value === "sprint");
    expect(sprint.disabled).toBeFalsy();
  });

  it("renders Backlog rows as a 0 to 50 stepper at the stored value", () => {
    render(<WholeBoardSection config={mkConfig({ backlogRows: 12 })} columns={3} />);
    const stp = reg.find("Stepper", /backlog rows/i);
    expect(stp.value).toBe(12);
    expect(stp.min).toBe(0);
    expect(stp.max).toBe(50);
  });

  it.each([0, 1, 12, 50])("autosaves Backlog rows = %i immediately", (n) => {
    render(<WholeBoardSection config={mkConfig({ backlogRows: 8 })} columns={3} />);
    act(() => reg.find("Stepper", /backlog rows/i).onChange(n));
    expect(st.saveField).toHaveBeenCalledTimes(1);
    expect(st.saveField).toHaveBeenCalledWith("backlogRows", n);
  });

  it("explains the backlog setting: top of the ranked backlog, 0 turns it off", () => {
    render(<WholeBoardSection config={mkConfig()} columns={3} />);
    expect(screen.getByText(/top of the ranked backlog/i).textContent).toMatch(/0 turns it off/);
  });

  it("shows the section heading 'Whole board'", () => {
    render(<WholeBoardSection config={mkConfig()} columns={3} />);
    expect(screen.getByRole("heading", { name: /^Whole board/i })).toBeTruthy();
  });

  it("reflects a changed config prop (controlled by StandupConfig, not local state)", () => {
    const { rerender } = render(<WholeBoardSection config={mkConfig({ doneWindow: "sprint", backlogRows: 3 })} columns={3} />);
    expect(reg.find("Segmented", /done window/i).value).toBe("sprint");
    rerender(<WholeBoardSection config={mkConfig({ doneWindow: "yesterday", backlogRows: 20 })} columns={3} />);
    expect(reg.find("Segmented", /done window/i).value).toBe("yesterday");
    expect(reg.find("Stepper", /backlog rows/i).value).toBe(20);
  });
});
