// Auto-update state (ADR 0016, ux-spec §2.1 Update button, §9.4 Display). Go decides when to check (app start when
// the last check is a day old, the day rollover, or Check now) and sends update:status; this module only mirrors it.
import { signal } from "@preact/signals";
import { api } from "../api/index";
import { toAppError } from "../api/errors";
import { on } from "../api/events";
import type { UpdateStatus } from "../api/types";

/** A build with no channel: no Update button, no checks (also the state before the first Status call returns). */
export const NO_UPDATE: UpdateStatus = {
  channel: "", currentVersion: "", enabled: true, lastCheckedAt: 0, lastError: "", available: null,
  phase: "idle", progress: 0, error: "", errorCode: "",
};

export const updateStatus = signal<UpdateStatus>(NO_UPDATE);
/** The Update confirm dialog (opened from the header button). */
export const updateDialogOpen = signal(false);
/** Check now is running (the button shows "Checking…"). */
export const checkingNow = signal(false);
/** The last Check now failure, shown under the button ("" = none). */
export const checkError = signal("");

/** Whether the header shows the Update button: a channel build with a newer release. */
export function updateAvailable(s: UpdateStatus = updateStatus.value): boolean {
  return s.channel !== "" && s.available !== null;
}

/** Whether an install is under way (the dialog shows progress and cannot start another). */
export function installing(s: UpdateStatus = updateStatus.value): boolean {
  return s.phase === "downloading" || s.phase === "verifying" || s.phase === "installing" || s.phase === "restarting";
}

/** Reads the status once at start (local only: no request). */
export async function loadUpdateStatus(): Promise<void> {
  try {
    updateStatus.value = await api().update.status();
  } catch {
    updateStatus.value = NO_UPDATE;
  }
}

export function installUpdateEvents(): () => void {
  return on("update:status", (s) => {
    updateStatus.value = s;
  });
}

/** Configure → Display → Check now: one releases-list request. */
export async function checkNow(): Promise<void> {
  if (checkingNow.value) return;
  checkingNow.value = true;
  checkError.value = "";
  try {
    updateStatus.value = await api().update.checkNow();
  } catch (e) {
    checkError.value = toAppError(e).message || "Couldn't check for updates.";
  } finally {
    checkingNow.value = false;
  }
}

/** Install and restart. Progress and failures arrive through update:status; a rejection is mirrored there too. */
export async function installUpdate(): Promise<void> {
  if (installing()) return;
  try {
    await api().update.install();
  } catch (e) {
    const err = toAppError(e);
    if (updateStatus.value.phase !== "failed") {
      updateStatus.value = { ...updateStatus.value, phase: "failed", error: err.message || "Couldn't install the update.", errorCode: "install" };
    }
  }
}

/** Opens the release page in the browser (What's new, and the fallback when this copy can't replace itself). */
export function openReleasePage(): void {
  const url = updateStatus.value.available?.pageUrl;
  if (url) void api().app.openUrl(url).catch(() => {});
}
