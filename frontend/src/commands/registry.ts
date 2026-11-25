// The command registry (F14, ux-spec §15.4): registration, availability, and the search that ranks commands for the
// palette. Pure; the context and the usage counts come in as arguments.
import { fuzzyBest } from "./score";
import type { Command, Ctx } from "./types";

/** Registration order is the tie-break after the score and the usage count. */
const registry: Command[] = [];

/** Registers commands once (a repeated id replaces the earlier entry in place). */
export function registerCommands(list: Command[]): void {
  for (const c of list) {
    const at = registry.findIndex((x) => x.id === c.id);
    if (at >= 0) registry[at] = c;
    else registry.push(c);
  }
}

export const allCommands = (): readonly Command[] => registry;

export const titleOf = (c: Command, ctx: Ctx): string => (typeof c.title === "function" ? c.title(ctx) : c.title);

/** Hidden commands never appear (Cancel sync while idle, Reconnect Jira while connected, ticket commands without a ticket). */
export const isVisible = (c: Command, ctx: Ctx): boolean => c.visible?.(ctx) ?? true;

/** true when runnable, else the reason. */
export const availability = (c: Command, ctx: Ctx): true | string => c.available?.(ctx) ?? true;

/** A command as the palette lists it. */
export interface CommandHit {
  command: Command;
  title: string;
  /** Matched title characters to ink (empty for an alias match or no query). */
  indexes: number[];
  score: number;
  detail?: string;
  /** The reason it cannot run, or "". */
  disabled: string;
  /** Position in the list it came from, for stable ordering. */
  order: number;
}

export type Usage = Readonly<Record<string, number>>;

function toHit(c: Command, ctx: Ctx, order: number, score: number, indexes: number[]): CommandHit {
  const a = availability(c, ctx);
  return { command: c, title: titleOf(c, ctx), indexes, score, detail: c.detail?.(ctx), disabled: a === true ? "" : a, order };
}

/**
 * Commands matching `query`, best first: score, then how often they were run, then registry order. An empty query lists
 * everything in registry order (the `>` mode before typing).
 */
export function searchCommands(list: readonly Command[], query: string, ctx: Ctx, usage: Usage = {}): CommandHit[] {
  const out: CommandHit[] = [];
  list.forEach((c, order) => {
    if (!isVisible(c, ctx)) return;
    if (query.trim() === "") {
      out.push(toHit(c, ctx, order, 0, []));
      return;
    }
    const m = fuzzyBest(query, titleOf(c, ctx), c.keywords);
    if (m) out.push(toHit(c, ctx, order, m.score, m.indexes));
  });
  return out.sort((a, b) => b.score - a.score || (usage[b.command.id] ?? 0) - (usage[a.command.id] ?? 0) || a.order - b.order);
}

/** What to run when nothing is typed and nothing has been run yet (ux-spec §15.2). */
export const DEFAULT_SUGGESTED = ["sync.resync", "view.group", "nav.person", "nav.standup", "nav.configure"] as const;

/** The empty palette's commands: the five most run (visible ones), topped up from the defaults. */
export function suggestedCommands(list: readonly Command[], ctx: Ctx, usage: Usage = {}, count = 5): CommandHit[] {
  const visible = list.filter((c) => isVisible(c, ctx));
  const used = visible
    .map((c, i) => ({ c, n: usage[c.id] ?? 0, i }))
    .filter((x) => x.n > 0)
    .sort((a, b) => b.n - a.n || a.i - b.i)
    .map((x) => x.c);
  const picked = [...used];
  for (const id of DEFAULT_SUGGESTED) {
    const c = visible.find((x) => x.id === id);
    if (c && !picked.includes(c)) picked.push(c);
  }
  return picked.slice(0, count).map((c, i) => toHit(c, ctx, i, 0, []));
}
