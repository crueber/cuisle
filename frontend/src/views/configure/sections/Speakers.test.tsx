/* eslint-disable @typescript-eslint/no-explicit-any */
// Configure > Speakers section (ux-spec §9.3 ordered lists, §9.4 Speakers; architecture §5.3).
//
// SPEC-NOTE (approach): state functions mocked via vi.mock("state/config"); Checkbox/Switch/Segmented/Combobox replaced
//   by fakes (see Source.test.tsx). connection.searchUsers has no state wrapper, so it uses a fake Api (setApi).
// SPEC-NOTE: rows are role="listitem" in document order = config.speakers order (the list is the only list in the
//   section). A row is found by its speaker name. Each row has an include Checkbox whose label contains the name.
// SPEC-NOTE: row number: included rows show their 1-based rank among INCLUDED rows (first two included rows -> "1",
//   "2"); an excluded row shows "—" (em dash) instead of a number.
// SPEC-NOTE: up/down buttons are buttons inside the row with accessible names matching /\bup\b/i and /\bdown\b/i
//   (e.g. "Move Dana Whitaker up"). They are `disabled` at the ends of the list. moveSpeaker(accountId, toPosition)
//   takes the 0-based target index in the FULL list (excluded rows count), e.g. Iris (index 1) up -> 0, down -> 2.
// SPEC-NOTE: Alt+ArrowUp / Alt+ArrowDown are handled at ROW level (keydown bubbling from any control in the row, the
//   test fires it on the row's include checkbox) and call moveSpeaker like the buttons. Plain arrows do nothing.
//   Moving past an end is a no-op (no call).
// SPEC-NOTE: toolbar buttons are named "Shuffle", "A–Z" (en dash; hyphen tolerated) and "Most active" and call
//   sortSpeakers("shuffle" | "az" | "active"). After Shuffle a transient note containing "Shuffled just now" appears.
// SPEC-NOTE: "Reshuffle daily" is a Switch -> saveField("reshuffleDaily", bool). "Unassigned as a speaker" is a
//   Segmented (First/Last/Skip, values first|last|skip) -> saveField("unassignedPosition", value).
// SPEC-NOTE: "Add someone from this space…" is a Combobox with clearOnSelect, async load = connection.searchUsers(
//   config.id, query), and onSelect(UserOption) -> addSpeaker(user). A Remove button (/remove/i) exists ONLY on
//   rows with manual === true and calls removeSpeaker(accountId); the Undo toast belongs to that state function.
// SPEC-NOTE: with no board chosen (new standup) no rows or toolbar render, only an empty state mentioning "board".


import { act, fireEvent, render, screen, within } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../../api";
import type { StandupConfig, UserOption } from "../../../api/types";
import { SpeakersSection } from "./Speakers";

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

vi.mock("../../../components/Combobox", async () => {
  const { h } = await import("preact");
  return {
    Combobox: (p: any) => {
      reg.rec("Combobox", p);
      return h("input", { role: "combobox", "aria-label": p.label, disabled: p.disabled, placeholder: p.placeholder, value: p.value ? p.getLabel(p.value) : "", readOnly: true });
    },
  };
});

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

