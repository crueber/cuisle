// mediaSingle images (full -> thumb -> placeholder fallback) and file cards.
import { useState } from "preact/hooks";
import type { JSX } from "preact";
import type { AdfAttrs } from "../../api/types";
import { formatBytes } from "../../format";
import { openLink } from "./marks";
import type { AdfContext } from "./props";

type Stage = "full" | "thumb" | "none";

function stageFor(cached: string | undefined): Stage {
  return cached === "full" || cached === "thumb" ? cached : "none";
}

function frameStyle(a: AdfAttrs, maxW: number | undefined): JSX.CSSProperties | undefined {
  if (!a.width) return undefined;
  const cap = maxW ? `min(${a.width}px, ${maxW}px)` : `${a.width}px`;
  return { width: `min(${cap}, 100%)` };
}

/**
 * A mediaSingle (block) or an inline image. The parent keys this component by attachment and cache state, so a
 * re-fetched image (EnsureMedia) starts again from the stage its new cache state implies.
 */
export function Media({ a, ctx, maxW }: { a: AdfAttrs; ctx: AdfContext; maxW?: number }) {
  const id = Number(a.attachmentId ?? 0);
  const inline = a.block !== true;
  const [stage, setStage] = useState<Stage>(() => stageFor(a.cached));
  const Root = inline ? "span" : "figure";
  const rootClass = inline
    ? "adf-media adf-media-inline"
    : `adf-media adf-media-block${a.layout === "center" ? " adf-media-center" : ""}`;
  const caption = !inline && a.caption ? <figcaption class="adf-caption">{a.caption}</figcaption> : null;

  if (a.missing) {
    return (
      <Root class={rootClass} data-adf="media" style={frameStyle(a, maxW)}>
        <a href={ctx.issueUrl} class="adf-muted-link" onClick={(e) => openLink(e, ctx.issueUrl, ctx.onOpenUrl)}>
          attachment unavailable ↗
        </a>
        {caption}
      </Root>
    );
  }

  const ratio: JSX.CSSProperties | undefined =
    a.width && a.height ? { aspectRatio: `${a.width} / ${a.height}` } : undefined;
  const alt = a.caption || a.name || "";
  return (
    <Root class={rootClass} data-adf="media" style={frameStyle(a, maxW)}>
      {stage === "none" ? (
        <span class="adf-media-ph" style={ratio}>
          <button type="button" class="adf-btn" onClick={() => ctx.onLoadImage(id)}>
            Load image
          </button>
        </span>
      ) : (
        <button type="button" class="adf-media-hit" onClick={() => ctx.onOpenImage(id)}>
          <img
            src={stage === "full" ? `/media/${id}` : `/media/${id}/thumb`}
            alt={alt}
            width={a.width}
            height={a.height}
            loading="lazy"
            onError={() => setStage(stage === "full" ? "thumb" : "none")}
          />
        </button>
      )}
      {caption}
    </Root>
  );
}

/** The upper-case extension after the last dot, or FILE when the name has none. */
export function extensionOf(name: string): string {
  const dot = name.lastIndexOf(".");
  return dot > 0 && dot < name.length - 1 ? name.slice(dot + 1).toUpperCase() : "FILE";
}

/** A file card: a click downloads the file to the OS temp folder and opens it (ctx.onOpenFile). */
export function FileCard({ a, ctx }: { a: AdfAttrs; ctx: AdfContext }) {
  const name = a.name ?? "";
  if (a.missing) {
    return (
      <div class="adf-file adf-file-missing" data-adf="file">
        <span class="adf-file-name">{name}</span>
        <a href={ctx.issueUrl} class="adf-muted-link" onClick={(e) => openLink(e, ctx.issueUrl, ctx.onOpenUrl)}>
          attachment unavailable ↗
        </a>
      </div>
    );
  }
  const id = Number(a.attachmentId ?? 0);
  return (
    <button type="button" class="adf-file" data-adf="file" onClick={() => ctx.onOpenFile(id)}>
      <span class="adf-file-ext">{extensionOf(name)}</span>
      <span class="adf-file-body">
        <span class="adf-file-name">{name}</span>
        <span class="adf-file-meta">{formatBytes(a.bytes ?? 0)}</span>
      </span>
    </button>
  );
}
