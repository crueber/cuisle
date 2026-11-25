// Step lists for the ticket modal's J/K (ux-spec §14.1, testing §4 "Stepping order") and the row-to-modal opener.
// Pure builders over a StandupView; `openTicketFromRow` reads the current route and view. No facade call is made
// when a modal opens (AGENTS invariant 2): the only call is the fallback "open in Jira" for a backlog row that has
// no local issue id yet.
import { api } from "../api/index";
import { toAppError } from "../api/errors";
import type { StandupView, TicketView } from "../api/types";
import { openTicket } from "./ticket";
import { statusSections, type BoardGroup } from "../layout/board";
import { boardGroupOf, buildBoardLanes, currentView } from "./standup";
import { route } from "./route";
import { showToast } from "./ui";

const mineOf = (view: StandupView, person: string): TicketView[] =>
  (view.tickets ?? []).filter((t) => (person === "unassigned" ? t.assignee === "" : t.assignee === person));

const dedupe = (ids: number[]): number[] => Array.from(new Set(ids));

/** Person view: the person's tickets in walk order (lane by lane, board rank inside a lane), then Done. */
export function personSteps(view: StandupView, person: string): number[] {
  const mine = mineOf(view, person);
  const out: number[] = [];
  for (const lane of view.lanes ?? []) for (const t of mine) if (t.lane === lane.lane) out.push(t.id);
  return dedupe(out);
}

/**
 * Whole board, in on-screen order: the lanes top to bottom (each speaker's active tickets), or, grouped by status, the
 * sections in walk order with their rows sorted as drawn (ux-spec §6.5); then every Done ticket, then the backlog.
 */
export function boardSteps(view: StandupView, group: BoardGroup = "assignee"): number[] {
  const out: number[] = [];
  if (group === "status") for (const s of statusSections(view)) for (const t of s.tickets) out.push(t.id);
  else for (const box of buildBoardLanes(view)) for (const t of box.tickets) out.push(t.id);
  for (const t of view.tickets ?? []) if (t.lane === "done") out.push(t.id);
  for (const b of view.backlog ?? []) if (b.issueId > 0) out.push(b.issueId);
  return dedupe(out);
}

/** The step list for the screen the user is on: the person view when a person is selected, else the board. */
export function stepsFor(view: StandupView, person: string | null, group: BoardGroup = boardGroupOf(view.standupId)): number[] {
  return person === null ? boardSteps(view, group) : personSteps(view, person);
}

/** Resolves a row's key to its local issue id (a ticket of the view, or a backlog item); 0 when unknown. */
export function issueIdOfKey(view: StandupView, key: string): number {
  return (view.tickets ?? []).find((t) => t.key === key)?.id ?? (view.backlog ?? []).find((b) => b.key === key)?.issueId ?? 0;
}

/**
 * A row was clicked (or Enter was pressed on it): opens the modal on that issue with the current screen's step list.
 * Falls back to the browser only when the row has no local issue yet.
 */
export function openTicketFromRow(key: string): void {
  const r = route.value;
  const view = currentView.value;
  if (r.view !== "standup" || !view) return;
  const id = issueIdOfKey(view, key);
  if (id <= 0) {
    api()
      .app.openIssue(key)
      .catch((e: unknown) => showToast({ message: toAppError(e).message, timeoutMs: 6000 }));
    return;
  }
  const steps = stepsFor(view, r.person);
  openTicket(view.standupId, id, steps.includes(id) ? steps : [id]);
}
