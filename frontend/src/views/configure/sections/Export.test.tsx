/* eslint-disable @typescript-eslint/no-explicit-any */
// Configure > Export section (per standup; ux-spec §9.4 Export; product-spec F10; architecture §3.2 SinkService, §5.3).
//
// SPEC-NOTE (approach): Export talks to SinkService through the facade (fake Api via setApi: sink.kinds/list/save/send/
//   retry/delete) because there is no state wrapper for sinks. Autosave goes through state/save.enqueue (mocked here
//   to capture jobs; real signals kept) and the Undo toast through state/ui.showToast (mocked). The tests "settle" by
//   running every captured job's run(); an implementation that calls sink.save directly for IMMEDIATE controls is also
//   accepted, but TEXT fields must go through enqueue with debounceMs === TEXT_DEBOUNCE_MS (400), invariant 3.
//   Shared components are recording/interactive fakes (see Source.test.tsx).
// SPEC-NOTE: on mount the section calls sink.kinds() and sink.list() exactly once each (no polling) and shows the
//   sinks whose standupId equals config.id or 0 ("all standups"); sinks scoped to another standup are not shown.
// SPEC-NOTE: each row shows the sink name, its kind's displayName ("File (JSON)"), a destination detail line (the
//   folder path; found as text or as an input value), and renders its kind's schema fields inline and editable:
//   text/url/path -> TextField (url gets type "url"), select -> Combobox over field.options, bool -> a Switch or a
//   Checkbox labelled with the field label (config value "true"/"false"), secret -> TextField type "password" that is
//   never prefilled with a secret. A Name TextField (/name/i) edits SinkConfig.name.
// SPEC-NOTE: every autosave sends the FULL SinkInput {id, kind, name, standupId, config (all string values), secret,
//   sendOn, enabled}; secret is null unless the secret field itself was edited (then the typed string).
// SPEC-NOTE: Send on is a Segmented labelled /send on/i with values "manual" | "after_sync" (labels "Manual" and
//   "After each sync"). "Send now" calls sink.send(sink.id, config.id, request) with request.kind === "current".
// SPEC-NOTE: status line: lastDelivery.status "delivered" -> text "Delivered 2 min ago · <receipt>" (relative to
//   Date.now(), deliveredAt in unix ms); "failed" or "dead" -> text containing "Failed" plus a Retry button calling
//   sink.retry(lastDelivery.id); no lastDelivery -> neither text nor Retry. The section re-reads sink.list() when a
//   "sink:delivered" / "sink:failed" event arrives (api/events bus), never on a timer.
// SPEC-NOTE: "+ Add destination" is a Combobox (/add destination/i) with static `options` = the registered kinds (label = displayName).
//   Choosing a kind reveals that kind's fields; the first debounced edit saves SinkInput{id: 0, kind, standupId:
//   config.id (this standup), sendOn "manual", enabled true, secret null, config containing the typed values}.
// SPEC-NOTE: Delete removes the row at once and shows an Undo toast via showToast({message containing the sink name,
//   action: { label: "Undo" } }). Whether sink.delete is sent immediately or deferred to the toast expiry is not
//   specified and not asserted.


import { act, render, screen, waitFor } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../../api";
import { createMemoryBus, setEventBus } from "../../../api/events";
import type { OutboxItem, SinkConfig, SinkInput, SinkKind, StandupConfig } from "../../../api/types";
import { ExportSection } from "./Export";

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

const mocks = vi.hoisted(() => ({ enqueue: vi.fn(), showToast: vi.fn() }));
vi.mock("../../../state/save", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../state/save")>()),
  enqueue: mocks.enqueue,
}));
vi.mock("../../../state/ui", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../state/ui")>()),
  showToast: mocks.showToast,
}));
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

vi.mock("../../../components/Checkbox", async () => {
  const { h } = await import("preact");
  return {
    Checkbox: (p: any) => {
      reg.rec("Checkbox", p);
      return h("input", { type: "checkbox", "aria-label": p.label, checked: p.checked, disabled: p.disabled, onChange: (e: any) => p.onChange(e.currentTarget.checked) });
    },
  };
});

vi.mock("../../../components/TextField", async () => {
  const { h } = await import("preact");
  return {
    TextField: (p: any) => {
      reg.rec("TextField", p);
      return h("div", {},
        h("input", { type: p.type ?? "text", "aria-label": p.label, value: p.value, placeholder: p.placeholder, "aria-invalid": p.error ? "true" : undefined, readOnly: true }),
        p.error ? h("span", { role: "alert" }, p.error) : null);
    },
  };
});

