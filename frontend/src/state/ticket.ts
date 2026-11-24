// Ticket modal state (WP-T7; architecture §4.9, ux-spec §14). The open session (which issue, the step list, the inline-card
// back stack), the active issue's detail, optimistic light writes, the Refresh state machine, pending comments and the
// comment-draft autosave. `route.ts` is frozen and has no `route.ticket`, so the session lives here.
//
// Network rules (AGENTS invariant 2): opening, stepping and the back stack make no facade call; `loadTicket` calls only
// TicketService.Get (local). Writes, Refresh, transitions and drafts are explicit user triggers. A write is optimistic,
// reverted on failure, and NEVER re-sent by a timer: only the user's explicit Retry re-sends it. Every timer in this file
// is UI-only (the draft debounce, the Refresh countdown tick).
import { signal } from "@preact/signals";
import { toAppError, type AppError } from "../api/errors";
import { on } from "../api/events";
import { api } from "../api/index";
import type {
  CommentInput,
  PersonRef,
  RailRow,
  TicketDetail,
  TicketView,
  TransitionList,
  TransitionOption,
  UserOption,
} from "../api/types";
import type { EditField, PendingComment, RefreshState, WriteState } from "../views/ticket/props";
import { views } from "./standup";

export interface TicketSession {
  standupId: number;
  issueId: number;
  /** Issue ids in the current view's order (J/K). */
  steps: number[];
  /** Inline-card back stack: the issue ids left behind, oldest first. */
  stack: number[];
}

export const DRAFT_DEBOUNCE_MS = 400;
export const REFRESH_COOLDOWN_MS = 10_000;

const idleWrite = (): WriteState => ({ phase: "idle", reason: "" });
const idleWrites = (): Record<EditField, WriteState> => ({ status: idleWrite(), assignee: idleWrite(), flag: idleWrite() });
const idleRefresh = (): RefreshState => ({ phase: "idle", at: 0, cooldownMs: 0, waitMs: 0, reason: "" });
const emptyDraft = (): CommentInput => ({ text: "", mentions: [] });

// ── session ──
export const ticketSession = signal<TicketSession | null>(null);

/** Opens the modal for an issue (stack empty). Makes no facade call. */
export function openTicket(standupId: number, issueId: number, steps: number[]): void {
  ticketSession.value = { standupId, issueId, steps, stack: [] };
}

export function closeTicket(): void {
  void flushDraft();
  ticketSession.value = null;
}

/** J/K: moves to the neighbour in `steps`, clears the stack, stops at the ends. Returns whether it moved. */
export function stepTicket(delta: 1 | -1): boolean {
  const s = ticketSession.value;
  if (!s) return false;
  const at = s.steps.indexOf(s.issueId);
  const next = s.steps[at + delta];
  if (at < 0 || next === undefined) return false;
  ticketSession.value = { ...s, issueId: next, stack: [] };
  return true;
}

/** Stepper / modal onStep: opens another issue that is in `steps`, clears the stack. Returns whether it moved. */
export function stepToTicket(issueId: number): boolean {
  const s = ticketSession.value;
  if (!s || !s.steps.includes(issueId)) return false;
  ticketSession.value = { ...s, issueId, stack: [] };
  return true;
}

/** Inline card / issue row to a synced issue: pushes the current issue on the stack. */
export function openLinkedTicket(issueId: number): void {
  const s = ticketSession.value;
  if (!s) return;
  ticketSession.value = { ...s, issueId, stack: [...s.stack, s.issueId] };
}

/** Backspace / "← KEY": pops the stack. Returns false when the stack is empty. */
export function backTicket(): boolean {
  const s = ticketSession.value;
  if (!s || s.stack.length === 0) return false;
  const stack = s.stack.slice(0, -1);
  ticketSession.value = { ...s, issueId: s.stack[s.stack.length - 1]!, stack };
  return true;
}

/** Stepper position: 1-based index in `steps` (0 when the issue is not in the list) and the total. */
export function ticketPosition(s: TicketSession | null): { index: number; total: number } {
  if (!s) return { index: 0, total: 0 };
  return { index: s.steps.indexOf(s.issueId) + 1, total: s.steps.length };
}

