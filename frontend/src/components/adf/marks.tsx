// Text marks (ux-spec §14.4): native elements, links through ctx.onOpenUrl, textColor through the contrast clamp.
import type { ComponentChildren } from "preact";
import type { AdfMark } from "../../api/types";
import { clampTextColor } from "./contrast";
import type { AdfContext } from "./props";

const SAFE_URL = /^(https?:\/\/|mailto:)/i;

/** Every link opens in the OS browser through the host; the webview never navigates. */
export function openLink(e: MouseEvent, url: string, open: (url: string) => void): void {
  e.preventDefault();
  open(url);
}

/** Wraps text in its marks, first mark innermost. */
export function applyMarks(text: string, marks: AdfMark[], ctx: AdfContext): ComponentChildren {
  return marks.reduce<ComponentChildren>((kids, m) => wrapMark(m, kids, ctx), text);
}

function wrapMark(m: AdfMark, kids: ComponentChildren, ctx: AdfContext): ComponentChildren {
  switch (m.t) {
    case "strong":
      return <strong>{kids}</strong>;
    case "em":
      return <em>{kids}</em>;
    case "strike":
      return <s>{kids}</s>;
    case "underline":
      return <u>{kids}</u>;
    case "sub":
      return <sub>{kids}</sub>;
    case "sup":
      return <sup>{kids}</sup>;
    case "code":
      return <code class="adf-code-mark">{kids}</code>;
    case "link": {
      const href = m.v ?? "";
      if (!SAFE_URL.test(href)) return kids;
      return (
        <a href={href} class="adf-link" onClick={(e) => openLink(e, href, ctx.onOpenUrl)}>
          {kids}
        </a>
      );
    }
    case "color": {
      const color = clampTextColor(m.v ?? "", ctx.theme);
      return color ? <span style={{ color }}>{kids}</span> : kids;
    }
    default:
      return kids;
  }
}
