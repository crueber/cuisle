// WP-38F: site normalisation keeps http:// for loopback hosts (the plain-http fake Jira) and nothing else.
import { render, screen, waitFor } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { setApi, type Api } from "../../api";
import type { Connection } from "../../api/types";
import { connection } from "../../state/app";
import { ConnectScreen } from "./ConnectScreen";

vi.mock("../../state/config", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../state/config")>()), createStandup: vi.fn(async () => undefined) }));
vi.mock("../../state/route", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../state/route")>()), goConfigure: vi.fn() }));

const BASE = { state: "ok", source: "jira", account: "me@acme.com", displayName: "M", devActivity: "ok", capabilities: {} } as unknown as Connection;
let connect: Mock<(...a: unknown[]) => Promise<Connection>>;

beforeEach(() => {
  connect = vi.fn(async () => BASE);
  setApi(new Proxy({}, {
    get: (_t, svc: string) => new Proxy({}, {
      get: (_s, m: string) => (...a: unknown[]) => (svc === "connection" && m === "connect" ? connect(...a) : Promise.reject(new Error(`unstubbed ${svc}.${m}`))),
    }),
  }) as unknown as Api);
});

async function submitSite(typed: string): Promise<string> {
  const user = userEvent.setup();
  render(<ConnectScreen />);
  await user.type(screen.getByLabelText(/jira site/i), typed.replace(/[[{]/g, (c) => c + c)); // user-event: "[[" types "["
  await user.type(screen.getByLabelText(/email/i), "me@acme.com");
  await user.type(screen.getByLabelText(/api token/i), "tok");
  await user.click(screen.getByRole("button", { name: /^connect$/i }));
  await waitFor(() => expect(connect).toHaveBeenCalledTimes(1));
  return (connect.mock.calls[0]![0] as { baseUrl: string }).baseUrl;
}

describe("ConnectScreen: loopback sites", () => {
  it.each([
    ["http://127.0.0.1:5000/", "http://127.0.0.1:5000"],
    ["127.0.0.1:5000", "http://127.0.0.1:5000"],
    ["localhost:8080", "http://localhost:8080"],
    ["http://[::1]:9", "http://[::1]:9"],
    ["https://127.0.0.1:5000", "https://127.0.0.1:5000"],
    ["HTTP://Foo.Atlassian.net/", "https://foo.atlassian.net"],
    ["http://acme.atlassian.net", "https://acme.atlassian.net"],
    ["acme.atlassian.net", "https://acme.atlassian.net"],
  ])("normalises %j to %j", async (typed, expected) => {
    expect(await submitSite(typed)).toBe(expected);
  });

  it("round-trips a stored loopback http base URL through the reconnect prefill", async () => {
    connection.value = { ...BASE, baseUrl: "http://127.0.0.1:5000", state: "invalid" } as Connection;
    const user = userEvent.setup();
    render(<ConnectScreen reconnect />);
    expect((screen.getByLabelText(/jira site/i) as HTMLInputElement).value).toBe("http://127.0.0.1:5000");
    await user.type(screen.getByLabelText(/api token/i), "tok");
    await user.click(screen.getByRole("button", { name: /^connect$/i }));
    await waitFor(() => expect(connect).toHaveBeenCalledTimes(1));
    expect((connect.mock.calls[0]![0] as { baseUrl: string }).baseUrl).toBe("http://127.0.0.1:5000");
  });

  it("prefills a normal https site as the bare host", () => {
    connection.value = { ...BASE, baseUrl: "https://acme.atlassian.net", state: "invalid" } as Connection;
    render(<ConnectScreen reconnect />);
    expect((screen.getByLabelText(/jira site/i) as HTMLInputElement).value).toBe("acme.atlassian.net");
  });
});
