// The initial command set (F14, ux-spec §15.4). Registry order matters: it is the tie-break after score and usage, and it
// is the order the empty palette falls back to (Resync, Group board by…, Go to person…, Switch standup…, Open Configure).
// Lane names never appear here: nothing in this file knows the lanes.
import { api, toAppError } from "../api";
import { setAppSetting } from "../state/app";
import { createStandup, duplicateStandup, leaveConfigure } from "../state/config";
import { isMac } from "../state/palette";
import { goConfigure, goConnect, route } from "../state/route";
import { selectPerson, setBoardGroup, switchStandup } from "../state/standup";
import { cancelSync, startSync } from "../state/sync";
import { resetZoom, zoomBy, zoomOf, ZOOM_DEFAULT, ZOOM_STEPS } from "../state/zoom";
import { refreshTicket } from "../state/ticket";
import { keyLegendOpen, showToast } from "../state/ui";
import { checkError, checkNow, updateAvailable, updateDialogOpen, updateStatus } from "../state/update";
import { openIssueKey } from "../views/ticket/TicketModal.util";
import { copyText, openTicketIn } from "./actions";
import { registerCommands } from "./registry";
import type { Command, Ctx } from "./types";

const standupOnly = (c: Ctx): true | string => (c.onStandup ? true : "open a standup first");
const hasStandup = (c: Ctx): true | string => (c.standupId !== null ? true : "no standup");
const isRunning = (c: Ctx): boolean => c.sync?.state === "running";

function themeChoices(): Command[] {
  const mk = (value: "system" | "light" | "dark", title: string, detail?: (c: Ctx) => string): Command => ({
    id: `display.theme.${value}`,
    title,
    category: "Display",
    icon: "sun",
        detail,
    checked: (c) => c.theme === value,
    run: () => setAppSetting("theme", value),
  });
  return [
    mk("system", "System", (c) => `follows ${isMac() ? "macOS" : "your OS"} · currently ${c.resolvedTheme}`),
    mk("light", "Light"),
    mk("dark", "Dark"),
  ];
}

function sizeChoices(): Command[] {
  const mk = (value: "standard" | "large", title: string, keywords: string[]): Command => ({
    id: `display.text_size.${value}`,
    title,
    category: "Display",
    icon: "text",
    keywords,
    checked: (c) => c.textSize === value,
    run: () => setAppSetting("text_size", value),
  });
  return [mk("standard", "Standard", ["normal"]), mk("large", "Large (TV)", ["big", "tv"])];
}

async function copyDiagnostics(): Promise<void> {
  try {
    const d = await api().app.diagnostics();
    copyText(JSON.stringify(d, null, 2), "Copied diagnostics");
  } catch (e) {
    showToast({ message: toAppError(e).message || "Couldn't read the diagnostics.", timeoutMs: 4000 });
  }
}

async function checkForUpdates(): Promise<void> {
  await checkNow();
  const err = checkError.value;
  if (err) {
    showToast({ message: err, timeoutMs: 4000 });
    return;
  }
  const v = updateStatus.value.available?.version;
  showToast({ message: v ? `Cuisle ${v} is available` : "You're up to date", timeoutMs: 3500 });
}

