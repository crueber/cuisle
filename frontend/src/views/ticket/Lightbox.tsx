// Lightbox: the ticket's images full-window (ux-spec §14.7; props frozen in props.ts). The image is never upscaled: it is
// shown at natural size or fitted into the window (minus the 64px top bar), centred. ← → step the images, Esc returns to
// the ticket; the listeners are on the document, so they work wherever focus is. A thumb-only image is ensured
// (TicketService.EnsureMedia, an explicit network trigger) once when it becomes current.
import { useEffect, useRef, useState } from "preact/hooks";
import type { AttachmentView } from "../../api/types";
import { Icon } from "../../components/Icon";
import { trapTab, useDialogFocus } from "../../components/modal";
import { formatBytes, shortDay } from "../../format";
import type { LightboxProps } from "./props";
import "./Lightbox.css";

export { formatBytes as formatSize };

const BAR_PX = 64;
const SIDE_PX = 64; // room for the 56px edge buttons
const BOTTOM_PX = 48; // breathing room under the image

/** Natural size when it fits, else scaled down (never up) to the window minus the bar; integer pixels. */
export function fitImage(w: number, h: number, vw: number, vh: number): { w: number; h: number } {
  const maxW = Math.max(1, vw - 2 * SIDE_PX);
  const maxH = Math.max(1, vh - BAR_PX - BOTTOM_PX);
  const k = Math.min(1, maxW / w, maxH / h);
  return { w: Math.max(1, Math.round(w * k)), h: Math.max(1, Math.round(h * k)) };
}

function useWindowSize(): { w: number; h: number } {
  const [size, setSize] = useState({ w: window.innerWidth, h: window.innerHeight });
  useEffect(() => {
    const on = () => setSize({ w: window.innerWidth, h: window.innerHeight });
    window.addEventListener("resize", on);
    return () => window.removeEventListener("resize", on);
  }, []);
  return size;
}

export function Lightbox({ issueKey, images, index, onIndex, onOpenFile, ensure, onClose }: LightboxProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const win = useWindowSize();
  const [full, setFull] = useState<ReadonlySet<number>>(new Set());
  const asked = useRef(new Set<number>());
  const cur: AttachmentView | undefined = images[index];
  const last = images.length - 1;

  const latest = useRef({ index, last, onIndex, onClose });
  latest.current = { index, last, onIndex, onClose };

  // Focus moves into the dialog when it opens, and returns to the opener when it closes.
  useDialogFocus(rootRef);

  // Keys, on the document: ← → step (never past the ends), Tab stays trapped, Esc closes.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey || e.metaKey || e.altKey || e.isComposing) return;
      if (e.key === "Tab") {
        trapTab(e, rootRef.current);
        return;
      }
      const s = latest.current;
      if (e.key === "ArrowRight" || e.key === "ArrowLeft") {
        e.preventDefault(); // never scroll the page behind
        const next = s.index + (e.key === "ArrowRight" ? 1 : -1);
        if (next >= 0 && next <= s.last) s.onIndex(next);
      } else if (e.key === "Escape") {
        e.preventDefault();
        s.onClose();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  // Ensure the full image once when a thumb-only image becomes current (on open or by stepping).
  const curId = cur?.id;
  const curCached = cur?.cached;
  useEffect(() => {
    if (curId === undefined || curCached === "full" || asked.current.has(curId)) return;
    asked.current.add(curId);
    ensure(curId).then(
      () => setFull((prev) => new Set(prev).add(curId)),
      () => undefined, // the thumb stays and the dialog stays open
    );
  }, [curId, curCached]);

  if (!cur) return null;
  const known = cur.width > 0 && cur.height > 0;
  const size = known ? fitImage(cur.width, cur.height, win.w, win.h) : null;
  const pct = size ? (size.w === cur.width ? "100%" : `fit ${Math.round((size.w / cur.width) * 100)}%`) : "";
  const showFull = cur.cached === "full" || full.has(cur.id);
  const meta = [formatBytes(cur.bytes), known ? `${cur.width}×${cur.height}` : "", cur.authorName, shortDay(cur.createdAt)].filter(Boolean);

  return (
    <div class="lb" role="dialog" aria-modal="true" aria-label="Image viewer" tabIndex={-1} ref={rootRef}>
      <div class="lb-bar">
        <span class="lb-key">{issueKey}</span>
        <span class="lb-name">{cur.name}</span>
        <span class="lb-meta">{meta.join(" · ")}</span>
        <span class="lb-spacer" />
        {pct ? <span class="lb-zoom">{pct}</span> : null}
        <span class="lb-count">{`${index + 1} / ${images.length}`}</span>
        <button type="button" class="lb-btn" onClick={() => onOpenFile(cur.id)}>
          Open file
        </button>
        <button type="button" class="lb-btn lb-close" aria-label="Close image" onClick={onClose}>
          <Icon name="close" size={22} />
        </button>
      </div>
      <div
        class="lb-stage"
        onClick={(e) => {
          if (e.target === e.currentTarget) onClose(); // the dark area around the image, not the image itself
        }}
      >
        <img
          key={cur.id}
          class="lb-img"
          src={showFull ? cur.url : cur.thumbUrl}
          alt={cur.name}
          style={size ? { width: `${size.w}px`, height: `${size.h}px` } : undefined}
        />
      </div>
      {images.length > 1 ? (
        <>
          <button type="button" class="lb-nav lb-prev" aria-label="Previous image" disabled={index <= 0} onClick={() => onIndex(index - 1)}>
            <Icon name="chevron-down" size={28} class="lb-chev" />
          </button>
          <button type="button" class="lb-nav lb-next" aria-label="Next image" disabled={index >= last} onClick={() => onIndex(index + 1)}>
            <Icon name="chevron-down" size={28} class="lb-chev" />
          </button>
        </>
      ) : null}
    </div>
  );
}