// ── the active issue (what the modal shows) ──
export const ticketDetail = signal<TicketDetail | null>(null);
export const ticketError = signal<AppError | null>(null);
export const ticketWrites = signal<Record<EditField, WriteState>>(idleWrites());
export const ticketRefresh = signal<RefreshState>(idleRefresh());
/** Parts changed by a Resync while open ("Updated in Jira · Reload"); [] = no bar. */
export const ticketUpdated = signal<string[]>([]);
export const ticketPending = signal<PendingComment[]>([]);
export const ticketDraft = signal<CommentInput>(emptyDraft());

/** Per-issue UI state: write phases, the closures an explicit Retry re-runs, pending comments and the Refresh machine. */
interface Rec {
  writes: Record<EditField, WriteState>;
  retry: Partial<Record<EditField, () => Promise<void>>>;
  pending: PendingComment[];
  refresh: { phase: RefreshState["phase"]; at: number; reason: string; cooldownUntil: number; waitUntil: number };
}

let active: { standupId: number; issueId: number } | null = null;
let loadSeq = 0;
let localSeq = 0;
const recs = new Map<number, Rec>();
const keyCache = new Map<number, string>();
const knownUsers = new Map<string, UserOption>();
let unsubs: Array<() => void> = [];
let ticker: ReturnType<typeof setTimeout> | undefined;
let draftTimer: ReturnType<typeof setTimeout> | undefined;
let draftPending: { issueId: number; input: CommentInput } | null = null;

function rec(issueId: number): Rec {
  let r = recs.get(issueId);
  if (!r) {
    r = { writes: idleWrites(), retry: {}, pending: [], refresh: { phase: "idle", at: 0, reason: "", cooldownUntil: 0, waitUntil: 0 } };
    recs.set(issueId, r);
  }
  return r;
}
const isActive = (issueId: number): boolean => active !== null && active.issueId === issueId;

function publishWrites(issueId: number): void {
  if (isActive(issueId)) ticketWrites.value = { ...rec(issueId).writes };
}
function publishPending(issueId: number): void {
  if (isActive(issueId)) ticketPending.value = [...rec(issueId).pending];
}
const wholeSecondsLeft = (until: number, now: number): number => (until > now ? Math.ceil((until - now) / 1000) * 1000 : 0);

function publishRefresh(): void {
  if (!active) {
    ticketRefresh.value = idleRefresh();
    stopTicker();
    return;
  }
  const r = rec(active.issueId).refresh;
  const now = Date.now();
  const cooldownMs = wholeSecondsLeft(r.cooldownUntil, now);
  const waitMs = r.phase === "waiting" ? wholeSecondsLeft(r.waitUntil, now) : 0;
  ticketRefresh.value = { phase: r.phase, at: r.at, cooldownMs, waitMs, reason: r.reason };
  if (cooldownMs > 0 || waitMs > 0) startTicker();
  else stopTicker();
}
/** The countdown display tick (a self-rescheduling one-second timeout): UI only, it never starts a request. */
function startTicker(): void {
  if (ticker !== undefined) return;
  ticker = setTimeout(() => {
    ticker = undefined;
    publishRefresh(); // reschedules itself while a cooldown or a wait remains
  }, 1000);
}
function stopTicker(): void {
  if (ticker !== undefined) clearTimeout(ticker);
  ticker = undefined;
}
function publishAll(): void {
  if (!active) {
    ticketWrites.value = idleWrites();
    ticketPending.value = [];
  } else {
    publishWrites(active.issueId);
    publishPending(active.issueId);
  }
  publishRefresh();
}

