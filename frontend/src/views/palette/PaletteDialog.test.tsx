// P4 tests: the palette dialog (F14, ux-spec §15.1-15.7) against the mock api: ARIA wiring, the keyboard table, mode chip,
// nested commands, Esc unwinding, opening tickets, the not-synced fetch row and its inline errors, recents, and the
// network rule (typing only reads locally; the fetch row is the one request).
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi } from "../../api";
import { setEventBus } from "../../api/events";
import { createMockApi, type MockApi } from "../../api/mock";
import { initFromBoot, settings } from "../../state/app";
import { closePalette, installRecents, openPalette, paletteOpen, recentTicketIds } from "../../state/palette";
import { route } from "../../state/route";
import { closeTicket, ticketSession } from "../../state/ticket";
import { toasts } from "../../state/ui";
import { fetchErrorText, PaletteDialog } from "./PaletteDialog";

let mock: MockApi;
let off: () => void;

async function setup(mode: "" | ">" | "#" | "@" = "") {
  mock = createMockApi();
  setApi(mock);
  setEventBus(mock.bus);
  initFromBoot(await mock.app.bootstrap());
  route.value = { view: "standup", standupId: route.value.view === "standup" ? route.value.standupId : 1, person: null };
  off = installRecents();
  mock.calls.length = 0;
  openPalette(mode);
  return render(<PaletteDialog />);
}

beforeEach(() => {
  closeTicket();
  closePalette();
});
afterEach(() => {
  off?.();
  cleanup();
  closePalette();
  closeTicket();
  toasts.value = [];
});

const input = () => screen.getByRole("combobox") as HTMLInputElement;
const type = async (text: string) => {
  await act(async () => {
    fireEvent.input(input(), { target: { value: text } });
  });
};
const press = async (k: string, init: KeyboardEventInit = {}) => {
  await act(async () => {
    fireEvent.keyDown(input(), { key: k, ...init });
  });
};
const options = () => screen.queryAllByRole("option");
const selected = () => options().find((o) => o.getAttribute("aria-selected") === "true");
const methods = () => mock.calls.map((c) => c.method);

describe("accessibility", () => {
  it("an ARIA combobox controlling a listbox, with the active option and a live region", async () => {
    await setup();
    const box = input();
    expect(box.getAttribute("aria-expanded")).toBe("true");
    const list = screen.getByRole("listbox");
    expect(box.getAttribute("aria-controls")).toBe(list.id);
    expect(screen.getByRole("dialog").getAttribute("aria-modal")).toBe("true");
    const active = box.getAttribute("aria-activedescendant")!;
    expect(document.getElementById(active)!.getAttribute("aria-selected")).toBe("true");
    expect(screen.getByRole("status").textContent).toMatch(/^\d+ results?$/);
  });

  it("disabled commands are aria-disabled and cannot run", async () => {
    await setup(">");
    await type("install update");
    const opt = selected()!;
    expect(opt.getAttribute("aria-disabled")).toBe("true");
    expect(opt.textContent).toContain("none waiting");
    await press("Enter");
    expect(paletteOpen.value).not.toBeNull(); // nothing ran, still open
  });

  it("the input has focus on open", async () => {
    await setup();
    expect(document.activeElement).toBe(input());
  });
});

describe("modes", () => {
  it("typing > # @ first switches to a chip and consumes the character; Backspace on empty removes the chip", async () => {
    await setup();
    await type(">");
    expect(screen.getByRole("button", { name: /commands only/i }).textContent).toBe(">");
    expect(input().value).toBe("");
    await press("Backspace");
    expect(screen.queryByRole("button", { name: /only/i })).toBeNull();
    await type("@");
    expect(screen.getByRole("button", { name: /people only/i })).toBeTruthy();
  });

  it("opening with a mode (Go to person…) starts in it", async () => {
    await setup("@");
    expect(screen.getByRole("button", { name: /people only/i })).toBeTruthy();
    await waitFor(() => expect(screen.getByText("Unassigned")).toBeTruthy());
  });

  it("no prefix searches everything: commands for words", async () => {
    await setup();
    await type("resync");
    expect(options()[0]!.textContent).toContain("Resync");
  });
});