vi.mock("../../../components/Checkbox", async () => {
  const { h } = await import("preact");
  return {
    Checkbox: (p: any) => {
      reg.rec("Checkbox", p);
      return h("input", { type: "checkbox", "aria-label": p.label, checked: p.checked, disabled: p.disabled, onChange: (e: any) => p.onChange(e.currentTarget.checked) });
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
      { lane: "qa", label: "QA", limitDays: 3, columns: ["Verify"] },
      { lane: "review", label: "Review", limitDays: 2, columns: ["Code Review"] },
      { lane: "prog", label: "In progress", limitDays: 5, columns: ["Doing"] },
      { lane: "todo", label: "To do", limitDays: 0, columns: ["Ready"] },
      { lane: "done", label: "Done", limitDays: 0, columns: ["Closed"] },
    ],
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
function installFakeApi(over: Record<string, (...a: any[]) => any> = {}) {
  const calls: string[] = [];
  const fake = new Proxy({}, {
    get: (_t, svc: string) => new Proxy({}, {
      get: (_s, m: string) => (...a: unknown[]) => {
        const key = `${svc}.${m}`;
        calls.push(key);
        const f = over[key];
        return f ? f(...a) : Promise.reject(new Error(`unstubbed ${key}`));
      },
    }),
  }) as unknown as Api;
  setApi(fake);
  return { calls, over };
}

const row = (name: string): HTMLElement => {
  const el = screen.getByText(name).closest('li, [role="listitem"]');
  if (!el) throw new Error(`no list row for ${name}`);
  return el as HTMLElement;
};
const include = (name: string) => within(row(name)).getByRole("checkbox") as HTMLInputElement;
const upBtn = (name: string) => within(row(name)).getByRole("button", { name: /\bup\b/i }) as HTMLButtonElement;
const downBtn = (name: string) => within(row(name)).getByRole("button", { name: /\bdown\b/i }) as HTMLButtonElement;
const NAMES = ["Dana Whitaker", "Iris Calloway", "Amara Osei", "Lee Tran"];

beforeEach(() => {
  reg.clear();
  Object.values(st).forEach((f) => f.mockClear());
  installFakeApi();
});

describe("SpeakersSection: list", () => {
  it("renders every speaker, in config order, including excluded ones", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(4);
    items.forEach((li, i) => expect(li.textContent).toContain(NAMES[i] ?? ""));
  });

  it("has an include checkbox per row reflecting speaker.included", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    expect(NAMES.map((n) => include(n).checked)).toEqual([true, true, false, true]);
  });

  it("numbers included rows and shows an em dash for excluded rows", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    expect(row("Dana Whitaker").textContent).toMatch(/\b1\b/);
    expect(row("Iris Calloway").textContent).toMatch(/\b2\b/);
    expect(row("Amara Osei").textContent).toContain("—");
    expect(row("Dana Whitaker").textContent).not.toContain("—");
  });

  it("shows the speaker note for rows that have one", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    expect(row("Amara Osei").textContent).toContain("no tickets right now");
  });

  it("shows the footnote about who is listed", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    expect(screen.getByText(/anyone with tickets on this board in the last 30 days/i)).toBeTruthy();
  });

  it("makes no network call just by rendering", () => {
    const { calls } = installFakeApi();
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    expect(screen.getAllByRole("listitem")).toHaveLength(4);
    expect(calls).toEqual([]);
  });
});

describe("SpeakersSection: include", () => {
  it("including an excluded speaker calls setSpeakerIncluded(id, true)", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    act(() => { include("Amara Osei").click(); });
    expect(st.setSpeakerIncluded).toHaveBeenCalledTimes(1);
    expect(st.setSpeakerIncluded).toHaveBeenCalledWith("u-amara", true);
  });

  it("excluding an included speaker calls setSpeakerIncluded(id, false)", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    act(() => { include("Iris Calloway").click(); });
    expect(st.setSpeakerIncluded).toHaveBeenCalledTimes(1);
    expect(st.setSpeakerIncluded).toHaveBeenCalledWith("u-iris", false);
  });
});

describe("SpeakersSection: reorder", () => {
  it("moves a speaker up with the up button", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    act(() => { upBtn("Iris Calloway").click(); });
    expect(st.moveSpeaker).toHaveBeenCalledTimes(1);
    expect(st.moveSpeaker).toHaveBeenCalledWith("u-iris", 0);
  });

  it("moves a speaker down with the down button (excluded rows count as positions)", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    act(() => { downBtn("Iris Calloway").click(); });
    expect(st.moveSpeaker).toHaveBeenCalledTimes(1);
    expect(st.moveSpeaker).toHaveBeenCalledWith("u-iris", 2);
  });

  it("disables up on the first row and down on the last row", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    expect(upBtn("Dana Whitaker").disabled).toBe(true);
    expect(downBtn("Dana Whitaker").disabled).toBe(false);
    expect(upBtn("Lee Tran").disabled).toBe(false);
    expect(downBtn("Lee Tran").disabled).toBe(true);
  });

  it("Alt+ArrowDown on a row moves it down", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    fireEvent.keyDown(include("Iris Calloway"), { key: "ArrowDown", altKey: true });
    expect(st.moveSpeaker).toHaveBeenCalledTimes(1);
    expect(st.moveSpeaker).toHaveBeenCalledWith("u-iris", 2);
  });

  it("Alt+ArrowUp on a row moves it up", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    fireEvent.keyDown(include("Iris Calloway"), { key: "ArrowUp", altKey: true });
    expect(st.moveSpeaker).toHaveBeenCalledTimes(1);
    expect(st.moveSpeaker).toHaveBeenCalledWith("u-iris", 0);
  });

  it("ignores plain arrows and Alt+Arrow past the ends", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    fireEvent.keyDown(include("Iris Calloway"), { key: "ArrowDown" });
    fireEvent.keyDown(include("Dana Whitaker"), { key: "ArrowUp", altKey: true });
    fireEvent.keyDown(include("Lee Tran"), { key: "ArrowDown", altKey: true });
    expect(st.moveSpeaker).not.toHaveBeenCalled();
    expect(screen.getAllByRole("listitem")).toHaveLength(4);
  });
});