vi.mock("../../../components/Combobox", async () => {
  const { h } = await import("preact");
  return {
    Combobox: (p: any) => {
      reg.rec("Combobox", p);
      return h("input", { role: "combobox", "aria-label": p.label, disabled: p.disabled, placeholder: p.placeholder, value: p.value ? p.getLabel(p.value) : "", readOnly: true });
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

const CAPS = { current: true, range: false, formats: ["cuisle.export.v1"], needsSecret: false, idempotent: true };
const field = (key: string, label: string, type: string, extra: object = {}) => ({ key, label, help: "", type, options: [] as string[], required: false, default: "", ...extra });
const FILE_KIND: SinkKind = { kind: "file", displayName: "File (JSON)", capabilities: CAPS, schema: [field("folder", "Folder", "path", { required: true })] };
const DEMO_KIND: SinkKind = {
  kind: "demo", displayName: "Demo hook", capabilities: { ...CAPS, needsSecret: true },
  schema: [
    field("endpoint", "Endpoint", "url"),
    field("note", "Note", "text"),
    field("format", "Format", "select", { options: ["json", "ndjson"], default: "json" }),
    field("pretty", "Pretty print", "bool"),
    field("token", "Token", "secret"),
  ],
};
const sink = (over: Partial<SinkConfig> = {}): SinkConfig => ({
  id: 7, kind: "file", name: "Team folder", standupId: 1, config: { folder: "/Users/me/standups" }, hasSecret: false,
  sendOn: "manual", enabled: true, lastDelivery: null, ...over,
});
const outbox = (over: Partial<OutboxItem> = {}): OutboxItem => ({
  id: 31, sinkId: 7, standupId: 1, cause: "manual", payloadKind: "current", status: "delivered", attempts: 1, errorCode: "", error: "",
  receipt: "cuisle-MOB-2026-11-23.json", createdAt: Date.now() - 125_000, deliveredAt: Date.now() - 120_000, ...over,
});
const saved = (i: SinkInput): SinkConfig => ({
  id: i.id || 12, kind: i.kind, name: i.name, standupId: i.standupId, config: i.config ?? {}, hasSecret: i.secret != null,
  sendOn: i.sendOn, enabled: i.enabled, lastDelivery: null,
});

let bus: ReturnType<typeof createMemoryBus>;
let sinks: SinkConfig[];
let ran = 0;
type Job = { key: string; run: () => Promise<unknown>; debounceMs?: number; onSaved?: (r: unknown) => void };
const jobs = (): Job[] => mocks.enqueue.mock.calls.map((c) => c[0] as Job);
/** Runs every captured autosave job that has not run yet (what save.ts would do). */
async function settle() {
  const all = jobs();
  while (ran < all.length) {
    const j = all[ran++]!;
    const r = await j.run();
    j.onSaved?.(r);
  }
}
const api = (over: Record<string, (...a: any[]) => any> = {}) =>
  installFakeApi({
    "sink.kinds": vi.fn(async () => [FILE_KIND, DEMO_KIND]),
    "sink.list": vi.fn(async () => sinks),
    "sink.save": vi.fn(async (i: SinkInput) => saved(i)),
    "sink.send": vi.fn(async () => 99),
    "sink.retry": vi.fn(async () => undefined),
    "sink.delete": vi.fn(async () => undefined),
    "sink.outbox": vi.fn(async () => []),
    ...over,
  });
const lastSave = (over: Record<string, any>): SinkInput => {
  const calls = over["sink.save"].mock.calls;
  return calls[calls.length - 1][0];
};
const hasText = (s: string | RegExp) => !!(screen.queryByText(s) ?? (typeof s === "string" ? screen.queryByDisplayValue(s) : null));

beforeEach(() => {
  reg.clear();
  Object.values(st).forEach((f) => f.mockClear());
  mocks.enqueue.mockReset();
  mocks.showToast.mockReset();
  ran = 0;
  sinks = [sink()];
  bus = createMemoryBus();
  setEventBus(bus);
});

describe("ExportSection: listing", () => {
  it("loads kinds and sinks once each, with no polling", async () => {
    const { over } = api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    expect(await screen.findByText(/File \(JSON\)/)).toBeTruthy();
    expect(over["sink.kinds"]).toHaveBeenCalledTimes(1);
    expect(over["sink.list"]).toHaveBeenCalledTimes(1);
    expect(over["sink.save"]).not.toHaveBeenCalled();
    expect(over["sink.send"]).not.toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: /^Export/i })).toBeTruthy();
  });

  it("shows destinations for this standup and for all standups, not for other standups", async () => {
    sinks = [sink(), sink({ id: 8, name: "Shared archive", standupId: 0 }), sink({ id: 9, name: "Other team", standupId: 2 })];
    api();
    render(<ExportSection config={mkConfig({ id: 1 })} columns={3} />);
    await waitFor(() => expect(hasText("Team folder")).toBe(true));
    expect(hasText("Shared archive")).toBe(true);
    expect(hasText("Other team")).toBe(false);
  });

  it("shows the kind's display name and the destination folder for a file sink", async () => {
    api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    await waitFor(() => expect(hasText("Team folder")).toBe(true));
    expect(screen.getByText(/File \(JSON\)/)).toBeTruthy();
    expect(hasText("/Users/me/standups")).toBe(true);
  });

  it("shows an empty state that points at Add destination when there are none", async () => {
    sinks = [];
    const { over } = api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    await waitFor(() => expect(over["sink.list"]).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(reg.has("Combobox", /add destination/i)).toBe(true));
    expect(screen.queryByRole("button", { name: /send now/i })).toBeNull();
  });
});

describe("ExportSection: add destination", () => {
  it("lists the registered sink kinds by display name", async () => {
    api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    await waitFor(() => expect(reg.has("Combobox", /add destination/i)).toBe(true));
    await waitFor(() => expect(reg.find("Combobox", /add destination/i).options?.length).toBe(2));
    const cc = reg.find("Combobox", /add destination/i);
    expect(cc.options.map((o: any) => cc.getLabel(o))).toEqual(["File (JSON)", "Demo hook"]);
  });

  it("choosing a kind reveals its fields and saves a new standup-scoped sink after a debounced edit", async () => {
    sinks = [];
    const { over } = api();
    render(<ExportSection config={mkConfig({ id: 1 })} columns={3} />);
    await waitFor(() => expect(reg.find("Combobox", /add destination/i).options?.length).toBe(2));
    const c = reg.find("Combobox", /add destination/i);
    act(() => c.onSelect(c.options.find((o: any) => c.getLabel(o) === "File (JSON)")));
    await waitFor(() => expect(reg.has("TextField", /folder/i)).toBe(true));
    const before = jobs().length;
    act(() => reg.find("TextField", /folder/i).onInput("/tmp/out"));
    expect(jobs().length).toBeGreaterThan(before);
    expect(jobs().at(-1)?.debounceMs).toBe(400);
    await settle();
    expect(over["sink.save"]).toHaveBeenCalled();
    const input = lastSave(over);
    expect(input).toMatchObject({ id: 0, kind: "file", standupId: 1, sendOn: "manual", enabled: true, secret: null });
    expect(input.config).toMatchObject({ folder: "/tmp/out" });
    expect(typeof input.name).toBe("string");
  });
});

describe("ExportSection: autosave of an existing destination", () => {
  it("send on change saves the full SinkInput immediately (no text debounce)", async () => {
    const { over } = api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    await waitFor(() => expect(reg.has("Segmented", /send on/i)).toBe(true));
    const seg = reg.find("Segmented", /send on/i);
    expect(seg.value).toBe("manual");
    expect(seg.options.map((o: any) => o.value)).toEqual(["manual", "after_sync"]);
    expect(seg.options.map((o: any) => o.label)).toEqual(["Manual", "After each sync"]);
    act(() => seg.onChange("after_sync"));
    expect(jobs().filter((j) => j.debounceMs).length).toBe(0);
    await settle();
    expect(over["sink.save"]).toHaveBeenCalledTimes(1);
    expect(lastSave(over)).toEqual({
      id: 7, kind: "file", name: "Team folder", standupId: 1, config: { folder: "/Users/me/standups" }, secret: null, sendOn: "after_sync", enabled: true,
    });
  });

  it("renaming debounces by 400 ms through the autosave queue, then saves the new name", async () => {
    const { over } = api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    await waitFor(() => expect(reg.has("TextField", /name/i)).toBe(true));
    expect(reg.find("TextField", /name/i).value).toBe("Team folder");
    act(() => reg.find("TextField", /name/i).onInput("Team folder 2"));
    expect(jobs()).toHaveLength(1);
    expect(jobs()[0]?.debounceMs).toBe(400);
    expect(over["sink.save"]).not.toHaveBeenCalled();
    await settle();
    expect(lastSave(over)).toMatchObject({ id: 7, name: "Team folder 2", config: { folder: "/Users/me/standups" }, secret: null });
  });

  it("editing the folder keeps the other fields and debounces", async () => {
    const { over } = api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    await waitFor(() => expect(reg.has("TextField", /folder/i)).toBe(true));
    expect(reg.find("TextField", /folder/i).value).toBe("/Users/me/standups");
    act(() => reg.find("TextField", /folder/i).onInput("/Users/me/archive"));
    expect(jobs().at(-1)?.debounceMs).toBe(400);
    await settle();
    expect(lastSave(over)).toMatchObject({ id: 7, name: "Team folder", sendOn: "manual", config: { folder: "/Users/me/archive" } });
  });
});

describe("ExportSection: schema-rendered fields", () => {
  const demo = () => sink({ id: 20, kind: "demo", name: "Hook", config: { endpoint: "https://hooks.example/in", note: "n1", format: "json", pretty: "false" }, hasSecret: true });

  it("renders every schema field type generically", async () => {
    sinks = [demo()];
    api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    await waitFor(() => expect(reg.has("TextField", /endpoint/i)).toBe(true));
    const endpoint = reg.find("TextField", /endpoint/i);
    expect(endpoint.type).toBe("url");
    expect(endpoint.value).toBe("https://hooks.example/in");
    expect(reg.find("TextField", /note/i).value).toBe("n1");
    const fmt = reg.find("Combobox", /format/i);
    expect(fmt.options.map((o: any) => fmt.getLabel(o))).toEqual(["json", "ndjson"]);
    expect(fmt.getLabel(fmt.value)).toBe("json");
    const pretty = screen.queryByRole("switch", { name: /pretty print/i }) ?? screen.getByRole("checkbox", { name: /pretty print/i });
    expect(pretty).toBeTruthy();
    const token = reg.find("TextField", /token/i);
    expect(token.type).toBe("password");
    expect(token.value).toBe("");
  });

  it("a select change saves immediately with the chosen option", async () => {
    sinks = [demo()];
    const { over } = api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    await waitFor(() => expect(reg.has("Combobox", /format/i)).toBe(true));
    const fmt = reg.find("Combobox", /format/i);
    act(() => fmt.onSelect(fmt.options.find((o: any) => fmt.getLabel(o) === "ndjson")));
    await settle();
    expect(lastSave(over).config).toMatchObject({ format: "ndjson", endpoint: "https://hooks.example/in" });
  });

  it("a bool toggle saves 'true' / 'false' strings immediately", async () => {
    sinks = [demo()];
    const { over } = api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    await waitFor(() => expect(screen.queryByRole("switch", { name: /pretty print/i }) ?? screen.queryByRole("checkbox", { name: /pretty print/i })).toBeTruthy());
    const ctl = (screen.queryByRole("switch", { name: /pretty print/i }) ?? screen.getByRole("checkbox", { name: /pretty print/i })) as HTMLElement;
    act(() => { ctl.click(); });
    await settle();
    expect(lastSave(over).config).toMatchObject({ pretty: "true" });
  });

  it("editing a secret sends it once and edits to other fields send secret null", async () => {
    sinks = [demo()];
    const { over } = api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    await waitFor(() => expect(reg.has("TextField", /token/i)).toBe(true));
    act(() => reg.find("TextField", /token/i).onInput("s3cret"));
    expect(jobs().at(-1)?.debounceMs).toBe(400);
    await settle();
    expect(lastSave(over).secret).toBe("s3cret");
    act(() => reg.find("TextField", /note/i).onInput("n2"));
    await settle();
    expect(lastSave(over)).toMatchObject({ secret: null, config: { note: "n2" } });
  });

  it("never renders a stored secret value anywhere", async () => {
    sinks = [demo()];
    api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    await waitFor(() => expect(reg.has("TextField", /token/i)).toBe(true));
    expect(reg.find("TextField", /token/i).value).toBe("");
    expect(document.body.textContent).not.toMatch(/s3cret/);
  });
});

describe("ExportSection: send now, status and retry", () => {
  it("Send now sends the current standup to that destination", async () => {
    const { over } = api();
    render(<ExportSection config={mkConfig({ id: 1 })} columns={3} />);
    const btn = await screen.findByRole("button", { name: /send now/i });
    act(() => { btn.click(); });
    await waitFor(() => expect(over["sink.send"]).toHaveBeenCalledTimes(1));
    expect(over["sink.send"]).toHaveBeenCalledWith(7, 1, expect.objectContaining({ kind: "current" }));
  });

  it("shows 'Delivered 2 min ago · file name' for a delivered export and no Retry", async () => {
    sinks = [sink({ lastDelivery: outbox() })];
    api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    const line = await screen.findByText(/Delivered 2 min ago/);
    expect(line.textContent).toMatch(/Delivered 2 min ago\s*·\s*.*cuisle-MOB-2026-11-23\.json/);
    expect(screen.queryByRole("button", { name: /retry/i })).toBeNull();
  });

  it.each(["failed", "dead"])("shows a Failed line with a Retry button for a %s delivery, and Retry retries that outbox item", async (status) => {
    sinks = [sink({ lastDelivery: outbox({ status, error: "Disk full", errorCode: "sink_failed", receipt: "", deliveredAt: 0 }) })];
    const { over } = api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    expect(await screen.findByText(/Failed/)).toBeTruthy();
    expect(screen.queryByText(/Delivered/)).toBeNull();
    act(() => { screen.getByRole("button", { name: /retry/i }).click(); });
    await waitFor(() => expect(over["sink.retry"]).toHaveBeenCalledTimes(1));
    expect(over["sink.retry"]).toHaveBeenCalledWith(31);
  });

  it("shows neither delivery text nor Retry before the first delivery", async () => {
    sinks = [sink({ lastDelivery: null })];
    api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    expect(await screen.findByRole("button", { name: /send now/i })).toBeTruthy();
    expect(screen.queryByText(/Delivered|Failed/)).toBeNull();
    expect(screen.queryByRole("button", { name: /retry/i })).toBeNull();
  });

  it("re-reads the sink list when a sink:failed event arrives (no timers)", async () => {
    const list = vi.fn()
      .mockResolvedValueOnce([sink()])
      .mockResolvedValue([sink({ lastDelivery: outbox({ status: "failed", error: "Disk full", receipt: "", deliveredAt: 0 }) })]);
    api({ "sink.list": list });
    render(<ExportSection config={mkConfig()} columns={3} />);
    await screen.findByRole("button", { name: /send now/i });
    expect(screen.queryByText(/Failed/)).toBeNull();
    act(() => bus.emit("sink:failed", { outboxId: 31, sinkId: 7, standupId: 1, kind: "file", errorCode: "sink_failed", errorMessage: "Disk full", attempts: 3, willRetry: true }));
    expect(await screen.findByText(/Failed/)).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(2);
  });

  it("re-reads the sink list when a sink:delivered event arrives", async () => {
    const list = vi.fn()
      .mockResolvedValueOnce([sink()])
      .mockResolvedValue([sink({ lastDelivery: outbox() })]);
    api({ "sink.list": list });
    render(<ExportSection config={mkConfig()} columns={3} />);
    await screen.findByRole("button", { name: /send now/i });
    act(() => bus.emit("sink:delivered", { outboxId: 31, sinkId: 7, standupId: 1, kind: "file", ref: "cuisle-MOB-2026-11-23.json" }));
    expect(await screen.findByText(/Delivered 2 min ago/)).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(2);
  });
});

describe("ExportSection: delete", () => {
  it("removes the row and offers Undo in a toast", async () => {
    const { over } = api();
    render(<ExportSection config={mkConfig()} columns={3} />);
    const del = await screen.findByRole("button", { name: /delete|remove/i });
    act(() => { del.click(); });
    await waitFor(() => expect(mocks.showToast).toHaveBeenCalledTimes(1));
    const toast = mocks.showToast.mock.calls[0]![0];
    expect(toast.message).toContain("Team folder");
    expect(toast.action.label).toBe("Undo");
    expect(toast.action.run).toBeTypeOf("function");
    await waitFor(() => expect(screen.queryByRole("button", { name: /send now/i })).toBeNull());
    expect(over["sink.send"]).not.toHaveBeenCalled();
  });
});

describe("ExportSection: no native select", () => {
  it("renders no native select (every dropdown is the shared Combobox)", async () => {
    sinks = [sink({ id: 20, kind: "demo", name: "Hook", config: { format: "json" } })];
    const { container } = render((api(), <ExportSection config={mkConfig()} columns={3} />));
    await waitFor(() => expect(reg.has("Combobox", /format/i)).toBe(true));
    expect(container.querySelector("select")).toBeNull();
  });
});
