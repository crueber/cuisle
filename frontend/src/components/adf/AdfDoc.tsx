// AdfDoc: renders a normalised ADF document (Go's adf.Doc) as Preact elements. Never innerHTML; never navigates.
import { useLayoutEffect, useRef, useState } from "preact/hooks";
import type { JSX } from "preact";
import { renderNodes } from "./AdfNode";
import type { AdfDocProps } from "./props";
import "./AdfDoc.css";

export function AdfDoc({ doc, ctx, size = "body", clampLines, class: cls }: AdfDocProps) {
  const clampOn = (clampLines ?? 0) > 0;
  const [expandedDoc, setExpandedDoc] = useState<AdfDocProps["doc"] | null>(null);
  const expanded = expandedDoc === doc;
  const [overflow, setOverflow] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const clamped = clampOn && !expanded;

  // Only a clamped rail paragraph can overflow; measure its inner box when doc or clamp changes.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!clamped || !el) return;
    setOverflow(el.scrollHeight > el.clientHeight + 1);
  }, [clamped, doc, clampLines]);

  // The clamp lives on the inner box only, so the "Show all" control (a sibling) is never clipped by it.
  const innerStyle = clamped ? ({ "--adf-clamp": String(clampLines) } as JSX.CSSProperties) : undefined;
  return (
    <div class={["adf", cls ?? ""].filter(Boolean).join(" ")} data-size={size}>
      <div ref={ref} class={clamped ? "adf-body adf-clamped" : "adf-body"} style={innerStyle}>
        {renderNodes(doc.c ?? [], ctx)}
      </div>
      {clampOn && (overflow || expanded) ? (
        <button type="button" class="adf-show-all" aria-expanded={expanded ? "true" : "false"} onClick={() => setExpandedDoc(expanded ? null : doc)}>
          {expanded ? "Show less" : "Show all"}
        </button>
      ) : null}
    </div>
  );
}