describe("SpeakersSection: sort toolbar", () => {
  it.each([
    [/^shuffle$/i, "shuffle"],
    [/^A.Z$/, "az"],
    [/^most active$/i, "active"],
  ] as const)("the %s button calls sortSpeakers('%s')", (name, mode) => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    act(() => { screen.getByRole("button", { name }).click(); });
    expect(st.sortSpeakers).toHaveBeenCalledTimes(1);
    expect(st.sortSpeakers).toHaveBeenCalledWith(mode);
  });

  it("shows a transient 'Shuffled just now' note after shuffling", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    expect(screen.queryByText(/Shuffled just now/i)).toBeNull();
    expect(screen.getByRole("button", { name: /^shuffle$/i })).toBeTruthy();
    act(() => { screen.getByRole("button", { name: /^shuffle$/i }).click(); });
    expect(screen.getByText(/Shuffled just now/i)).toBeTruthy();
  });
});

describe("SpeakersSection: reshuffle daily and unassigned", () => {
  it("renders Reshuffle daily as a switch mirroring config", () => {
    render(<SpeakersSection config={mkConfig({ reshuffleDaily: true })} columns={3} />);
    expect(reg.find("Switch", /reshuffle daily/i).checked).toBe(true);
  });

  it("toggling Reshuffle daily autosaves immediately", () => {
    render(<SpeakersSection config={mkConfig({ reshuffleDaily: false })} columns={3} />);
    act(() => reg.find("Switch", /reshuffle daily/i).onChange(true));
    expect(st.saveField).toHaveBeenCalledTimes(1);
    expect(st.saveField).toHaveBeenCalledWith("reshuffleDaily", true);
  });

  it("offers Unassigned as a speaker: First / Last / Skip", () => {
    render(<SpeakersSection config={mkConfig({ unassignedPosition: "last" })} columns={3} />);
    const seg = reg.find("Segmented", /unassigned/i);
    expect(seg.value).toBe("last");
    expect(seg.options.map((o: any) => o.value)).toEqual(["first", "last", "skip"]);
    expect(seg.options.map((o: any) => o.label)).toEqual(["First", "Last", "Skip"]);
  });

  it("autosaves an unassigned position change immediately", () => {
    render(<SpeakersSection config={mkConfig({ unassignedPosition: "last" })} columns={3} />);
    act(() => reg.find("Segmented", /unassigned/i).onChange("skip"));
    expect(st.saveField).toHaveBeenCalledTimes(1);
    expect(st.saveField).toHaveBeenCalledWith("unassignedPosition", "skip");
  });
});

describe("SpeakersSection: add and remove", () => {
  const user: UserOption = { accountId: "u-new", displayName: "Noor Haddad", initials: "NH", onBoard: false };

  it("add-someone combobox clears on select and searches users for this standup", async () => {
    const { over } = installFakeApi({ "connection.searchUsers": vi.fn(async () => [user]) });
    render(<SpeakersSection config={mkConfig({ id: 7 })} columns={3} />);
    const c = reg.find("Combobox", /add someone/i);
    expect(c.clearOnSelect).toBe(true);
    expect(c.placeholder).toMatch(/add someone from this space/i);
    await expect(c.load("no", new AbortController().signal)).resolves.toEqual([user]);
    expect(over["connection.searchUsers"]).toHaveBeenCalledWith(7, "no");
  });

  it("selecting a person calls addSpeaker(user)", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    act(() => reg.find("Combobox", /add someone/i).onSelect(user));
    expect(st.addSpeaker).toHaveBeenCalledTimes(1);
    expect(st.addSpeaker).toHaveBeenCalledWith(user);
  });

  it("offers Remove on every speaker, board or manual (maintainer, 2026-11-23)", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    expect(screen.getAllByRole("listitem")).toHaveLength(4);
    expect(screen.getAllByRole("button", { name: /remove/i })).toHaveLength(4);
    act(() => { within(row("Dana Whitaker")).getByRole("button", { name: /remove/i }).click(); });
    expect(st.removeSpeaker).toHaveBeenCalledWith("u-dana");
  });

  it("removing a manual speaker calls removeSpeaker(accountId)", () => {
    render(<SpeakersSection config={mkConfig()} columns={3} />);
    act(() => { within(row("Lee Tran")).getByRole("button", { name: /remove/i }).click(); });
    expect(st.removeSpeaker).toHaveBeenCalledTimes(1);
    expect(st.removeSpeaker).toHaveBeenCalledWith("u-lee");
  });
});

describe("SpeakersSection: new standup", () => {
  it("shows an empty state mentioning the board until one is chosen", () => {
    render(<SpeakersSection config={mkConfig({ draft: true, space: null, board: null, speakers: [] })} columns={3} />);
    expect(screen.getByRole("heading", { name: /^Speakers/i })).toBeTruthy();
    expect(screen.queryAllByRole("listitem")).toHaveLength(0);
    expect(screen.queryByRole("button", { name: /^shuffle$/i })).toBeNull();
    expect(screen.getAllByText(/board/i).length).toBeGreaterThan(0);
  });
});
