// Shared actions of the palette and its commands (F14): open a ticket in a standup's modal, copy to the clipboard.
import { leaveConfigure } from "../state/config";
import { goStandup, route } from "../state/route";
import { refreshView, switchStandup, views } from "../state/standup";
import { boardSteps, stepsFor } from "../state/ticketSteps";
import { openTicket } from "../state/ticket";
import { showToast } from "../state/ui";

/**
 * Opens the ticket modal on an issue in a standup (ux-spec §15.3): leaves Configure, shows that standup behind the modal,
 * and steps through that standup's own view order. An issue that is not on that screen (done, or no longer on the board)
 * opens alone. Local reads only; the view is requested from Go only when it was never loaded.
 */
export async function openTicketIn(standupId: number, issueId: number): Promise<void> {
  if (route.value.view === "configure") await leaveConfigure();
  const r = route.value;
  if (r.view !== "standup") return;
  if (r.standupId !== standupId) await switchStandup(standupId);
  const view = views.value.get(standupId) ?? (await refreshView(standupId));
  const now = route.value;
  const person = now.view === "standup" && now.standupId === standupId ? now.person : null;
  const steps = [stepsFor(view, person), boardSteps(view)].find((s) => s.includes(issueId)) ?? [issueId];
  openTicket(standupId, issueId, steps);
}

/** `@`: shows a person on their standup (leaving Configure first), the whole `goStandup(standupId, accountId)` of the spec. */
export async function goPersonIn(standupId: number, accountId: string): Promise<void> {
  if (route.value.view === "configure") await leaveConfigure();
  if (route.value.view !== "standup") return;
  if (route.value.standupId !== standupId) await switchStandup(standupId);
  goStandup(standupId, accountId);
}

/** Copies text and confirms with a toast; a webview that refuses the clipboard says nothing. */
export function copyText(text: string, message: string): void {
  void navigator.clipboard?.writeText(text).then(
    () => showToast({ message, timeoutMs: 2500 }),
    () => undefined,
  );
}
