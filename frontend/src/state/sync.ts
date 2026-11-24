// FROZEN SIGNATURES (architecture §5.4, jira-sync §4.7). Implementation: WP-15.
import { signal } from "@preact/signals";
import { api } from "../api/index";
import { toAppError } from "../api/errors";
import { on } from "../api/events";
import type { ConnectionAuth, SyncFinished, SyncMode, SyncStarted, SyncStatus } from "../api/types";
import { connection } from "./app";
import { refreshView } from "./standup";
import { goConnect } from "./route";
import { banners, dismissBanner, showBanner, type BannerKind } from "./ui";

export const syncStatus = signal<ReadonlyMap<number, SyncStatus>>(new Map());

/** The trigger of each run we have seen start; only "manual" runs get banners. */
const runTrigger = new Map<number, string>();

function blank(standupId: number): SyncStatus {
  return {
    standupId, runId: 0, state: "idle", mode: "", phase: "", done: 0, total: 0, lastSuccessAt: 0, lastAttemptAt: 0,
    lastStatus: "", warnings: [], errorCode: "", errorMessage: "", needsSync: false,
  } as SyncStatus;
}

function patchStatus(standupId: number, patch: Partial<SyncStatus>): void {
  const m = new Map(syncStatus.value);
  m.set(standupId, { ...(m.get(standupId) ?? blank(standupId)), ...patch });
  syncStatus.value = m;
}

/** Shows the auth banner once per kind; the Connect action opens the Connect screen. */
function raiseAuth(kind: "auth_missing" | "auth_invalid"): void {
  if (banners.value.some((b) => b.kind === kind)) return;
  if (kind === "auth_missing") {
    showBanner({ kind, message: "Connect to Jira to sync", action: { label: "Connect", run: goConnect }, dismissible: false });
  } else {
    showBanner({ kind, message: "Jira rejected the token — Reconnect", action: { label: "Connect", run: goConnect }, dismissible: false });
  }
}

function clearAuthBanners(): void {
  const drop = new Set<BannerKind>(["auth_missing", "auth_invalid"]);
  for (const b of banners.value) if (drop.has(b.kind)) dismissBanner(b.id);
}

/** "3 h", "2 days" … for the offline banner. */
// Timestamps from Go are Unix milliseconds (architecture conventions).
function ago(unixMs: number): string {
  const s = Math.max(0, (Date.now() - unixMs) / 1000);
  if (s < 3600) return `${Math.max(1, Math.round(s / 60))} min`;
  if (s < 48 * 3600) return `${Math.round(s / 3600)} h`;
  return `${Math.round(s / 86400)} days`;
}

export async function startSync(standupId: number, mode: SyncMode): Promise<void> {
  try {
    await api().sync.start(standupId, mode);
  } catch (e) {
    const err = toAppError(e);
    if (err.code === "auth_missing" || err.code === "auth_invalid") {
      raiseAuth(err.code);
    } else if (err.code === "offline") {
      if (!banners.value.some((b) => b.kind === "offline")) {
        showBanner({ kind: "offline", message: "Offline — showing cached data", dismissible: true });
      }
    } else {
      showBanner({ kind: "error", message: err.message, dismissible: true });
    }
  }
}

export async function cancelSync(standupId: number): Promise<void> {
  try {
    await api().sync.cancel(standupId);
  } catch (e) {
    showBanner({ kind: "error", message: toAppError(e).message, dismissible: true });
  }
}

function onStarted(p: SyncStarted): void {
  runTrigger.set(p.runId, p.trigger);
  patchStatus(p.standupId, { runId: p.runId, state: "running", mode: p.mode, phase: "", done: 0, total: 0 });
}

function onProgress(p: { runId: number; standupId: number; phase: string; done: number; total: number }): void {
  // The media phase keeps ticking after sync:finished (jira-sync §2.14), and a progress event can outlive its run: only
  // the run that is currently running may move the status (a late tick must not turn an idle standup into a progress bar).
  const cur = syncStatus.value.get(p.standupId);
  if (!cur || cur.state !== "running" || cur.runId !== p.runId) return;
  patchStatus(p.standupId, { phase: p.phase, done: p.done, total: p.total });
}

function onFinished(p: SyncFinished): void {
  const trigger = runTrigger.get(p.runId);
  runTrigger.delete(p.runId);
  const warnings = p.warnings ?? [];
  if (p.status === "ok" || p.status === "partial") {
    patchStatus(p.standupId, {
      state: "idle", lastStatus: p.status, warnings, lastSuccessAt: p.finishedAt, lastAttemptAt: p.finishedAt,
      errorCode: "", errorMessage: "",
    });
    void refreshView(p.standupId).catch(() => undefined);
    return;
  }
  if (p.status === "cancelled") {
    patchStatus(p.standupId, { state: "idle", lastStatus: "cancelled", warnings, errorCode: "", errorMessage: "" });
    return;
  }
  patchStatus(p.standupId, {
    state: "failed", lastStatus: "failed", warnings, errorCode: p.errorCode ?? "", errorMessage: p.errorMessage ?? "",
  });
  // Banners only for failures of a sync the user started; sync-on-open stays quiet (ux-spec §2.1).
  if (trigger === "manual" && p.errorCode === "offline") {
    if (banners.value.some((b) => b.kind === "offline")) return;
    const last = syncStatus.value.get(p.standupId)?.lastSuccessAt ?? 0;
    const message = last > 0 ? `Offline — showing data from ${ago(last)} ago` : "Offline — no data synced yet";
    showBanner({ kind: "offline", message, dismissible: true });
  }
}

function onAuth(p: ConnectionAuth): void {
  const c = connection.value;
  if (c) connection.value = { ...c, state: p.state };
  if (p.state === "ok") clearAuthBanners();
  else if (p.state === "missing") raiseAuth("auth_missing");
  else raiseAuth("auth_invalid");
}

/** Subscribes to sync:* and connection:auth (refreshes the view on ok|partial, raises banners for user-started failures). */
export function installSyncEvents(): () => void {
  const offs = [
    on("sync:started", onStarted),
    on("sync:progress", onProgress),
    on("sync:finished", onFinished),
    on("connection:auth", onAuth),
  ];
  return () => {
    for (const off of offs) off();
  };
}
