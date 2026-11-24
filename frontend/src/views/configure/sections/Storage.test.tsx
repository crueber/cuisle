/* eslint-disable @typescript-eslint/no-explicit-any */
// Configure > Display > Storage section (ux-spec §14.11; architecture AppService MediaCache / ClearMediaCache).
// Exports StorageSection (AppSectionProps). It is its OWN section component (Display.tsx does not render it, so
// Display.test.tsx's "no network calls" stays true); WP-T9 places it under Display.
//
// SPEC-NOTE (approach): as in Display.test.tsx, state/app's setAppSetting is replaced with vi.fn (the real `settings`
//   signal is seeded directly). Api calls use a fake Api that rejects unstubbed methods. The real Stepper is used.
// SPEC-NOTE (load): on mount it calls app.diagnostics() and app.mediaCache() once each and nothing else (no Jira
//   traffic, no connection or standup call).
// SPEC-NOTE (size): local content size = diagnostics().dbBytes + mediaCache().bytes + mediaCache().thumbBytes (the
//   database plus the whole media cache, thumbnails included); "images" = bytes + thumbBytes. The line reads
//   "<total> · images <images>", e.g. "1.2 GB · images 640 MB".
// SPEC-NOTE (units): sizes are decimal (1 KB = 1000 B, 1 MB = 1000 KB, 1 GB = 1000 MB, matching the mockup's
//   "412 KB"): >= 1 GB shows one decimal ("1.2 GB", "2.4 GB"); below that whole MB ("640 MB"). Values below 100 MB are not
//   asserted except the two exact multiples of ten used here ("50 MB", "650 MB").
//   The cleanup cap, however, is stored in MEBIBYTES like every *_mb setting (Go uses << 20): its GB field converts
//   with 1 GB = 1024 MB, so the default 2048 shows as 2.
// SPEC-NOTE (clear): "Clear image cache" (a button) calls app.clearMediaCache() once with no confirmation (the images
//   re-download on demand, nothing is lost) and shows the returned info at once: images and total are recomputed
//   from it (dbBytes stays what diagnostics reported). A rejection shows a role="alert" and leaves the sizes as they were.
// SPEC-NOTE (cap): a Stepper labelled /cleanup offer above/i, value in whole GB (default 2 when the setting
//   storage.cleanup_offer_mb is unset or not a number), suffix containing "GB", step 1. Every change autosaves at once
//   through state/app setAppSetting("storage.cleanup_offer_mb", String(gb * 1024)). No Save button.
import { act, fireEvent, render, screen, waitFor } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../../api";
import type { Diagnostics, MediaCacheInfo } from "../../../api/types";
import { settings } from "../../../state/app";
import { StorageSection } from "./Storage";

const app = vi.hoisted(() => ({
  initFromBoot: vi.fn(), tabStandups: vi.fn(() => []), setAppSetting: vi.fn(), applyAppearance: vi.fn(), refreshStandups: vi.fn(async () => {}),
}));
vi.mock("../../../state/app", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../state/app")>()),
  ...app,
}));

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

const diag = (dbBytes: number) => ({ build: {}, os: "darwin", dataDir: "/d", logDir: "/l", dbBytes, schemaVersion: 3, recentRuns: [] }) as unknown as Diagnostics;
const media = (bytes: number, thumbBytes: number): MediaCacheInfo => ({ bytes, thumbBytes, files: 12, capMb: 1024 });

async function mountLoaded(over: Record<string, (...a: any[]) => any> = {}) {
  const stubs: Record<string, any> = {
    "app.diagnostics": vi.fn(async () => diag(600_000_000)),
    "app.mediaCache": vi.fn(async () => media(600_000_000, 40_000_000)),
    ...over,
  };
  const fake = installFakeApi(stubs);
  const view = render(<StorageSection columns={3} />);
  await screen.findByRole("heading", { name: /^storage/i });
  await waitFor(() => expect(document.body.textContent).toMatch(/images/i));
  return { ...fake, stubs, ...view };
}
const text = () => (document.body.textContent ?? "").replace(/\s+/g, " ");

beforeEach(() => {
  Object.values(app).forEach((f) => f.mockClear());
  settings.value = {};
});

