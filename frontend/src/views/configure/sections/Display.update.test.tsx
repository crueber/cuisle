// Configure > Display > Updates (ADR 0016): the "Check for updates daily" switch (app setting update.check_daily,
// on by default, autosaved), and Check now with the current version and the last-check time. Builds without a
// channel show only the version.
import { act, cleanup, fireEvent, render, screen } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi } from "../../../api";
import { setEventBus } from "../../../api/events";
import { createMockApi, type MockApi } from "../../../api/mock";
import type { UpdateStatus } from "../../../api/types";
import { settings } from "../../../state/app";
import { NO_UPDATE, checkError, updateStatus } from "../../../state/update";
import { DisplaySection, lastCheckedText } from "./Display";

const NOW = Date.UTC(2026, 10, 24, 12, 0, 0);
const CHANNEL: Partial<UpdateStatus> = { channel: "github", currentVersion: "v1.2.0", lastCheckedAt: NOW - 3 * 3_600_000 };

let mock: MockApi;
function setup(u: Partial<UpdateStatus>, failures?: Record<string, { code: string; message?: string }>) {
  mock = createMockApi({ update: u, failures });
  setApi(mock);
  setEventBus(mock.bus);
  updateStatus.value = { ...NO_UPDATE, ...u } as UpdateStatus;
  checkError.value = "";
  settings.value = { theme: "system", text_size: "standard", sync_on_open: "stale", sync_stale_hours: "6" };
}

beforeEach(() => vi.spyOn(Date, "now").mockReturnValue(NOW));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Display: Updates", () => {
  it("formats the last check", () => {
    expect(lastCheckedText(0, NOW)).toBe("not checked yet");
    expect(lastCheckedText(NOW - 3 * 3_600_000, NOW)).toBe("last checked 3h ago");
    expect(lastCheckedText(NOW - 5 * 60_000, NOW)).toBe("last checked 5 min ago");
  });

  it("a build without a channel has no Updates row: no switch, no Check now", () => {
    setup({ currentVersion: "dev" });
    render(<DisplaySection columns={3} />);
    expect(screen.queryByText("Updates")).toBeNull();
    expect(screen.queryByRole("switch", { name: /check for updates daily/i })).toBeNull();
    expect(screen.queryByRole("button", { name: "Check now" })).toBeNull();
  });

  it("the daily switch is on by default and autosaves update.check_daily", async () => {
    setup(CHANNEL);
    render(<DisplaySection columns={3} />);
    const sw = screen.getByRole("switch", { name: "Check for updates daily" });
    expect(sw.getAttribute("aria-checked")).toBe("true");
    expect(document.body.textContent).toContain("Once a day Cuisle asks GitHub whether there's a newer release");
    await act(async () => {
      fireEvent.click(sw);
    });
    expect(mock.calls.some((c) => c.method === "app.setAppSetting" && c.args[0] === "update.check_daily" && c.args[1] === "off")).toBe(true);
  });

  it("shows the switch off when the setting is off", () => {
    setup(CHANNEL);
    settings.value = { ...settings.value, "update.check_daily": "off" };
    render(<DisplaySection columns={3} />);
    expect(screen.getByRole("switch", { name: "Check for updates daily" }).getAttribute("aria-checked")).toBe("false");
  });

  it("shows the version and last check, and Check now asks once and updates the line", async () => {
    setup(CHANNEL);
    render(<DisplaySection columns={3} />);
    expect(document.body.textContent).toContain("Cuisle v1.2.0 · last checked 3h ago");
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Check now" }));
      await new Promise((r) => setTimeout(r, 0));
    });
    expect(mock.calls.filter((c) => c.method === "update.checkNow")).toHaveLength(1);
    expect(document.body.textContent).toContain("last checked 1 min ago");
    expect(document.body.textContent).toContain("You're up to date.");
  });

  it("says when a newer version is available", () => {
    setup({ ...CHANNEL, available: { version: "v1.3.0", pageUrl: "https://x/r", publishedAt: NOW, canInstall: true } });
    render(<DisplaySection columns={3} />);
    expect(document.body.textContent).toContain("Cuisle v1.3.0 is available");
  });

  it("shows a Check now failure as an alert", async () => {
    setup(CHANNEL, { "update.checkNow": { code: "offline", message: "Couldn't reach the release server." } });
    render(<DisplaySection columns={3} />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Check now" }));
      await new Promise((r) => setTimeout(r, 0));
    });
    const line = screen.getByText("Couldn't reach the release server.");
    expect(line.getAttribute("data-alert")).toBe("true");
  });

  it("makes no request on render", () => {
    setup(CHANNEL);
    render(<DisplaySection columns={3} />);
    expect(mock.calls.filter((c) => c.method.startsWith("update."))).toEqual([]);
  });
});
