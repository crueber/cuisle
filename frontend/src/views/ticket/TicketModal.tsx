// TicketModal: the ticket "Docket" (ux-spec §14, ADR 0015). It owns the frame and the keyboard; the header, the field rail,
// the content column (WP-T8) and the lightbox are its parts. All state (session, detail, optimistic writes, refresh,
// drafts) lives in state/ticket.ts. Opening makes ONE facade call (TicketService.Get); nothing else is fetched until the
// user asks (Status list, Refresh, writes, an image that is not cached).
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { api, toAppError } from "../../api";
import type { AttachmentView, CommentOrder, TicketDetail } from "../../api/types";
import type { AdfContext } from "../../components/adf/props";
import { trapTab, useDialogFocus } from "../../components/modal";
import { connection, setAppSetting, settings } from "../../state/app";
import { paletteOpen } from "../../state/palette";
import { updateDialogOpen } from "../../state/update";
import {
  applyAssign,
  applyFlag,
  applyTransition,
  discardComment,
  editComment,
  editDraft,
  flushDraft,
  loadAssignees,
  loadTicket,
  loadTransitions,
  releaseTicket,
  refreshTicket,
  reloadTicket,
  retryComment,
  retryWrite,
  sendComment,
  ticketDetail,
  ticketDraft,
  ticketError,
  ticketKeyOf,
  ticketPending,
  ticketRefresh,
  ticketUpdated,
  ticketWrites,
} from "../../state/ticket";
import { showToast } from "../../state/ui";
import { FieldRail } from "./FieldRail";
import { Lightbox } from "./Lightbox";
import type { EditField, ModalGeometry, TicketModalProps } from "./props";
import { TicketContent } from "./TicketContent";
import { TicketHeader } from "./TicketHeader";
import { openIssueKey, openLink } from "./TicketModal.util";
import "./TicketModal.css";

/** Content column width by window width (the ux-spec §14.2 table); between rows the nearest smaller row's value applies. */
function contentColumn(vw: number): number {
  return vw >= 1920 ? 880 : vw >= 1440 ? 840 : vw >= 1280 ? 836 : 700;
}

/** Modal geometry for an unscaled CSS-pixel window (ux-spec §14.2). The four table rows are exact. */
export function modalGeometry(vw: number, vh: number): ModalGeometry {
  const mx = vw >= 1440 ? 100 : vw >= 1280 ? 32 : 12;
  const my = vh >= 1000 ? 40 : vh >= 900 ? 32 : vh >= 720 ? 24 : 12;
  const modalW = Math.max(1, Math.min(vw >= 1440 ? 1320 : 1240, vw - 2 * mx));
  const modalH = Math.max(1, Math.min(1000, vh - 2 * my));
  const narrow = vw < 900;
  // Under 900 wide there is no rail column: the content takes the width (the State group becomes a strip).
  const contentW = narrow ? Math.max(1, Math.min(880, modalW - 8)) : Math.min(contentColumn(vw), modalW - 292);
  const railW = narrow ? 0 : modalW - contentW - 8; // 3px borders and the rail rule
  return { modalW, modalH, contentW, railW, short: vh < 800, narrow, stackedLabels: railW < 340 };
}

const HINTS: Array<[string[], string]> = [
  [["J", "K"], "step"],
  [["C"], "comment"],
  [["S"], "status"],
  [["A"], "assignee"],
  [["F"], "flag"],
  [["R"], "refresh"],
  [["O"], "open in Jira"],
  [["Esc"], "close"],
];

function Hints() {
  return (
    <div class="tm-hints" data-part="hints">
      {HINTS.map(([keys, what]) => (
        <span class="tm-hint" key={what}>
          {keys.map((k) => (
            <kbd key={k}>{k}</kbd>
          ))}
          <span>{what}</span>
        </span>
      ))}
    </div>
  );
}

/** Keys typed into these never step, flag, refresh … (the composer, the Status filter, any text field). */
function isTyping(t: Element | null): boolean {
  if (!t) return false;
  const el = t as HTMLElement;
  return el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT" || el.isContentEditable || el.getAttribute("role") === "combobox";
}

const initials = (name: string): string =>
  name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0]!.toUpperCase())
    .join("");

