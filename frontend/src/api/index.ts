// FROZEN CONTRACT (implemented in Wave 0). The typed facade: the ONLY way the UI talks to Go (architecture §4.4).
// Every method rejects with AppError. Tests and `vite --mode mock` install the mock with setApi(createMockApi()).
import * as AppSvc from "../bindings/github.com/crueber/cuisle/internal/app/appservice";
import * as StandupSvc from "../bindings/github.com/crueber/cuisle/internal/app/standupservice";
import * as ConfigSvc from "../bindings/github.com/crueber/cuisle/internal/app/configservice";
import * as ConnSvc from "../bindings/github.com/crueber/cuisle/internal/app/connectionservice";
import * as SyncSvc from "../bindings/github.com/crueber/cuisle/internal/app/syncservice";
import * as SinkSvc from "../bindings/github.com/crueber/cuisle/internal/app/sinkservice";
import * as HistorySvc from "../bindings/github.com/crueber/cuisle/internal/app/historyservice";
import * as TicketSvc from "../bindings/github.com/crueber/cuisle/internal/app/ticketservice";
import * as UpdateSvc from "../bindings/github.com/crueber/cuisle/internal/app/updateservice";
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
    /** Opens an http, https or mailto link in the OS (the webview never navigates). */
    openUrl(url: string): Promise<void>;
    diagnostics(): Promise<T.Diagnostics>;
    reportPerf(marks: Record<string, number>): Promise<void>;
    logClientError(message: string, stack: string): Promise<void>;
    mediaCache(): Promise<T.MediaCacheInfo>;
    clearMediaCache(): Promise<T.MediaCacheInfo>;
  };
  standup: {
    list(): Promise<T.StandupTab[]>;
    view(standupId: number): Promise<T.StandupView>;
    /** Every speaker of every live standup (the palette's `@` mode). Local only. */
    people(): Promise<T.PersonHit[]>;
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
    createLane(standupId: number, label: string, columnName: string): Promise<T.StandupConfig>;
    renameLane(standupId: number, lane: string, label: string): Promise<T.SaveAck>;
    deleteLane(standupId: number, lane: string, moveTo: string): Promise<T.StandupConfig>;
    setSpeakerIncluded(standupId: number, accountId: string, included: boolean): Promise<T.SaveAck>;
    moveSpeaker(standupId: number, accountId: string, toPosition: number): Promise<T.SaveAck>;
    sortSpeakers(standupId: number, mode: T.SpeakerSort): Promise<T.SpeakerConfig[]>;
    addSpeaker(standupId: number, accountId: string): Promise<T.SpeakerConfig[]>;
    removeSpeaker(standupId: number, accountId: string): Promise<T.SaveAck>;
  };
  connection: {
    get(): Promise<T.Connection>;
    connect(input: T.ConnectInput): Promise<T.Connection>;
    /** deleteContent: also delete synced content (offered only past storage.cleanup_offer_mb, ux-spec §14.11). */
    disconnect(deleteContent: boolean): Promise<void>;
    searchSpaces(query: string): Promise<T.SpaceOption[]>;
    searchBoards(spaceKey: string, query: string): Promise<T.BoardOption[]>;
    searchUsers(standupId: number, query: string): Promise<T.UserOption[]>;
    validateFilter(standupId: number, expr: string): Promise<T.FilterCheck>;
    ticketFields(): Promise<T.TicketFieldPrefs>;
    pinTicketField(fieldId: string): Promise<T.TicketFieldPrefs>;
    unpinTicketField(fieldId: string): Promise<T.TicketFieldPrefs>;
    moveTicketField(fieldId: string, toPosition: number): Promise<T.SaveAck>;
    setTicketFieldHidden(fieldId: string, hidden: boolean): Promise<T.TicketFieldPrefs>;
  };
  sync: {
    start(standupId: number, mode: T.SyncMode): Promise<number>;
    cancel(standupId: number): Promise<void>;
    status(): Promise<T.SyncStatus[]>;
  };
  /** Ticket modal (ADR 0015, ux-spec §14). `get` is local only; transitions, the writes, refresh, ensureMedia and
   *  openAttachment are explicit network triggers. */
  ticket: {
    get(standupId: number, issueId: number): Promise<T.TicketDetail>;
    /** The palette's ticket finder (ux-spec §15.3). LOCAL ONLY. currentStandupId orders and homes the hits (0 = none). */
    find(query: string, currentStandupId: number, limit: number): Promise<T.TicketFind>;
    /** The palette's "not synced" fetch: the ONE network call of the palette (one request, interactive lane). */
    fetchByKey(standupId: number, key: string): Promise<T.TicketDetail>;
    /** One hit per known issue id, in the order given (unknown ids skipped), tier "" and match []. LOCAL ONLY. The palette's
     *  recent tickets (app setting palette.recent_tickets holds issue ids). */
    byIds(issueIds: number[], currentStandupId: number): Promise<T.TicketHit[]>;
    transitions(issueId: number): Promise<T.TransitionList>;
    transition(standupId: number, issueId: number, transitionId: string): Promise<T.TicketDetail>;
    assign(standupId: number, issueId: number, accountId: string): Promise<T.TicketDetail>;
    setFlag(standupId: number, issueId: number, flagged: boolean): Promise<T.TicketDetail>;
    addComment(standupId: number, issueId: number, input: T.CommentInput): Promise<T.TicketDetail>;
    refresh(standupId: number, issueId: number): Promise<T.TicketDetail>;
    draft(issueId: number): Promise<T.CommentInput>;
    saveDraft(issueId: number, input: T.CommentInput): Promise<T.SaveAck>;
    ensureMedia(attachmentId: number): Promise<T.AttachmentView>;
    openAttachment(attachmentId: number): Promise<void>;
    searchMentions(standupId: number, query: string): Promise<T.UserOption[]>;
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
  /** Auto-update (ADR 0016). `status` is local; `checkNow` sends one releases-list request; `install` downloads,
   *  verifies and installs, then the app restarts (progress arrives as update:status). */
  update: {
    status(): Promise<T.UpdateStatus>;
    checkNow(): Promise<T.UpdateStatus>;
    install(): Promise<void>;
  };
}

