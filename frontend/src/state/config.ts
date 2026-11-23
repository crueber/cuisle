// FROZEN SIGNATURES (architecture §5.3, ux-spec §9). Implementation: WP-15. Optimistic updates + save.enqueue.
import { signal } from "@preact/signals";
import { api } from "../api/index";
import { toAppError } from "../api/errors";
import type {
  LaneInfo, MapLane, SaveAck, SpaceOption, BoardOption, SpeakerConfig, SpeakerSort, StandupConfig, StandupPatch, UserOption, WalkLane,
} from "../api/types";
import { refreshStandups, standups, tabStandups } from "./app";
import { enqueue, failedKeys, fieldErrors, flush, retryFailed, TEXT_DEBOUNCE_MS } from "./save";
import { goConfigure, goStandup, navigate, route } from "./route";
import { refreshView } from "./standup";
import { startSync } from "./sync";
import { showBanner, showToast } from "./ui";

export const config = signal<StandupConfig | null>(null);
export const configLoading = /* @__PURE__ */ signal(false);

/** The standup screen that was open before Configure (Done returns there). */
let returnTo: { standupId: number; person: string | null } | null = null;
route.subscribe((r) => {
  if (r.view === "standup") returnTo = { standupId: r.standupId, person: r.person };
});

/** Source fields change the data, so they need a sync first and do not re-request the view (architecture §5.3). */
const SOURCE_FIELDS = new Set<string>(["scope", "extraFilter"]);

// Every change of `config` that did not come from loadConfig bumps this token, so a late Get never overwrites it.
let loadToken = 0;

function setConfig(next: StandupConfig | null): void {
  loadToken++;
  draftText.clear();
  config.value = next;
}

// Typed text that has not been saved yet, by queue key; a server-shaped reply must not overwrite it.
const draftText = new Map<string, { id: number; field: string; value: string }>();

// List operations (speakers, standups) are deltas, so they must not coalesce: each gets its own queue key and runs
// after the previous operation on the same list. They never retry, so the order cannot be reshuffled.
const listTails = new Map<string, Promise<unknown>>();
let listSeq = 0;

function listOp<R>(list: string, run: () => Promise<R>, onSaved?: (result: R) => void): void {
  enqueue<R>({
    key: `${list}#${++listSeq}`,
    noRetry: true,
    run: () => {
      const prev = listTails.get(list) ?? Promise.resolve();
      const p = prev.then(() => run());
      listTails.set(list, p.catch(() => undefined));
      return p;
    },
    onSaved,
  });
}

function patchConfig(id: number, fn: (c: StandupConfig) => StandupConfig): void {
  const c = config.value;
  if (!c || c.id !== id) return;
  config.value = fn(c);
}

/** A server-shaped reply replaces the config, but keeps text the user typed that is still waiting to save. */
function replaceConfig(id: number, next: StandupConfig): void {
  let out = next;
  for (const d of draftText.values()) if (d.id === id) out = { ...out, [d.field]: d.value } as StandupConfig;
  if (config.value?.id === id) config.value = out;
}

function applyAck(id: number, ack: SaveAck): void {
  patchConfig(id, (c) => ({ ...c, rev: Math.max(c.rev, ack.rev), needsSync: c.needsSync || ack.needsSync }));
}

function refreshViewQuietly(id: number): void {
  void refreshView(id).catch(() => undefined);
}

/** Queues an acked save: the ack updates rev/needsSync, and the view is re-requested unless the data needs a sync. */
function saveAck(id: number, key: string, run: () => Promise<SaveAck>, view = true, debounceMs?: number): void {
  enqueue<SaveAck>({
    key: `${id}:${key}`,
    run,
    debounceMs,
    onSaved: (ack) => {
      applyAck(id, ack);
      if (view) refreshViewQuietly(id);
    },
  });
}

function fail(e: unknown): void {
  showBanner({ kind: "error", message: toAppError(e).message, dismissible: true });
}

/** Loads a standup's config; a reply that was overtaken by another load, a leave or an edit is dropped. */
export async function loadConfig(standupId: number): Promise<void> {
  const token = ++loadToken;
  configLoading.value = true;
  try {
    const c = await api().config.get(standupId);
    if (token === loadToken) config.value = c;
  } finally {
    if (token === loadToken) configLoading.value = false;
  }
}

