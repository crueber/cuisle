/* eslint-disable @typescript-eslint/no-explicit-any */
// Configure > Ticket fields section (per connection; ux-spec §14.8; architecture ConnectionService ticket-field methods).
// Exports TicketFieldsSection (AppSectionProps, like Connection: it has no standup config).
//
// SPEC-NOTE (approach): like Connection.test.tsx, the section talks straight to the facade (no state wrapper exists
//   for per-connection prefs) through a fake Api installed with setApi that rejects unstubbed methods. The shared
//   Combobox is a recording fake (its DOM is specified by components/Combobox.test.tsx); the real Switch is used.
// SPEC-NOTE (load): on mount it calls connection.ticketFields() exactly once and nothing else (no write, no standup
//   or Jira call). It renders from the TicketFieldPrefs it got; every mutating method that returns TicketFieldPrefs
//   (pin, unpin, setHidden) replaces the shown state with that result.
// SPEC-NOTE (DOM): h2 "Ticket fields". A Combobox labelled /pin a field/i (placeholder "Pin a field…", static
//   `options` = prefs.available, label = option name, key = fieldId). A <ul> named /pinned/i lists the pinned fields
//   in order; each <li> shows the name and holds: a button "Move <name> up" and "Move <name> down" (the first row's
//   up and the last row's down are disabled), a button "Unpin <name>", and a Switch named "Hide <name>" (unchecked).
//   Drag-reordering may exist as an extra; these buttons are the accessible contract (as in Speakers).
//   Nothing pinned: the text "Nothing pinned" (case-insensitive) in place of the list.
// SPEC-NOTE (hidden): when prefs.hidden is non-empty a disclosure <button> named "Hidden (<n>)" (aria-expanded false
//   at first) sits under the list; the hidden names are NOT in the DOM while collapsed. Expanding lists them, each with
//   a checked Switch named "Hide <name>"; turning it off calls setTicketFieldHidden(fieldId, false).
// SPEC-NOTE (autosave): every action calls the api at once, with no Save button anywhere. Calls:
//   Combobox select -> pinTicketField(fieldId); Unpin -> unpinTicketField(fieldId); Hide switch ->
//   setTicketFieldHidden(fieldId, true); Move down/up -> moveTicketField(fieldId, newIndex) where newIndex is the
//   0-based pinned position the field moves to.
// SPEC-NOTE (optimistic move): moveTicketField returns only a SaveAck, so the list reorders at once and stays reordered
//   on success. If any write rejects, the section shows an element with role="alert" and the list returns to the last
//   confirmed server state; the raw error text is never shown.
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../../api";
import type { TicketFieldPrefs } from "../../../api/types";
import { TicketFieldsSection } from "./TicketFields";

const reg = vi.hoisted(() => ({ last: null as any }));
vi.mock("../../../components/Combobox", async () => {
  const { h } = await import("preact");
  return {
    Combobox: (p: any) => {
      if (/pin a field/i.test(p.label)) reg.last = p;
      return h("input", { role: "combobox", "aria-label": p.label, placeholder: p.placeholder, readOnly: true, value: "" });
    },
  };
});

const fp = (fieldId: string, name: string, position: number, hidden = false, kind = "text") => ({ fieldId, name, kind, position, hidden });
const fo = (fieldId: string, name: string) => ({ fieldId, name, kind: "text", custom: true });

const SEV = fp("customfield_10001", "Severity", 0, false, "option");
const PLAT = fp("customfield_10002", "Platforms hit", 1, false, "labels");
const SUP = fp("customfield_10003", "Support ticket", 2, false, "url");
const ROLL = fp("customfield_10004", "Rollout", -1, true, "number");
const ENV = fo("customfield_10005", "Environment");
const REG = fo("customfield_10006", "Region");

