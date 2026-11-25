// Auto-update in the standup header (ux-spec §2.1 Update, ADR 0016): the Update button sits between Resync and
// Configure in the same button family, only when a channel build has a newer release; clicking it opens the confirm
// dialog (current → new, What's new, Install and restart) with progress and failure states.
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi } from "../../api";
import { setEventBus } from "../../api/events";
import { createMockApi, type MockApi } from "../../api/mock";
import type { StandupTab, StandupView, SyncStatus, UpdateStatus } from "../../api/types";
import { standups } from "../../state/app";
import { route } from "../../state/route";
import { views } from "../../state/standup";
import { syncStatus } from "../../state/sync";
import { NO_UPDATE, installUpdateEvents, updateDialogOpen, updateStatus } from "../../state/update";
import { rightClusterWidth } from "./headerText";
import { headerCollapse } from "../../layout/responsive";
import { Header } from "./Header";

vi.mock("./Tabs", async () => {
  const { h } = await import("preact");
  return { Tabs: (p: { availW: number }) => h("div", { "data-testid": "tabs", "data-availw": String(p.availW) }) };
});

const NOW = Date.UTC(2026, 10, 23, 15, 0, 0);

const AVAILABLE: Partial<UpdateStatus> = {
  channel: "github", currentVersion: "v1.2.0", lastCheckedAt: NOW - 3_600_000,
  available: { version: "v1.3.0", pageUrl: "https://github.com/crueber/cuisle/releases/tag/v1.3.0", publishedAt: NOW, canInstall: true },
};

const tab = (id: number): StandupTab => ({
  id, rev: 1, name: `S${id}`, spaceKey: "MOB", position: id, activeCount: 1, speakerCount: 1, configured: true, needsSync: false, lastSyncedAt: 0,
});

function sync(): SyncStatus {
  return {
    standupId: 1, runId: 1, state: "idle", mode: "incremental", phase: "", done: 0, total: 0, lastSuccessAt: NOW, lastAttemptAt: NOW,
    lastStatus: "ok", warnings: [], errorCode: "", errorMessage: "", needsSync: false,
  };
}

function view(): StandupView {
  return {
    standupId: 1, rev: 1, generatedAt: NOW, today: "2026-11-23", asOf: NOW, live: true, name: "S1", spaceKey: "MOB", boardName: "B", boardType: "scrum",
    scope: "sprint", sprint: null, pulseDays: 21, axis: [], lanes: [], speakers: [], tickets: [], backlog: [], backlogTotal: 0, counts: { active: 0, unassigned: 0, done: 0, backlog: 0 },
    options: { rowsMode: "fit", showCommits: true, showPRs: true, showComments: true, shadeWeekends: true, doneWindow: "sprint", doneLabel: "this sprint", devActivity: "ok" },
    sync: sync(), warnings: [], previewKey: "", lastSyncedAt: NOW,
  };
}

let mock: MockApi;
let offEvents: () => void;

function setup(update: Partial<UpdateStatus> | null, install?: "ok" | "verify" | "download") {
  mock = createMockApi({ update: update ?? undefined, updateInstall: install });
  setApi(mock);
  setEventBus(mock.bus);
  offEvents = installUpdateEvents();
  standups.value = [tab(1)];
  views.value = new Map([[1, view()]]);
  route.value = { view: "standup", standupId: 1, person: null };
  syncStatus.value = new Map([[1, sync()]]);
  updateStatus.value = update ? { ...NO_UPDATE, ...update } as UpdateStatus : NO_UPDATE;
  updateDialogOpen.value = false;
}

beforeEach(() => {
  vi.spyOn(Date, "now").mockReturnValue(NOW);
});
afterEach(() => {
  cleanup();
  offEvents?.();
  vi.restoreAllMocks();
});

const buttonsInOrder = () => Array.from(document.querySelectorAll<HTMLButtonElement>("header button.sf-header__configure"));

describe("Header: Update button", () => {
  it("is absent when there is no newer release, and in builds without a channel", () => {
    setup(null);
    render(<Header vw={1920} />);
    expect(screen.queryByRole("button", { name: /update/i })).toBeNull();
    cleanup();
    setup({ ...AVAILABLE, channel: "" });
    render(<Header vw={1920} />);
    expect(screen.queryByRole("button", { name: /update/i })).toBeNull();
    cleanup();
    setup({ ...AVAILABLE, available: null });
    render(<Header vw={1920} />);
    expect(screen.queryByRole("button", { name: /update/i })).toBeNull();
  });

  it("sits between Resync and Configure in the same button family, with the version in its tooltip", () => {
    setup(AVAILABLE);
    render(<Header vw={1920} />);
    const names = buttonsInOrder().map((b) => b.getAttribute("aria-label"));
    expect(names.slice(1)).toEqual(["Resync (R)", "Update to v1.3.0", "Configure"]); // Find comes first (F14)
    expect(names[0]).toMatch(/^Find a ticket or run a command/);
    const btn = screen.getByRole("button", { name: "Update to v1.3.0" });
    expect(btn.classList.contains("sf-header__configure")).toBe(true);
    expect(btn.getAttribute("title")).toBe("Cuisle v1.3.0 is available");
    expect(btn.querySelector("svg")).not.toBeNull();
    expect(btn.textContent).toBe("Update");
  });

  it("is icon-only when the header hides labels (below 1280)", () => {
    setup(AVAILABLE);
    render(<Header vw={1100} />);
    const btn = screen.getByRole("button", { name: "Update to v1.3.0" });
    expect(btn.textContent).toBe("");
    expect(btn.querySelector("svg")).not.toBeNull();
  });

  it("takes its width out of the tabs' room", () => {
    for (const vw of [1100, 1300, 1440, 1920]) {
      const c = headerCollapse(vw);
      expect(rightClusterWidth(c, true)).toBeGreaterThan(rightClusterWidth(c, false) + 44);
    }
    setup(null);
    render(<Header vw={1920} />);
    const without = Number(screen.getByTestId("tabs").dataset.availw);
    cleanup();
    setup(AVAILABLE);
    render(<Header vw={1920} />);
    const withUpdate = Number(screen.getByTestId("tabs").dataset.availw);
    expect(without - withUpdate).toBe(rightClusterWidth(headerCollapse(1920), true) - rightClusterWidth(headerCollapse(1920), false));
  });
});

