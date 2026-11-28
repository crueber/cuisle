// Comments section of the ticket content column (ux-spec §14.5; props.ts CommentsProps, PendingComment).
// Draws every stored comment in `order`, the new-since-standup marker, pending comments (sending / failed) and the
// composer. Makes no api call: the order toggle, retry, edit and discard are callbacks. Nothing retries by itself.
import { useEffect, useState } from "preact/hooks";
import type { CommentOrder, CommentView } from "../../api/types";
import { AdfDoc } from "../../components/adf/AdfDoc";
import type { AdfContext } from "../../components/adf/props";
import { Segmented } from "../../components/Segmented";
import { MONTHS } from "../../format";
import { Composer } from "./Composer";
import type { CommentsProps, PendingComment } from "./props";
import "./Comments.css";

const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const ORDER: { value: CommentOrder; label: string }[] = [
  { value: "newest", label: "Newest" },
  { value: "oldest", label: "Oldest" },
];

const pad = (n: number) => String(n).padStart(2, "0");

/** "Mon 23 Nov 08:55", local time. */
export function stamp(ms: number): string {
  const d = new Date(ms);
  return `${DAYS[d.getDay()] ?? ""} ${d.getDate()} ${MONTHS[d.getMonth()] ?? ""} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** Whole minutes under an hour, whole hours under a day, then whole days. */
export function age(createdAt: number, now: number): string {
  const min = Math.max(0, Math.floor((now - createdAt) / 60000));
  if (min < 60) return `${min}m`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}

type Entry = { kind: "comment"; comment: CommentView } | { kind: "marker"; count: number; newest: boolean; since: number };

function markerText(count: number, since: number, order: CommentOrder): string {
  const d = new Date(since);
  const day = `${DAYS[d.getDay()] ?? ""} ${d.getDate()} ${MONTHS[d.getMonth()] ?? ""}`.toUpperCase();
  return `${order === "newest" ? "↑" : "↓"} ${count} NEW SINCE STANDUP · ${day}`;
}

function Lock() {
  return (
    <svg class="cm-lock" width="12" height="12" viewBox="0 0 12 12" aria-hidden="true">
      <rect x="2" y="5" width="8" height="6" rx="1" fill="currentColor" />
      <path d="M4 5V3.5a2 2 0 0 1 4 0V5" fill="none" stroke="currentColor" stroke-width="1.5" />
    </svg>
  );
}

function CommentItem({ c, ctx, now }: { c: CommentView; ctx: AdfContext; now: number }) {
  return (
    <article class="cm-item" data-comment-id={c.id} data-self={c.author.self ? "true" : undefined}>
      <header class="cm-meta">
        <span class="cm-initials">{c.author.initials}</span>
        <span class="cm-name">{c.author.name}</span>
        <span class="cm-when">
          {stamp(c.createdAt)} · {age(c.createdAt, now)}
        </span>
        {c.edited ? <span class="cm-edited">edited</span> : null}
        {c.visibility ? (
          <span class="cm-vis">
            <Lock />
            {c.visibility}
          </span>
        ) : null}
      </header>
      <AdfDoc doc={c.body} ctx={ctx} size="comment" />
    </article>
  );
}

function PendingItem({
  p,
  selfName,
  selfInitials,
  onRetry,
  onEdit,
  onDiscard,
}: {
  p: PendingComment;
  selfName: string;
  selfInitials: string;
  onRetry: (localId: string) => void;
  onEdit: (localId: string) => void;
  onDiscard: (localId: string) => void;
}) {
  const [slow, setSlow] = useState(false);
  useEffect(() => {
    setSlow(false); // a retry starts over: "sending…" comes back only after 250 ms again
    if (p.state !== "sending") return;
    const t = setTimeout(() => setSlow(true), 250); // UI-only: "sending…" after 250 ms, never a network timer
    return () => clearTimeout(t);
  }, [p.state]);

  return (
    <article class="cm-item cm-pending" data-pending-id={p.localId} data-state={p.state}>
      <header class="cm-meta">
        <span class="cm-initials">{selfInitials}</span>
        <span class="cm-name">{selfName}</span>
        {p.state === "sending" && slow ? <span class="cm-sending">sending…</span> : null}
      </header>
      <p class="cm-text">{p.input.text}</p>
      {p.state === "sent" ? <p class="cm-kept">Couldn't refresh · showing your comment locally</p> : null}
      {p.state === "failed" ? (
        <>
          <p class="cm-failed">Not sent · {p.reason}</p>
          <p class="cm-kept">Your text is kept.</p>
          <div class="cm-actions">
            <button type="button" class="cm-btn" onClick={() => onRetry(p.localId)}>
              Retry
            </button>
            <button type="button" class="cm-btn" onClick={() => onEdit(p.localId)}>
              Edit
            </button>
            <button type="button" class="cm-btn" onClick={() => onDiscard(p.localId)}>
              Discard
            </button>
          </div>
        </>
      ) : null}
    </article>
  );
}

export function Comments(props: CommentsProps) {
  const { comments, order, onOrder, newSince, pending, onRetry, onEdit, onDiscard, composer, ctx } = props;
  const now = Date.now();

  // Stored comments arrive oldest first. "newest" draws them reversed; the marker sits between the new and old ones.
  const stored = order === "newest" ? [...comments].reverse() : comments;
  const newCount = newSince > 0 ? comments.filter((c) => c.createdAt > newSince).length : 0;
  const entries: Entry[] = stored.map((c) => ({ kind: "comment", comment: c }));
  if (newCount > 0) {
    const at = order === "newest" ? newCount : comments.length - newCount;
    entries.splice(at, 0, { kind: "marker", count: newCount, newest: order === "newest", since: newSince });
  }

  // Pending comments sit at the newest end: newest first reverses them, oldest first keeps them as given.
  const pendingOrdered = order === "newest" ? [...pending].reverse() : pending;
  const pendingNodes = pendingOrdered.map((p) => (
    <PendingItem
      key={`p-${p.localId}`}
      p={p}
      selfName={composer.selfName}
      selfInitials={composer.selfInitials}
      onRetry={onRetry}
      onEdit={onEdit}
      onDiscard={onDiscard}
    />
  ));

  return (
    <section class="tk-section cm" data-section="comments">
      <h2 class="tk-head">
        Comments <span class="tk-count">{comments.length}</span>
      </h2>
      <div class="cm-order">
        <Segmented<CommentOrder>
          id="cm-order"
          label="Comment order"
          value={order}
          options={ORDER}
          onChange={(v) => {
            if (v !== order) onOrder(v);
          }}
        />
      </div>
      <Composer {...composer} />
      <div class="cm-list">
        {order === "newest" ? pendingNodes : null}
        {entries.map((e) =>
          e.kind === "marker" ? (
            <div key="standup-marker" class="cm-marker" data-marker="standup">
              <span>{markerText(e.count, e.since, e.newest ? "newest" : "oldest")}</span>
            </div>
          ) : (
            <CommentItem key={e.comment.id} c={e.comment} ctx={ctx} now={now} />
          ),
        )}
        {order === "oldest" ? pendingNodes : null}
      </div>
    </section>
  );
}
