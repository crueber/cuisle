// Inline and block cards (synced issue or URL) and the unknown-node fallback.
import type { ComponentChildren } from "preact";
import type { AdfAttrs } from "../../api/types";
import { Icon } from "../Icon";
import type { IconName } from "../props";
import { openLink } from "./marks";
import type { AdfContext } from "./props";

const TYPE_ICON: Partial<Record<string, IconName>> = {
  story: "story",
  bug: "bug",
  task: "task",
  subtask: "task",
  spike: "spike",
};

export function Card({ a, ctx }: { a: AdfAttrs; ctx: AdfContext }) {
  const block = a.block === true ? "true" : undefined;
  const issueId = a.issueId ?? 0;
  if (issueId > 0) {
    return (
      <button type="button" class="adf-card" data-adf="card" data-block={block} onClick={() => ctx.onOpenIssue(issueId)}>
        <Icon name={TYPE_ICON[a.typeKind ?? ""] ?? "other"} size={14} />
        <span class="adf-card-key">{a.key}</span>
        <span class="adf-card-title">{a.summary}</span>
        <span class="adf-card-lozenge" data-lozenge={a.statusLozenge}>
          {a.statusName}
        </span>
      </button>
    );
  }
  const url = a.url ?? "";
  return (
    <a href={url} class="adf-card adf-card-url" data-adf="card" data-block={block} onClick={(e) => openLink(e, url, ctx.onOpenUrl)}>
      <span class="adf-card-host">{a.host}</span>
      <span class="adf-card-path">{a.path}</span>
    </a>
  );
}

/** Go's `unknown` node, or any type the table does not know: its text, then a muted link to the ticket in Jira. */
export function Unknown({ text, ctx, children }: { text: string; ctx: AdfContext; children?: ComponentChildren }) {
  return (
    <div class="adf-unknown" data-adf="unknown">
      {text}
      {children}
      <a href={ctx.issueUrl} class="adf-muted-link" onClick={(e) => openLink(e, ctx.issueUrl, ctx.onOpenUrl)}>
        unsupported block ↗
      </a>
    </div>
  );
}