export function TicketModal(props: TicketModalProps) {
  const { standupId, issueId, steps, stack, vw, vh, onStep, onOpenLinked, onBack, onClose } = props;
  const dialogRef = useRef<HTMLDivElement>(null);
  const [lightbox, setLightbox] = useState<number | null>(null);
  const [hintsOpen, setHintsOpen] = useState(false);
  const [focusToken, setFocusToken] = useState(0);
  const [openToken, setOpenToken] = useState<{ field: "status" | "assignee"; token: number } | null>(null);
  const [expandAll, setExpandAll] = useState<{ open: boolean; token: number } | undefined>(undefined);

  const geometry = useMemo(() => modalGeometry(vw, vh), [vw, vh]);
  const shown = ticketDetail.value;
  const detail: TicketDetail | null = shown && shown.issueId === issueId ? shown : null;
  const error = ticketError.value;
  const writes = ticketWrites.value;
  const offline = detail?.can.reason === "offline";
  const order: CommentOrder = settings.value["ticket.comment_order"] === "oldest" ? "oldest" : "newest";
  const wantedTheme = settings.value.theme; // read for the dependency: the resolved theme is on <html>
  const theme: "light" | "dark" = (document.documentElement.dataset.theme ?? wantedTheme) === "dark" ? "dark" : "light";
  const images: AttachmentView[] = detail ? detail.attachments.filter((a) => a.image) : [];

  // Load whenever the issue changes (a local read; the only call opening makes) and reset per-ticket UI overlays.
  useEffect(() => {
    setLightbox(null);
    setHintsOpen(false);
    setExpandAll(undefined);
    void loadTicket(standupId, issueId);
  }, [standupId, issueId]);

  // Mount: take focus (keys work from the dialog), and give it back to the opener on close. Unmount: save the draft.
  useDialogFocus(dialogRef, releaseTicket);

  const step = (delta: 1 | -1) => {
    const at = steps.indexOf(issueId);
    const next = at < 0 ? undefined : steps[at + delta];
    if (next !== undefined) onStep(next);
  };

  // The modal's own keyboard map (ux-spec §14.10). Global keys are suspended while it is open (WP-T9).
  const live = useRef({ props, detail, offline, lightbox, hintsOpen, geometry, step });
  live.current = { props, detail, offline, lightbox, hintsOpen, geometry, step };
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const s = live.current;
      const dlg = dialogRef.current;
      const target = e.target as Element | null;
      if (!dlg || s.lightbox !== null) return; // the lightbox owns the keys while it is open
      if (paletteOpen.peek() !== null || updateDialogOpen.peek()) return; // higher overlay owns the keys
      if (e.ctrlKey || e.metaKey || e.altKey || e.isComposing) return;
      if (e.key === "Tab") {
        trapTab(e, dlg);
        return;
      }
      if (!(target === document.body || target === document.documentElement || (target && dlg.contains(target)))) return;
      if (e.defaultPrevented) return; // a combobox closed itself: the modal stays

      if (e.key === "Escape") {
        e.preventDefault();
        const el = target as HTMLElement | null;
        if (s.hintsOpen) setHintsOpen(false);
        else if (el && (el.tagName === "TEXTAREA" || el.isContentEditable)) {
          el.blur(); // flushes the draft (the composer's onBlur); focus stays inside the modal
          dlg.focus();
        } else s.props.onClose();
        return;
      }
      if (isTyping(target)) return;

      const key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
      const d = s.detail;
      switch (key) {
        case "j":
        case "ArrowRight":
          s.step(1);
          break;
        case "k":
        case "ArrowLeft":
          s.step(-1);
          break;
        case "c":
          setFocusToken((n) => n + 1);
          break;
        case "s":
          if (d && d.can.transition && !s.offline && ticketWrites.value.status.phase !== "saving") {
            setOpenToken((p) => ({ field: "status", token: (p?.token ?? 0) + 1 }));
          }
          break;
        case "a":
          if (d && d.can.assign && !s.offline && ticketWrites.value.assignee.phase !== "saving") {
            setOpenToken((p) => ({ field: "assignee", token: (p?.token ?? 0) + 1 }));
          }
          break;
        case "f":
          if (d && d.can.flag && !s.offline && ticketWrites.value.flag.phase !== "saving") void applyFlag(!d.row.blocked);
          break;
        case "r":
          void refreshTicket();
          break;
        case "o":
          if (d) openIssueKey(d.row.key);
          break;
        case "e":
          setExpandAll((p) => ({ open: !(p?.open ?? false), token: (p?.token ?? 0) + 1 }));
          break;
        case "Backspace":
          if (s.props.stack.length > 0) s.props.onBack();
          break;
        case "?":
          if (s.geometry.short) setHintsOpen((v) => !v);
          break;
        default:
          return;
      }
      e.preventDefault();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  const scrimDown = useRef(false);
  const backId = stack[stack.length - 1];
  const backKey = backId === undefined ? "" : ticketKeyOf(backId) || "back";
  const key = detail?.row.key ?? ticketKeyOf(issueId);

  const openAttachment = (id: number) => {
    api()
      .ticket.openAttachment(id)
      .catch((e: unknown) => showToast({ message: toAppError(e).message, timeoutMs: 6000 }));
  };
  const ensureMedia = (id: number): Promise<AttachmentView> => api().ticket.ensureMedia(id);

  const attachmentCache = useMemo(() => {
    const m = new Map<number, string>();
    if (detail) {
      for (const a of detail.attachments) m.set(a.id, a.cached);
    }
    return m;
  }, [detail?.attachments]);

  const ctx: AdfContext | null = detail && {
    onOpenIssue: (id) => onOpenLinked(id),
    onOpenUrl: (url) => openLink(url, detail.row.key),
    onOpenImage: (id) => {
      const at = images.findIndex((a) => a.id === id);
      if (at >= 0) setLightbox(at);
    },
    onOpenFile: openAttachment,
    onLoadImage: (id) => {
      ensureMedia(id).catch((e: unknown) => showToast({ message: toAppError(e).message, timeoutMs: 6000 }));
    },
    issueUrl: detail.url,
    theme,
    expandAll,
    mediaCached: (id) => attachmentCache.get(id),
  };

  const selfName = connection.value?.displayName || "you";
  const copyKey = () => {
    if (!detail) return;
    void navigator.clipboard?.writeText(detail.row.key).then(
      () => showToast({ message: `Copied ${detail.row.key}`, timeoutMs: 2500 }),
      () => undefined,
    );
  };

  const rail = (layout: "column" | "strip" | "inline") =>
    detail && ctx ? (
      <FieldRail
        detail={detail}
        geometry={geometry}
        layout={layout === "strip" ? "strip" : "column"}
        rest={layout === "inline"}
        writes={writes}
        offline={offline}
        openToken={openToken}
        ctx={ctx}
        loadTransitions={loadTransitions}
        loadAssignees={loadAssignees}
        onTransition={(o) => void applyTransition(o)}
        onAssign={(id) => void applyAssign(id)}
        onFlag={(f) => void applyFlag(f)}
        onRetry={(f: EditField) => void retryWrite(f)}
        onOpenUrl={(url) => openLink(url, detail.row.key)}
        onOpenParent={() => {
          const p = detail.parent;
          if (!p) return;
          if (p.issueId > 0) onOpenLinked(p.issueId);
          else openIssueKey(p.key);
        }}
      />
    ) : null;

  const disabledReason = !detail ? "" : offline ? "Offline · can't post" : detail.can.reason === "auth" ? "Reconnect to post" : detail.can.reason === "read_only" ? "Read-only access" : "";
  const scale = (px: number) => `calc(var(--scale) * ${px}px)`;

  return (
    <div
      class="tm-scrim"
      // A click on the mask itself (pressed and released there, so a text selection dragged out of the modal doesn't count)
      // closes the modal. A focused comment box blurs on the press, which flushes its draft first.
      onMouseDown={(e) => {
        scrimDown.current = e.target === e.currentTarget;
      }}
      onClick={(e) => {
        const down = scrimDown.current;
        scrimDown.current = false;
        if (down && e.target === e.currentTarget) onClose();
      }}
    >
      <div
        class="tm"
        role="dialog"
        aria-modal="true"
        aria-label={key ? `Ticket ${key}` : "Ticket"}
        tabIndex={-1}
        ref={dialogRef}
        data-short={geometry.short ? "true" : "false"}
        data-narrow={geometry.narrow ? "true" : "false"}
        style={{ width: scale(geometry.modalW), height: scale(geometry.modalH) }}
      >
        {detail ? (
          <TicketHeaderHost
            detail={detail}
            geometry={geometry}
            backKey={backKey}
            onBack={onBack}
            onRefresh={() => void refreshTicket()}
            onOpenInJira={() => openIssueKey(detail.row.key)}
            onCopyKey={copyKey}
            onClose={onClose}
          />
        ) : (
          <div class="tm-bare">
            <span class="tm-bare-key">{key}</span>
            <span class="tm-bare-spacer" />
            <button type="button" class="tm-bare-close" aria-label="Close" onClick={onClose}>
              ✕
            </button>
          </div>
        )}

        {error && !detail ? (
          <div class="tm-error" role="alert">
            {error.message}
          </div>
        ) : null}

        {detail && ctx && geometry.narrow ? <div class="tm-strip">{rail("strip")}</div> : null}

        {detail && ctx ? (
          <div class="tm-body">
            <div class="tm-content" style={{ maxWidth: scale(geometry.contentW) }}>
              {geometry.narrow ? <div class="tm-rest">{rail("inline")}</div> : null}
              <TicketContentHost
                detail={detail}
                geometry={geometry}
                ctx={ctx}
                standupId={standupId}
                order={order}
                selfName={selfName}
                offline={offline}
                disabledReason={disabledReason}
                focusToken={focusToken}
              />
            </div>
            {geometry.narrow ? null : (
              <div class="tm-rail" style={{ width: scale(geometry.railW) }}>
                {rail("column")}
              </div>
            )}
          </div>
        ) : (
          <div class="tm-body tm-body-empty" />
        )}

        {geometry.short ? (
          <>
            <button type="button" class="tm-help" aria-label="Keyboard shortcuts" aria-expanded={hintsOpen ? "true" : "false"} onClick={() => setHintsOpen((v) => !v)}>
              ?
            </button>
            {hintsOpen ? (
              <div class="tm-pop">
                <Hints />
              </div>
            ) : null}
          </>
        ) : (
          <footer class="tm-footer" data-part="footer">
            <Hints />
          </footer>
        )}
      </div>

      {lightbox !== null && detail && images.length > 0 ? (
        <Lightbox
          issueKey={detail.row.key}
          images={images}
          index={Math.min(lightbox, images.length - 1)}
          onIndex={setLightbox}
          onOpenFile={openAttachment}
          ensure={ensureMedia}
          onClose={() => setLightbox(null)}
        />
      ) : null}
    </div>
  );
}