/** Patches one field optimistically and autosaves it (text fields pass { text: true } for the 400 ms debounce). */
export function saveField<K extends keyof StandupPatch>(field: K, value: NonNullable<StandupPatch[K]>, opts?: { text?: boolean }): void {
  const c = config.value;
  if (!c) return;
  const id = c.id;
  const name = String(field);
  const key = `${id}:${name}`;
  config.value = { ...c, [name]: value } as StandupConfig;
  if (opts?.text) draftText.set(key, { id, field: name, value: String(value) });
  enqueue<SaveAck>({
    key,
    run: () => api().config.update(id, { [name]: value } as StandupPatch),
    debounceMs: opts?.text ? TEXT_DEBOUNCE_MS : undefined,
    onSaved: (ack) => {
      if (draftText.get(key)?.value === String(value)) draftText.delete(key);
      applyAck(id, ack);
      if (!SOURCE_FIELDS.has(name)) refreshViewQuietly(id);
    },
  });
}

// Server-shaped responses replace local state (SetSpace, SetBoard, SortSpeakers, AddSpeaker, Create, Duplicate).
// SetSpace and SetBoard call Jira, so they never retry.
export function setSpace(space: SpaceOption): void {
  const id = config.value?.id;
  if (id === undefined) return;
  enqueue<StandupConfig>({ key: `${id}:space`, noRetry: true, run: () => api().config.setSpace(id, space.id), onSaved: (next) => replaceConfig(id, next) });
}

export function setBoard(board: BoardOption): void {
  const id = config.value?.id;
  if (id === undefined) return;
  enqueue<StandupConfig>({
    key: `${id}:board`,
    noRetry: true,
    run: () => api().config.setBoard(id, board.id),
    onSaved: (next) => {
      replaceConfig(id, next);
      void refreshStandups().catch(() => undefined);
    },
  });
}

export function setColumnLane(columnName: string, lane: MapLane): void {
  const c = config.value;
  if (!c) return;
  config.value = { ...c, columns: c.columns.map((col) => (col.columnName === columnName ? { ...col, lane, auto: false } : col)) } as StandupConfig;
  saveAck(c.id, `column:${columnName}`, () => api().config.setColumnLane(c.id, columnName, lane), false);
}

/** Moves a column to toIndex among the columns mapped to the same lane (tickets in that lane follow this order). */
export function moveColumn(columnName: string, toIndex: number): void {
  const c = config.value;
  const col = c?.columns.find((x) => x.columnName === columnName);
  if (!c || !col) return;
  const group = c.columns.filter((x) => x.lane === col.lane && x !== col).sort((a, b) => a.order - b.order);
  group.splice(Math.max(0, Math.min(toIndex, group.length)), 0, col);
  const order = new Map(group.map((x, i) => [x.columnName, i]));
  config.value = { ...c, columns: c.columns.map((x) => ({ ...x, order: order.get(x.columnName) ?? x.order })) };
  const id = c.id;
  // An ordered list op, like speaker moves: every move is sent, in order (never merged into the latest).
  listOp<SaveAck>(`${id}:columnOrder`, () => api().config.moveColumn(id, columnName, toIndex), (ack) => {
    applyAck(id, ack);
    refreshViewQuietly(id);
  });
}

export function setWalkOrder(lanes: WalkLane[]): void {
  const c = config.value;
  if (!c) return;
  const byLane = new Map<string, LaneInfo>(c.lanes.map((l) => [l.lane, l]));
  const walk = lanes.map((l) => byLane.get(l)).filter((l): l is LaneInfo => l !== undefined);
  const done = byLane.get("done");
  config.value = { ...c, lanes: done ? [...walk, done] : walk };
  saveAck(c.id, "walkOrder", () => api().config.setWalkOrder(c.id, lanes));
}

