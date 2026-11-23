// FROZEN CONTRACT (implemented in Wave 0). The typed facade: the ONLY way the UI talks to Go (architecture §4.4).
// Every method rejects with AppError. Tests and `vite --mode mock` install the mock with setApi(createMockApi()).
import * as AppSvc from "../bindings/github.com/crueber/cuisle/internal/app/appservice";
import * as StandupSvc from "../bindings/github.com/crueber/cuisle/internal/app/standupservice";
import * as ConfigSvc from "../bindings/github.com/crueber/cuisle/internal/app/configservice";
import * as ConnSvc from "../bindings/github.com/crueber/cuisle/internal/app/connectionservice";
import * as SyncSvc from "../bindings/github.com/crueber/cuisle/internal/app/syncservice";
import * as SinkSvc from "../bindings/github.com/crueber/cuisle/internal/app/sinkservice";
import * as HistorySvc from "../bindings/github.com/crueber/cuisle/internal/app/historyservice";
import { wrap } from "./errors";
import type * as T from "./types";

export * from "./types";
export { AppError, toAppError } from "./errors";
export type { ErrorCode } from "./errors";

export interface Api {
  app: {
    bootstrap(): Promise<T.Bootstrap>;
    setAppSetting(key: string, value: string): Promise<T.SaveAck>;
    setUIState(patch: T.UIStatePatch): Promise<void>;
    openIssue(issueKey: string): Promise<void>;
    diagnostics(): Promise<T.Diagnostics>;
    reportPerf(marks: Record<string, number>): Promise<void>;
    logClientError(message: string, stack: string): Promise<void>;
  };
  standup: {
    list(): Promise<T.StandupTab[]>;
    view(standupId: number): Promise<T.StandupView>;
  };
  config: {
    get(standupId: number): Promise<T.StandupConfig>;
    create(): Promise<T.StandupConfig>;
    duplicate(standupId: number): Promise<T.StandupConfig>;
    copySettingsFrom(standupId: number, fromStandupId: number): Promise<T.StandupConfig>;
    discardDraft(standupId: number): Promise<void>;
    delete(standupId: number): Promise<T.SaveAck>;
    restore(standupId: number): Promise<T.SaveAck>;
    move(standupId: number, toPosition: number): Promise<T.SaveAck>;
    update(standupId: number, patch: T.StandupPatch): Promise<T.SaveAck>;
    setSpace(standupId: number, spaceId: string): Promise<T.StandupConfig>;
    setBoard(standupId: number, boardId: string): Promise<T.StandupConfig>;
    setColumnLane(standupId: number, columnName: string, lane: T.MapLane): Promise<T.SaveAck>;
    moveColumn(standupId: number, columnName: string, toIndex: number): Promise<T.SaveAck>;
    setWalkOrder(standupId: number, lanes: T.WalkLane[]): Promise<T.SaveAck>;
    setLaneLimit(standupId: number, lane: T.WalkLane, days: number): Promise<T.SaveAck>;
    setSpeakerIncluded(standupId: number, accountId: string, included: boolean): Promise<T.SaveAck>;
    moveSpeaker(standupId: number, accountId: string, toPosition: number): Promise<T.SaveAck>;
    sortSpeakers(standupId: number, mode: T.SpeakerSort): Promise<T.SpeakerConfig[]>;
    addSpeaker(standupId: number, accountId: string): Promise<T.SpeakerConfig[]>;
    removeSpeaker(standupId: number, accountId: string): Promise<T.SaveAck>;
  };
  connection: {
    get(): Promise<T.Connection>;
    connect(input: T.ConnectInput): Promise<T.Connection>;
    disconnect(): Promise<void>;
    searchSpaces(query: string): Promise<T.SpaceOption[]>;
    searchBoards(spaceKey: string, query: string): Promise<T.BoardOption[]>;
    searchUsers(standupId: number, query: string): Promise<T.UserOption[]>;
    validateFilter(standupId: number, expr: string): Promise<T.FilterCheck>;
  };
  sync: {
    start(standupId: number, mode: T.SyncMode): Promise<number>;
    cancel(standupId: number): Promise<void>;
    status(): Promise<T.SyncStatus[]>;
  };
  sink: {
    kinds(): Promise<T.SinkKind[]>;
    list(): Promise<T.SinkConfig[]>;
    save(input: T.SinkInput): Promise<T.SinkConfig>;
    delete(sinkId: number): Promise<void>;
    send(sinkId: number, standupId: number, req: T.SendRequest): Promise<number>;
    outbox(sinkId: number, limit: number): Promise<T.OutboxItem[]>;
    retry(outboxId: number): Promise<void>;
  };
  history: {
    range(standupId: number): Promise<T.HistoryRange>;
    viewAsOf(standupId: number, date: string): Promise<T.StandupView>;
  };
}