// ── events (subscribed lazily, on the bus that is current when the first issue loads) ──
function ensureSubscribed(): void {
  if (unsubs.length > 0) return;
  unsubs = [
    on("ticket:updated", (p) => {
      if (!active || p.issueId !== active.issueId || p.cause !== "sync") return;
      const changed = p.changed ?? [];
      const next = [...ticketUpdated.value];
      for (const part of changed) if (!next.includes(part)) next.push(part);
      if (next.length !== ticketUpdated.value.length) ticketUpdated.value = next;
    }),
    on("sync:throttled", (p) => {
      if (!active || p.reason !== "rate_limited" || p.resumeAt <= 0) return;
      const r = rec(active.issueId).refresh;
      if (r.phase !== "refreshing" && r.phase !== "waiting") return;
      r.phase = "waiting";
      r.waitUntil = p.resumeAt;
      publishRefresh();
    }),
    on("media:ready", (p) => {
      const d = ticketDetail.value;
      if (!d || d.issueId !== p.issueId) return;
      if (!d.attachments.some((a) => a.id === p.attachmentId && a.cached !== p.cached)) return;
      ticketDetail.value = { ...d, attachments: d.attachments.map((a) => (a.id === p.attachmentId ? { ...a, cached: p.cached } : a)) };
    }),
  ];
  const onPageHide = (): void => void flushDraft();
  window.addEventListener("pagehide", onPageHide);
  unsubs.push(() => window.removeEventListener("pagehide", onPageHide));
}
function unsubscribe(): void {
  for (const u of unsubs) u();
  unsubs = [];
}

// ── loading ──
/** Key of an issue the modal has seen or the cached views know (for the "← KEY" back button). */
export function ticketKeyOf(issueId: number): string {
  for (const v of views.value.values()) {
    const t = v.tickets.find((x) => x.id === issueId);
    if (t) return t.key;
    const b = v.backlog.find((x) => x.issueId === issueId);
    if (b) return b.key;
  }
  return keyCache.get(issueId) ?? "";
}

/** Makes (standupId, issueId) the active issue and loads it with TicketService.Get (local, no network). */
export async function loadTicket(standupId: number, issueId: number): Promise<void> {
  ensureSubscribed();
  if (!active || active.issueId !== issueId || active.standupId !== standupId) {
    void flushDraft(); // the old issue's unsaved draft, filed under the OLD issue id
    active = { standupId, issueId };
    ticketDetail.value = null;
    ticketError.value = null;
    ticketUpdated.value = [];
    ticketDraft.value = emptyDraft();
    publishAll();
  }
  const seq = ++loadSeq;
  try {
    const d = await api().ticket.get(standupId, issueId);
    if (seq !== loadSeq) return;
    keyCache.set(d.issueId, d.row.key);
    reconcileSent(issueId, d);
    ticketDetail.value = d;
    ticketError.value = null;
    ticketUpdated.value = [];
    if (!(draftPending && draftPending.issueId === issueId)) ticketDraft.value = d.draft ?? emptyDraft();
  } catch (e) {
    if (seq !== loadSeq) return;
    ticketDetail.value = null;
    ticketError.value = toAppError(e);
  }
}

/** "Reload" on the updated bar: Get again, replace the detail, clear ticketUpdated. */
export async function reloadTicket(): Promise<void> {
  if (!active) return;
  const { standupId, issueId } = active;
  const seq = ++loadSeq;
  try {
    const d = await api().ticket.get(standupId, issueId);
    if (seq !== loadSeq || !isActive(issueId)) return;
    reconcileSent(issueId, d);
    ticketDetail.value = d;
    ticketUpdated.value = [];
  } catch (e) {
    if (seq !== loadSeq) return;
    ticketError.value = toAppError(e);
  }
}

/** The modal went away: save the draft, drop the subscriptions and the shown issue (per-issue cooldowns are kept). */
export function releaseTicket(): void {
  void flushDraft();
  unsubscribe();
  stopTicker();
  loadSeq++;
  active = null;
  ticketDetail.value = null;
  ticketError.value = null;
  ticketUpdated.value = [];
  ticketDraft.value = emptyDraft();
  publishAll();
}

/** Closes every subscription and clears every signal and timer (tests, unmount). */
export function resetTicketState(): void {
  unsubscribe();
  stopTicker();
  clearTimeout(draftTimer);
  draftTimer = undefined;
  draftPending = null;
  loadSeq++;
  active = null;
  recs.clear();
  keyCache.clear();
  knownUsers.clear();
  ticketSession.value = null;
  ticketDetail.value = null;
  ticketError.value = null;
  ticketWrites.value = idleWrites();
  ticketRefresh.value = idleRefresh();
  ticketUpdated.value = [];
  ticketPending.value = [];
  ticketDraft.value = emptyDraft();
}