// Go never sends nil lists (architecture §3.2); `arr` only guards the generated `T[] | null` types.
const arr = <X>(p: PromiseLike<X[] | null>): Promise<X[]> => wrap(p).then((v) => v ?? []);
// The generated types carry `| null` on list fields; `as` narrows R to Fix<R> while checking assignability to Api.
const as = <R>(p: PromiseLike<R>): Promise<T.Fix<R>> => wrap(p) as unknown as Promise<T.Fix<R>>;

export const wailsApi: Api = {
  app: {
    bootstrap: () => as(AppSvc.Bootstrap()),
    setAppSetting: (k, v) => as(AppSvc.SetAppSetting(k, v)),
    setUIState: (p) => wrap(AppSvc.SetUIState(p)),
    openIssue: (k) => wrap(AppSvc.OpenIssue(k)),
    openUrl: (u) => wrap(AppSvc.OpenURL(u)),
    diagnostics: () => as(AppSvc.Diagnostics()),
    reportPerf: (m) => wrap(AppSvc.ReportPerf(m)),
    logClientError: (m, s) => wrap(AppSvc.LogClientError(m, s)),
    mediaCache: () => as(AppSvc.MediaCache()),
    clearMediaCache: () => as(AppSvc.ClearMediaCache()),
  },
  standup: {
    list: () => as(arr(StandupSvc.List())),
    view: (id) => as(StandupSvc.View(id)),
    people: () => as(arr(StandupSvc.People())),
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
    createLane: (id, l, c) => as(ConfigSvc.CreateLane(id, l, c)),
    renameLane: (id, l, n) => as(ConfigSvc.RenameLane(id, l, n)),
    deleteLane: (id, l, m) => as(ConfigSvc.DeleteLane(id, l, m)),
    setSpeakerIncluded: (id, a, inc) => as(ConfigSvc.SetSpeakerIncluded(id, a, inc)),
    moveSpeaker: (id, a, to) => as(ConfigSvc.MoveSpeaker(id, a, to)),
    sortSpeakers: (id, m) => as(arr(ConfigSvc.SortSpeakers(id, m))),
    addSpeaker: (id, a) => as(arr(ConfigSvc.AddSpeaker(id, a))),
    removeSpeaker: (id, a) => as(ConfigSvc.RemoveSpeaker(id, a)),
  },
  connection: {
    get: () => as(ConnSvc.Connection()),
    connect: (i) => as(ConnSvc.Connect(i)),
    disconnect: (del) => wrap(ConnSvc.Disconnect(del)),
    searchSpaces: (q) => as(arr(ConnSvc.SearchSpaces(q))),
    searchBoards: (k, q) => as(arr(ConnSvc.SearchBoards(k, q))),
    searchUsers: (id, q) => as(arr(ConnSvc.SearchUsers(id, q))),
    validateFilter: (id, e) => as(ConnSvc.ValidateFilter(id, e)),
    ticketFields: () => as(ConnSvc.TicketFields()),
    pinTicketField: (f) => as(ConnSvc.PinTicketField(f)),
    unpinTicketField: (f) => as(ConnSvc.UnpinTicketField(f)),
    moveTicketField: (f, to) => as(ConnSvc.MoveTicketField(f, to)),
    setTicketFieldHidden: (f, h) => as(ConnSvc.SetTicketFieldHidden(f, h)),
  },
  sync: {
    start: (id, m) => wrap(SyncSvc.Start(id, m)),
    cancel: (id) => wrap(SyncSvc.Cancel(id)),
    status: () => as(arr(SyncSvc.Status())),
  },
  ticket: {
    get: (st, id) => as(TicketSvc.Get(st, id)),
    find: (q, st, limit) => as(TicketSvc.FindByKey(q, st, limit)),
    fetchByKey: (st, key) => as(TicketSvc.FetchByKey(st, key)),
    byIds: (ids, st) => as(arr(TicketSvc.ByIDs(ids, st))),
    transitions: (id) => as(TicketSvc.Transitions(id)),
    transition: (st, id, t) => as(TicketSvc.Transition(st, id, t)),
    assign: (st, id, a) => as(TicketSvc.Assign(st, id, a)),
    setFlag: (st, id, f) => as(TicketSvc.SetFlag(st, id, f)),
    addComment: (st, id, i) => as(TicketSvc.AddComment(st, id, i)),
    refresh: (st, id) => as(TicketSvc.Refresh(st, id)),
    draft: (id) => as(TicketSvc.Draft(id)),
    saveDraft: (id, i) => as(TicketSvc.SaveDraft(id, i)),
    ensureMedia: (id) => as(TicketSvc.EnsureMedia(id)),
    openAttachment: (id) => wrap(TicketSvc.OpenAttachment(id)),
    searchMentions: (st, q) => as(arr(TicketSvc.SearchMentions(st, q))),
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
  update: {
    status: () => as(UpdateSvc.Status()) as Promise<T.UpdateStatus>,
    checkNow: () => as(UpdateSvc.CheckNow()) as Promise<T.UpdateStatus>,
    install: () => wrap(UpdateSvc.Install()),
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
