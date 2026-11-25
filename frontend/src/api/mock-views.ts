// WP-14: pure helpers that keep a fixture StandupView in step with its StandupConfig after a mock mutation. They never
// compute pulse data (ADR 0008): ticket segments, marks and labels stay as the fixtures have them.
import type * as T from "./types";

/** Walk lanes from the config, then the Done lane (its configured label), with the Jira columns mapped to each. */
export function laneInfos(cfg: T.StandupConfig): T.LaneInfo[] {
  const cols = (lane: string) => cfg.columns.filter((c) => c.lane === lane).map((c) => c.columnName);
  const done: T.LaneInfo = cfg.laneOptions?.find((o) => o.lane === "done") ?? {
    lane: "done", label: "Done", defaultLabel: "Done", kind: "done", custom: false, hue: 0, limitDays: 0, columns: [],
  };
  return [...cfg.lanes.map((l) => ({ ...l, columns: cols(l.lane) })), { ...done, columns: cols("done") }];
}

const DONE_LABEL: Record<string, string> = { sprint: "this sprint", "14d": "last 14 days", yesterday: "since yesterday" };

export function viewOptions(cfg: T.StandupConfig): T.ViewOptions {
  return {
    rowsMode: cfg.rowsMode,
    showCommits: cfg.showCommits,
    showPRs: cfg.showPRs,
    showComments: cfg.showComments,
    shadeWeekends: cfg.shadeWeekends,
    doneWindow: cfg.doneWindow,
    doneLabel: DONE_LABEL[cfg.doneWindow] ?? cfg.doneWindow,
    devActivity: "ok",
  };
}

/** Included speakers in config order, then excluded ones; the Unassigned entry is placed by unassignedPosition. */
export function speakersFor(view: T.StandupView, cfg: T.StandupConfig): T.SpeakerView[] {
  const prior = new Map(view.speakers.map((s): [string, T.SpeakerView] => [s.id, s]));
  const people: T.SpeakerView[] = [...cfg.speakers]
    .sort((a, b) => a.position - b.position)
    .map((s) => {
      const p = prior.get(s.accountId);
      return {
        id: s.accountId,
        name: s.name,
        first: s.name.split(/\s+/)[0] || s.name,
        initials: s.initials,
        unassigned: false,
        included: s.included,
        active: p?.active ?? s.active,
        done: p?.done ?? 0,
      };
    });
  const included = people.filter((s) => s.included);
  const excluded = people.filter((s) => !s.included);
  const un = prior.get("unassigned");
  if (un) {
    const entry: T.SpeakerView = { ...un, included: cfg.unassignedPosition !== "skip" };
    if (cfg.unassignedPosition === "first") included.unshift(entry);
    else if (entry.included) included.push(entry);
    else excluded.push(entry);
  }
  return [...included, ...excluded];
}

export function idleSync(standupId: number, at: number): T.SyncStatus {
  return {
    standupId,
    runId: 0,
    state: "idle",
    mode: "incremental",
    phase: "",
    done: 0,
    total: 0,
    lastSuccessAt: at,
    lastAttemptAt: at,
    lastStatus: "ok",
    warnings: [],
    errorCode: "",
    errorMessage: "",
    needsSync: false,
  };
}

/** Copies the config-driven fields of a view. Pulse, axis and tickets are left alone (the fixture owns them). */
export function refreshView(view: T.StandupView, cfg: T.StandupConfig): void {
  view.rev = cfg.rev;
  view.name = cfg.name;
  view.scope = cfg.scope;
  view.spaceKey = cfg.space?.key ?? view.spaceKey;
  view.boardName = cfg.board?.name ?? view.boardName;
  view.boardType = cfg.board?.type ?? view.boardType;
  view.options = { ...viewOptions(cfg), devActivity: view.options.devActivity };
  view.lanes = laneInfos(cfg);
  view.speakers = speakersFor(view, cfg);
  remapWorkingTickets(view, cfg);
}

/**
 * Like view.Build, a ticket shows in the lane its status's column maps to now. The mock only moves tickets between
 * working lanes (built-in or custom) and relabels their current segment; done/backlog/hidden moves need a real sync.
 */
function remapWorkingTickets(view: T.StandupView, cfg: T.StandupConfig): void {
  const working = new Set(cfg.lanes.map((l) => l.lane));
  const laneOf = new Map<string, string>();
  for (const c of cfg.columns) for (const s of c.statusNames) laneOf.set(s, c.lane);
  for (const t of view.tickets) {
    const next = laneOf.get(t.statusName);
    if (!next || next === t.lane || !working.has(next) || !working.has(t.lane)) continue;
    const last = t.pulse.segs.at(-1);
    if (last && last.lane === t.lane) last.lane = next;
    t.lane = next;
  }
}