export function setLaneLimit(lane: WalkLane, days: number): void {
  const c = config.value;
  if (!c) return;
  config.value = { ...c, lanes: c.lanes.map((l) => (l.lane === lane ? { ...l, limitDays: days } : l)) };
  saveAck(c.id, `limit:${lane}`, () => api().config.setLaneLimit(c.id, lane, days));
}

export function setSpeakerIncluded(accountId: string, included: boolean): void {
  const c = config.value;
  if (!c) return;
  config.value = { ...c, speakers: c.speakers.map((s) => (s.accountId === accountId ? { ...s, included } : s)) };
  saveAck(c.id, `included:${accountId}`, () => api().config.setSpeakerIncluded(c.id, accountId, included));
}

/** The list with one speaker moved to index `to` (after removal, as the server does) and positions dense again. */
function reorder(list: SpeakerConfig[], accountId: string, to: number): SpeakerConfig[] {
  const from = list.findIndex((s) => s.accountId === accountId);
  if (from < 0) return list;
  const rest = list.filter((_, i) => i !== from);
  rest.splice(Math.min(Math.max(to, 0), rest.length), 0, list[from]!);
  return rest.map((s, i) => ({ ...s, position: i }));
}

function sendMove(id: number, accountId: string, toPosition: number): void {
  listOp<SaveAck>(
    `${id}:speakers`,
    () => api().config.moveSpeaker(id, accountId, toPosition),
    (ack) => {
      applyAck(id, ack);
      refreshViewQuietly(id);
    },
  );
}

/** Moves a speaker: the list reorders at once, then the move is sent. */
export function moveSpeaker(accountId: string, toPosition: number): void {
  const id = config.value?.id;
  if (id === undefined) return;
  patchConfig(id, (c) => ({ ...c, speakers: reorder(c.speakers, accountId, toPosition) }));
  sendMove(id, accountId, toPosition);
}

function setSpeakers(id: number, list: SpeakerConfig[]): void {
  patchConfig(id, (cur) => ({ ...cur, speakers: list }));
  refreshViewQuietly(id);
}

export function sortSpeakers(mode: SpeakerSort): void {
  const id = config.value?.id;
  if (id === undefined) return;
  listOp<SpeakerConfig[]>(`${id}:speakers`, () => api().config.sortSpeakers(id, mode), (list) => setSpeakers(id, list));
}

/** Adds a speaker; `at` puts them at that index in the returned list (Undo of a removal restores the old place). */
function addSpeakerById(id: number, accountId: string, at?: number): void {
  listOp<SpeakerConfig[]>(`${id}:speakers`, () => api().config.addSpeaker(id, accountId), (list) =>
    setSpeakers(id, at === undefined ? list : reorder(list, accountId, at)),
  );
}

export function addSpeaker(user: UserOption): void {
  const id = config.value?.id;
  if (id !== undefined) addSpeakerById(id, user.accountId);
}

/** Removes a speaker (manual or from the board) with an Undo toast (re-AddSpeaker + MoveSpeaker back to the original place). */
export function removeSpeaker(accountId: string): void {
  const c = config.value;
  if (!c) return;
  const id = c.id;
  const at = c.speakers.findIndex((s) => s.accountId === accountId);
  const removed = c.speakers[at];
  if (!removed) return;
  config.value = { ...c, speakers: c.speakers.filter((_, i) => i !== at).map((s, i) => ({ ...s, position: i })) };
  listOp<SaveAck>(
    `${id}:speakers`,
    () => api().config.removeSpeaker(id, accountId),
    (ack) => {
      applyAck(id, ack);
      refreshViewQuietly(id);
    },
  );
  showToast({
    message: `Removed ${removed.name}`,
    action: {
      label: "Undo",
      run: () => {
        addSpeakerById(id, accountId, at);
        sendMove(id, accountId, at);
      },
    },
    timeoutMs: 10_000,
  });
}

let filterSeq = 0;

/** Validates the extra filter on blur/Enter via ConnectionService.ValidateFilter; only the newest answer for the current text counts. */
export function validateFilter(): void {
  const c = config.value;
  if (!c) return;
  const id = c.id;
  const expr = c.extraFilter;
  const seq = ++filterSeq;
  api()
    .connection.validateFilter(id, expr)
    .then(
      (check) => {
        const cur = config.value;
        if (seq === filterSeq && cur && cur.id === id && cur.extraFilter === expr) config.value = { ...cur, filterCheck: check };
      },
      fail,
    );
}

