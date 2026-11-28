// TicketContent: the scrolling content column of the ticket modal (ux-spec §14.4, §14.5, §14.9; props.ts
// TicketContentProps). It composes AnchorStrip, the description (ADF), Attachments, IssueRows and Comments, and wires
// their open callbacks to the AdfContext. Rendering makes no api call: the data comes from `detail`.
import { useEffect, useRef, useState } from "preact/hooks";
import type { AdfDoc, IssueRow, TicketDetail } from "../../api/types";
import { AdfDoc as AdfBody } from "../../components/adf/AdfDoc";
import type { AnchorId, AnchorSection, TicketContentProps } from "./props";
import { AnchorStrip } from "./AnchorStrip";
import { Attachments } from "./Attachments";
import { Comments } from "./Comments";
import { IssueRows } from "./IssueRows";
import "./TicketContent.css";

const hasNodes = (d: AdfDoc | null | undefined): d is AdfDoc => (d?.c.length ?? 0) > 0;

/** Strip entries from the detail: empty sections are omitted; Comments always shows (the composer lives there). */
export function anchorSections(detail: TicketDetail, commentCount: number): AnchorSection[] {
  const out: AnchorSection[] = [];
  if (hasNodes(detail.description)) out.push({ id: "description", label: "DESCRIPTION", count: "" });
  const attachments = detail.attachments?.length ?? 0;
  if (attachments > 0) out.push({ id: "attachments", label: "ATTACHMENTS", count: String(attachments) });
  const subtasks = detail.subtasks?.length ?? 0;
  if (subtasks > 0) out.push({ id: "subtasks", label: "SUBTASKS", count: `${detail.subtasksDone}/${subtasks}` });
  const links = (detail.linkGroups ?? []).reduce((n, g) => n + (g.rows?.length ?? 0), 0);
  if (links > 0) out.push({ id: "links", label: "LINKS", count: String(links) });
  out.push({ id: "comments", label: "COMMENTS", count: String(commentCount) });
  return out;
}

export function TicketContent(props: TicketContentProps) {
  const { detail, ctx, updated, onReload, comments } = props;
  const rootRef = useRef<HTMLDivElement>(null);
  const sections = anchorSections(detail, comments.comments.length);
  const [tracked, setTracked] = useState<AnchorId | null>(null);
  const active = sections.find((s) => s.id === tracked)?.id ?? sections[0]?.id ?? "comments";
  const sectionsKey = sections.map((s) => s.id).join(",");

  // The entry follows the reader (no timers): the first section in DOM order that intersects the band just below the
  // sticky strip becomes current. The band is unverified in a real browser (jsdom has no layout).
  useEffect(() => {
    const root = rootRef.current;
    if (!root || typeof IntersectionObserver === "undefined") return;
    const visible = new Set<string>();
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          const id = e.target.getAttribute("data-section");
          if (!id) continue;
          if (e.isIntersecting) visible.add(id);
          else visible.delete(id);
        }
        const first = Array.from(root.querySelectorAll("[data-section]"))
          .map((el) => el.getAttribute("data-section"))
          .find((id) => id !== null && visible.has(id));
        if (first) setTracked(first as AnchorId);
      },
      { root: root.parentElement, rootMargin: "-40px 0px -70% 0px" },
    );
    root.querySelectorAll("[data-section]").forEach((el) => io.observe(el));
    return () => io.disconnect();
  }, [sectionsKey]);

  const jump = (id: AnchorId) => {
    setTracked(id);
    rootRef.current?.querySelector<HTMLElement>(`[data-section="${id}"]`)?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  const openRow = (row: IssueRow) => {
    if (row.issueId > 0) ctx.onOpenIssue(row.issueId);
    else ctx.onOpenUrl(row.url);
  };

  // Column padding steps down with the modal width (ux-spec §14.4: 32, 24 at 1280, 20 at 1024).
  const { modalW } = props.geometry;
  const pad = modalW >= 1200 ? "var(--size-modal-pad)" : modalW >= 960 ? "var(--size-modal-pad-1280)" : "var(--size-modal-pad-1024)";

  return (
    <div class="tc" ref={rootRef} style={`--tc-pad:${pad};padding:0 ${pad} ${pad}`}>
      <AnchorStrip sections={sections} active={active} onJump={jump} />

      {updated.length > 0 ? (
        <div class="tc-updated" role="status">
          <span>Updated in Jira · {updated.join(", ")}</span>
          <button type="button" class="tc-reload" onClick={() => onReload()}>
            Reload
          </button>
        </div>
      ) : null}

      {hasNodes(detail.description) ? (
        <section class="tc-desc" data-section="description">
          <AdfBody doc={detail.description} ctx={ctx} size="body" />
        </section>
      ) : null}

      <Attachments items={detail.attachments ?? []} onOpenImage={ctx.onOpenImage} onOpenFile={ctx.onOpenFile} />

      <IssueRows
        subtasks={detail.subtasks ?? []}
        subtasksDone={detail.subtasksDone}
        linkGroups={detail.linkGroups ?? []}
        onOpen={openRow}
      />

      <Comments {...comments} />
    </div>
  );
}
