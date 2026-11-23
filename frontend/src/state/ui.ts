// FROZEN SIGNATURES. Implementation: WP-15. Transient UI state: toasts, banners, popovers.
import { signal } from "@preact/signals";

export interface ToastSpec {
  id: number;
  message: string;
  action?: { label: string; run: () => void };
  timeoutMs: number; // 10 000 for undo toasts
}

export type BannerKind = "offline" | "auth_missing" | "auth_invalid" | "recovered" | "error";

export interface BannerSpec {
  id: number;
  kind: BannerKind;
  message: string;
  action?: { label: string; run: () => void };
  dismissible: boolean;
}

export const toasts = signal<ToastSpec[]>([]);
export const banners = signal<BannerSpec[]>([]);
export const keyLegendOpen = signal(false);
export const switcherOpen = signal(false);

let seq = 0;

/** Shows a toast that removes itself after its own timeoutMs. */
export function showToast(t: Omit<ToastSpec, "id">): number {
  const id = ++seq;
  toasts.value = [...toasts.value, { ...t, id }];
  setTimeout(() => dismissToast(id), t.timeoutMs);
  return id;
}

export function dismissToast(id: number): void {
  toasts.value = toasts.value.filter((t) => t.id !== id);
}

/** Runs a toast's action once: the toast is removed first, so a second click or Ctrl+Z cannot run it again. */
export function runToastAction(id: number): void {
  const t = toasts.value.find((x) => x.id === id);
  if (!t) return;
  dismissToast(id);
  t.action?.run();
}

/** Ctrl/Cmd+Z while an Undo toast is visible (architecture §5.3): runs the newest one. Returns true when it ran. */
export function undoLatestToast(): boolean {
  for (let i = toasts.value.length - 1; i >= 0; i--) {
    const t = toasts.value[i]!;
    if (t.action?.label === "Undo") {
      runToastAction(t.id);
      return true;
    }
  }
  return false;
}

/** Shows a banner. Banners never expire on their own. */
export function showBanner(b: Omit<BannerSpec, "id">): number {
  const id = ++seq;
  banners.value = [...banners.value, { ...b, id }];
  return id;
}

export function dismissBanner(id: number): void {
  banners.value = banners.value.filter((b) => b.id !== id);
}
