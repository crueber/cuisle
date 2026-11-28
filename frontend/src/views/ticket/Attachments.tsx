// Attachments section of the ticket content column (ux-spec §14.4 "Attachments section"; props.ts AttachmentsProps).
// Images first as 160x100 tiles (always the thumbnail), then file cards. Opening goes through the callbacks.
import type { AttachmentView } from "../../api/types";
import { formatBytes, shortDay } from "../../format";
import type { AttachmentsProps } from "./props";
import "./Attachments.css";

function meta(a: AttachmentView): string {
  return `${formatBytes(a.bytes)} · ${a.authorInitials} · ${shortDay(a.createdAt)}`;
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