// ── detail and cached-row patches ──
function patchDetail(issueId: number, fn: (d: TicketDetail) => TicketDetail): void {
  const d = ticketDetail.value;
  if (d && d.issueId === issueId && isActive(issueId)) ticketDetail.value = fn(d);
}
function mapStateRail(d: TicketDetail, fieldId: string, fn: (r: RailRow) => RailRow): TicketDetail {
  return { ...d, rail: d.rail.map((g) => (g.id === "state" ? { ...g, rows: g.rows.map((r) => (r.fieldId === fieldId ? fn(r) : r)) } : g)) };
}
const railOf = (d: TicketDetail, fieldId: string): RailRow | undefined => d.rail.find((g) => g.id === "state")?.rows.find((r) => r.fieldId === fieldId);

/** Applies `patch` to the issue's row in every cached view that has it; returns the previous values per view. */
function patchRows(issueId: number, patch: Partial<TicketView>): Map<number, Partial<TicketView>> {
  const before = new Map<number, Partial<TicketView>>();
  const next = new Map(views.value);
  for (const [id, v] of next) {
    const at = v.tickets.findIndex((t) => t.id === issueId);
    if (at < 0) continue;
    const row = v.tickets[at]!;
    const prev: Record<string, unknown> = {};
    for (const k of Object.keys(patch)) prev[k] = (row as unknown as Record<string, unknown>)[k];
    before.set(id, prev as Partial<TicketView>);
    const tickets = v.tickets.slice();
    tickets[at] = { ...row, ...patch };
    next.set(id, { ...v, tickets });
  }
  if (before.size > 0) views.value = next;
  return before;
}
function restoreRows(issueId: number, before: Map<number, Partial<TicketView>>): void {
  const next = new Map(views.value);
  let changed = false;
  for (const [id, prev] of before) {
    const v = next.get(id);
    const at = v ? v.tickets.findIndex((t) => t.id === issueId) : -1;
    if (!v || at < 0) continue;
    const tickets = v.tickets.slice();
    tickets[at] = { ...tickets[at]!, ...prev };
    next.set(id, { ...v, tickets });
    changed = true;
  }
  if (changed) views.value = next;
}

/** A server answer: replaces the shown detail (when that issue is open) and the issue's row in every cached view. */
/** The plain text of a rendered comment body (mention and text nodes), for matching a sent comment against the detail. */
function bodyText(nodes: readonly { x?: string | null; a?: { label?: string | null } | null; c?: readonly unknown[] | null }[]): string {
  let out = "";
  for (const n of nodes) {
    out += n.x ?? n.a?.label ?? "";
    if (n.c) out += bodyText(n.c as never);
  }
  return out;
}
const squash = (s: string): string => s.replace(/\s+/g, "");

/**
 * A comment that was sent but whose refresh failed stays on screen as a "sent" bubble (the detail lacks it). Once any
 * detail of the issue carries it (Refresh, Reload, a later write), the bubble goes.
 */
function reconcileSent(issueId: number, d: TicketDetail): void {
  const r = recs.get(issueId);
  if (!r || !r.pending.some((p) => p.state === "sent")) return;
  const have = d.comments.map((c) => squash(bodyText(c.body.c ?? [])));
  r.pending = r.pending.filter((p) => p.state !== "sent" || !have.includes(squash(p.input.text)));
  publishPending(issueId);
}

function applyAuthoritative(issueId: number, d: TicketDetail): void {
  keyCache.set(d.issueId, d.row.key);
  reconcileSent(issueId, d);
  if (isActive(issueId)) ticketDetail.value = d;
  const next = new Map(views.value);
  let changed = false;
  for (const [id, v] of next) {
    const at = v.tickets.findIndex((t) => t.id === issueId);
    if (at < 0) continue;
    const old = v.tickets[at]!;
    const tickets = v.tickets.slice();
    tickets[at] = v.pulseDays === d.pulseDays ? d.row : { ...d.row, pulse: old.pulse };
    next.set(id, { ...v, tickets });
    changed = true;
  }
  if (changed) views.value = next;
}