describe("keyboard (ux-spec §15.6)", () => {
  it("↑ ↓ move and wrap; Home and End jump; PageDown moves by 8 and clamps", async () => {
    await setup(">");
    const n = options().length;
    expect(n).toBeGreaterThan(9);
    const idx = () => options().indexOf(selected()!);
    expect(idx()).toBe(0);
    await press("ArrowUp");
    expect(idx()).toBe(n - 1);
    await press("ArrowDown");
    expect(idx()).toBe(0);
    await press("End");
    expect(idx()).toBe(n - 1);
    await press("Home");
    await press("PageDown");
    expect(idx()).toBe(8);
    await press("PageDown");
    await press("PageDown");
    expect(idx()).toBe(n - 1);
    await press("PageUp");
    expect(idx()).toBe(n - 9);
  });

  it("Tab does nothing: focus stays in the input", async () => {
    await setup();
    const e = new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true });
    await act(async () => {
      input().dispatchEvent(e);
    });
    expect(e.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(input());
  });

  it("keys are not seen by anything behind the palette", async () => {
    await setup();
    const seen = vi.fn();
    document.addEventListener("keydown", seen);
    await press("ArrowDown");
    await press("r");
    document.removeEventListener("keydown", seen);
    expect(seen).not.toHaveBeenCalled();
  });

  it("↵ runs the selected command, counts its use and closes", async () => {
    await setup(">");
    route.value = { view: "standup", standupId: 1, person: "u-iris" };
    await type("whole board");
    await press("Enter");
    expect(paletteOpen.value).toBeNull();
    expect(settings.value["palette.command_usage"]).toContain("nav.board");
    expect((route.value as { person: string | null }).person).toBeNull();
  });

  it("⌘↵ opens the selected ticket in Jira and closes", async () => {
    await setup();
    await type("1421");
    await waitFor(() => expect(screen.getAllByRole("option").length).toBeGreaterThan(0));
    await press("Enter", { ctrlKey: true });
    await waitFor(() => expect(methods()).toContain("app.openIssue"));
    expect(mock.calls.find((c) => c.method === "app.openIssue")!.args[0]).toBe("MOB-1421");
    expect(paletteOpen.value).toBeNull();
  });
});

