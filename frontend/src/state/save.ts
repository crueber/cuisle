// FROZEN SIGNATURES (architecture §5.3). Implementation: WP-15. Optimistic updates + save.enqueue.
// Per-key serial queue, last-write-wins coalescing, 400 ms text debounce, "Saving…" only after 250 ms, Saved for 2 s
// then "All changes saved", retry 3× (0.5 s, 1 s, 2 s) for retryable errors, validation errors stay on the field.
import { signal } from "@preact/signals";
import { toAppError, type AppError } from "../api/errors";
import { api } from "../api/index";

export type SaveState = "idle" | "saving" | "saved" | "settled" | "failed";

export const saveState = signal<SaveState>("idle"); // settled = "All changes saved"
export const fieldErrors = signal<ReadonlyMap<string, AppError>>(new Map()); // by key
export const failedKeys = signal<ReadonlySet<string>>(new Set());

export const TEXT_DEBOUNCE_MS = 400;
export const SAVING_DELAY_MS = 250;
export const SAVED_MS = 2000;
export const RETRY_DELAYS_MS = [500, 1000, 2000] as const;

export interface SaveJob<R = unknown> {
  key: string; // `${standupId}:${field}` | `app:${settingKey}` | `${standupId}:speakers` | "standups"
  run: () => Promise<R>; // carries the FULL new value (never a delta)
  debounceMs?: number; // text inputs: TEXT_DEBOUNCE_MS; omit = immediate
  onSaved?: (result: R) => void;
  noRetry?: boolean; // network work (Jira calls) and ordered list deltas never retry (AGENTS invariant 2)
}

type Timer = ReturnType<typeof setTimeout>;

/** The type-erased job the queue keeps. */
interface Job {
  run: () => Promise<unknown>;
  onSaved?: (result: unknown) => void;
  noRetry?: boolean;
}

interface KeyState {
  busy: boolean; // a run chain is active (an attempt in flight or a retry waiting)
  pending: Job | null; // newest job that has not started yet
  debounce: Timer | null; // debounce timer that holds `pending` back
  retry: Timer | null; // retry backoff timer
  last: Job | null; // last job that ran (retryFailed re-runs it)
  attempts: number; // retries used in the current chain
}

let keys = new Map<string, KeyState>();
let busyCount = 0; // keys whose run chain is active
let gen = 0; // bumped by resetSaveQueue so stale completions are ignored
let lastOk = false; // the most recent final outcome was a success
let savingTimer: Timer | null = null;
let settleTimer: Timer | null = null;
let waiters: Array<() => void> = [];

function stateOf(key: string): KeyState {
  let st = keys.get(key);
  if (!st) {
    st = { busy: false, pending: null, debounce: null, retry: null, last: null, attempts: 0 };
    keys.set(key, st);
  }
  return st;
}

function setState(next: SaveState): void {
  if (saveState.value !== next) saveState.value = next;
}

function clearSettle(): void {
  if (settleTimer) clearTimeout(settleTimer);
  settleTimer = null;
}

function report(e: unknown): void {
  const err = e instanceof Error ? e : new Error(String(e));
  void api().app.logClientError(err.message, err.stack ?? "").catch(() => undefined);
}

/** Recomputes the indicator after a transition (a run started or finished). */
function refresh(): void {
  if (busyCount > 0) {
    clearSettle();
    if (!savingTimer) {
      savingTimer = setTimeout(() => {
        savingTimer = null;
        if (busyCount > 0) setState("saving");
      }, SAVING_DELAY_MS);
    }
    return;
  }
  if (savingTimer) clearTimeout(savingTimer);
  savingTimer = null;
  if (failedKeys.value.size > 0) {
    clearSettle();
    setState("failed");
    return;
  }
  if (lastOk) {
    setState("saved");
    clearSettle();
    settleTimer = setTimeout(() => {
      settleTimer = null;
      if (saveState.value === "saved") saveState.value = "settled";
    }, SAVED_MS);
    return;
  }
  clearSettle();
  setState("idle");
}

function isIdle(): boolean {
  for (const st of keys.values()) if (st.busy || st.pending) return false;
  return true;
}

function notifyIdle(): void {
  if (waiters.length === 0 || !isIdle()) return;
  const w = waiters;
  waiters = [];
  for (const resolve of w) resolve();
}

/** Starts the pending job for a key unless the key is busy or still debouncing. Returns true when a run started. */
function startNext(key: string, st: KeyState): boolean {
  if (st.busy || st.debounce || !st.pending) return false;
  const job = st.pending;
  st.pending = null;
  st.busy = true;
  st.attempts = 0;
  busyCount++;
  attempt(key, st, job, gen);
  return true;
}

