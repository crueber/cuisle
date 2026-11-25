// Builds the command context (F14) from the live signals. Read when the list is rendered and again when a command runs,
// so a command always sees the state of that moment.
import { connection, settings, tabStandups } from "../state/app";
import type { PaletteMode } from "../state/palette";
import { route } from "../state/route";
import { boardGroupOf, views } from "../state/standup";
import { syncStatus } from "../state/sync";
import { ticketDetail, ticketSession } from "../state/ticket";
import { updateStatus } from "../state/update";
import { syncLabel } from "../views/standup/headerText";
import type { Ctx, TicketRef } from "./types";

export interface CtxExtras {
  /** The best ticket match of the query, when the modal is not open. */
  matched?: TicketRef | null;
  setMode?: (mode: PaletteMode) => void;
}

export function buildCtx(extras: CtxExtras = {}): Ctx {
  const r = route.value;
  const standupId = r.view === "standup" || r.view === "configure" ? r.standupId : null;
  const view = standupId === null ? null : (views.value.get(standupId) ?? null);
  const sync = standupId === null ? undefined : syncStatus.value.get(standupId);
  const s = settings.value;
  const theme = s.theme === "light" || s.theme === "dark" ? s.theme : "system";
  const session = ticketSession.value;
  const detail = ticketDetail.value;
  const open: TicketRef | null =
    session && detail && detail.issueId === session.issueId ? { issueId: detail.issueId, key: detail.row.key, standupId: session.standupId } : null;
  return {
    route: r,
    standupId,
    onStandup: r.view === "standup",
    onBoard: r.view === "standup" && r.person === null,
    view,
    tabs: tabStandups(),
    group: standupId === null ? "assignee" : boardGroupOf(standupId),
    sync,
    syncText: syncLabel(sync, view?.lastSyncedAt ?? 0, Date.now()).text,
    theme,
    textSize: s.text_size === "large" ? "large" : "standard",
    resolvedTheme: (document.documentElement.dataset.theme ?? theme) === "dark" ? "dark" : "light",
    update: updateStatus.value,
    authInvalid: connection.value?.state === "invalid",
    ticket: open ?? extras.matched ?? null,
    ui: { setMode: extras.setMode ?? (() => undefined) },
  };
}
