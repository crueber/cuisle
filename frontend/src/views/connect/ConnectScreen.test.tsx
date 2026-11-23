/* eslint-disable @typescript-eslint/no-explicit-any */
// ConnectScreen: first-run Connect card and the reconnect variant (ux-spec §9.7; product-spec F1; architecture R4).
//
// SPEC-NOTE (approach): REAL shared components (TextField etc.) render; the facade is a fake Api (setApi) whose
//   connection.connect is a vi.fn; state/config (createStandup) and state/route (goConfigure/navigate) are mocked with
//   vi.fn (real signals kept, notably state/app `connection`). Never calls WP-15 stubs that throw.
// SPEC-NOTE: fields are found by label (getByLabelText): /jira site/i, /email/i, /api token/i (type="password"). The site
//   field has placeholder "your-site.atlassian.net". The link "Create an API token ↗" (role link, name /create an api
//   token/i) points at https://id.atlassian.com/manage-profile/security/api-tokens. The note "Classic API tokens only.
//   Stored in your system keychain." is one text node. The wordmark text is "CUISLE". The primary button is named
//   "Connect"; all fields live in one <form> (Enter submits).
// SPEC-NOTE: submit calls connection.connect({ source: "jira", baseUrl, account, secret }) once. baseUrl is normalised:
//   trimmed, "https://" prepended when no scheme is typed, trailing slashes removed (acme.atlassian.net and
//   "https://acme.atlassian.net/" both become "https://acme.atlassian.net"). account is trimmed.
// SPEC-NOTE: client-side required check: with an empty site, email or token nothing is sent and the empty field(s) get
//   aria-invalid="true" (with an inline message).
// SPEC-NOTE: while the call is pending the Connect button is disabled and a second activation sends nothing.
// SPEC-NOTE: failure (AppError) shows err.message inline under the field named by AppError.field ("baseUrl" -> site,
//   "account" -> email, "secret" -> token: that input gets aria-invalid="true"); with an empty/unknown field the message is
//   still visible (e.g. keychain_unavailable, offline) and no input is flagged. The typed values are kept and the button is
//   enabled again.
// SPEC-NOTE: success (first run) stores the returned Connection in state/app `connection`, calls createStandup() and then
//   navigates to Configure (goConfigure(...) or navigate({view:"configure"...}), after createStandup). Success in
//   `reconnect` mode stores the Connection and does NOT create a standup. `reconnect` prefills site and email from the
//   current `connection` signal and leaves the token empty.

import { render, screen, waitFor } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { AppError, setApi, type Api } from "../../api";
import type { Connection } from "../../api/types";
import { connection } from "../../state/app";
import { ConnectScreen } from "./ConnectScreen";

const st = vi.hoisted(() => ({
  loadConfig: vi.fn(), saveField: vi.fn(), createStandup: vi.fn(async () => undefined), duplicateStandup: vi.fn(), deleteStandup: vi.fn(),
  moveStandup: vi.fn(), leaveConfigure: vi.fn(),
}));
const rt = vi.hoisted(() => ({ goConfigure: vi.fn(), goStandup: vi.fn(), goConnect: vi.fn(), navigate: vi.fn() }));
vi.mock("../../state/config", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../state/config")>()), ...st }));
vi.mock("../../state/route", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../state/route")>()), ...rt }));

const OK: Connection = { state: "ok", source: "jira", baseUrl: "https://acme.atlassian.net", account: "me@acme.com", displayName: "Morgan Ellis", devActivity: "ok", capabilities: {} as any };
let connect: Mock<(...a: any[]) => any>;

function install(impl?: (...a: any[]) => any) {
  connect = vi.fn(impl ?? (async () => OK));
  setApi(new Proxy({}, {
    get: (_t, svc: string) => new Proxy({}, {
      get: (_s, m: string) => (...a: unknown[]) => (svc === "connection" && m === "connect" ? connect(...a) : Promise.reject(new Error(`unstubbed ${svc}.${m}`))),
    }),
  }) as unknown as Api);
}
const site = () => screen.getByLabelText(/jira site/i) as HTMLInputElement;
const email = () => screen.getByLabelText(/email/i) as HTMLInputElement;
const token = () => screen.getByLabelText(/api token/i) as HTMLInputElement;
const submit = () => screen.getByRole("button", { name: /^connect(ing…?)?$/i }) as HTMLButtonElement;

async function fill(user: ReturnType<typeof userEvent.setup>, s = "acme.atlassian.net", e = "me@acme.com", t = "tok-123") {
  if (s) await user.type(site(), s);
  if (e) await user.type(email(), e);
  if (t) await user.type(token(), t);
}
const navigatedToConfigure = () =>
  rt.goConfigure.mock.calls.length + rt.navigate.mock.calls.filter((c) => (c[0] as any)?.view === "configure").length;

beforeEach(() => {
  Object.values(st).forEach((f) => f.mockClear());
  Object.values(rt).forEach((f) => f.mockReset());
  st.createStandup.mockReset();
  st.createStandup.mockImplementation(async () => undefined);
  connection.value = null;
  install();
});

describe("ConnectScreen: content", () => {
  it("shows the wordmark, the three fields, the connect button and the token note", () => {
    render(<ConnectScreen />);
    expect(screen.getByText("CUISLE")).toBeTruthy();
    expect(site()).toBeTruthy();
    expect(email()).toBeTruthy();
    expect(token().type).toBe("password");
    expect(submit()).toBeTruthy();
    const note = screen.getByText(/Classic API tokens only/i);
    expect(note.textContent).toMatch(/keychain/i);
  });

  it("uses the your-site.atlassian.net placeholder and starts empty", () => {
    render(<ConnectScreen />);
    expect(screen.getByPlaceholderText("your-site.atlassian.net")).toBeTruthy();
    expect([site().value, email().value, token().value]).toEqual(["", "", ""]);
  });

  it("links to Atlassian's API token page", () => {
    render(<ConnectScreen />);
    const link = screen.getByRole("link", { name: /create an api token/i }) as HTMLAnchorElement;
    expect(link.getAttribute("href")).toBe("https://id.atlassian.com/manage-profile/security/api-tokens");
  });

  it("has no axe violations", async () => {
    const { container } = render(<ConnectScreen />);
    expect(submit()).toBeTruthy();
    const results = await axe.run(container, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"] },
      rules: { "color-contrast": { enabled: false } },
    });
    expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)).toEqual([]);
  });

  it("makes no call until the user submits", () => {
    render(<ConnectScreen />);
    expect(submit()).toBeTruthy();
    expect(connect).not.toHaveBeenCalled();
  });
});

