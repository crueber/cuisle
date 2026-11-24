// The node table: one case per normalised node type (ux-spec §14.4). Unknown types fall through to Unknown.
import { Fragment } from "preact";
import type { ComponentChildren, JSX } from "preact";
import type { AdfAttrs, AdfNode as Node } from "../../api/types";
import { Expand } from "./blocks";
import { Card, Unknown } from "./cards";
import { DateChip, Emoji, Mention, Status } from "./inline";
import { applyMarks } from "./marks";
import { FileCard, Media } from "./media";
import type { AdfContext } from "./props";

const HEADINGS = ["h1", "h2", "h3", "h4", "h5", "h6"] as const;
const PANEL_KINDS = ["info", "note", "success", "warning", "error"] as const;
type PanelKind = (typeof PANEL_KINDS)[number];
const PANEL_LABEL: Record<PanelKind, string> = {
  info: "Info panel",
  note: "Note panel",
  success: "Success panel",
  warning: "Warning panel",
  error: "Error panel",
};
const PANEL_GLYPH: Record<PanelKind, string> = { info: "i", note: "✎", success: "✓", warning: "!", error: "×" };

export function renderNodes(nodes: Node[], ctx: AdfContext, maxW?: number): ComponentChildren {
  return nodes.map((n, i) => <Fragment key={i}>{renderNode(n, ctx, maxW)}</Fragment>);
}

export function renderNode(n: Node, ctx: AdfContext, maxW?: number): ComponentChildren {
  const a: AdfAttrs = n.a ?? {};
  const kids = (): ComponentChildren => renderNodes(n.c ?? [], ctx, maxW);
  switch (n.t) {
    case "text":
      return applyMarks(n.x ?? "", n.m ?? [], ctx);
    case "p":
      return <p>{kids()}</p>;
    case "br":
      return <br />;
    case "h": {
      const level = Math.min(6, Math.max(1, a.level ?? 1));
      const Tag = HEADINGS[level - 1] ?? "h1";
      return <Tag class={`adf-h adf-h${level}`}>{kids()}</Tag>;
    }
    case "ul":
      return <ul class="adf-ul">{kids()}</ul>;
    case "ol":
      return <ol class="adf-ol" start={a.order && a.order > 1 ? a.order : undefined}>{kids()}</ol>;
    case "li":
      return <li>{kids()}</li>;
    case "task":
      return <ul class="adf-task">{kids()}</ul>;
    case "taskItem": {
      const done = a.done === true;
      return (
        <li class="adf-task-item" data-done={done ? "true" : "false"}>
          <input type="checkbox" class="adf-task-box" checked={done} disabled aria-label={plainText(n)} />
          <div class="adf-task-body">{kids()}</div>
        </li>
      );
    }
    case "decision":
      return (
        <div class="adf-decision" data-adf="decision">
          <span class="adf-micro">Decision</span>
          <div class="adf-decision-body">{kids()}</div>
        </div>
      );
    case "quote":
      return <blockquote class="adf-quote">{kids()}</blockquote>;
    case "rule":
      return <hr class="adf-rule" />;
    case "code":
      return (
        <div class="adf-code">
          {a.lang ? <span class="adf-code-lang">{a.lang}</span> : null}
          <pre class="adf-pre">
            <code>{n.x ?? ""}</code>
          </pre>
        </div>
      );
    case "panel": {
      const kind: PanelKind = (PANEL_KINDS as readonly string[]).includes(a.panel ?? "") ? (a.panel as PanelKind) : "note";
      return (
        <div class="adf-panel" role="note" aria-label={PANEL_LABEL[kind]} data-adf="panel" data-panel={kind}>
          <span class="adf-panel-icon" aria-hidden="true">
            {PANEL_GLYPH[kind]}
          </span>
          <div class="adf-panel-body">{kids()}</div>
        </div>
      );
    }
    case "table":
      return (
        <div class="adf-table-scroll">
          <table class="adf-table">
            <tbody>{kids()}</tbody>
          </table>
        </div>
      );
    case "tr":
      return <tr>{kids()}</tr>;
    case "th":
    case "td":
      return cell(n, ctx, maxW);
    case "expand":
      return (
        <Expand title={a.title ?? ""} collapsed={a.collapsed === true} ctx={ctx}>
          {kids()}
        </Expand>
      );
    case "status":
      return <Status label={a.label ?? ""} lozenge={a.lozenge ?? ""} />;
    case "mention":
      return <Mention label={a.label ?? ""} self={a.self === true} />;
    case "date":
      return <DateChip date={a.date ?? ""} past={a.past === true} />;
    case "emoji":
      return <Emoji glyph={a.emoji ?? ""} shortName={a.shortName ?? ""} />;
    case "card":
      return <Card a={a} ctx={ctx} />;
    case "media": {
      const key = `${a.attachmentId ?? ""}:${a.cached ?? ""}`;
      return <Media key={key} a={a} ctx={ctx} maxW={maxW} />;
    }
    case "file":
      return <FileCard a={a} ctx={ctx} />;
    default:
      return (
        <Unknown text={n.x ?? ""} ctx={ctx}>
          {kids()}
        </Unknown>
      );
  }
}

function cell(n: Node, ctx: AdfContext, maxW?: number): JSX.Element {
  const a: AdfAttrs = n.a ?? {};
  const colSpan = Math.max(1, a.colspan ?? 1);
  const rowSpan = Math.max(1, a.rowspan ?? 1);
  const width = (a.colwidth ?? []).reduce((sum, w) => sum + w, 0);
  const style = width > 0 ? { width: `${width}px` } : undefined;
  const body = renderNodes(n.c ?? [], ctx, maxW);
  return n.t === "th" ? (
    <th colSpan={colSpan} rowSpan={rowSpan} style={style}>
      {body}
    </th>
  ) : (
    <td colSpan={colSpan} rowSpan={rowSpan} style={style}>
      {body}
    </td>
  );
}

/** The visible text of a node, for accessible names (the task checkbox). */
export function plainText(n: Node): string {
  const own = n.x ?? n.a?.label ?? n.a?.emoji ?? "";
  return own + (n.c ?? []).map(plainText).join("");
}
