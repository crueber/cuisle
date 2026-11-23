// WP-38F: App shell wiring: KeyLegend, the "/" switcher request, popovers cleared on route change, toast actions,
// and the reconnect detour of the auth banner.
import { act, cleanup, fireEvent, render, screen } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { connection, standups } from "./state/app";
import { route } from "./state/route";
import { views } from "./state/standup";
import { keyLegendOpen, showToast, switcherOpen, toasts } from "./state/ui";
import type { StandupTab, StandupView } from "./api/types";

vi.mock("./views/standup/StandupScreen", async () => {
  const { h } = await import("preact");
  return {
    StandupScreen: () =>
      h("nav", { class: "sf-tabs", "data-mode": (window as unknown as { __mode?: string }).__mode ?? "full" },
        h("button", { class: "sf-tab" }, "tab-one"),
        h("button", { class: "cbx-menu-btn", onClick: () => ((window as unknown as { __menu?: number }).__menu = 1) }, "menu")),
  };
});
vi.mock("./views/configure/ConfigureScreen", async () => {
  const { h } = await import("preact");
  return { ConfigureScreen: () => h("div", { "data-testid": "configure" }, "configure-double") };
});

const tab = (id: number): StandupTab => ({ id, rev: 1, name: `S${id}`, spaceKey: "K", position: id, activeCount: 0, speakerCount: 0, configured: true, needsSync: false, lastSyncedAt: 0 });
const view = { standupId: 1, rev: 1, options: { showCommits: true, showPRs: false, showComments: true, devActivity: "ok" } } as unknown as StandupView;

beforeEach(() => {
  standups.value = [tab(1)];
  views.value = new Map([[1, view]]);
  route.value = { view: "standup", standupId: 1, person: null };
  keyLegendOpen.value = false;
  switcherOpen.value = false;
  toasts.value = [];
  (window as unknown as { __mode?: string }).__mode = "full";
  (window as unknown as { __menu?: number }).__menu = 0;
});
afterEach(() => cleanup());

describe("KeyLegend", () => {
  it("renders when keyLegendOpen is set, with the options of the current view, and closes through its own button", () => {
    render(<App />);
    expect(screen.queryByRole("dialog")).toBeNull();
    act(() => {
      keyLegendOpen.value = true;
    });
    const dialog = screen.getByRole("dialog", { name: /keys and pulse legend/i });
    expect(dialog.textContent).toMatch(/Commit tick/);
    expect(dialog.textContent).toMatch(/Comment that day/);
    expect(dialog.textContent).not.toMatch(/PR activity/);
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(keyLegendOpen.value).toBe(false);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("Esc closes it", () => {
    render(<App />);
    act(() => {
      keyLegendOpen.value = true;
    });
    fireEvent.keyDown(document, { key: "Escape" });
    expect(keyLegendOpen.value).toBe(false);
  });

  it("is cleared when the route changes", () => {
    render(<App />);
    act(() => {
      keyLegendOpen.value = true;
    });
    act(() => {
      route.value = { view: "configure", standupId: 1 };
    });
    expect(keyLegendOpen.value).toBe(false);
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("switcherOpen (ux-spec §8 '/')", () => {
  it("focuses the first tab when the tabs are shown", () => {
    render(<App />);
    act(() => {
      switcherOpen.value = true;
    });
    expect(document.activeElement).toBe(screen.getByText("tab-one"));
  });

  it("opens the switcher menu when the tabs collapsed to it", () => {
    (window as unknown as { __mode?: string }).__mode = "menu";
    render(<App />);
    act(() => {
      switcherOpen.value = true;
    });
    expect((window as unknown as { __menu?: number }).__menu).toBe(1);
  });

  it("is a one-shot request: consumed at once, so a later Esc is not swallowed", () => {
    render(<App />);
    act(() => {
      switcherOpen.value = true;
    });
    expect(switcherOpen.value).toBe(false);
  });

  it("is cleared on route change", () => {
    render(<App />);
    switcherOpen.value = true; // set without a render, as if the screen was not mounted
    act(() => {
      route.value = { view: "configure", standupId: 1 };
    });
    expect(switcherOpen.value).toBe(false);
  });
});

describe("toasts", () => {
  it("the action dismisses the toast and runs once", () => {
    const run = vi.fn();
    render(<App />);
    act(() => {
      showToast({ message: "Removed Sam", action: { label: "Undo", run }, timeoutMs: 10_000 });
    });
    const undo = screen.getByRole("button", { name: "Undo" });
    fireEvent.click(undo);
    fireEvent.click(undo);
    expect(run).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("Removed Sam")).toBeNull();
  });
});

describe("auth banner Connect", () => {
  beforeEach(() => {
    connection.value = { state: "invalid", source: "jira", baseUrl: "https://acme.atlassian.net", account: "me@acme.com", displayName: "", devActivity: "ok", capabilities: {} } as unknown as typeof connection.value;
  });

  it("with standups shows the reconnect form with a Cancel that goes back", () => {
    route.value = { view: "connect" };
    render(<App />);
    expect(screen.getByRole("heading", { name: /reconnect to jira/i })).toBeTruthy();
    expect(screen.queryByText(/standups from your jira board/i)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(route.value.view).not.toBe("connect");
  });

  it("with no standups shows the first-run Connect screen", () => {
    standups.value = [];
    route.value = { view: "connect" };
    render(<App />);
    expect(screen.getByText(/standups from your jira board/i)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
  });

  it("Esc leaves the reconnect screen", () => {
    route.value = { view: "connect" };
    render(<App />);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(route.value.view).not.toBe("connect");
  });

  it("a successful reconnect (a fresh ok connection) returns without creating a standup", () => {
    route.value = { view: "standup", standupId: 1, person: null };
    // goConnect remembers where we were.
    return import("./state/route").then(({ goConnect }) => {
      goConnect();
      render(<App />);
      act(() => {
        connection.value = { ...connection.value!, state: "ok" };
      });
      expect(route.value).toEqual({ view: "standup", standupId: 1, person: null });
    });
  });
});
