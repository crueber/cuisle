// FROZEN SIGNATURES (architecture §4.3). Implementation: WP-15. Switching speaker never calls Go.
import { computed, signal, type ReadonlySignal } from "@preact/signals";
import { api } from "../api/index";
import type { LaneInfo, SpeakerView, StandupView, TicketView } from "../api/types";
import { on } from "../api/events";
import { refreshStandups, standups, tabStandups } from "./app";
import { navigate, route, type Route } from "./route";
import { showBanner } from "./ui";
import { toAppError } from "../api/errors";
import type { BoardGroup } from "../layout/board";

/** Cached views by standup id (kept for all standups once loaded). */
export const views = signal<ReadonlyMap<number, StandupView>>(new Map());
/** Standups whose cached view is stale (data:changed, rev moved, day rolled). */
export const staleViews = signal<ReadonlySet<number>>(new Set());

export interface TicketGroup {
  lane: LaneInfo;
  tickets: TicketView[];
}

export interface BoardLane {
  speaker: SpeakerView;
  tickets: TicketView[]; // active (non-done) tickets in walk-lane order, then rank
}

export const currentView: ReadonlySignal<StandupView | null> = computed(() => {
  const r = route.value;
  if (r.view !== "standup" && r.view !== "configure") return null;
  if (r.standupId === null) return null;
  return views.value.get(r.standupId) ?? null;
});

export const currentPerson: ReadonlySignal<SpeakerView | null> = /* @__PURE__ */ computed(() => {
  const r = route.value;
  const view = currentView.value;
  if (r.view !== "standup" || r.person === null || !view) return null;
  return view.speakers?.find((s) => s.id === r.person) ?? null;
});

/** Selected speaker's tickets grouped by view.lanes (walk order, Done last), empty groups dropped. */
export const personGroups: ReadonlySignal<TicketGroup[]> = computed(() => {
  const r = route.value;
  const view = currentView.value;
  if (r.view !== "standup" || r.person === null || !view) return [];
  const person = r.person;
  const mine = (view.tickets ?? []).filter((t) => (person === "unassigned" ? t.assignee === "" : t.assignee === person));
  const groups: TicketGroup[] = [];
  for (const lane of view.lanes ?? []) {
    const tickets = mine.filter((t) => t.lane === lane.lane);
    if (tickets.length > 0) groups.push({ lane, tickets });
  }
  return groups;
});

/** Board lanes: Unassigned first (if it has tickets), then speakers in view order. Pure; BoardView calls it with its prop. */
export function buildBoardLanes(view: StandupView): BoardLane[] {
  const tickets = view.tickets ?? [];
  const speakers = view.speakers ?? [];
  const walk = (view.lanes ?? []).filter((l) => l.lane !== "done").map((l) => l.lane);
  const active = tickets.filter((t) => t.lane !== "done");
  const laneRank = (lane: string) => {
    const i = walk.indexOf(lane);
    return i === -1 ? walk.length : i;
  };
  const ticketsOf = (accountId: string) =>
    active
      .filter((t) => (accountId === "unassigned" ? t.assignee === "" : t.assignee === accountId))
      .map((t, rank) => ({ t, rank }))
      .sort((a, b) => laneRank(a.t.lane) - laneRank(b.t.lane) || a.rank - b.rank)
      .map((x) => x.t);

  const lanes: BoardLane[] = [];
  const unassigned = speakers.find((s) => s.unassigned || s.id === "unassigned");
  const unassignedTickets = ticketsOf("unassigned");
  if (unassignedTickets.length > 0) {
    lanes.push({
      speaker: unassigned ?? { id: "unassigned", name: "Unassigned", first: "Unassigned", initials: "··", unassigned: true, included: false, active: unassignedTickets.length, done: 0 },
      tickets: unassignedTickets,
    });
  }
  // Speaking order: included speakers first, then excluded speakers who still have tickets (ux-spec §6.2).
  const ordered = [...speakers.filter((s) => s.included), ...speakers.filter((s) => !s.included)];
  for (const s of ordered) {
    if (s.unassigned || s.id === "unassigned") continue;
    const own = ticketsOf(s.id);
    if (s.included || own.length > 0) lanes.push({ speaker: s, tickets: own });
  }
  return lanes;
}