// Go never sends nil lists (architecture §3.2); `arr` only guards the generated `T[] | null` types.
const arr = <X>(p: PromiseLike<X[] | null>): Promise<X[]> => wrap(p).then((v) => v ?? []);
// The generated types carry `| null` on list fields; the casts below narrow them to the Fix<> types.
const as = <X>(p: PromiseLike<unknown>): Promise<X> => wrap(p) as Promise<X>;

export const wailsApi: Api = {
  app: {
    bootstrap: () => as(AppSvc.Bootstrap()),
    setAppSetting: (k, v) => as(AppSvc.SetAppSetting(k, v)),
    setUIState: (p) => wrap(AppSvc.SetUIState(p)),
    openIssue: (k) => wrap(AppSvc.OpenIssue(k)),
    diagnostics: () => as(AppSvc.Diagnostics()),
    reportPerf: (m) => wrap(AppSvc.ReportPerf(m)),
    logClientError: (m, s) => wrap(AppSvc.LogClientError(m, s)),
  },
  standup: {
    list: () => as(arr(StandupSvc.List())),
    view: (id) => as(StandupSvc.View(id)),
  },
  config: {
    get: (id) => as(ConfigSvc.Get(id)),
    create: () => as(ConfigSvc.Create()),
    duplicate: (id) => as(ConfigSvc.Duplicate(id)),
    copySettingsFrom: (id, from) => as(ConfigSvc.CopySettingsFrom(id, from)),
    discardDraft: (id) => wrap(ConfigSvc.DiscardDraft(id)),
    delete: (id) => as(ConfigSvc.Delete(id)),
    restore: (id) => as(ConfigSvc.Restore(id)),
    move: (id, to) => as(ConfigSvc.Move(id, to)),
    update: (id, p) => as(ConfigSvc.Update(id, p)),
    setSpace: (id, s) => as(ConfigSvc.SetSpace(id, s)),
    setBoard: (id, b) => as(ConfigSvc.SetBoard(id, b)),
    setColumnLane: (id, c, l) => as(ConfigSvc.SetColumnLane(id, c, l)),
    moveColumn: (id, c, i) => as(ConfigSvc.MoveColumn(id, c, i)),
    setWalkOrder: (id, l) => as(ConfigSvc.SetWalkOrder(id, l)),
    setLaneLimit: (id, l, d) => as(ConfigSvc.SetLaneLimit(id, l, d)),
    setSpeakerIncluded: (id, a, inc) => as(ConfigSvc.SetSpeakerIncluded(id, a, inc)),
    moveSpeaker: (id, a, to) => as(ConfigSvc.MoveSpeaker(id, a, to)),
    sortSpeakers: (id, m) => as(arr(ConfigSvc.SortSpeakers(id, m))),
    addSpeaker: (id, a) => as(arr(ConfigSvc.AddSpeaker(id, a))),
    removeSpeaker: (id, a) => as(ConfigSvc.RemoveSpeaker(id, a)),
  },
  connection: {
    get: () => as(ConnSvc.Connection()),
    connect: (i) => as(ConnSvc.Connect(i)),
    disconnect: () => wrap(ConnSvc.Disconnect()),
    searchSpaces: (q) => as(arr(ConnSvc.SearchSpaces(q))),
    searchBoards: (k, q) => as(arr(ConnSvc.SearchBoards(k, q))),
    searchUsers: (id, q) => as(arr(ConnSvc.SearchUsers(id, q))),
    validateFilter: (id, e) => as(ConnSvc.ValidateFilter(id, e)),
  },
  sync: {
    start: (id, m) => wrap(SyncSvc.Start(id, m)),
    cancel: (id) => wrap(SyncSvc.Cancel(id)),
    status: () => as(arr(SyncSvc.Status())),
  },
  sink: {
    kinds: () => as(arr(SinkSvc.Kinds())),
    list: () => as(arr(SinkSvc.List())),
    save: (i) => as(SinkSvc.Save(i)),
    delete: (id) => wrap(SinkSvc.Delete(id)),
    send: (s, st, r) => wrap(SinkSvc.Send(s, st, r)),
    outbox: (s, l) => as(arr(SinkSvc.Outbox(s, l))),
    retry: (id) => wrap(SinkSvc.Retry(id)),
  },
  history: {
    range: (id) => as(HistorySvc.Range(id)),
    viewAsOf: (id, d) => as(HistorySvc.ViewAsOf(id, d)),
  },
};

let current: Api = wailsApi;

/** The active facade. */
export function api(): Api {
  return current;
}

/** Installs a facade (mock in tests and `--mode mock`). */
export function setApi(impl: Api): void {
  current = impl;
}
