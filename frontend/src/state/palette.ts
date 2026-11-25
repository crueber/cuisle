// Command palette state (F14, ux-spec §15). The EAGER half: whether the palette is open, the platform's shortcut label,
// and the recents, which must be recorded from every ticket-open path. The dialog, the command registry and the scorer
// are a lazy chunk (views/palette, commands) loaded on first open, so this file stays tiny.
import { effect, signal, untracked } from "@preact/signals";
import { setAppSetting, settings } from "./app";
import { route } from "./route";
import { ticketSession } from "./ticket";

/** The prefix that selects a section only: `>` commands, `#` tickets, `@` people ("" = everything). */
export type PaletteMode = "" | ">" | "#" | "@";

/** Open palette (the mode it opens in), or null. */
export const paletteOpen = signal<{ mode: PaletteMode } | null>(null);

export const RECENT_KEY = "palette.recent_tickets";
export const RECENT_MAX = 8;

/** ⌘ on macOS, Ctrl elsewhere. */
export const isMac = (): boolean => /Mac|iPhone|iPad/.test(navigator.platform);
export const findKeyLabel = (): string => (isMac() ? "⌘K" : "Ctrl K");

/** The element to give the focus back to when the palette closes. It is captured when the palette is OPENED (openPalette, in
 *  the same synchronous step as the key press or click), because the lazy dialog may mount late and by then something else
 *  (a re-render, the page) may have taken the focus. If nothing had focus at that moment, the dialog adopts whatever has it
 *  as it mounts (adoptOpener): over the ticket modal, the modal's own focus step may land just after the key press. */
let opener: HTMLElement | null = null;

const usable = (a: Element | null): a is HTMLElement => a instanceof HTMLElement && a !== document.body && a !== document.documentElement;

/** Opens the palette on the standup or Configure screen (never on the Connect screen, which has nothing to find). */
export function openPalette(mode: PaletteMode = ""): void {
  if (route.peek().view === "connect") return;
  if (paletteOpen.peek() === null) {
    const a = document.activeElement;
    opener = usable(a) ? a : null;
  }
  paletteOpen.value = { mode };
}

export function closePalette(): void {
  paletteOpen.value = null;
}

/** The dialog, as it mounts: when the open-time capture found nothing, the element that has the focus now is the opener. */
export function adoptOpener(active: Element | null, dialog: Element | null): void {
  if (opener === null && usable(active) && !dialog?.contains(active)) opener = active;
}

/** Gives the focus back to the element that opened the palette (the dialog calls it as it unmounts), once. An opener that
 *  has left the page falls back to the ticket modal when one is open, so Esc from the palette over the modal always lands
 *  in the modal. */
export function restoreFocus(): void {
  const el = opener;
  opener = null;
  if (el && document.contains(el)) {
    el.focus();
    return;
  }
  if (ticketSession.peek() !== null) document.querySelector<HTMLElement>(".tm")?.focus();
}

/** Recents not saved yet: stepping J/K through tickets saves once, 1 s after the last step (a UI timer, no request). */
let pending: number[] | null = null;
let saveTimer: ReturnType<typeof setTimeout> | undefined;

/** The recent issue ids, most recent first (a corrupt setting reads as none). */
export function recentTicketIds(): number[] {
  if (pending) return pending;
  try {
    const v: unknown = JSON.parse(settings.peek()[RECENT_KEY] || "[]");
    return Array.isArray(v) ? v.slice(0, RECENT_MAX) : [];
  } catch {
    return [];
  }
}

/** Moves an issue to the front of the recents (max 8) and saves the app setting. */
export function recordRecent(issueId: number): void {
  const ids = recentTicketIds();
  if (ids[0] === issueId) return;
  pending = [issueId, ...ids.filter((x) => x !== issueId)].slice(0, RECENT_MAX);
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    setAppSetting(RECENT_KEY, JSON.stringify(pending));
    pending = null;
  }, 1000);
}

/** Records every ticket the modal shows, whichever path opened it (palette, row, inline card, stepping). */
export function installRecents(): () => void {
  return effect(() => {
    const s = ticketSession.value;
    if (s) untracked(() => recordRecent(s.issueId));
  });
}
