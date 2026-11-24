// Attachments section of the ticket content column (ux-spec §14.4 "Attachments section"; props.ts AttachmentsProps).
// Images first as 160x100 tiles (always the thumbnail), then file cards. Opening goes through the callbacks.
import type { AttachmentView } from "../../api/types";
import type { AttachmentsProps } from "./props";
import "./Attachments.css";

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** Decimal units (1 KB = 1000 B): "412 KB", "18.4 KB". */
function formatSize(n: number): string {
  if (n >= 1e6) return `${Math.round(n / 1e6)} MB`;
  if (n >= 1e3) return n >= 1e5 ? `${Math.round(n / 1e3)} KB` : `${(n / 1e3).toFixed(1)} KB`;
  return `${n} B`;
}

/** Local calendar day, "18 Nov". */
function dayLabel(ms: number): string {
  const d = new Date(ms);
  return `${d.getDate()} ${MONTHS[d.getMonth()] ?? ""}`;
}

function meta(a: AttachmentView): string {
  return `${formatSize(a.bytes)} · ${a.authorInitials} · ${dayLabel(a.createdAt)}`;
}

export function Attachments({ items, onOpenImage, onOpenFile }: AttachmentsProps) {
  if (items.length === 0) return null;
  const ordered = [...items.filter((a) => a.image), ...items.filter((a) => !a.image)];
  return (
    <section class="tk-section att" data-section="attachments">
      <h2 class="tk-head">
        Attachments <span class="tk-count">{items.length}</span>
      </h2>
      <div class="att-grid">
        {ordered.map((a) =>
          a.image ? (
            <button
              key={a.id}
              type="button"
              class="att-tile"
              data-attachment-id={a.id}
              data-kind="image"
              onClick={() => onOpenImage(a.id)}
            >
              <img class="att-thumb" src={a.thumbUrl} alt={a.name} loading="lazy" />
              <span class="att-name">{a.name}</span>
              <span class="att-meta">{meta(a)}</span>
            </button>
          ) : (
            <button
              key={a.id}
              type="button"
              class="att-card"
              data-attachment-id={a.id}
              data-kind="file"
              onClick={() => onOpenFile(a.id)}
            >
              <span class="att-ext">{a.ext}</span>
              <span class="att-body">
                <span class="att-name">{a.name}</span>
                <span class="att-meta">{meta(a)}</span>
              </span>
            </button>
          ),
        )}
      </div>
    </section>
  );
}
