// FROZEN CONTRACT: props of the ticket modal components (ux-spec §14, architecture §4.9).
// Owners: WP-T7 TicketModal, TicketHeader, FieldRail, StatusCombo, Lightbox · WP-T8 TicketContent, AnchorStrip,
// Attachments, IssueRows, Comments, Composer. Data comes from TicketService via api/; optimistic write and draft state
// live in state/ticket*.ts (WP-T7) and reach these components as props.
import type {
  AttachmentView,
  CommentInput,
  CommentOrder,
  CommentView,
  IssueRow,
  LinkGroup,
  TicketDetail,
  TransitionList,
  TransitionOption,
  UserOption,
} from "../../api/types";
import type { AdfContext } from "../../components/adf/props";

/** Modal geometry for the current window (ux-spec §14.2), computed once per resize. */
export interface ModalGeometry {
  modalW: number; // min(1320, vw − 2·margin)
  modalH: number; // min(1000, vh − 2·margin)
  contentW: number; // min(880, modalW − 380)
  railW: number; // the rest
  short: boolean; // vh < 800: short header, no footer
  narrow: boolean; // < 900 CSS px wide (1024 at Large): State group becomes a strip under the header
  stackedLabels: boolean; // rail < 340: labels above values
}

export interface TicketModalProps {
  standupId: number;
  issueId: number;
  /** Issue ids in the current view's order (person: walk then Done; board: lanes, Done, backlog) for J/K. */
  steps: number[];
  /** Inline-card back stack (route.ticket.stack); non-empty shows "← KEY" and enables Backspace. */
  stack: number[];
  vw: number; // unscaled CSS px
  vh: number;
  onStep: (issueId: number) => void; // J/K, ←/→ (keyboard only, no header stepper): replaces the open ticket, keeps the stack empty
  onOpenLinked: (issueId: number) => void; // inline card / issue row to a synced issue: pushes onto the stack
  onBack: () => void; // Backspace / "← KEY"
  onClose: () => void; // Esc (last layer) / ✕ — focus returns to the opening row
}

/** Refresh button state (ux-spec §14.12). */
export interface RefreshState {
  phase: "idle" | "refreshing" | "done" | "failed" | "waiting"; // waiting = queued behind the rate limiter
  at: number; // ms of the last successful refresh ("refreshed 9:58 · just this ticket"); 0 = none
  cooldownMs: number; // > 0: button disabled with a "↻ 8s" countdown
  waitMs: number; // phase waiting: "waiting for Jira (rate limit) · 6s"
  reason: string; // phase failed: "offline" or Jira's reason
}

export interface TicketHeaderProps {
  detail: TicketDetail;
  geometry: ModalGeometry;
  backKey: string; // key of the previous ticket in the stack ("" = none)
  refresh: RefreshState;
  onBack: () => void;
  onRefresh: () => void;
  onOpenInJira: () => void;
  onCopyKey: () => void;
  onClose: () => void;
}

/** One light write's UI state (optimistic: the value already shows the new state while saving). */
export interface WriteState {
  phase: "idle" | "saving" | "failed"; // "saving…" appears after 250 ms
  reason: string; // failed: "Jira rejected the change: <reason>"
}

export type EditField = "status" | "assignee" | "flag";

export interface FieldRailProps {
  detail: TicketDetail;
  geometry: ModalGeometry;
  /** "column" = right rail; "strip" = the State group only, as the 44px strip under the header (narrow). */
  layout: "column" | "strip";
  writes: Record<EditField, WriteState>;
  offline: boolean;
  /** Bumped by S / A to open the Status / Assignee combobox. */
  openToken: { field: "status" | "assignee"; token: number } | null;
  ctx: AdfContext; // paragraph fields render through AdfDoc
  loadTransitions: (signal: AbortSignal) => Promise<TransitionList>;
  loadAssignees: (query: string, signal: AbortSignal) => Promise<UserOption[]>;
  onTransition: (option: TransitionOption) => void; // needsScreen options open option.url instead
  onAssign: (accountId: string) => void; // "" = unassign
  onFlag: (flagged: boolean) => void;
  onRetry: (field: EditField) => void;
  onOpenUrl: (url: string) => void;
  /** Opens the parent row's issue (in the modal when it is synced, else in Jira). Absent = the row is plain text. */
  onOpenParent?: () => void;
}