describe("StorageSection: size", () => {
  it("loads diagnostics and the media cache once each and calls nothing else", async () => {
    const { calls } = await mountLoaded();
    expect(calls.slice().sort()).toEqual(["app.diagnostics", "app.mediaCache"]);
  });

  it("shows the database plus media total and the images part: 1.2 GB · images 640 MB", async () => {
    await mountLoaded();
    expect(text()).toMatch(/1\.2 GB\s*·\s*images 640 MB/i);
  });

  it("counts the thumbnails in both the total and images", async () => {
    await mountLoaded({ "app.mediaCache": vi.fn(async () => media(0, 650_000_000)), "app.diagnostics": vi.fn(async () => diag(1_750_000_000)) });
    expect(text()).toMatch(/2\.4 GB\s*·\s*images 650 MB/i);
  });
});

describe("StorageSection: Clear image cache", () => {
  it("calls clearMediaCache once and shows the returned sizes", async () => {
    const clear = vi.fn(async () => media(0, 50_000_000));
    await mountLoaded({ "app.clearMediaCache": clear });
    fireEvent.click(screen.getByRole("button", { name: /clear image cache/i }));
    expect(clear).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(text()).toMatch(/images 50 MB/i));
    expect(text()).not.toMatch(/images 640 MB/i);
    expect(text()).toMatch(/650 MB\s*·\s*images 50 MB/i);
  });

  it("does not clear anything until the button is pressed", async () => {
    const clear = vi.fn(async () => media(0, 0));
    await mountLoaded({ "app.clearMediaCache": clear });
    expect(screen.getByRole("button", { name: /clear image cache/i })).toBeTruthy();
    expect(clear).not.toHaveBeenCalled();
  });

  it("alerts and keeps the old sizes when clearing fails", async () => {
    await mountLoaded({ "app.clearMediaCache": vi.fn(async () => { throw new Error("disk says no (secret)"); }) });
    fireEvent.click(screen.getByRole("button", { name: /clear image cache/i }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(text()).not.toMatch(/disk says no/);
    expect(text()).toMatch(/images 640 MB/i);
  });
});

describe("StorageSection: cleanup offer cap", () => {
  it("shows the default 2048 MB as 2 GB", async () => {
    await mountLoaded();
    const s = screen.getByRole("spinbutton", { name: /cleanup offer above/i });
    expect(s.getAttribute("aria-valuenow")).toBe("2");
    expect(s.textContent).toMatch(/2\s*GB/);
  });

  it("shows the stored cap in GB", async () => {
    settings.value = { "storage.cleanup_offer_mb": "4096" };
    await mountLoaded();
    expect(screen.getByRole("spinbutton", { name: /cleanup offer above/i }).getAttribute("aria-valuenow")).toBe("4");
  });

  it("falls back to 2 GB when the stored value is not a number", async () => {
    settings.value = { "storage.cleanup_offer_mb": "lots" };
    await mountLoaded();
    expect(screen.getByRole("spinbutton", { name: /cleanup offer above/i }).getAttribute("aria-valuenow")).toBe("2");
  });

  it("autosaves an increase as megabytes at once", async () => {
    await mountLoaded();
    fireEvent.click(screen.getByRole("button", { name: "Increase" }));
    expect(app.setAppSetting).toHaveBeenCalledTimes(1);
    expect(app.setAppSetting).toHaveBeenCalledWith("storage.cleanup_offer_mb", "3072");
  });

  it("autosaves a decrease as megabytes at once", async () => {
    await mountLoaded();
    fireEvent.click(screen.getByRole("button", { name: "Decrease" }));
    expect(app.setAppSetting).toHaveBeenCalledTimes(1);
    expect(app.setAppSetting).toHaveBeenCalledWith("storage.cleanup_offer_mb", "1024");
  });

  it("autosaves from the keyboard too", async () => {
    await mountLoaded();
    fireEvent.keyDown(screen.getByRole("spinbutton", { name: /cleanup offer above/i }), { key: "ArrowUp" });
    expect(app.setAppSetting).toHaveBeenCalledWith("storage.cleanup_offer_mb", "3072");
  });

  it("has no Save button and writes no setting on load", async () => {
    await mountLoaded();
    expect(screen.queryByRole("button", { name: /^save/i })).toBeNull();
    expect(app.setAppSetting).not.toHaveBeenCalled();
  });

  it("never touches the network beyond the two local reads (no Jira, no standup)", async () => {
    const { calls } = await mountLoaded();
    act(() => { fireEvent.click(screen.getByRole("button", { name: "Increase" })); });
    expect(calls.every((c) => c === "app.diagnostics" || c === "app.mediaCache")).toBe(true);
  });
});
