/* eslint-disable @typescript-eslint/no-explicit-any */
// Configure > Connection section (app-wide; ux-spec §9.4 Connection, product-spec F1).
//
// SPEC-NOTE (approach): the section reads the real `connection` signal (state/app, seeded directly) and talks to the
//   facade for Disconnect (fake Api via setApi, since there is no state wrapper). The inline Connect form is a
//   recording fake of views/connect/ConnectScreen so only its props are asserted.
// SPEC-NOTE: shows the site (baseUrl or its host), the account email, and a tag with the exact text "Connected" while
//   connection.state === "ok" (other states: no "Connected" tag).
// SPEC-NOTE: "Reconnect" (button) mounts the inline form `<ConnectScreen reconnect />` (imported from
//   "../../connect/ConnectScreen"); the form is absent until clicked. The standalone card is not wrapped by the section.
// SPEC-NOTE: Disconnect is a two-step inline confirm (no window.confirm: webviews block native dialogs): clicking
//   "Disconnect" calls nothing yet and reveals a button whose name matches /confirm/i plus a "Cancel" button;
//   Confirm calls connection.disconnect() exactly once, Cancel calls nothing and hides the confirmation.


import { act, fireEvent, render, screen, waitFor } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../../api";
import type { Connection } from "../../../api/types";
import { connection } from "../../../state/app";
import { ConnectionSection } from "./Connection";

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


vi.mock("../../connect/ConnectScreen", async () => {
  const { h } = await import("preact");
  return {
    ConnectScreen: (p: any) => {
      reg.rec("ConnectScreen", { ...p, label: "connect" });
      return h("div", { "data-testid": "connect-form", "data-reconnect": String(!!p.reconnect) });
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

const conn = (over: Partial<Connection> = {}): Connection => ({
  state: "ok", source: "jira", baseUrl: "https://acme.atlassian.net", account: "me@acme.com", displayName: "Morgan Ellis", devActivity: "ok", capabilities: {} as any, ...over,
});

beforeEach(() => {
  reg.clear();
  connection.value = conn();
});

describe("ConnectionSection: status", () => {
  it("shows the site, the account email and a Connected tag", () => {
    installFakeApi();
    render(<ConnectionSection columns={3} />);
    expect(screen.getByRole("heading", { name: /^Connection/i })).toBeTruthy();
    expect(screen.getByText(/acme\.atlassian\.net/)).toBeTruthy();
    expect(screen.getByText(/me@acme\.com/)).toBeTruthy();
    expect(screen.getByText("Connected")).toBeTruthy();
  });

  it("does not show a Connected tag when the token was rejected, and still offers Reconnect", () => {
    installFakeApi();
    connection.value = conn({ state: "invalid" });
    render(<ConnectionSection columns={3} />);
    expect(screen.getByRole("button", { name: /reconnect/i })).toBeTruthy();
    expect(screen.queryByText("Connected")).toBeNull();
  });

  it("makes no network call just by rendering", () => {
    const { calls } = installFakeApi();
    render(<ConnectionSection columns={3} />);
    expect(screen.getByRole("button", { name: /reconnect/i })).toBeTruthy();
    expect(calls).toEqual([]);
  });
});

describe("ConnectionSection: reconnect", () => {
  it("opens the Connect form inline in reconnect mode when Reconnect is clicked", () => {
    installFakeApi();
    render(<ConnectionSection columns={3} />);
    expect(screen.getByRole("button", { name: /reconnect/i })).toBeTruthy();
    expect(screen.queryByTestId("connect-form")).toBeNull();
    act(() => { screen.getByRole("button", { name: /reconnect/i }).click(); });
    expect(screen.getByTestId("connect-form").getAttribute("data-reconnect")).toBe("true");
    expect(reg.find("ConnectScreen", /connect/).reconnect).toBe(true);
  });
});

describe("ConnectionSection: disconnect", () => {
  it("asks for confirmation before disconnecting", () => {
    const { over } = installFakeApi({ "connection.disconnect": vi.fn(async () => undefined) });
    render(<ConnectionSection columns={3} />);
    act(() => { screen.getByRole("button", { name: /^disconnect$/i }).click(); });
    expect(over["connection.disconnect"]).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /confirm/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /cancel/i })).toBeTruthy();
  });

  it("disconnects exactly once after Confirm", async () => {
    const { over } = installFakeApi({ "connection.disconnect": vi.fn(async () => undefined), "connection.get": vi.fn(async () => conn({ state: "missing", baseUrl: "", account: "" })) });
    render(<ConnectionSection columns={3} />);
    act(() => { screen.getByRole("button", { name: /^disconnect$/i }).click(); });
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));
    await waitFor(() => expect(over["connection.disconnect"]).toHaveBeenCalledTimes(1));
    expect(over["connection.disconnect"]).toHaveBeenCalledWith();
  });

  it("Cancel hides the confirmation and disconnects nothing", () => {
    const { over } = installFakeApi({ "connection.disconnect": vi.fn(async () => undefined) });
    render(<ConnectionSection columns={3} />);
    act(() => { screen.getByRole("button", { name: /^disconnect$/i }).click(); });
    act(() => { screen.getByRole("button", { name: /cancel/i }).click(); });
    expect(over["connection.disconnect"]).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: /confirm/i })).toBeNull();
    expect(screen.getByRole("button", { name: /^disconnect$/i })).toBeTruthy();
  });
});