export interface StatusComboProps {
  id: string;
  statusName: string;
  lane: string; // swatch of the current status (detail.row.lane)
  open?: boolean; // controlled open (S key)
  onOpenChange?: (open: boolean) => void;
  disabled: boolean;
  offline: boolean; // cached list shown disabled with "Offline · open in Jira"
  saving: boolean;
  load: (signal: AbortSignal) => Promise<TransitionList>; // called when the combobox opens (spinner after 250 ms)
  onSelect: (option: TransitionOption) => void;
  onOpenInJira: (url: string) => void; // "screen · ↗ Jira" rows
}

export interface LightboxProps {
  issueKey: string;
  images: AttachmentView[]; // the ticket's images, in attachment order
  index: number;
  onIndex: (index: number) => void; // ← →
  onOpenFile: (attachmentId: number) => void;
  /** Called on open/step when cached != "full" (explicit network trigger, TicketService.EnsureMedia). */
  ensure: (attachmentId: number) => Promise<AttachmentView>;
  onClose: () => void; // Esc returns to the ticket
}

export type AnchorId = "description" | "attachments" | "subtasks" | "links" | "comments";

export interface AnchorSection {
  id: AnchorId;
  label: string; // "ATTACHMENTS"
  count: string; // "5", "3/5", "" for description
}

export interface AnchorStripProps {
  sections: AnchorSection[]; // empty sections omitted
  active: AnchorId;
  onJump: (id: AnchorId) => void;
}

/** A comment the user sent from this modal that is not stored yet (frontend state, ux-spec §14.5). */
export interface PendingComment {
  localId: string;
  input: CommentInput;
  createdAt: number;
  /** sending: 60% opacity, "sending…" after 250 ms; sent: posted, but the refresh failed so the detail lacks it (shown as sent with a note). */
  state: "sending" | "failed" | "sent";
  reason: string; // failed: the cause only ("couldn't reach Jira"); the component writes "Not sent · " once
}

export interface TicketContentProps {
  detail: TicketDetail;
  geometry: ModalGeometry;
  ctx: AdfContext;
  /** Names of parts changed by a Resync while open (ticket:updated): shows "Updated in Jira · Reload". [] = no bar. */
  updated: string[];
  onReload: () => void;
  comments: CommentsProps;
}

export interface AttachmentsProps {
  items: AttachmentView[]; // images (160×100 tiles) then files (240×56 cards)
  onOpenImage: (attachmentId: number) => void;
  onOpenFile: (attachmentId: number) => void;
}

export interface IssueRowsProps {
  subtasks: IssueRow[];
  subtasksDone: number;
  linkGroups: LinkGroup[];
  onOpen: (row: IssueRow) => void; // issueId > 0 → modal (stack), else row.url in the browser
}

export interface ComposerProps {
  draft: CommentInput; // autosaved per issue (400 ms debounce, flush on blur/close)
  selfName: string; // "posts to Jira as <name>" (the connected account; "Mock User" in mock mode)
  selfInitials: string;
  disabled: boolean; // !detail.can.comment or offline
  disabledReason: string;
  focusToken: number; // bumped by C
  onChange: (input: CommentInput) => void;
  onSend: (input: CommentInput) => void; // Send / Ctrl+Enter; disabled when the text is empty
  onBlur: () => void;
  searchMentions: (query: string, signal: AbortSignal) => Promise<UserOption[]>; // "@" opens the people combobox
}

export interface CommentsProps {
  comments: CommentView[]; // oldest first from Go; drawn in `order`
  order: CommentOrder;
  onOrder: (order: CommentOrder) => void; // persists app setting ticket.comment_order
  newSince: number; // the "↑ N NEW SINCE STANDUP" marker boundary; 0 = none
  pending: PendingComment[];
  onRetry: (localId: string) => void;
  onEdit: (localId: string) => void; // text goes back into the composer
  onDiscard: (localId: string) => void;
  composer: ComposerProps;
  ctx: AdfContext;
}
