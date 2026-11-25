// Zoom of the standup screen content (F15, ux-spec §16): one level per standup, shared by its person view and Whole board.
// Zoom is a CSS scale on everything below the header, and every layout function re-runs on the effective size, so a
// zoomed screen reflows (columns, density, rail) instead of just magnifying. Persisted at once (UI state, no debounce).
import { signal } from "@preact/signals";
import { api } from "../api/index";
import { route } from "./route";

/** The nine steps (ux-spec §16.2), in percent. */
export const ZOOM_STEPS: readonly number[] = [70, 80, 90, 100, 110, 125, 150, 175, 200];
export const ZOOM_DEFAULT = 100;

/** Zoom per standup id (absent = 100). Seeded from boot.ui.zoom, written through on every change. */
export const zooms = signal<ReadonlyMap<number, number>>(new Map());

export const zoomOf = (standupId: number): number => zooms.value.get(standupId) ?? ZOOM_DEFAULT;

/** The zoom of the standup on screen (100 off the standup screen). */
export function currentZoom(): number {
  const r = route.value;
  return r.view === "standup" ? zoomOf(r.standupId) : ZOOM_DEFAULT;
}

/** Seeds the per-standup levels from the boot payload; a value that is not one of the steps reads as 100. */
export function seedZooms(raw: Record<string, number | undefined> | null | undefined): void {
  const m = new Map<number, number>();
  for (const [id, v] of Object.entries(raw ?? {})) if (v !== undefined && ZOOM_STEPS.includes(v) && v !== ZOOM_DEFAULT) m.set(Number(id), v);
  zooms.value = m;
}

/** Bumped on every change so the control can flash its toast; `at` is the percent the change landed on. */
export const zoomFlash = signal<{ seq: number; percent: number }>({ seq: 0, percent: ZOOM_DEFAULT });

// The lane (board) or group (person) at the top of the scroll area, remembered across the reflow (ux-spec §16.3).
let anchorKey: string | null = null;

function scroller(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".person-scroll") ?? document.querySelector<HTMLElement>(".sf-main");
}

/**
 * The topmost lane or group that is fully visible (a `data-anchor` element), by its key. Nothing is remembered while the
 * screen is at its top: it simply stays there (on the board the title row scrolls with the lanes, and it must stay in view).
 */
export function captureAnchor(): void {
  anchorKey = null;
  const box = scroller();
  if (!box || box.scrollTop <= 1) return;
  const top = box.getBoundingClientRect().top;
  let best: { key: string; y: number } | null = null;
  box.querySelectorAll<HTMLElement>("[data-anchor]").forEach((el) => {
    const y = el.getBoundingClientRect().top - top;
    if (y < -1) return; // cut off at the top: not fully visible
    if (!best || y < best.y) best = { key: el.dataset.anchor ?? "", y };
  });
  anchorKey = (best as { key: string } | null)?.key ?? null;
}

/** After the reflow: scrolls so the remembered lane or group is at the top again. No-op when nothing was remembered. */
export function restoreAnchor(): void {
  const key = anchorKey;
  anchorKey = null;
  if (key === null) return;
  const box = scroller();
  if (!box) return;
  const el = Array.from(box.querySelectorAll<HTMLElement>("[data-anchor]")).find((e) => e.dataset.anchor === key);
  if (!el) return;
  box.scrollTop += el.getBoundingClientRect().top - box.getBoundingClientRect().top;
}

/** Sets one standup's zoom and persists it. Returns false when it is already that level. */
export function setZoom(standupId: number, percent: number): boolean {
  if (!ZOOM_STEPS.includes(percent) || zoomOf(standupId) === percent) return false;
  captureAnchor();
  const next = new Map(zooms.value);
  if (percent === ZOOM_DEFAULT) next.delete(standupId);
  else next.set(standupId, percent);
  zooms.value = next;
  zoomFlash.value = { seq: zoomFlash.value.seq + 1, percent };
  api()
    .app.setUIState({ standupId, zoom: percent })
    .catch(() => undefined);
  return true;
}

/** The step after (dir 1) or before (dir -1) the level; a level between steps moves to the next one in that direction. */
export function stepFrom(percent: number, dir: 1 | -1): number {
  if (dir === 1) return ZOOM_STEPS.find((s) => s > percent) ?? percent;
  return [...ZOOM_STEPS].reverse().find((s) => s < percent) ?? percent;
}

/** + / −: steps the zoom of the standup on screen. Returns true when the key was for the standup screen (even at an end). */
export function zoomBy(dir: 1 | -1): boolean {
  const r = route.value;
  if (r.view !== "standup") return false;
  setZoom(r.standupId, stepFrom(zoomOf(r.standupId), dir));
  return true;
}

/** 0: back to 100%. */
export function resetZoom(): boolean {
  const r = route.value;
  if (r.view !== "standup") return false;
  setZoom(r.standupId, ZOOM_DEFAULT);
  return true;
}