describe("ConnectScreen: submit", () => {
  it.each([
    ["acme.atlassian.net", "https://acme.atlassian.net"],
    ["https://acme.atlassian.net", "https://acme.atlassian.net"],
    ["https://acme.atlassian.net/", "https://acme.atlassian.net"],
    ["  acme.atlassian.net/  ", "https://acme.atlassian.net"],
  ])("normalises the site %j to %j", async (typed, expected) => {
    const user = userEvent.setup();
    render(<ConnectScreen />);
    await fill(user, typed);
    await user.click(submit());
    await waitFor(() => expect(connect).toHaveBeenCalledTimes(1));
    expect(connect).toHaveBeenCalledWith({ source: "jira", baseUrl: expected, account: "me@acme.com", secret: "tok-123" });
  });

  it("trims the email", async () => {
    const user = userEvent.setup();
    render(<ConnectScreen />);
    await fill(user, "acme.atlassian.net", "  me@acme.com ");
    await user.click(submit());
    await waitFor(() => expect(connect).toHaveBeenCalledTimes(1));
    expect(connect.mock.calls[0]?.[0].account).toBe("me@acme.com");
  });

  it("Enter in the token field submits once", async () => {
    const user = userEvent.setup();
    render(<ConnectScreen />);
    await fill(user);
    await user.type(token(), "{Enter}");
    await waitFor(() => expect(connect).toHaveBeenCalledTimes(1));
  });

  it("does not send anything with empty fields and flags the empty ones", async () => {
    const user = userEvent.setup();
    render(<ConnectScreen />);
    await user.type(site(), "acme.atlassian.net");
    await user.click(submit());
    expect(connect).not.toHaveBeenCalled();
    expect(email().getAttribute("aria-invalid")).toBe("true");
    expect(token().getAttribute("aria-invalid")).toBe("true");
    expect(site().getAttribute("aria-invalid")).not.toBe("true");
  });

  it("disables the button while pending and ignores a second activation", async () => {
    install(() => new Promise(() => {}));
    const user = userEvent.setup();
    render(<ConnectScreen />);
    await fill(user);
    await user.click(submit());
    await waitFor(() => expect(submit().disabled).toBe(true));
    await user.click(submit());
    expect(connect).toHaveBeenCalledTimes(1);
  });
});

