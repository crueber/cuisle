/* eslint-disable @typescript-eslint/no-explicit-any */
// Configure > Display section (app-wide; ux-spec §9.4 Display, §13; architecture §6.4 app_setting keys).
//
// SPEC-NOTE (approach): state/app functions are mocked via vi.mock("state/app") (the real `settings` signal is kept and
//   seeded directly); Segmented and Stepper are recording fakes (see Source.test.tsx for the rationale). Display
//   persists ONLY through state/app.ts setAppSetting(key, value) with STRING values and the app_setting keys:
//   "theme" light|dark|system, "text_size" standard|large, "sync_on_open" stale|always|never, "sync_stale_hours" 1-48.
// SPEC-NOTE: current values come from state/app `settings`; a missing key falls back to the documented default
//   (theme system, text_size standard, sync_on_open stale, sync_stale_hours 6).
// SPEC-NOTE: control labels are matched as /theme/i, /text size/i, /sync on open/i, /stale after/i. The "Stale after"
//   Stepper (min 1, max 48, suffix "h") exists ONLY while sync_on_open is "stale".
// SPEC-NOTE: Display is app-wide: it never touches state/config (no saveField) and makes no network calls.


import { act, render, screen } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../../api";
import { settings } from "../../../state/app";
import { DisplaySection } from "./Display";

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

const app = vi.hoisted(() => ({
  initFromBoot: vi.fn(), tabStandups: vi.fn(() => []), setAppSetting: vi.fn(), applyAppearance: vi.fn(), refreshStandups: vi.fn(async () => {}),
}));
vi.mock("../../../state/app", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../state/app")>()),
  ...app,
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

vi.mock("../../../components/Stepper", async () => {
  const { h } = await import("preact");
  return {
    Stepper: (p: any) => {
      reg.rec("Stepper", p);
      return h("input", { role: "spinbutton", "aria-label": p.label, value: p.value, readOnly: true });
    },
  };
});

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
  reg.clear();
  Object.values(st).forEach((f) => f.mockClear());
  Object.values(app).forEach((f) => f.mockClear());
  settings.value = { theme: "system", text_size: "standard", sync_on_open: "stale", sync_stale_hours: "6" };
  installFakeApi();
});

describe("DisplaySection: theme and text size", () => {
  it("offers Light / Dark / System with the stored theme", () => {
    settings.value = { ...settings.value, theme: "dark" };
    render(<DisplaySection columns={3} />);
    const seg = reg.find("Segmented", /theme/i);
    expect(seg.value).toBe("dark");
    expect(seg.options.map((o: any) => o.value)).toEqual(["light", "dark", "system"]);
    expect(seg.options.map((o: any) => o.label)).toEqual(["Light", "Dark", "System"]);
  });

  it("falls back to System when no theme is stored", () => {
    settings.value = {};
    render(<DisplaySection columns={3} />);
    expect(reg.find("Segmented", /theme/i).value).toBe("system");
  });

  it.each(["light", "dark", "system"])("choosing %s calls setAppSetting('theme', value) at once", (v) => {
    render(<DisplaySection columns={3} />);
    act(() => reg.find("Segmented", /theme/i).onChange(v));
    expect(app.setAppSetting).toHaveBeenCalledTimes(1);
    expect(app.setAppSetting).toHaveBeenCalledWith("theme", v);
  });

  it("offers Standard / Large (TV) with the stored text size", () => {
    settings.value = { ...settings.value, text_size: "large" };
    render(<DisplaySection columns={3} />);
    const seg = reg.find("Segmented", /text size/i);
    expect(seg.value).toBe("large");
    expect(seg.options.map((o: any) => o.value)).toEqual(["standard", "large"]);
    expect(seg.options[0].label).toBe("Standard");
    expect(seg.options[1].label).toMatch(/^Large/);
  });

  it.each(["standard", "large"])("choosing %s calls setAppSetting('text_size', value) at once", (v) => {
    render(<DisplaySection columns={3} />);
    act(() => reg.find("Segmented", /text size/i).onChange(v));
    expect(app.setAppSetting).toHaveBeenCalledTimes(1);
    expect(app.setAppSetting).toHaveBeenCalledWith("text_size", v);
  });
});

describe("DisplaySection: sync on open", () => {
  it("offers When stale / Always / Never with the stored policy", () => {
    settings.value = { ...settings.value, sync_on_open: "always" };
    render(<DisplaySection columns={3} />);
    const seg = reg.find("Segmented", /sync on open/i);
    expect(seg.value).toBe("always");
    expect(seg.options.map((o: any) => o.value)).toEqual(["stale", "always", "never"]);
    expect(seg.options.map((o: any) => o.label)).toEqual(["When stale", "Always", "Never"]);
  });

  it.each(["stale", "always", "never"])("choosing %s calls setAppSetting('sync_on_open', value)", (v) => {
    settings.value = { ...settings.value, sync_on_open: v === "stale" ? "never" : "stale" };
    render(<DisplaySection columns={3} />);
    act(() => reg.find("Segmented", /sync on open/i).onChange(v));
    expect(app.setAppSetting).toHaveBeenCalledTimes(1);
    expect(app.setAppSetting).toHaveBeenCalledWith("sync_on_open", v);
  });

  it("shows 'Stale after' in hours (1-48) when the policy is When stale", () => {
    settings.value = { ...settings.value, sync_on_open: "stale", sync_stale_hours: "12" };
    render(<DisplaySection columns={3} />);
    const s = reg.find("Stepper", /stale after/i);
    expect(s.value).toBe(12);
    expect(s.min).toBe(1);
    expect(s.max).toBe(48);
    expect(s.suffix).toBe("h");
  });

  it("defaults 'Stale after' to 6 hours when unset", () => {
    settings.value = { sync_on_open: "stale" };
    render(<DisplaySection columns={3} />);
    expect(reg.find("Stepper", /stale after/i).value).toBe(6);
  });

  it("autosaves 'Stale after' as a string setting immediately", () => {
    render(<DisplaySection columns={3} />);
    act(() => reg.find("Stepper", /stale after/i).onChange(24));
    expect(app.setAppSetting).toHaveBeenCalledTimes(1);
    expect(app.setAppSetting).toHaveBeenCalledWith("sync_stale_hours", "24");
  });

  it.each(["always", "never"])("hides 'Stale after' when the policy is %s", (policy) => {
    settings.value = { ...settings.value, sync_on_open: policy };
    render(<DisplaySection columns={3} />);
    expect(reg.has("Segmented", /sync on open/i)).toBe(true);
    expect(reg.has("Stepper", /stale after/i)).toBe(false);
  });
});

describe("DisplaySection: app-wide", () => {
  it("never writes standup config and makes no network calls", () => {
    const { calls } = installFakeApi();
    render(<DisplaySection columns={3} />);
    act(() => reg.find("Segmented", /theme/i).onChange("light"));
    expect(app.setAppSetting).toHaveBeenCalledTimes(1);
    expect(st.saveField).not.toHaveBeenCalled();
    expect(calls).toEqual([]);
  });

  it("shows the heading 'Display'", () => {
    render(<DisplaySection columns={1} />);
    expect(screen.getByRole("heading", { name: /^Display/i })).toBeTruthy();
  });
});