/** Whole-board grouping per standup id (F13; absent = assignee). Seeded from boot.ui.boardGroup, written through on every change. */
export const boardGroups = signal<ReadonlyMap<number, BoardGroup>>(new Map());

export const boardGroupOf = (standupId: number): BoardGroup => boardGroups.value.get(standupId) ?? "assignee";

/** Seeds the per-standup choices from the boot payload (unknown values read as assignee). */
export function seedBoardGroups(raw: Record<string, string | undefined> | null | undefined): void {
  const m = new Map<number, BoardGroup>();
  for (const [id, v] of Object.entries(raw ?? {})) if (v === "status") m.set(Number(id), "status");
  boardGroups.value = m;
}

/** Sets one standup's grouping and persists it at once (UI state, not Configure: no save indicator, no debounce). */
export function setBoardGroup(standupId: number, group: BoardGroup): void {
  if (boardGroupOf(standupId) === group) return;
  boardGroups.value = new Map(boardGroups.value).set(standupId, group);
  api()
    .app.setUIState({ standupId, boardGroup: group })
    .catch(() => undefined);
}

/** G: toggles the grouping on the whole board of the current standup. On a person view it does nothing (returns false). */
export function toggleBoardGroup(): boolean {
  const r = route.value;
  if (r.view !== "standup" || r.person !== null) return false;
  setBoardGroup(r.standupId, boardGroupOf(r.standupId) === "status" ? "assignee" : "status");
  return true;
}

export const boardLanes: ReadonlySignal<BoardLane[]> = /* @__PURE__ */ computed(() => {
  const view = currentView.value;
  return view ? buildBoardLanes(view) : [];
});

export const doneTickets: ReadonlySignal<TicketView[]> = /* @__PURE__ */ computed(() => {
  const view = currentView.value;
  return view ? (view.tickets ?? []).filter((t) => t.lane === "done") : [];
});

export const loadingView = /* @__PURE__ */ signal(false);

export function selectPerson(person: string | null): void {
  const r = route.value;
  if (r.view !== "standup") return;
  navigate({ view: "standup", standupId: r.standupId, person });
}

/** The speaking order used by the arrow keys: whole board, then each included speaker in view order. */
function cycleOrder(view: StandupView): (string | null)[] {
  return [null, ...(view.speakers ?? []).filter((s) => s.included).map((s) => s.id)];
}

function step(delta: 1 | -1): void {
  const r = route.value;
  if (r.view !== "standup") return;
  const view = views.value.get(r.standupId);
  if (!view) return;
  const order = cycleOrder(view);
  const at = order.indexOf(r.person);
  const from = at === -1 ? 0 : at;
  const next = order[(((from + delta) % order.length) + order.length) % order.length] ?? null;
  navigate({ view: "standup", standupId: r.standupId, person: next });
}

/** ↓ : next included speaker; after the last → whole board; from whole board → first speaker. */
export function nextSpeaker(): void {
  step(1);
}
export function prevSpeaker(): void {
  step(-1);
}

/** What B remembers: the person it left, and the exact route object it produced (identity = "nothing navigated since"). */
let boardReturn: { standupId: number; person: string; route: Route } | null = null;

/**
 * B: on a person, remember them and show the whole board; on the whole board reached by that same B press (and not
 * navigated away from since, which would have replaced the route object), go back to that person if they still speak.
 * Otherwise on the board it is a no-op.
 */
export function toggleBoard(): void {
  const r = route.value;
  if (r.view !== "standup") return;
  if (r.person !== null) {
    const to: Route = { view: "standup", standupId: r.standupId, person: null };
    boardReturn = { standupId: r.standupId, person: r.person, route: to };
    navigate(to);
    return;
  }
  const back = boardReturn;
  boardReturn = null;
  if (!back || back.route !== r) return;
  const view = views.value.get(r.standupId);
  if (!view || !(view.speakers ?? []).some((s) => s.id === back.person && s.included)) return;
  navigate({ view: "standup", standupId: r.standupId, person: back.person });
}

/** The person to keep when switching to `view`: only when they speak there (included). */
function keepPerson(view: StandupView, person: string | null): string | null {
  if (person === null) return null;
  return (view.speakers ?? []).some((s) => s.id === person && s.included) ? person : null;
}