// ── light writes (optimistic; never retried automatically) ──
const rateText = (retryInMs: number): string =>
  retryInMs > 0 ? `Jira is rate-limiting · try again in ${Math.ceil(retryInMs / 1000)}s` : "Jira is rate-limiting · try again shortly";
function failureText(e: unknown): string {
  const ae = toAppError(e);
  if (ae.code === "write_rejected") return `Jira rejected the change: ${ae.message}`;
  if (ae.code === "rate_limited") return rateText(ae.retryInMs);
  return ae.message;
}

interface WriteSpec {
  field: EditField;
  standupId: number;
  issueId: number;
  /** Optimistic change of the open detail, and its exact inverse. */
  apply: (d: TicketDetail) => TicketDetail;
  revert: (d: TicketDetail) => TicketDetail;
  /** The same change on the cached rows behind the modal. */
  rows: Partial<TicketView>;
  call: () => Promise<TicketDetail>;
}

async function performWrite(spec: WriteSpec): Promise<void> {
  const { field, issueId } = spec;
  const r = rec(issueId);
  if (r.writes[field].phase === "saving") return;
  r.writes[field] = { phase: "saving", reason: "" };
  delete r.retry[field];
  publishWrites(issueId);
  patchDetail(issueId, spec.apply);
  const before = patchRows(issueId, spec.rows);
  try {
    const d = await spec.call();
    r.writes[field] = idleWrite();
    applyAuthoritative(issueId, d);
  } catch (e) {
    patchDetail(issueId, spec.revert);
    restoreRows(issueId, before);
    r.writes[field] = { phase: "failed", reason: failureText(e) };
    r.retry[field] = () => performWrite(spec);
  }
  publishWrites(issueId);
}

function currentDetail(issueId: number): TicketDetail | null {
  const d = ticketDetail.value;
  return d && d.issueId === issueId ? d : null;
}

export function applyTransition(option: TransitionOption): Promise<void> {
  const d = active ? currentDetail(active.issueId) : null;
  if (!active || !d) return Promise.resolve();
  const { standupId, issueId } = active;
  const prevLane = d.row.lane;
  const prevName = d.row.statusName;
  const prevText = railOf(d, "status")?.text ?? prevName;
  const set = (lane: string, name: string, text: string) => (x: TicketDetail): TicketDetail => ({
    ...mapStateRail(x, "status", (row) => ({ ...row, text })),
    row: { ...x.row, lane, statusName: name },
  });
  return performWrite({
    field: "status",
    standupId,
    issueId,
    apply: set(option.toLane, option.toStatusName, option.toStatusName),
    revert: set(prevLane, prevName, prevText),
    rows: { lane: option.toLane, statusName: option.toStatusName },
    call: () => api().ticket.transition(standupId, issueId, option.id),
  });
}

function personFor(accountId: string): PersonRef {
  const known = knownUsers.get(accountId);
  if (known) return { accountId, name: known.displayName, initials: known.initials, self: false };
  for (const v of views.value.values()) {
    const s = v.speakers.find((x) => x.id === accountId);
    if (s) return { accountId, name: s.name, initials: s.initials, self: false };
  }
  return { accountId, name: accountId, initials: accountId.slice(0, 2).toUpperCase(), self: false };
}

/** "" = unassign. */
export function applyAssign(accountId: string): Promise<void> {
  const d = active ? currentDetail(active.issueId) : null;
  if (!active || !d) return Promise.resolve();
  const { standupId, issueId } = active;
  const prevAssignee = d.row.assignee;
  const prevUsers = railOf(d, "assignee")?.users ?? [];
  const set = (assignee: string, users: PersonRef[]) => (x: TicketDetail): TicketDetail => ({
    ...mapStateRail(x, "assignee", (row) => ({ ...row, users })),
    row: { ...x.row, assignee },
  });
  return performWrite({
    field: "assignee",
    standupId,
    issueId,
    apply: set(accountId, accountId ? [personFor(accountId)] : []),
    revert: set(prevAssignee, prevUsers),
    rows: { assignee: accountId },
    call: () => api().ticket.assign(standupId, issueId, accountId),
  });
}

