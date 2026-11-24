// FROZEN CONTRACT. Application types, re-exported from the generated bindings (Go structs are the source of truth).
// Go never sends nil lists or maps (architecture §3.2), so Fix<> removes `| null` from list and map fields at the type
// level. Pointer fields (e.g. StandupView.sprint) keep `| null`. Never import src/bindings outside src/api.
import type * as A from "../bindings/github.com/crueber/cuisle/internal/app/models";
import type * as V from "../bindings/github.com/crueber/cuisle/internal/view/models";
import type * as S from "../bindings/github.com/crueber/cuisle/internal/sync/models";
import type * as K from "../bindings/github.com/crueber/cuisle/internal/sink/models";
import type * as St from "../bindings/github.com/crueber/cuisle/internal/settings/models";
import type * as So from "../bindings/github.com/crueber/cuisle/internal/source/models";
import type * as B from "../bindings/github.com/crueber/cuisle/internal/buildinfo/models";
import type * as Adf from "../bindings/github.com/crueber/cuisle/internal/adf/models";

type IsMap<T> = T extends readonly unknown[] ? false : T extends object ? (string extends keyof T ? true : `${number}` extends keyof T ? true : false) : false;
type FixField<F> = [Exclude<F, null | undefined>] extends [readonly (infer U)[]]
  ? Fix<U>[]
  : IsMap<Exclude<F, null | undefined>> extends true
    ? Fix<Exclude<F, null | undefined>>
    : null extends F
      ? Fix<Exclude<F, null>> | null
      : Fix<F>;
// Optional struct members (Go `omitempty`, e.g. the adf node tree) stay optional, because Go really omits them; maps
// keep their index signatures as before.
export type Fix<T> = T extends readonly (infer U)[]
  ? Fix<U>[]
  : T extends object
    ? IsMap<T> extends true
      ? { [K in keyof T]-?: undefined extends T[K] ? FixField<T[K]> | undefined : FixField<T[K]> }
      : { [K in keyof T]: FixField<Exclude<T[K], undefined>> }
    : T;

// ── view models ──
export type StandupView = Fix<V.StandupView>;
export type SprintInfo = Fix<V.SprintInfo>;
export type AxisDay = Fix<V.AxisDay>;
export type LaneInfo = Fix<V.LaneInfo>;
export type SpeakerView = Fix<V.SpeakerView>;
export type TicketView = Fix<V.TicketView>;
export type Pulse = Fix<V.Pulse>;
export type PulseSeg = Fix<V.PulseSeg>;
export type PulseMark = Fix<V.PulseMark>;
export type PulseSpan = Fix<V.PulseSpan>;
export type BacklogItem = Fix<V.BacklogItem>;
export type Counts = Fix<V.Counts>;
export type ViewOptions = Fix<V.ViewOptions>;
export type SyncStatus = Fix<V.SyncStatus>;
export type StandupTab = Fix<V.StandupTab>;
export type StandupConfig = Fix<V.StandupConfig>;
export type ColumnMapping = Fix<V.ColumnMapping>;
export type SpeakerConfig = Fix<V.SpeakerConfig>;
export type SpaceOption = Fix<V.SpaceOption>;
export type BoardOption = Fix<V.BoardOption>;
export type FilterCheck = Fix<V.FilterCheck>;

// ── ticket modal (ADR 0015, ux-spec §14) ──
export type TicketDetail = Fix<V.TicketDetail>;
export type IssueRefView = Fix<V.IssueRefView>;
export type IssueRow = Fix<V.IssueRow>;
export type LinkGroup = Fix<V.LinkGroup>;
export type AttachmentView = Fix<V.AttachmentView>;
export type PersonRef = Fix<V.PersonRef>;
export type CommentView = Fix<V.CommentView>;
export type CommentInput = Fix<V.CommentInput>;
export type Mention = Fix<V.Mention>;
export type RailGroup = Fix<V.RailGroup>;
export type RailRow = Fix<V.RailRow>;
export type TransitionList = Fix<V.TransitionList>;
export type TransitionOption = Fix<V.TransitionOption>;
export type TicketCaps = Fix<V.TicketCaps>;
export type DevSummary = Fix<V.DevSummary>;
export type DevPR = Fix<V.DevPR>;
export type TicketFieldPrefs = Fix<V.TicketFieldPrefs>;
export type FieldPref = Fix<V.FieldPref>;
export type FieldOption = Fix<V.FieldOption>;
/** Parsed rich text (Go internal/adf): the frontend never sees raw ADF. */
export type AdfDoc = Fix<Adf.Doc>;
export type AdfNode = Fix<Adf.Node>;
export type AdfMark = Fix<Adf.Mark>;
export type AdfAttrs = Fix<Adf.Attrs>;