export async function createStandup(): Promise<void> {
  try {
    const c = await api().config.create();
    setConfig(c);
    goConfigure(c.id);
    await refreshStandups();
  } catch (e) {
    fail(e);
  }
}

export async function duplicateStandup(standupId: number): Promise<void> {
  try {
    const copy = await api().config.duplicate(standupId);
    setConfig(copy);
    goConfigure(copy.id);
    await refreshStandups();
  } catch (e) {
    fail(e);
  }
}

export async function copySettingsFrom(fromStandupId: number): Promise<void> {
  const c = config.value;
  if (!c) return;
  const id = c.id;
  try {
    const next = await api().config.copySettingsFrom(id, fromStandupId);
    replaceConfig(id, next);
    refreshViewQuietly(id);
  } catch (e) {
    fail(e);
  }
}

async function undoDelete(standupId: number): Promise<void> {
  try {
    await api().config.restore(standupId);
    await refreshStandups();
  } catch (e) {
    fail(e);
  }
}

/** Soft delete with a 10 s "Deleted “Name” · Undo" toast (Ctrl/Cmd+Z while visible). */
export async function deleteStandup(standupId: number): Promise<void> {
  const name = standups.value.find((t) => t.id === standupId)?.name ?? "Standup";
  try {
    await api().config.delete(standupId);
    await refreshStandups();
  } catch (e) {
    fail(e);
    return;
  }
  if (config.value?.id === standupId) {
    setConfig(null);
    const next = tabStandups()[0] ?? standups.value[0];
    if (next) {
      goConfigure(next.id);
      try {
        await loadConfig(next.id);
      } catch (e) {
        fail(e);
      }
    } else {
      // goConfigure(null) would resolve to the deleted id, so name the empty selection explicitly.
      navigate({ view: "configure", standupId: null });
    }
  }
  showToast({
    message: `Deleted “${name}”`,
    action: { label: "Undo", run: () => void undoDelete(standupId) },
    timeoutMs: 10_000,
  });
}

export async function moveStandup(standupId: number, toPosition: number): Promise<void> {
  listOp<SaveAck>("standups", () => api().config.move(standupId, toPosition), () => {
    void refreshStandups().catch(() => undefined);
  });
}

let leaving = false;

/**
 * Done/Esc: flush saves, DiscardDraft empty drafts, SyncService.Start(id,"full") for each standup the refreshed list
 * says needs a sync, return. A second call while one is running does nothing.
 */
export async function leaveConfigure(): Promise<void> {
  if (leaving) return;
  leaving = true;
  try {
    await flush();
    try {
      await refreshStandups();
    } catch {
      // keep the tabs we already have
    }

    let discarded = false;
    for (const t of standups.value) {
      if (!t.configured && t.spaceKey === "") {
        discarded = true;
        await api().config.discardDraft(t.id).catch(() => undefined);
      }
    }
    if (discarded) {
      try {
        await refreshStandups();
      } catch {
        // keep the tabs we already have
      }
    }

    // Only the server's own flag decides; a standup that does not need a sync never gets a full one.
    await Promise.all(standups.value.filter((t) => t.configured && t.needsSync).map((t) => startSync(t.id, "full")));

    const failed = failedKeys.value.size + fieldErrors.value.size;
    if (failed > 0) {
      showToast({
        message: `${failed} change${failed === 1 ? "" : "s"} not saved`,
        action: { label: "Retry", run: retryFailed },
        timeoutMs: 10_000,
      });
    }

    // The next visit to Configure loads fresh data.
    setConfig(null);
    const back = returnTo;
    const first = tabStandups()[0];
    if (back && standups.value.some((t) => t.id === back.standupId && t.configured)) {
      goStandup(back.standupId, back.person);
    } else if (first) {
      goStandup(first.id, null);
    } else {
      navigate({ view: "configure", standupId: standups.value[0]?.id ?? null });
    }
  } finally {
    leaving = false;
  }
}