export function applyFlag(flagged: boolean): Promise<void> {
  const d = active ? currentDetail(active.issueId) : null;
  if (!active || !d) return Promise.resolve();
  const { standupId, issueId } = active;
  const prevBlocked = d.row.blocked;
  const prev = railOf(d, "flagged");
  const prevText = prev?.text ?? (prevBlocked ? "Flagged" : "Not flagged");
  const prevSub = prev?.sub ?? "";
  const set = (blocked: boolean, text: string, sub: string) => (x: TicketDetail): TicketDetail => ({
    ...mapStateRail(x, "flagged", (row) => ({ ...row, text, sub })),
    row: { ...x.row, blocked },
  });
  return performWrite({
    field: "flag",
    standupId,
    issueId,
    apply: set(flagged, flagged ? "Flagged" : "Not flagged", flagged ? prevSub : ""),
    revert: set(prevBlocked, prevText, prevSub),
    rows: { blocked: flagged },
    call: () => api().ticket.setFlag(standupId, issueId, flagged),
  });
}

/** The user's explicit Retry: re-sends the last failed write of that field once. */
export async function retryWrite(field: EditField): Promise<void> {
  if (!active) return;
  const r = rec(active.issueId);
  const again = r.retry[field];
  if (!again) return;
  delete r.retry[field];
  await again();
}

/** TicketService.Transitions for the active issue (network; called when the Status combobox opens). */
export function loadTransitions(_signal: AbortSignal): Promise<TransitionList> {
  if (!active) return Promise.reject(new Error("No ticket is open."));
  return api().ticket.transitions(active.issueId);
}

/** TicketService.SearchMentions for the active standup (people combobox). */
export async function loadAssignees(query: string, _signal: AbortSignal): Promise<UserOption[]> {
  if (!active) return [];
  const users = await api().ticket.searchMentions(active.standupId, query);
  for (const u of users) knownUsers.set(u.accountId, u);
  return users;
}

// ── refresh ──
export async function refreshTicket(): Promise<void> {
  if (!active) return;
  const { standupId, issueId } = active;
  const r = rec(issueId).refresh;
  if (r.phase === "refreshing" || r.phase === "waiting" || r.cooldownUntil > Date.now()) return;
  r.phase = "refreshing";
  r.reason = "";
  r.waitUntil = 0;
  publishRefresh();
  try {
    const d = await api().ticket.refresh(standupId, issueId);
    r.phase = "done";
    r.at = Date.now();
    r.reason = "";
    r.waitUntil = 0;
    r.cooldownUntil = r.at + REFRESH_COOLDOWN_MS;
    applyAuthoritative(issueId, d);
    if (isActive(issueId)) ticketUpdated.value = [];
  } catch (e) {
    const ae = toAppError(e);
    r.waitUntil = 0;
    if (ae.code === "cooldown") {
      r.phase = "idle";
      r.reason = "";
      r.cooldownUntil = Date.now() + ae.retryInMs;
    } else if (ae.code === "rate_limited") {
      r.phase = "failed";
      r.reason = rateText(ae.retryInMs);
      r.cooldownUntil = Date.now() + ae.retryInMs;
    } else {
      r.phase = "failed";
      r.reason = ae.code === "offline" ? "offline" : ae.message;
    }
  }
  publishRefresh();
}

// ── comments and the draft ──
/** Composer text changed: updates ticketDraft now and saves it (TicketService.SaveDraft) 400 ms after the last change. */
export function editDraft(input: CommentInput): void {
  ticketDraft.value = input;
  if (!active) return;
  draftPending = { issueId: active.issueId, input };
  clearTimeout(draftTimer);
  draftTimer = setTimeout(() => void flushDraft(), DRAFT_DEBOUNCE_MS);
}