function prefs(over: Partial<TicketFieldPrefs> = {}): TicketFieldPrefs {
  return { pinned: [SEV, PLAT, SUP], hidden: [ROLL], available: [ENV, REG], ...over };
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

beforeEach(() => {
  reg.last = null;
});

async function mountLoaded(over: Record<string, (...a: any[]) => any> = {}, initial: TicketFieldPrefs = prefs()) {
  const stubs: Record<string, any> = { "connection.ticketFields": vi.fn(async () => initial), ...over };
  const fake = installFakeApi(stubs);
  const view = render(<TicketFieldsSection columns={3} />);
  await screen.findByRole("heading", { name: /^ticket fields/i });
  await waitFor(() => expect(document.body.textContent).toMatch(/Severity|nothing pinned/i)); // prefs arrived
  return { ...fake, stubs, ...view };
}
const pinnedList = () => screen.getByRole("list", { name: /pinned/i });
const pinnedNames = () => within(pinnedList()).getAllByRole("listitem").map((li) => within(li).getByText(/^(Severity|Platforms hit|Support ticket|Environment|Region|Rollout)$/).textContent);

describe("TicketFieldsSection: load and layout", () => {
  it("loads the prefs once on mount, calling nothing else", async () => {
    const { calls } = await mountLoaded();
    expect(calls).toEqual(["connection.ticketFields"]);
  });

  it("lists the pinned fields in order with the per-row controls", async () => {
    await mountLoaded();
    expect(pinnedNames()).toEqual(["Severity", "Platforms hit", "Support ticket"]);
    const first = within(pinnedList()).getAllByRole("listitem")[0] as HTMLElement;
    expect(within(first).getByRole("button", { name: "Unpin Severity" })).toBeTruthy();
    expect(within(first).getByRole("switch", { name: "Hide Severity" }).getAttribute("aria-checked")).toBe("false");
    expect((within(first).getByRole("button", { name: "Move Severity up" }) as HTMLButtonElement).disabled).toBe(true);
    expect((within(first).getByRole("button", { name: "Move Severity down" }) as HTMLButtonElement).disabled).toBe(false);
    const last = within(pinnedList()).getAllByRole("listitem")[2] as HTMLElement;
    expect((within(last).getByRole("button", { name: "Move Support ticket down" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("offers the pinnable fields through the Combobox 'Pin a field…'", async () => {
    await mountLoaded();
    expect(screen.getByRole("combobox", { name: /pin a field/i }).getAttribute("placeholder")).toMatch(/pin a field/i);
    expect(reg.last.options.map((o: any) => reg.last.getLabel(o))).toEqual(["Environment", "Region"]);
    expect(reg.last.getKey(ENV)).toBe("customfield_10005");
  });

  it("says nothing is pinned when the list is empty", async () => {
    const { container } = await mountLoaded({}, prefs({ pinned: [], hidden: [] }));
    expect(container.textContent).toMatch(/nothing pinned/i);
  });

  it("has no Save button (autosave)", async () => {
    await mountLoaded();
    expect(screen.queryByRole("button", { name: /^save/i })).toBeNull();
  });
});

describe("TicketFieldsSection: pin", () => {
  it("pins the chosen field at once and shows the server's result", async () => {
    const after = prefs({ pinned: [SEV, PLAT, SUP, fp("customfield_10005", "Environment", 3)], available: [REG] });
    const { stubs } = await mountLoaded({ "connection.pinTicketField": vi.fn(async () => after) });
    await act(async () => { reg.last.onSelect(ENV); });
    expect(stubs["connection.pinTicketField"]).toHaveBeenCalledTimes(1);
    expect(stubs["connection.pinTicketField"]).toHaveBeenCalledWith("customfield_10005");
    await waitFor(() => expect(pinnedNames()).toEqual(["Severity", "Platforms hit", "Support ticket", "Environment"]));
    expect(reg.last.options.map((o: any) => o.fieldId)).toEqual(["customfield_10006"]);
  });

  it("alerts and keeps the list when pinning fails", async () => {
    await mountLoaded({ "connection.pinTicketField": vi.fn(async () => { throw new Error("secret detail xyz"); }) });
    await act(async () => { reg.last.onSelect(ENV); });
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText(/secret detail xyz/)).toBeNull();
    expect(pinnedNames()).toEqual(["Severity", "Platforms hit", "Support ticket"]);
  });
});

describe("TicketFieldsSection: unpin", () => {
  it("unpins at once and shows the server's result (the field is available to pin again)", async () => {
    const after = prefs({ pinned: [SEV, SUP], available: [ENV, REG, fo("customfield_10002", "Platforms hit")] });
    const { stubs } = await mountLoaded({ "connection.unpinTicketField": vi.fn(async () => after) });
    fireEvent.click(screen.getByRole("button", { name: "Unpin Platforms hit" }));
    expect(stubs["connection.unpinTicketField"]).toHaveBeenCalledTimes(1);
    expect(stubs["connection.unpinTicketField"]).toHaveBeenCalledWith("customfield_10002");
    await waitFor(() => expect(pinnedNames()).toEqual(["Severity", "Support ticket"]));
    expect(reg.last.options.map((o: any) => o.fieldId)).toContain("customfield_10002");
  });
});

describe("TicketFieldsSection: reorder", () => {
  it("Move down sends the new 0-based position and reorders at once", async () => {
    const { stubs } = await mountLoaded({ "connection.moveTicketField": vi.fn(async () => ({ saved: true })) });
    fireEvent.click(screen.getByRole("button", { name: "Move Severity down" }));
    expect(stubs["connection.moveTicketField"]).toHaveBeenCalledTimes(1);
    expect(stubs["connection.moveTicketField"]).toHaveBeenCalledWith("customfield_10001", 1);
    expect(pinnedNames()).toEqual(["Platforms hit", "Severity", "Support ticket"]);
  });

  it("Move up sends the field's new position", async () => {
    const { stubs } = await mountLoaded({ "connection.moveTicketField": vi.fn(async () => ({ saved: true })) });
    fireEvent.click(screen.getByRole("button", { name: "Move Support ticket up" }));
    expect(stubs["connection.moveTicketField"]).toHaveBeenCalledWith("customfield_10003", 1);
    expect(pinnedNames()).toEqual(["Severity", "Support ticket", "Platforms hit"]);
  });

  it("stays reordered after a successful save", async () => {
    await mountLoaded({ "connection.moveTicketField": vi.fn(async () => ({ saved: true })) });
    fireEvent.click(screen.getByRole("button", { name: "Move Severity down" }));
    await act(async () => { await Promise.resolve(); });
    expect(pinnedNames()).toEqual(["Platforms hit", "Severity", "Support ticket"]);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("returns to the confirmed order and alerts when the save fails", async () => {
    await mountLoaded({ "connection.moveTicketField": vi.fn(async () => { throw new Error("nope"); }) });
    fireEvent.click(screen.getByRole("button", { name: "Move Severity down" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    await waitFor(() => expect(pinnedNames()).toEqual(["Severity", "Platforms hit", "Support ticket"]));
  });
});

describe("TicketFieldsSection: hide", () => {
  it("hides a pinned field at once and shows the server's result in the Hidden list", async () => {
    const after = prefs({ pinned: [PLAT, SUP], hidden: [ROLL, fp("customfield_10001", "Severity", -1, true, "option")] });
    const { stubs } = await mountLoaded({ "connection.setTicketFieldHidden": vi.fn(async () => after) });
    fireEvent.click(screen.getByRole("switch", { name: "Hide Severity" }));
    expect(stubs["connection.setTicketFieldHidden"]).toHaveBeenCalledTimes(1);
    expect(stubs["connection.setTicketFieldHidden"]).toHaveBeenCalledWith("customfield_10001", true);
    await waitFor(() => expect(pinnedNames()).toEqual(["Platforms hit", "Support ticket"]));
    expect(screen.getByRole("button", { name: /^hidden \(2\)$/i })).toBeTruthy();
  });

  it("keeps hidden fields out of the DOM until the 'Hidden (n)' disclosure is opened", async () => {
    await mountLoaded();
    const toggle = screen.getByRole("button", { name: /^hidden \(1\)$/i });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText("Rollout")).toBeNull();
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByText("Rollout")).toBeTruthy();
    expect(screen.getByRole("switch", { name: "Hide Rollout" }).getAttribute("aria-checked")).toBe("true");
  });

  it("un-hides a hidden field through its switch", async () => {
    const after = prefs({ hidden: [], pinned: [SEV, PLAT, SUP, fp("customfield_10004", "Rollout", 3, false, "number")] });
    const { stubs } = await mountLoaded({ "connection.setTicketFieldHidden": vi.fn(async () => after) });
    fireEvent.click(screen.getByRole("button", { name: /^hidden \(1\)$/i }));
    fireEvent.click(screen.getByRole("switch", { name: "Hide Rollout" }));
    expect(stubs["connection.setTicketFieldHidden"]).toHaveBeenCalledTimes(1);
    expect(stubs["connection.setTicketFieldHidden"]).toHaveBeenCalledWith("customfield_10004", false);
    await waitFor(() => expect(pinnedNames()).toEqual(["Severity", "Platforms hit", "Support ticket", "Rollout"]));
    expect(screen.queryByRole("button", { name: /^hidden/i })).toBeNull();
  });

  it("shows no Hidden disclosure when nothing is hidden", async () => {
    await mountLoaded({}, prefs({ hidden: [] }));
    expect(screen.queryByRole("button", { name: /^hidden/i })).toBeNull();
  });

  it("alerts and leaves the switch where it was when hiding fails", async () => {
    await mountLoaded({ "connection.setTicketFieldHidden": vi.fn(async () => { throw new Error("nope"); }) });
    fireEvent.click(screen.getByRole("switch", { name: "Hide Severity" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    await waitFor(() => expect(screen.getByRole("switch", { name: "Hide Severity" }).getAttribute("aria-checked")).toBe("false"));
    expect(pinnedNames()).toEqual(["Severity", "Platforms hit", "Support ticket"]);
  });
});