const refreshSeq = new Map<number, number>();
let switchSeq = 0;

/** Re-requests a standup's view and replaces the cache entry. */
export async function refreshView(standupId: number): Promise<StandupView> {
  const seq = (refreshSeq.get(standupId) ?? 0) + 1;
  refreshSeq.set(standupId, seq);
  const v = await api().standup.view(standupId);
  // A reply that was overtaken by a newer request or higher rev must not roll the view back.
  const cached = views.value.get(standupId);
  if (seq !== refreshSeq.get(standupId)) return cached ?? v;
  if (cached && v.rev < cached.rev) return cached;
  views.value = new Map(views.value).set(standupId, v);
  if (staleViews.value.has(standupId)) {
    const s = new Set(staleViews.value);
    s.delete(standupId);
    staleViews.value = s;
  }
  return v;
}

function markStale(ids: number[]): void {
  if (ids.length === 0) return;
  const next = new Set(staleViews.value);
  for (const id of ids) next.add(id);
  staleViews.value = next;
}

/** Shows the cached view at once; calls StandupService.View only when missing or stale. Keeps the person if they speak there. */
export async function switchStandup(standupId: number): Promise<void> {
  const mySwitch = ++switchSeq;
  const r = route.value;
  const prev = r.view === "standup" ? r.person : null;
  const cached = views.value.get(standupId);
  const tab = standups.value.find((t) => t.id === standupId);
  const stale = !cached || staleViews.value.has(standupId) || (tab !== undefined && cached.rev < tab.rev);
  navigate({
    view: "standup",
    standupId,
    person: cached ? keepPerson(cached, prev) : prev,
  });
  if (!stale) return;
  loadingView.value = true;
  try {
    const fresh = await refreshView(standupId);
    const now = route.value;
    if (now.view === "standup" && now.standupId === standupId) {
      const nextPerson = keepPerson(fresh, now.person);
      if (nextPerson !== now.person) {
        navigate({ view: "standup", standupId, person: nextPerson });
      }
    }
  } catch (e) {
    if (mySwitch === switchSeq) {
      const err = toAppError(e);
      showBanner({ kind: "error", message: err.message, dismissible: true });
    }
  } finally {
    if (mySwitch === switchSeq) loadingView.value = false;
  }
}

/** ← / → : the previous / next standup tab, wrapping. Returns false (nothing to do) with fewer than two standups. */
export function cycleStandup(delta: 1 | -1): boolean {
  const r = route.value;
  if (r.view !== "standup") return false;
  const tabs = tabStandups();
  if (tabs.length < 2) return false;
  const at = tabs.findIndex((t) => t.id === r.standupId);
  const from = at === -1 ? 0 : at;
  const next = tabs[(((from + delta) % tabs.length) + tabs.length) % tabs.length];
  if (!next) return false;
  void switchStandup(next.id);
  return true;
}

/** 1-based position among tab standups (keys 1–9). */
export async function switchByNumber(n: number): Promise<void> {
  const tab = Number.isInteger(n) ? tabStandups()[n - 1] : undefined;
  if (!tab) return;
  await switchStandup(tab.id);
}

/** Subscribes to data:changed and day:rolled; returns unsubscribe. */
export function installStandupEvents(): () => void {
  const offChanged = on("data:changed", (p) => {
    const r = route.value;
    const current = r.view === "standup" || r.view === "configure" ? r.standupId : null;
    const targets = p.standupId === 0 ? [...views.value.keys()] : [p.standupId];
    const others: number[] = [];
    for (const id of targets) {
      if (id === current) void refreshView(id).catch(() => markStale([id]));
      else others.push(id);
    }
    markStale(others);
    void refreshStandups().catch(() => undefined);
  });
  const offRolled = on("day:rolled", () => {
    const r = route.value;
    const current = r.view === "standup" || r.view === "configure" ? r.standupId : null;
    const others = [...views.value.keys()].filter((id) => id !== current);
    markStale(others);
    if (current !== null && views.value.has(current)) void refreshView(current).catch(() => markStale([current]));
  });
  return () => {
    offChanged();
    offRolled();
  };
}
