/* eslint-disable @typescript-eslint/no-explicit-any */
// Configure > Connection: the Disconnect confirm's "delete synced content" offer (ux-spec §14.11, ADR 0015, ADR 0011).
// Extends Connection.test.tsx (which stays as is and must keep passing).
//
// SPEC-NOTE (when the size is read): rendering makes no api call (Connection.test.tsx asserts that). The local content
//   size is read when the user clicks "Disconnect", i.e. when the inline confirm opens, through app.diagnostics()
//   (dbBytes) and app.mediaCache() (bytes + thumbBytes); content size = dbBytes + bytes + thumbBytes (the same sum as
//   the Storage section). The confirm itself (Confirm and Cancel buttons) appears AT ONCE and never waits for the
//   lookup; the offer is added when the sizes arrive. If either lookup fails or never answers, there is simply no
//   offer (the default, safe path), and Confirm calls disconnect(false).
// SPEC-NOTE (cap): the offer shows only when content size is STRICTLY greater than the cap, the app setting
//   storage.cleanup_offer_mb (MiB, so x * 1048576 bytes), default 2048. Exactly at the cap: no offer.
// SPEC-NOTE (offer): one Checkbox (role checkbox), UNCHECKED every time the confirm opens, whose accessible name
//   starts "Also delete synced content (<size>)", where size uses the Storage formatter (decimal, one decimal in GB:
//   "2.4 GB"). The text "History of status, assignee and flags is kept." is visible beside it (it may be part of the
//   checkbox label or its description). Under the cap the confirm shows no checkbox and no such text.
// SPEC-NOTE (call): Confirm calls connection.disconnect(deleteContent) exactly once, with the checkbox value:
//   false by default, true only when it was checked. Cancel closes the confirm, calls nothing, and a re-opened confirm
//   starts unchecked again.
import { act, fireEvent, render, screen, waitFor } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../../api";
import type { Connection, Diagnostics, MediaCacheInfo } from "../../../api/types";
import { connection, settings } from "../../../state/app";
import { ConnectionSection } from "./Connection";

