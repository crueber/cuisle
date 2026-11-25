// Mock-only mirror of Go's lane model (internal/settings lanes.go, internal/view lanes.go): custom lanes with ids
// "c<N>" (never reused), rotating palette hues 1..8, labels for built-in lanes, laneOptions and walkLeftToRight.
// The real app gets all of this from Go; the mock keeps fixtures and screenshots in step with it.
import { AppError } from "./errors";
import type * as T from "./types";

export const BUILTIN_LABEL: Record<string, string> = {
  backlog: "Backlog", todo: "To Do", prog: "In Progress", review: "In Review", qa: "QA", done: "Done", hidden: "Hidden",
};
const BUILTIN_WORKING = ["todo", "prog", "review", "qa"];
const KIND: Record<string, T.LaneKind> = { done: "done", backlog: "backlog", hidden: "hidden" };
const HUES = 8;
const MAX_CUSTOM = 12;
const MAX_LABEL = 40;

const invalid = (message: string, field: string): AppError => new AppError("validation", message, "", false, field);
export const isCustom = (lane: string): boolean => /^c[1-9]\d*$/.test(lane);
const seqOf = (lane: string): number => (isCustom(lane) ? Number(lane.slice(1)) : 0);

export function laneInfo(lane: string, label: string, limitDays: number, hue = 0): T.LaneInfo {
  return {
    lane, label, defaultLabel: BUILTIN_LABEL[lane] ?? "", kind: KIND[lane] ?? "working", custom: isCustom(lane), hue,
    limitDays, columns: [],
  };
}

/** The four built-in working lanes in walk order with the given limits (a new standup's lanes). */
export function defaultLanes(limits: Record<string, number> = {}): T.LaneInfo[] {
  return ["qa", "review", "prog", "todo"].map((l) => laneInfo(l, BUILTIN_LABEL[l]!, limits[l] ?? 0));
}

/** Rebuilds laneOptions (labels and hues kept), each lane's columns, and walkLeftToRight from cfg.lanes/columns. */
export function syncLanes(cfg: T.StandupConfig): void {
  const prior = new Map((cfg.laneOptions ?? []).map((o): [string, T.LaneInfo] => [o.lane, o]));
  const walk = cfg.lanes.map((l) => ({ ...(prior.get(l.lane) ?? l), ...l, columns: colsOf(cfg, l.lane) }));
  const byLane = new Map(walk.map((l): [string, T.LaneInfo] => [l.lane, l]));
  const label = (l: string) => prior.get(l)?.label ?? BUILTIN_LABEL[l]!;
  const opt = (l: string): T.LaneInfo => byLane.get(l) ?? { ...laneInfo(l, label(l), 0), columns: colsOf(cfg, l) };
  const custom = walk.filter((l) => l.custom).sort((a, b) => seqOf(a.lane) - seqOf(b.lane));
  cfg.lanes = walk;
  cfg.laneOptions = [opt("backlog"), ...BUILTIN_WORKING.map(opt), ...custom, opt("done"), opt("hidden")];
  const first = (l: string) => {
    const i = cfg.columns.findIndex((c) => c.lane === l);
    return i < 0 ? 1e9 : i;
  };
  cfg.walkLeftToRight = cfg.laneOptions
    .filter((o) => o.kind === "working" && byLane.has(o.lane))
    .map((o) => o.lane)
    .sort((a, b) => first(a) - first(b));
}

function colsOf(cfg: T.StandupConfig, lane: string): string[] {
  return cfg.columns.filter((c) => c.lane === lane).map((c) => c.columnName);
}

function checkLabel(cfg: T.StandupConfig, self: string, label: string): void {
  if (label === "") throw invalid("Give the lane a name.", "label");
  if (label.length > MAX_LABEL) throw invalid(`Keep lane names to ${MAX_LABEL} characters.`, "label");
  const clash = cfg.laneOptions.find((o) => o.lane !== self && o.label.toLowerCase() === label.toLowerCase());
  if (clash) throw invalid(`There's already a lane called ${clash.label}.`, "label");
}

/** CreateLane: a new custom working lane at the end of the walk (or the existing one with that name). */
export function createLane(cfg: T.StandupConfig, seq: number, rawLabel: string): { lane: string; seq: number } {
  const label = rawLabel.trim();
  const same = cfg.lanes.find((l) => l.custom && l.label.toLowerCase() === label.toLowerCase());
  if (same) return { lane: same.lane, seq };
  checkLabel(cfg, "", label);
  if (cfg.lanes.filter((l) => l.custom).length >= MAX_CUSTOM) {
    throw invalid(`A standup can have up to ${MAX_CUSTOM} lanes of its own.`, "label");
  }
  const next = seq + 1;
  const lane = `c${next}`;
  // The lowest palette colour no live custom lane uses; rotate by sequence only when all are taken (settings.nextHue).
  const used = new Set(cfg.lanes.filter((l) => l.custom).map((l) => l.hue));
  const free = Array.from({ length: HUES }, (_, i) => i + 1).find((h) => !used.has(h));
  cfg.lanes = [...cfg.lanes, laneInfo(lane, label, 5, free ?? ((next - 1) % HUES) + 1)];
  return { lane, seq: next };
}

/** RenameLane: custom lanes need a name; built-in working lanes and Done reset to their label with "". */
export function renameLane(cfg: T.StandupConfig, lane: string, rawLabel: string): void {
  const label = rawLabel.trim();
  const target = cfg.laneOptions.find((o) => o.lane === lane);
  if (!target || target.kind === "backlog" || target.kind === "hidden") throw invalid("That lane can't be renamed.", "lane");
  const next = label === "" && !target.custom ? (BUILTIN_LABEL[lane] ?? "") : label;
  checkLabel(cfg, lane, next);
  target.label = next;
  const walk = cfg.lanes.find((l) => l.lane === lane);
  if (walk) walk.label = next;
}

/** DeleteLane: a custom lane goes; its columns move to moveTo (needed only when it has columns). */
export function deleteLane(cfg: T.StandupConfig, lane: string, moveTo: string): boolean {
  if (!isCustom(lane)) throw invalid("Only lanes you added can be deleted.", "lane");
  if (!cfg.lanes.some((l) => l.lane === lane)) return false;
  const cols = cfg.columns.filter((c) => c.lane === lane);
  if (cols.length > 0) {
    if (moveTo === lane || !cfg.laneOptions.some((o) => o.lane === moveTo)) {
      throw invalid("Choose a lane for its columns.", "moveTo");
    }
    for (const c of cols) Object.assign(c, { lane: moveTo, auto: false, order: 1e9 });
  }
  cfg.lanes = cfg.lanes.filter((l) => l.lane !== lane);
  cfg.laneOptions = cfg.laneOptions.filter((o) => o.lane !== lane);
  return cols.length > 0;
}

/** The highest custom-lane number a config uses (the mock's starting id counter). */
export const maxSeq = (cfg: T.StandupConfig): number => Math.max(0, ...cfg.lanes.map((l) => seqOf(l.lane)));