/** Saves a pending draft change now (blur, close, step, pagehide). No call when nothing is pending. */
export async function flushDraft(): Promise<void> {
  clearTimeout(draftTimer);
  draftTimer = undefined;
  const p = draftPending;
  if (!p) return;
  draftPending = null;
  try {
    await api().ticket.saveDraft(p.issueId, p.input);
  } catch {
    // The text stays in the composer; the next edit or flush saves it again.
  }
}

const runes = (s: string): number => Array.from(s).length;

async function submitComment(issueId: number, standupId: number, localId: string): Promise<void> {
  const r = rec(issueId);
  const at = r.pending.findIndex((p) => p.localId === localId);
  if (at < 0) return;
  const input = r.pending[at]!.input;
  r.pending = r.pending.map((p) => (p.localId === localId ? { ...p, state: "sending", reason: "" } : p));
  publishPending(issueId);
  try {
    const d = await api().ticket.addComment(standupId, issueId, input);
    const lost = d.warnings.includes("refresh_failed") && !d.comments.some((c) => squash(bodyText(c.body.c ?? [])) === squash(input.text));
    // The write succeeded; if the refresh did not bring the comment back, keep the bubble (as sent) until a later detail does.
    r.pending = lost
      ? r.pending.map((p) => (p.localId === localId ? { ...p, state: "sent", reason: "" } : p))
      : r.pending.filter((p) => p.localId !== localId);
    applyAuthoritative(issueId, d);
    if (isActive(issueId)) ticketDraft.value = emptyDraft();
  } catch (e) {
    const ae = toAppError(e);
    const why = ae.code === "rate_limited" ? rateText(ae.retryInMs) : ae.message;
    r.pending = r.pending.map((p) => (p.localId === localId ? { ...p, state: "failed", reason: why } : p));
    // The text must survive a restart: store it as the draft (explicitly; nothing re-sends it by itself).
    void api().ticket.saveDraft(issueId, input).catch(() => undefined);
  }
  publishPending(issueId);
}

export async function sendComment(input: CommentInput): Promise<void> {
  if (!active || input.text.trim() === "") return;
  const { standupId, issueId } = active;
  const r = rec(issueId);
  const pending: PendingComment = {
    localId: `local-${++localSeq}`,
    input: { text: input.text, mentions: input.mentions.map((m) => ({ ...m })) },
    createdAt: Date.now(),
    state: "sending",
    reason: "",
  };
  r.pending = [...r.pending, pending];
  // The text now lives in the bubble: a pending debounced save of it must not resurrect the draft.
  clearTimeout(draftTimer);
  draftTimer = undefined;
  if (draftPending?.issueId === issueId) draftPending = null;
  ticketDraft.value = emptyDraft();
  await submitComment(issueId, standupId, pending.localId);
}

export async function retryComment(localId: string): Promise<void> {
  if (!active) return;
  await submitComment(active.issueId, active.standupId, localId);
}

/** Puts a failed comment's text back into the composer draft and drops the pending bubble. */
export function editComment(localId: string): void {
  if (!active) return;
  const r = rec(active.issueId);
  const p = r.pending.find((x) => x.localId === localId);
  if (!p) return;
  r.pending = r.pending.filter((x) => x.localId !== localId);
  publishPending(active.issueId);
  const now = ticketDraft.value;
  if (now.text === "") {
    editDraft(p.input);
    return;
  }
  // The composer already holds newer text: keep both (failed text first), shifting the newer mentions.
  const shift = runes(p.input.text) + 1;
  editDraft({
    text: `${p.input.text}\n${now.text}`,
    mentions: [...p.input.mentions, ...now.mentions.map((m) => ({ ...m, start: m.start + shift, end: m.end + shift }))],
  });
}

export function discardComment(localId: string): void {
  if (!active) return;
  const r = rec(active.issueId);
  const p = r.pending.find((x) => x.localId === localId);
  if (!p) return;
  r.pending = r.pending.filter((x) => x.localId !== localId);
  publishPending(active.issueId);
  // Delete the stored draft (saved when the send failed), unless the composer holds something newer.
  const text = ticketDraft.value.text;
  if (text === "" || text === p.input.text) editDraft(emptyDraft());
}