// ── app I/O ──
export type Bootstrap = Fix<A.Bootstrap>;
export type Connection = Fix<A.Connection>;
export type ConnectInput = A.ConnectInput;
export type SaveAck = Fix<A.SaveAck>;
export type UserOption = Fix<A.UserOption>;
export type Diagnostics = Fix<A.Diagnostics>;
export type RunInfo = Fix<A.RunInfo>;
export type SinkKind = Fix<A.SinkKind>;
export type SinkConfig = Fix<A.SinkConfig>;
export type SinkInput = A.SinkInput;
export type SendRequest = Fix<K.Request>;
export type OutboxItem = Fix<A.OutboxItem>;
export type HistoryRange = Fix<A.HistoryRange>;
export type StandupPatch = St.Patch;
export type UIStatePatch = St.UIStatePatch;
export type UIState = Fix<St.UIState>;
export type WindowState = Fix<St.WindowState>;
export type BuildInfo = Fix<B.Info>;
export type Capabilities = Fix<So.Capabilities>;
export type Writes = Fix<So.Writes>;
export type MediaCacheInfo = Fix<A.MediaCacheInfo>;
export type SinkField = Fix<K.Field>;
export type SinkCapabilities = Fix<K.Capabilities>;

// ── event payloads (architecture §3.3) ──
export type SyncStarted = Fix<S.SyncStarted>;
export type SyncProgress = Fix<S.SyncProgress>;
export type SyncFinished = Fix<S.SyncFinished>;
export type DataChanged = Fix<S.DataChanged>;
export type ConnectionAuth = Fix<S.ConnectionAuth>;
export type SinkDelivered = Fix<A.SinkDelivered>;
export type SinkFailed = Fix<A.SinkFailed>;
export type DayRolled = Fix<A.DayRolled>;
export type TicketUpdated = Fix<S.TicketUpdated>;
export type TicketWriteFailed = Fix<A.TicketWriteFailed>;
export type MediaReady = Fix<S.MediaReady>;
export type SyncThrottled = Fix<S.SyncThrottled>;

// ── string unions used across the UI ──
export type Lane = "todo" | "prog" | "review" | "qa" | "done";
export type WalkLane = "todo" | "prog" | "review" | "qa";
export type MapLane = "backlog" | Lane | "hidden";
export type TicketType = "story" | "bug" | "task" | "spike" | "subtask" | "epic" | "other";
export type RowsMode = "fit" | "roomy";
export type DoneWindow = "sprint" | "14d" | "yesterday";
export type UnassignedPosition = "first" | "last" | "skip";
export type SyncMode = "incremental" | "full";
export type Theme = "light" | "dark" | "system";
export type TextSize = "standard" | "large";
export type SpeakerSort = "shuffle" | "az" | "active";
export type CommentOrder = "newest" | "oldest"; // app setting ticket.comment_order
export type MediaCached = "full" | "thumb" | "none";
export type TicketWriteAction = "transition" | "assign" | "flag" | "comment";
export type RailGroupId = "state" | "details" | "custom" | "development" | "dates";
export type RailEdit = "" | "status" | "assignee" | "flag";
/** RailRow.kind: a field kind or a rail-only kind (Go view.RailKind*; member conventions documented there). */
export type FieldKind =
  | "text" | "paragraph" | "number" | "date" | "datetime" | "url" | "option" | "options" | "cascade" | "user" | "users"
  | "group" | "groups" | "version" | "versions" | "component" | "components" | "labels" | "project" | "team" | "unknown";
export type RailKind = FieldKind | "status" | "flag" | "parent" | "sprint" | "time" | "watchers" | "branch" | "commits" | "pr";
/** Normalised ADF node types (Go adf.Node* constants; ux-spec §14.4). */
export type AdfNodeType =
  | "p" | "h" | "text" | "br" | "ul" | "ol" | "li" | "task" | "taskItem" | "decision" | "quote" | "rule" | "code"
  | "panel" | "table" | "tr" | "th" | "td" | "expand" | "status" | "mention" | "date" | "emoji" | "card" | "media"
  | "file" | "unknown";
export type AdfMarkType = "strong" | "em" | "strike" | "underline" | "sub" | "sup" | "code" | "link" | "color";

/** window.__CUISLE_BOOT__ set by /boot.js before the module entry. */
declare global {
  interface Window {
    __CUISLE_BOOT__?: Bootstrap;
  }
}
