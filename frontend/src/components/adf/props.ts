// FROZEN CONTRACT: props of the ADF renderer (ux-spec §14.4, architecture §4.9). Owner: WP-T2.
// Go parses ADF into the adf.Node tree (api/types AdfDoc/AdfNode); these components only render it: one component per
// node type through a static table in AdfDoc, never innerHTML, and links never navigate the webview.
import type { AdfDoc, AdfNode } from "../../api/types";

/** What the renderer needs from its host (the ticket modal); every interaction goes through it. */
export interface AdfContext {
  /** Inline/block card resolved to a synced issue (attrs.issueId > 0): open it in the modal (back stack). */
  onOpenIssue: (issueId: number) => void;
  /** Any other link, external card or "unsupported block ↗": open in the OS browser. */
  onOpenUrl: (url: string) => void;
  /** mediaSingle click: open the lightbox at this attachment (attrs.attachmentId as a number). */
  onOpenImage: (attachmentId: number) => void;
  /** File card click: download to the OS temp folder and open (TicketService.OpenAttachment). */
  onOpenFile: (attachmentId: number) => void;
  /** "Load image" on a placeholder frame (TicketService.EnsureMedia). */
  onLoadImage: (attachmentId: number) => void;
  /** The ticket's Jira URL: target of "unsupported block ↗" and "attachment unavailable ↗". */
  issueUrl: string;
  /** Active theme, for the textColor contrast clamp (contrast.ts). */
  theme: "light" | "dark";
  /** Bumped by `E`: every expand toggles to the same state (true = expanded). undefined = as authored. */
  expandAll?: { open: boolean; token: number };
  /** Live cache state lookup from detail.attachments so media:ready updates inline media nodes immediately. */
  mediaCached?: (attachmentId: number) => string | undefined;
}

/** Text size of a document: the description at body size, comments at body−1, rail paragraphs at 15/22. */
export type AdfSize = "body" | "comment" | "field";

export interface AdfDocProps {
  doc: AdfDoc;
  ctx: AdfContext;
  size?: AdfSize; // default "body"
  /** Rail paragraph fields: clamp to N lines with "Show all" (ux-spec §14.6). 0/undefined = no clamp. */
  clampLines?: number;
  class?: string;
}

/** Props of every per-node component (block and inline); children render through the same table. */
export interface AdfNodeProps {
  node: AdfNode;
  ctx: AdfContext;
  /** Max rendered width in CSS px (media honour width/layout up to the column width). */
  maxW?: number;
}