vi.mock("../../connect/ConnectScreen", async () => {
  const { h } = await import("preact");
  return { ConnectScreen: () => h("div", { "data-testid": "connect-form" }) };
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

const conn = (over: Partial<Connection> = {}): Connection => ({
  state: "ok", source: "jira", baseUrl: "https://acme.atlassian.net", account: "me@acme.com", displayName: "Mock User", devActivity: "ok", capabilities: {} as any, ...over,
});
const diag = (dbBytes: number) => ({ build: {}, os: "darwin", dataDir: "/d", logDir: "/l", dbBytes, schemaVersion: 3, recentRuns: [] }) as unknown as Diagnostics;
const media = (bytes: number, thumbBytes = 0): MediaCacheInfo => ({ bytes, thumbBytes, files: 3, capMb: 1024 });

/** Sizes: [dbBytes, mediaBytes, thumbBytes]. */
function sizes(db: number, mb: number, thumb = 0) {
  return { "app.diagnostics": vi.fn(async () => diag(db)), "app.mediaCache": vi.fn(async () => media(mb, thumb)) };
}
const UNDER = sizes(600_000_000, 600_000_000, 40_000_000); // 1.24 GB < 2 GiB
const OVER = sizes(1_400_000_000, 1_000_000_000, 0); // 2.4 GB > 2 GiB

function setup(stubs: Record<string, (...a: any[]) => any>) {
  const all: Record<string, any> = {
    "connection.disconnect": vi.fn(async () => undefined),
    "connection.get": vi.fn(async () => conn({ state: "missing", baseUrl: "", account: "" })),
    ...stubs,
  };
  const fake = installFakeApi(all);
  render(<ConnectionSection columns={3} />);
  return { ...fake, stubs: all };
}
const openConfirm = () => act(() => { screen.getByRole("button", { name: /^disconnect$/i }).click(); });
const offer = () => screen.queryByRole("checkbox", { name: /^also delete synced content/i }) as HTMLInputElement | null;

beforeEach(() => {
  connection.value = conn();
  settings.value = {};
});

describe("Disconnect offer: reading the size", () => {
  it("reads nothing until Disconnect is clicked", () => {
    const { calls } = setup(OVER);
    expect(screen.getByRole("button", { name: /^disconnect$/i })).toBeTruthy();
    expect(calls).toEqual([]);
    openConfirm();
    expect(calls).toContain("app.diagnostics"); // the click is the trigger
    expect(calls).toContain("app.mediaCache");
  });

  it("reads the database and media sizes when the confirm opens, and shows the confirm at once", async () => {
    const { stubs } = setup(UNDER);
    openConfirm();
    expect(screen.getByRole("button", { name: /confirm/i })).toBeTruthy(); // synchronous, not awaiting the lookup
    await waitFor(() => {
      expect(stubs["app.diagnostics"]).toHaveBeenCalledTimes(1);
      expect(stubs["app.mediaCache"]).toHaveBeenCalledTimes(1);
    });
  });
});

describe("Disconnect offer: under the cap", () => {
  it("offers no deletion (and says nothing about it) once the sizes are known", async () => {
    const { stubs } = setup(UNDER);
    openConfirm();
    await waitFor(() => {
      expect(stubs["app.diagnostics"]).toHaveBeenCalled();
      expect(stubs["app.mediaCache"]).toHaveBeenCalled();
    });
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(offer()).toBeNull();
    expect(screen.queryByText(/delete synced content/i)).toBeNull();
    expect(screen.getByRole("button", { name: /confirm/i })).toBeTruthy();
  });

  it("disconnects with false", async () => {
    const { stubs } = setup(UNDER);
    openConfirm();
    await waitFor(() => expect(stubs["app.mediaCache"]).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));
    await waitFor(() => expect(stubs["connection.disconnect"]).toHaveBeenCalledTimes(1));
    expect(stubs["connection.disconnect"]).toHaveBeenCalledWith(false);
  });

  it("offers nothing when the content size is exactly the cap", async () => {
    const cap = 2048 * 1048576;
    const { stubs } = setup(sizes(cap - 1000, 1000, 0));
    openConfirm();
    await waitFor(() => expect(stubs["app.mediaCache"]).toHaveBeenCalled());
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(offer()).toBeNull();
  });
});

describe("Disconnect offer: over the cap", () => {
  it("shows one unchecked checkbox 'Also delete synced content (2.4 GB)' with the history reassurance", async () => {
    setup(OVER);
    openConfirm();
    const box = await screen.findByRole("checkbox", { name: /^also delete synced content \(2\.4 GB\)/i });
    expect((box as HTMLInputElement).checked).toBe(false);
    expect(screen.getAllByRole("checkbox")).toHaveLength(1);
    expect(document.body.textContent).toMatch(/History of status, assignee and flags is kept/);
    expect(screen.getByRole("button", { name: /confirm/i })).toBeTruthy();
  });

  it("disconnects with false by default", async () => {
    const { stubs } = setup(OVER);
    openConfirm();
    expect(await screen.findByRole("checkbox", { name: /^also delete synced content/i })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));
    await waitFor(() => expect(stubs["connection.disconnect"]).toHaveBeenCalledTimes(1));
    expect(stubs["connection.disconnect"]).toHaveBeenCalledWith(false);
  });

  it("disconnects with true when the box is checked, exactly once", async () => {
    const { stubs } = setup(OVER);
    openConfirm();
    fireEvent.click(await screen.findByRole("checkbox", { name: /^also delete synced content/i }));
    expect(offer()?.checked).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));
    await waitFor(() => expect(stubs["connection.disconnect"]).toHaveBeenCalledTimes(1));
    expect(stubs["connection.disconnect"]).toHaveBeenCalledWith(true);
  });

  it("calls nothing when only the box is toggled", async () => {
    const { stubs } = setup(OVER);
    openConfirm();
    fireEvent.click(await screen.findByRole("checkbox", { name: /^also delete synced content/i }));
    expect(stubs["connection.disconnect"]).not.toHaveBeenCalled();
  });

  it("Cancel hides the confirm without disconnecting, and a re-opened confirm starts unchecked", async () => {
    const { stubs } = setup(OVER);
    openConfirm();
    fireEvent.click(await screen.findByRole("checkbox", { name: /^also delete synced content/i }));
    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));
    expect(stubs["connection.disconnect"]).not.toHaveBeenCalled();
    expect(offer()).toBeNull();
    openConfirm();
    const again = await screen.findByRole("checkbox", { name: /^also delete synced content/i });
    expect((again as HTMLInputElement).checked).toBe(false);
  });

  it("honours a lower cap from storage.cleanup_offer_mb (1024 MB: 1.24 GB is over)", async () => {
    settings.value = { "storage.cleanup_offer_mb": "1024" };
    setup(UNDER);
    openConfirm();
    expect(await screen.findByRole("checkbox", { name: /^also delete synced content \(1\.2 GB\)/i })).toBeTruthy();
  });

  it("honours a higher cap (4096 MB: 2.4 GB is under, no offer)", async () => {
    settings.value = { "storage.cleanup_offer_mb": "4096" };
    const { stubs } = setup(OVER);
    openConfirm();
    await waitFor(() => expect(stubs["app.mediaCache"]).toHaveBeenCalled());
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(offer()).toBeNull();
  });
});

describe("Disconnect offer: lookup trouble never blocks disconnecting", () => {
  it("offers nothing when a size lookup fails, and Confirm disconnects with false", async () => {
    const { stubs } = setup({ ...OVER, "app.diagnostics": vi.fn(async () => { throw new Error("db busy"); }) });
    openConfirm();
    await waitFor(() => expect(stubs["app.diagnostics"]).toHaveBeenCalled());
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(offer()).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));
    await waitFor(() => expect(stubs["connection.disconnect"]).toHaveBeenCalledTimes(1));
    expect(stubs["connection.disconnect"]).toHaveBeenCalledWith(false);
  });

  it("Confirm works at once while the sizes are still pending, with false", async () => {
    const never = () => new Promise<never>(() => {});
    const { stubs } = setup({ "app.diagnostics": vi.fn(never), "app.mediaCache": vi.fn(never) });
    openConfirm();
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));
    await waitFor(() => expect(stubs["connection.disconnect"]).toHaveBeenCalledTimes(1));
    expect(stubs["connection.disconnect"]).toHaveBeenCalledWith(false);
    expect(stubs["app.diagnostics"]).toHaveBeenCalled(); // it did look, it just never answered
  });
});