/** Isolates high-frequency refresh countdown ticks so they don't re-render TicketModal/FieldRail/TicketContent. */
function TicketHeaderHost(props: Omit<Parameters<typeof TicketHeader>[0], "refresh">) {
  return <TicketHeader {...props} refresh={ticketRefresh.value} />;
}

/** Isolates composer draft & pending comment signals so typing never re-renders TicketHeader or FieldRail. */
function TicketContentHost({
  detail,
  geometry,
  ctx,
  standupId,
  order,
  selfName,
  offline,
  disabledReason,
  focusToken,
}: {
  detail: TicketDetail;
  geometry: ModalGeometry;
  ctx: AdfContext;
  standupId: number;
  order: CommentOrder;
  selfName: string;
  offline: boolean;
  disabledReason: string;
  focusToken: number;
}) {
  return (
    <TicketContent
      detail={detail}
      geometry={geometry}
      ctx={ctx}
      updated={ticketUpdated.value}
      onReload={() => void reloadTicket()}
      comments={{
        comments: detail.comments,
        order,
        onOrder: (o) => setAppSetting("ticket.comment_order", o),
        newSince: detail.newSince,
        pending: ticketPending.value,
        onRetry: (id) => void retryComment(id),
        onEdit: editComment,
        onDiscard: discardComment,
        ctx,
        composer: {
          draft: ticketDraft.value,
          selfName,
          selfInitials: initials(selfName),
          disabled: !detail.can.comment || offline,
          disabledReason,
          focusToken,
          onChange: editDraft,
          onSend: (input) => void sendComment(input),
          onBlur: () => void flushDraft(),
          searchMentions: (q) => api().ticket.searchMentions(standupId, q),
        },
      }}
    />
  );
}