describe("ConnectScreen: errors", () => {
  const MSG = "Jira rejected the token — check the email and API token.";

  it("shows the message under the token field and flags it when the error names the secret", async () => {
    install(async () => { throw new AppError("auth_invalid", MSG, "", false, "secret"); });
    const user = userEvent.setup();
    render(<ConnectScreen />);
    await fill(user);
    await user.click(submit());
    expect(await screen.findByText(MSG)).toBeTruthy();
    expect(token().getAttribute("aria-invalid")).toBe("true");
    expect(site().getAttribute("aria-invalid")).not.toBe("true");
    expect(email().getAttribute("aria-invalid")).not.toBe("true");
    const describedBy = token().getAttribute("aria-describedby") ?? "";
    expect(describedBy.split(/\s+/).some((id) => document.getElementById(id)?.textContent?.includes(MSG))).toBe(true);
  });

  it.each([
    ["baseUrl", "Jira site not found: acme.atlassian.net", () => site()],
    ["account", "That email is not a member of this Jira site.", () => email()],
  ] as const)("flags the %s field when the error names it", async (fieldName, message, el) => {
    install(async () => { throw new AppError("validation", message, "", false, fieldName); });
    const user = userEvent.setup();
    render(<ConnectScreen />);
    await fill(user);
    await user.click(submit());
    expect(await screen.findByText(message)).toBeTruthy();
    expect(el().getAttribute("aria-invalid")).toBe("true");
  });

  it.each([
    ["keychain_unavailable", "No Secret Service is running, so Cuisle cannot store the token. Start gnome-keyring or KWallet."],
    ["offline", "Can't reach acme.atlassian.net. Check your connection and try again."],
  ] as const)("still shows a %s message that names no field, without crashing", async (code, message) => {
    install(async () => { throw new AppError(code, message); });
    const user = userEvent.setup();
    render(<ConnectScreen />);
    await fill(user);
    await user.click(submit());
    expect(await screen.findByText(message)).toBeTruthy();
    expect([site(), email(), token()].filter((e) => e.getAttribute("aria-invalid") === "true")).toHaveLength(0);
  });

  it("keeps the typed values and re-enables Connect after a failure", async () => {
    install(async () => { throw new AppError("auth_invalid", MSG, "", false, "secret"); });
    const user = userEvent.setup();
    render(<ConnectScreen />);
    await fill(user);
    await user.click(submit());
    await screen.findByText(MSG);
    expect(site().value).toMatch(/acme\.atlassian\.net/);
    expect(email().value).toBe("me@acme.com");
    expect(token().value).toBe("tok-123");
    expect(submit().disabled).toBe(false);
  });

  it("does not store a connection or create a standup after a failure", async () => {
    install(async () => { throw new AppError("auth_invalid", MSG, "", false, "secret"); });
    const user = userEvent.setup();
    render(<ConnectScreen />);
    await fill(user);
    await user.click(submit());
    await screen.findByText(MSG);
    expect(connection.value).toBeNull();
    expect(st.createStandup).not.toHaveBeenCalled();
    expect(navigatedToConfigure()).toBe(0);
  });
});

describe("ConnectScreen: success", () => {
  it("stores the connection, creates a standup and goes to Configure (in that order)", async () => {
    const user = userEvent.setup();
    render(<ConnectScreen />);
    await fill(user);
    await user.click(submit());
    await waitFor(() => expect(navigatedToConfigure()).toBeGreaterThan(0));
    expect(connection.value).toEqual(OK);
    expect(st.createStandup).toHaveBeenCalledTimes(1);
    const navOrder = Math.min(
      ...rt.goConfigure.mock.invocationCallOrder,
      ...rt.navigate.mock.calls.map((c, i) => ((c[0] as any)?.view === "configure" ? rt.navigate.mock.invocationCallOrder[i]! : Infinity)),
    );
    expect(st.createStandup.mock.invocationCallOrder[0]!).toBeLessThan(navOrder);
  });
});

describe("ConnectScreen: reconnect", () => {
  it("prefills the site and email from the current connection and leaves the token empty", () => {
    connection.value = { ...OK, state: "invalid" };
    render(<ConnectScreen reconnect />);
    expect(site().value).toMatch(/acme\.atlassian\.net/);
    expect(email().value).toBe("me@acme.com");
    expect(token().value).toBe("");
    expect(token().type).toBe("password");
  });

  it("connects with the prefilled site and email plus the new token", async () => {
    connection.value = { ...OK, state: "invalid" };
    const user = userEvent.setup();
    render(<ConnectScreen reconnect />);
    await user.type(token(), "fresh-token");
    await user.click(submit());
    await waitFor(() => expect(connect).toHaveBeenCalledTimes(1));
    expect(connect).toHaveBeenCalledWith({ source: "jira", baseUrl: "https://acme.atlassian.net", account: "me@acme.com", secret: "fresh-token" });
  });

  it("stores the new connection but does not create a standup", async () => {
    connection.value = { ...OK, state: "invalid" };
    const user = userEvent.setup();
    render(<ConnectScreen reconnect />);
    await user.type(token(), "fresh-token");
    await user.click(submit());
    await waitFor(() => expect(connection.value?.state).toBe("ok"));
    expect(st.createStandup).not.toHaveBeenCalled();
  });

  it("without reconnect, a stale connection is not prefilled", () => {
    connection.value = { ...OK, state: "missing" };
    render(<ConnectScreen />);
    expect(site().value).toBe("");
    expect(email().value).toBe("");
  });
});