describe("Update dialog", () => {
  const open = () => fireEvent.click(screen.getByRole("button", { name: "Update to v1.3.0" }));

  it("shows current → new, What's new opens the release page, Not now closes", () => {
    setup(AVAILABLE);
    render(<Header vw={1920} />);
    open();
    const dlg = screen.getByRole("dialog", { name: "Update Cuisle" });
    expect(dlg.textContent).toContain("v1.2.0");
    expect(dlg.textContent).toContain("v1.3.0");
    fireEvent.click(screen.getByRole("button", { name: "What's new" }));
    expect(mock.calls.some((c) => c.method === "app.openUrl" && c.args[0] === AVAILABLE.available!.pageUrl)).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Not now" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("closes on a scrim click and on Escape", () => {
    setup(AVAILABLE);
    render(<Header vw={1920} />);
    open();
    const scrim = document.querySelector(".ud-scrim")!;
    fireEvent.mouseDown(scrim);
    fireEvent.click(scrim);
    expect(screen.queryByRole("dialog")).toBeNull();
    open();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("traps Tab and Shift+Tab inside the dialog", () => {
    setup(AVAILABLE);
    render(<Header vw={1920} />);
    open();
    const dlg = screen.getByRole("dialog");
    const names = () => (document.activeElement as HTMLElement).textContent;
    fireEvent.keyDown(dlg, { key: "Tab" });
    expect(names()).toBe("What's new");
    fireEvent.keyDown(dlg, { key: "Tab" });
    expect(names()).toBe("Not now");
    fireEvent.keyDown(dlg, { key: "Tab" });
    expect(names()).toBe("Install and restart");
    fireEvent.keyDown(dlg, { key: "Tab" });
    expect(names()).toBe("What's new"); // wrapped
    fireEvent.keyDown(dlg, { key: "Tab", shiftKey: true });
    expect(names()).toBe("Install and restart"); // and back
    expect(dlg.contains(document.activeElement)).toBe(true);
  });

  it("Install and restart runs the install and shows its progress", async () => {
    setup(AVAILABLE, "ok");
    render(<Header vw={1920} />);
    open();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Install and restart" }));
    });
    expect(mock.calls.filter((c) => c.method === "update.install")).toHaveLength(1);
    expect(within(screen.getByRole("dialog")).getByRole("status").textContent).toContain("Restarting");
    // while installing the dialog cannot be dismissed and a second install cannot start
    expect((screen.getByRole("button", { name: "Install and restart" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(screen.getByRole("dialog")).not.toBeNull();
  });

  it("a verification failure says nothing was installed and offers Try again", async () => {
    setup(AVAILABLE, "verify");
    render(<Header vw={1920} />);
    open();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Install and restart" }));
    });
    expect(screen.getByRole("alert").textContent).toBe("Couldn't verify the download, so nothing was installed.");
    expect(screen.getByRole("button", { name: "Try again" })).not.toBeNull();
    expect(screen.getByRole("button", { name: "Close" })).not.toBeNull();
  });

  it("installed but not restarted: says so and offers only Close", () => {
    setup({ ...AVAILABLE, phase: "failed", errorCode: "restart", error: "Cuisle v1.3.0 is installed. Quit and open Cuisle again to finish." });
    render(<Header vw={1920} />);
    open();
    expect(screen.getByRole("alert").textContent).toBe("Cuisle v1.3.0 is installed. Quit and open Cuisle again to finish.");
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Install and restart" })).toBeNull();
    expect(screen.getByRole("button", { name: "Close" })).not.toBeNull();
  });

  it("shows download progress as a percentage", () => {
    setup({ ...AVAILABLE, phase: "downloading", progress: 42 });
    render(<Header vw={1920} />);
    open();
    expect(within(screen.getByRole("dialog")).getByRole("status").textContent).toContain("Downloading… 42%");
    expect(screen.getByRole("progressbar", { name: "Update progress" }).getAttribute("aria-valuenow")).toBe("42");
  });

  it("offers the release page when this copy can't replace itself", () => {
    setup({ ...AVAILABLE, available: { ...AVAILABLE.available!, canInstall: false } });
    render(<Header vw={1920} />);
    open();
    expect(screen.queryByRole("button", { name: "Install and restart" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Open the release page" }));
    expect(mock.calls.some((c) => c.method === "app.openUrl")).toBe(true);
    expect(mock.calls.some((c) => c.method === "update.install")).toBe(false);
  });
});