export const BUILTIN: Command[] = [
  // ── Sync ──
  {
    id: "sync.resync",
    title: "Resync",
    category: "Sync",
    icon: "resync",
    keywords: ["refresh", "pull", "update"],
    shortcut: "R",
    detail: (c) => c.syncText,
    available: (c) => (c.standupId === null ? "no standup" : isRunning(c) ? "already syncing" : true),
    run: (c) => startSync(c.standupId!, "incremental"),
  },
  {
    id: "sync.full",
    title: "Full resync",
    category: "Sync",
    icon: "resync",
    keywords: ["rebuild"],
    shortcut: "⇧R",
    available: (c) => (c.standupId === null ? "no standup" : isRunning(c) ? "already syncing" : true),
    run: (c) => startSync(c.standupId!, "full"),
  },
  {
    id: "sync.cancel",
    title: "Cancel sync",
    category: "Sync",
    icon: "close",
    keywords: ["stop"],
    visible: isRunning,
    run: (c) => cancelSync(c.standupId!),
  },
  // ── View ──
  {
    id: "view.group",
    title: (c) => (c.group === "assignee" ? "Group board by status" : "Group board by assignee"),
    category: "View",
    icon: "columns",
    keywords: ["board", "swimlane"],
    shortcut: "G",
    detail: (c) => `now: ${c.group}`,
    available: standupOnly,
    run: (c) => {
      setBoardGroup(c.standupId!, c.group === "assignee" ? "status" : "assignee");
      if (c.route.view === "standup" && c.route.person !== null) selectPerson(null);
    },
  },
  // ── Zoom (F15): the standup screen below the header, one level per standup ──
  {
    id: "view.zoom.in",
    title: "Zoom in",
    category: "View",
    icon: "plus",
    keywords: ["bigger", "larger", "magnify"],
    shortcut: "+",
    detail: (c) => (c.standupId === null ? undefined : `now ${zoomOf(c.standupId)}%`),
    available: (c) => (!c.onStandup ? "open a standup first" : zoomOf(c.standupId!) >= ZOOM_STEPS[ZOOM_STEPS.length - 1]! ? "already at the largest" : true),
    run: () => void zoomBy(1),
  },
  {
    id: "view.zoom.out",
    title: "Zoom out",
    category: "View",
    icon: "minus",
    keywords: ["smaller", "shrink"],
    shortcut: "−",
    detail: (c) => (c.standupId === null ? undefined : `now ${zoomOf(c.standupId)}%`),
    available: (c) => (!c.onStandup ? "open a standup first" : zoomOf(c.standupId!) <= ZOOM_STEPS[0]! ? "already at the smallest" : true),
    run: () => void zoomBy(-1),
  },
  {
    id: "view.zoom.reset",
    title: "Reset zoom",
    category: "View",
    icon: "search",
    keywords: ["100%", "actual size"],
    shortcut: "0",
    detail: (c) => (c.standupId === null ? undefined : `now ${zoomOf(c.standupId)}%`),
    available: (c) => (!c.onStandup ? "open a standup first" : zoomOf(c.standupId!) === ZOOM_DEFAULT ? "already 100%" : true),
    run: () => void resetZoom(),
  },
  // ── Navigate ──
  {
    id: "nav.board",
    title: "Whole board",
    category: "Navigate",
    icon: "board",
    keywords: ["everyone"],
    shortcut: "B",
    available: (c) => (c.onStandup ? (c.onBoard ? "already here" : true) : "open a standup first"),
    run: () => selectPerson(null),
  },
  {
    id: "nav.person",
    title: "Go to person…",
    category: "Navigate",
    icon: "person",
    keywords: ["speaker", "assignee"],
    shortcut: "@",
    mode: "@",
  },
  {
    id: "nav.unassigned",
    title: "Unassigned",
    category: "Navigate",
    icon: "person",
    keywords: ["no owner"],
    detail: (c) => (c.view && c.view.counts.unassigned > 0 ? `${c.view.counts.unassigned} need an owner` : undefined),
    available: standupOnly,
    run: () => selectPerson("unassigned"),
  },
  {
    id: "nav.standup",
    title: "Switch standup…",
    category: "Navigate",
    icon: "layers",
    keywords: ["tab", "board", "team"],
    shortcut: "1–9",
    available: (c) => (c.tabs.length > 1 ? true : "only one standup"),
    placeholder: "Choose a standup",
    children: (c) =>
      c.tabs.map((t, i) => ({
        id: `nav.standup.${t.id}`,
        title: t.name,
        category: "Navigate" as const,
        icon: "layers",
        shortcut: i < 9 ? String(i + 1) : undefined,
        detail: () => (t.spaceKey !== "" ? t.spaceKey : undefined),
        checked: (x: Ctx) => x.standupId === t.id,
        run: async () => {
          if (route.value.view === "configure") await leaveConfigure();
          await switchStandup(t.id);
        },
      })),
  },
  {
    id: "nav.configure",
    title: "Open Configure",
    category: "Navigate",
    icon: "sliders",
    keywords: ["settings", "preferences"],
    shortcut: ",",
    available: standupOnly,
    run: () => goConfigure(null),
  },
  {
    id: "nav.keys",
    title: "Keyboard shortcuts",
    category: "Navigate",
    icon: "sliders",
    keywords: ["keys", "help", "hotkeys"],
    shortcut: "?",
    available: standupOnly,
    run: () => {
      keyLegendOpen.value = true;
    },
  },
  // ── Display ──
  {
    id: "display.theme",
    title: "Theme",
    category: "Display",
    icon: "sun",
    keywords: ["dark", "light", "appearance"],
    detail: (c) => `now: ${c.theme}`,
    placeholder: "Choose a theme",
    children: themeChoices,
  },
  {
    id: "display.text_size",
    title: "Text size",
    category: "Display",
    icon: "text",
    keywords: ["font", "zoom", "tv", "standard"],
    detail: (c) => `now: ${c.textSize === "large" ? "large (TV)" : "standard"}`,
    placeholder: "Choose a text size",
    children: sizeChoices,
  },
  // ── Configure ──
  {
    id: "cfg.new",
    title: "New standup",
    category: "Configure",
    icon: "plus",
    keywords: ["add", "create"],
    run: () => createStandup(),
  },
  {
    id: "cfg.duplicate",
    title: "Duplicate this standup",
    category: "Configure",
    icon: "copy",
    keywords: ["copy", "clone"],
    available: hasStandup,
    run: (c) => duplicateStandup(c.standupId!),
  },
  // ── App ──
  {
    id: "app.diagnostics",
    title: "Copy diagnostics",
    category: "App",
    icon: "copy",
    keywords: ["debug", "support", "log"],
    run: copyDiagnostics,
  },
  {
    id: "app.update.check",
    title: "Check for updates",
    category: "App",
    icon: "download",
    keywords: ["upgrade", "release"],
    available: (c) => (c.update.channel === "" ? "not in this build" : true),
    run: checkForUpdates,
  },
  {
    id: "app.update.install",
    title: "Install update",
    category: "App",
    icon: "download",
    keywords: ["upgrade"],
    detail: (c) => (c.update.available ? c.update.available.version : undefined),
    available: (c) => (updateAvailable(c.update) ? true : "none waiting"),
    run: () => {
      updateDialogOpen.value = true;
    },
  },
  {
    id: "app.reconnect",
    title: "Reconnect Jira",
    category: "App",
    icon: "resync",
    keywords: ["token", "login"],
    visible: (c) => c.authInvalid,
    run: () => goConnect(),
  },
  // ── Ticket (listed only with a ticket: the open modal, or the best match of the query) ──
  {
    id: "ticket.refresh",
    title: (c) => `Refresh ${c.ticket?.key ?? "ticket"} from Jira`,
    category: "Ticket",
    icon: "resync",
    keywords: ["re-fetch"],
    detail: () => "re-fetch just this ticket, then open",
    visible: (c) => c.ticket !== null,
    run: async (c) => {
      const t = c.ticket!;
      await openTicketIn(t.standupId, t.issueId);
      await refreshTicket({ standupId: t.standupId, issueId: t.issueId });
    },
  },
  {
    id: "ticket.open",
    title: (c) => `Open ${c.ticket?.key ?? "ticket"} in Jira`,
    category: "Ticket",
    icon: "external",
    shortcut: isMac() ? "⌘↵" : "Ctrl↵",
    keywords: ["browser"],
    visible: (c) => c.ticket !== null,
    run: (c) => openIssueKey(c.ticket!.key),
  },
  {
    id: "ticket.copy",
    title: (c) => `Copy ${c.ticket?.key ?? "ticket"}`,
    category: "Ticket",
    icon: "copy",
    keywords: ["key"],
    visible: (c) => c.ticket !== null,
    run: (c) => copyText(c.ticket!.key, `Copied ${c.ticket!.key}`),
  },
];

registerCommands(BUILTIN);
