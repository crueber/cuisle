// FROZEN SIGNATURES (architecture §4.5). Implementation: WP-15.
import { signal } from "@preact/signals";
import { api } from "../api/index";
import type { Bootstrap, UIStatePatch } from "../api/types";

export type Route =
  | { view: "standup"; standupId: number; person: string | null } // person: accountId | "unassigned" | null (whole board)
  | { view: "configure"; standupId: number | null }
  | { view: "connect" };

export const route = signal<Route>({ view: "connect" });

const UI_PERSIST_MS = 500;
let uiTimer: ReturnType<typeof setTimeout> | null = null;
let pendingUIPatch: UIStatePatch | null = null;

/** Flushes any debounced UI-state persistence immediately (e.g. on pagehide). */
export function flushUIState(): void {
  if (!uiTimer) return;
  clearTimeout(uiTimer);
  uiTimer = null;
  const patch = pendingUIPatch;
  pendingUIPatch = null;
  if (patch) void api().app.setUIState(patch).catch(() => undefined);
}

/** connect when connection is missing and there are no standups; configure when only drafts exist; else the last standup/person. */
export function initialRoute(boot: Bootstrap): Route {
  const all = boot.standups;
  if (all.length === 0) {
    if (boot.connection.state === "missing") return { view: "connect" };
    return { view: "configure", standupId: null };
  }
  const configured = all.filter((t) => t.configured).sort((a, b) => a.position - b.position);
  if (configured.length === 0) return { view: "configure", standupId: all[0]!.id };
  const remembered = configured.find((t) => t.id === boot.ui.lastStandupId) ?? configured[0]!;
  const rememberedPerson = (boot.ui.lastPerson as Record<string, string | undefined>)[String(remembered.id)];
  return { view: "standup", standupId: remembered.id, person: rememberedPerson ? rememberedPerson : null };
}

/** The UI-state patch that describes a route (the last standup, its person, the last view). */
function patchFor(to: Route): UIStatePatch | null {
  if (to.view === "standup") {
    return { lastStandupId: to.standupId, lastView: "standup", standupId: to.standupId, lastPerson: to.person ?? "" };
  }
  if (to.view === "configure") {
    return to.standupId === null ? { lastView: "configure" } : { lastStandupId: to.standupId, lastView: "configure" };
  }
  return null;
}

/** Navigates; persists UI state via AppService.SetUIState debounced 500 ms (fire-and-forget). */
export function navigate(to: Route): void {
  route.value = to;
  if (uiTimer) clearTimeout(uiTimer);
  uiTimer = null;
  pendingUIPatch = null;
  const patch = patchFor(to);
  if (!patch) return;
  pendingUIPatch = patch;
  uiTimer = setTimeout(() => {
    uiTimer = null;
    pendingUIPatch = null;
    api().app.setUIState(patch).catch(() => undefined);
  }, UI_PERSIST_MS);
}

export function goStandup(standupId: number, person?: string | null): void {
  navigate({ view: "standup", standupId, person: person ?? null });
}

/** Opens Configure (null = the current standup). */
export function goConfigure(standupId: number | null): void {
  const r = route.value;
  const current = r.view === "standup" || r.view === "configure" ? r.standupId : null;
  navigate({ view: "configure", standupId: standupId ?? current });
}

/** Where Cancel / a finished reconnect returns to. */
let connectReturn: Route | null = null;

export function goConnect(): void {
  const r = route.value;
  if (r.view !== "connect") connectReturn = r;
  navigate({ view: "connect" });
}

/** Leaves the (re)connect screen for the route it was opened from. */
export function leaveConnect(): void {
  const back = connectReturn;
  connectReturn = null;
  if (route.value.view !== "connect") return;
  navigate(back && back.view !== "connect" ? back : { view: "configure", standupId: null });
}