function attempt(key: string, st: KeyState, job: Job, g: number): void {
  st.last = job;
  let p: Promise<unknown>;
  try {
    p = Promise.resolve(job.run());
  } catch (e) {
    p = Promise.reject(e);
  }
  p.then(
    (r) => {
      if (g === gen) succeed(key, st, job, r);
    },
    (e: unknown) => {
      if (g === gen) failed(key, st, job, e, g);
    },
  );
}

function succeed(key: string, st: KeyState, job: Job, r: unknown): void {
  st.busy = false;
  busyCount--;
  st.attempts = 0;
  if (job.onSaved) {
    try {
      job.onSaved(r);
    } catch (e) {
      report(e);
    }
  }
  if (fieldErrors.value.has(key)) {
    const m = new Map(fieldErrors.value);
    m.delete(key);
    fieldErrors.value = m;
  }
  if (failedKeys.value.has(key)) {
    const s = new Set(failedKeys.value);
    s.delete(key);
    failedKeys.value = s;
  }
  lastOk = true;
  finishChain(key, st);
}

function failed(key: string, st: KeyState, job: Job, e: unknown, g: number): void {
  const err = toAppError(e);
  // Only pure database saves retry (db, timeout); anything that may have reached Jira never does.
  const transient = err.retryable && (err.code === "db" || err.code === "timeout") && !job.noRetry;
  if (transient && st.attempts < RETRY_DELAYS_MS.length) {
    const delay = RETRY_DELAYS_MS[st.attempts]!;
    st.attempts++;
    st.retry = setTimeout(() => {
      st.retry = null;
      let next = job;
      if (st.pending && !st.debounce) {
        next = st.pending; // a newer value supersedes the failed one
        st.pending = null;
        st.attempts = 0;
      }
      attempt(key, st, next, g);
    }, delay);
    return;
  }
  st.busy = false;
  busyCount--;
  st.attempts = 0;
  if (err.code === "validation") {
    const m = new Map(fieldErrors.value);
    m.set(key, err);
    fieldErrors.value = m;
  } else {
    const s = new Set(failedKeys.value);
    s.add(key);
    failedKeys.value = s;
  }
  lastOk = false;
  finishChain(key, st);
}

function finishChain(key: string, st: KeyState): void {
  startNext(key, st);
  refresh();
  notifyIdle();
}

/** Queues a save; a newer pending job for the same key replaces an older pending one. */
export function enqueue<R>(job: SaveJob<R>): void {
  const st = stateOf(job.key);
  const onSaved = job.onSaved;
  const internal: Job = {
    run: () => job.run(),
    onSaved: onSaved ? (r: unknown) => onSaved(r as R) : undefined,
    noRetry: job.noRetry,
  };
  st.pending = internal;
  if (st.debounce) {
    clearTimeout(st.debounce);
    st.debounce = null;
  }
  if (job.debounceMs !== undefined && job.debounceMs > 0) {
    const key = job.key;
    st.debounce = setTimeout(() => {
      st.debounce = null;
      if (startNext(key, st)) refresh();
    }, job.debounceMs);
    return;
  }
  if (startNext(job.key, st)) refresh();
}

/** Fires pending debounces now and resolves when the queue is idle (blur, Enter, route change, pagehide). */
export async function flush(): Promise<void> {
  let started = false;
  for (const [key, st] of keys) {
    if (st.debounce) {
      clearTimeout(st.debounce);
      st.debounce = null;
      started = startNext(key, st) || started;
    }
  }
  if (started) refresh();
  if (isIdle()) return;
  await new Promise<void>((resolve) => {
    waiters.push(resolve);
  });
}

/** Re-runs every failed key's last job. */
export function retryFailed(): void {
  let started = false;
  for (const key of failedKeys.value) {
    const st = keys.get(key);
    if (!st || !st.last || st.busy || st.pending || st.debounce) continue;
    st.pending = st.last;
    started = startNext(key, st) || started;
  }
  if (started) refresh();
}

/** Test helper: clears all queue state and timers. */
export function resetSaveQueue(): void {
  gen++;
  for (const st of keys.values()) {
    if (st.debounce) clearTimeout(st.debounce);
    if (st.retry) clearTimeout(st.retry);
  }
  keys = new Map();
  busyCount = 0;
  lastOk = false;
  if (savingTimer) clearTimeout(savingTimer);
  savingTimer = null;
  clearSettle();
  saveState.value = "idle";
  fieldErrors.value = new Map();
  failedKeys.value = new Set();
  const w = waiters;
  waiters = [];
  for (const resolve of w) resolve();
}