describe("nested commands and Esc", () => {
  it("↵ on Theme shows a breadcrumb and the choices, the current one checked; choosing saves and closes", async () => {
    await setup(">");
    await type("theme");
    await press("Enter");
    expect(screen.getByTestId("pal-crumb").textContent).toContain("Theme");
    expect(input().placeholder).toBe("Choose a theme");
    expect(options().map((o) => o.textContent)).toEqual([expect.stringContaining("System"), expect.stringContaining("Light"), expect.stringContaining("Dark")]);
    await type("dar");
    expect(options()).toHaveLength(1);
    await press("Enter");
    expect(paletteOpen.value).toBeNull();
    expect(settings.value.theme).toBe("dark");
  });

  it("→ enters a nested command like ↵", async () => {
    await setup(">");
    await type("text size");
    input().setSelectionRange(9, 9); // the caret at the end, as typing leaves it
    await press("ArrowRight");
    expect(screen.getByTestId("pal-crumb").textContent).toContain("Text size");
  });

  it("Esc unwinds a nested level, then the mode chip, then closes", async () => {
    await setup(">");
    await type("theme");
    await press("Enter");
    await press("Escape");
    expect(screen.queryByTestId("pal-crumb")).toBeNull();
    expect(screen.getByRole("button", { name: /commands only/i })).toBeTruthy();
    await press("Escape");
    expect(screen.queryByRole("button", { name: /only/i })).toBeNull();
    expect(paletteOpen.value).not.toBeNull();
    await press("Escape");
    expect(paletteOpen.value).toBeNull();
  });

  it("⌫ on an empty field goes back one nested level", async () => {
    await setup(">");
    await type("theme");
    await press("Enter");
    await press("Backspace");
    expect(screen.queryByTestId("pal-crumb")).toBeNull();
  });

  it("focus returns to the opener when it closes", async () => {
    const opener = document.createElement("button");
    document.body.append(opener);
    opener.focus();
    await setup();
    expect(document.activeElement).toBe(input());
    await press("Escape");
    cleanup();
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it("when nothing had focus at open (the modal's own focus step lands just after), the dialog adopts the element focused as it mounts", async () => {
    const late = document.createElement("div");
    late.tabIndex = -1;
    document.body.append(late);
    mock = createMockApi();
    setApi(mock);
    setEventBus(mock.bus);
    initFromBoot(await mock.app.bootstrap());
    route.value = { view: "standup", standupId: 1, person: null };
    off = installRecents();
    (document.activeElement as HTMLElement | null)?.blur();
    openPalette(); // nothing focused yet
    late.focus(); // the modal's focus effect runs before the palette's
    render(<PaletteDialog />);
    await press("Escape");
    cleanup();
    expect(document.activeElement).toBe(late);
    late.remove();
  });

  it("the opener is the element focused when the palette was OPENED, even if the lazy dialog mounts late and focus has moved", async () => {
    const opener = document.createElement("button");
    const other = document.createElement("button");
    document.body.append(opener, other);
    mock = createMockApi();
    setApi(mock);
    setEventBus(mock.bus);
    initFromBoot(await mock.app.bootstrap());
    route.value = { view: "standup", standupId: 1, person: null };
    off = installRecents();
    opener.focus();
    openPalette(); // the key press / click
    other.focus(); // the chunk is still loading and something else takes the focus
    render(<PaletteDialog />); // the chunk arrives
    expect(document.activeElement).toBe(input());
    await press("Escape");
    cleanup();
    expect(document.activeElement).toBe(opener);
    opener.remove();
    other.remove();
  });
});

describe("tickets", () => {
  it("a number lists tickets with the matched digits inked, then ticket commands", async () => {
    await setup();
    await type("1429");
    await waitFor(() => expect(document.querySelector(".pal-row--ticket")).not.toBeNull());
    const first = document.querySelector(".pal-row--ticket")!;
    expect(first.textContent).toContain("MOB-1429");
    expect(first.querySelector(".pal-hl")!.textContent).toBe("1429");
    expect(options().some((o) => o.textContent?.includes("Refresh MOB-1429 from Jira"))).toBe(true);
  });

  it("words find tickets by title: the title label, the matched summary characters inked, key not inked", async () => {
    await setup();
    await type("recipe card");
    await waitFor(() => expect(document.querySelector(".pal-row--ticket")).not.toBeNull());
    const first = document.querySelector(".pal-row--ticket")!;
    expect(first.querySelector(".pal-title")!.textContent).toMatch(/^Recipe card crops/);
    const inked = [...first.querySelectorAll(".pal-title .pal-hl")].map((m) => m.textContent);
    expect(inked).toEqual(["Recipe", "card"]);
    expect(first.querySelector(".pal-key .pal-hl")).toBeNull();
    const title = screen.getByText("TICKETS").parentElement!;
    expect(title.textContent).toContain("title");
  });

  it("a key hit still inks the key and leaves the title plain; a letters query offers no ticket commands", async () => {
    await setup();
    await type("1429");
    await waitFor(() => expect(document.querySelector(".pal-row--ticket")).not.toBeNull());
    const first = document.querySelector(".pal-row--ticket")!;
    expect(first.querySelector(".pal-key .pal-hl")!.textContent).toBe("1429");
    expect(first.querySelector(".pal-title .pal-hl")).toBeNull();
    await type("recipe");
    await waitFor(() => expect(document.querySelector(".pal-row--ticket .pal-title .pal-hl")).not.toBeNull());
    expect(options().some((o) => /Refresh MOB-\d+ from Jira/.test(o.textContent ?? ""))).toBe(false);
  });

  it("# mode finds by title too, and a single letter asks the user for more", async () => {
    await setup("#");
    await type("r");
    expect(screen.getByText(/a few letters of its title/)).toBeTruthy();
    expect(methods()).not.toContain("ticket.find");
    await type("recipe card");
    await waitFor(() => expect(document.querySelector(".pal-row--ticket")).not.toBeNull());
    expect(methods()).toContain("ticket.find");
    expect(input().placeholder).toBe("Find a ticket by key or title");
  });

  it("the placeholder names both ways to find a ticket", async () => {
    await setup();
    expect(input().placeholder).toBe("Find a ticket by key or title, or run a command");
  });

  it("↵ on a ticket opens the modal in its standup and records it as a recent", async () => {
    await setup();
    await type("1421");
    await waitFor(() => expect(document.querySelector(".pal-row--ticket")).not.toBeNull());
    await press("Enter");
    await waitFor(() => expect(ticketSession.value).not.toBeNull());
    expect(paletteOpen.value).toBeNull();
    const s = ticketSession.value!;
    expect(s.steps).toContain(s.issueId);
    expect(recentTicketIds()[0]).toBe(s.issueId);
  });

  it("recents are recorded from any open path, most recent first, max 8", async () => {
    await setup();
    closePalette();
    vi.useFakeTimers();
    for (let i = 1; i <= 10; i++) ticketSession.value = { standupId: 1, issueId: i, steps: [i], stack: [] };
    expect(methods()).not.toContain("app.setAppSetting"); // stepping saves once, after the last step
    vi.advanceTimersByTime(1100);
    vi.useRealTimers();
    expect(methods().filter((m) => m === "app.setAppSetting")).toHaveLength(1);
    const ids = JSON.parse(settings.value["palette.recent_tickets"]!) as number[];
    expect(ids).toEqual([10, 9, 8, 7, 6, 5, 4, 3]);
  });

  it("the empty palette lists the recent tickets (3) from the local lookup", async () => {
    await setup();
    closePalette();
    settings.value = { ...settings.value, "palette.recent_tickets": "[107,103,109,110]" };
    openPalette();
    cleanup();
    render(<PaletteDialog />);
    await waitFor(() => expect(document.querySelectorAll(".pal-row--ticket")).toHaveLength(3));
    expect(screen.getByText("RECENT TICKETS")).toBeTruthy();
  });
});

describe("the network rule (AGENTS invariant 2)", () => {
  it("typing and moving only read locally: no fetch, refresh or sync call", async () => {
    await setup();
    for (const t of ["1", "14", "142", "1429"]) await type(t);
    await type("@no");
    await type(">sta");
    await press("ArrowDown");
    const bad = methods().filter((m) => /fetchByKey|refresh|sync\.|transitions|searchMentions|checkNow/.test(m));
    expect(bad).toEqual([]);
    expect(methods().every((m) => m === "ticket.find" || m === "standup.people" || m === "ticket.byIds" || m === "app.setAppSetting" || m === "app.setUIState")).toBe(true);
  });

  it("an unsynced full key offers the fetch row first; ↵ makes exactly one fetchByKey call", async () => {
    await setup();
    await type("mob-1777");
    await waitFor(() => expect(document.querySelector(".pal-row--fetch")).not.toBeNull());
    expect(selected()!.className).toContain("pal-row--fetch");
    expect(methods().filter((m) => m === "ticket.fetchByKey")).toHaveLength(0);
    await press("Enter");
    await waitFor(() => expect(ticketSession.value).not.toBeNull());
    expect(methods().filter((m) => m === "ticket.fetchByKey")).toHaveLength(1);
    expect(paletteOpen.value).toBeNull();
  });

  it.each([
    ["mob-404", "No issue MOB-404 in Jira"],
    ["mob-403", "You don't have access"],
    ["mob-500", "Couldn't reach Jira · offline"],
  ])("%s fails inline in the row and the palette stays open", async (key, text) => {
    await setup();
    await type(key);
    await waitFor(() => expect(document.querySelector(".pal-row--fetch")).not.toBeNull());
    await press("Enter");
    await waitFor(() => expect(document.querySelector(".pal-fetch-error")?.textContent).toBe(text));
    expect(paletteOpen.value).not.toBeNull();
    expect(ticketSession.value).toBeNull();
  });
});

describe("fetchErrorText", () => {
  it("maps the codes", () => {
    expect(fetchErrorText("not_found", "MOB-1", "")).toBe("No issue MOB-1 in Jira");
    expect(fetchErrorText("forbidden", "MOB-1", "")).toBe("You don't have access");
    expect(fetchErrorText("offline", "MOB-1", "")).toBe("Couldn't reach Jira · offline");
    expect(fetchErrorText("rate_limited", "MOB-1", "")).toMatch(/busy/);
    expect(fetchErrorText("auth_invalid", "MOB-1", "")).toMatch(/token/);
    expect(fetchErrorText("whatever", "MOB-1", "boom")).toBe("boom");
  });
});
