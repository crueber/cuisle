// The palette's pure model (F14, ux-spec §15.1-15.5): parsing what was typed and arranging the results into sections.
// No DOM, no signals, no network: the dialog feeds it the find result, the people, the recents and the command context.
import type { PersonHit, TicketFind, TicketHit } from "../../api/types";
import { fuzzy } from "../../commands/score";
import { searchCommands, suggestedCommands, type CommandHit, type Usage } from "../../commands/registry";
import type { Command, Ctx } from "../../commands/types";
import type { PaletteMode } from "../../state/palette";

const PREFIXES: readonly string[] = [">", "#", "@"];

/** Typing `>`, `#` or `@` as the first character of an unprefixed field switches mode; the character is consumed. */
export function applyPrefix(raw: string, mode: PaletteMode, nested: boolean): { mode: PaletteMode; text: string } {
  if (mode === "" && !nested && raw.length > 0 && PREFIXES.includes(raw[0]!)) return { mode: raw[0] as PaletteMode, text: raw.slice(1) };
  return { mode, text: raw };
}

/** The query has a digit, so it can name a ticket (ux-spec §15.2: tickets then come first). */
export const hasDigit = (text: string): boolean => /\d/.test(text);

/** The query is worth asking the finder about: a digit (a key) or two characters (a title); the same floor as Go's. */
export const wantsTickets = (text: string): boolean => hasDigit(text) || [...text.replace(/\s/g, "")].length >= 2;

/** "mob-1729" is a fully specified key (letters, a hyphen, digits); returns it upper-cased, else null. */
export function fullKey(text: string): string | null {
  const m = /^\s*([A-Za-z][A-Za-z0-9_]+)-(\d+)\s*$/.exec(text);
  return m ? `${m[1]!.toUpperCase()}-${m[2]!}` : null;
}

export type Row =
  | { kind: "ticket"; id: string; hit: TicketHit; standupLabel: string }
  | { kind: "command"; id: string; hit: CommandHit }
  | { kind: "person"; id: string; accountId: string; name: string; initials: string; active: number; standupId: number; standupLabel: string; indexes: number[]; unassigned: boolean }
  | { kind: "fetch"; id: string; key: string }
  | { kind: "more"; id: string; text: string }
  | { kind: "note"; id: string; text: string };

export interface Section {
  id: string;
  title: string;
  /** The lowercase mono hint after the title ("exact", "one character off", "go to their person view"). */
  hint?: string;
  rows: Row[];
}

/** Rows the keyboard can land on. */
export const selectable = (r: Row): boolean => r.kind !== "more" && r.kind !== "note";

export interface ModelInput {
  mode: PaletteMode;
  /** The nested command being browsed (the breadcrumb), if any. */
  parent: Command | null;
  text: string;
  ctx: Ctx;
  usage: Usage;
  commands: readonly Command[];
  /** The find result for `findFor`; null until it arrives or when the query cannot name a ticket. */
  find: TicketFind | null;
  findFor: string;
  recents: TicketHit[];
  people: PersonHit[];
  /** The label of a standup id ("MOB"). */
  standupLabel: (id: number) => string;
}

const MAX_COMMANDS = 7;
const MAX_PEOPLE = 4;
const RECENT_SHOWN = 3;

function ticketRows(hits: TicketHit[], label: (id: number) => string): Row[] {
  return hits.map((hit) => ({ kind: "ticket", id: `t:${hit.issueId}`, hit, standupLabel: label(hit.standupId) }));
}

function ticketSections(i: ModelInput): Section[] {
  const f = i.find;
  const out: Section[] = [];
  const key = fullKey(i.text);
  const fresh = f !== null && i.findFor === i.text;
  const exact = fresh ? f.hits.filter((h) => h.tier === "exact") : [];
  // A fully specified key that is not synced: the fetch row comes first (ux-spec §15.3).
  if (fresh && key !== null && exact.length === 0 && i.ctx.standupId !== null) {
    out.push({ id: "tickets-fetch", title: "TICKETS", hint: "not synced", rows: [{ kind: "fetch", id: `fetch:${key}`, key }] });
  }
  if (!fresh) return out;
  const prefix = f.hits.filter((h) => h.tier === "prefix");
  const close = f.hits.filter((h) => h.tier === "close");
  if (exact.length > 0) out.push({ id: "tickets-exact", title: "TICKETS", hint: "exact", rows: ticketRows(exact, i.standupLabel) });
  if (prefix.length > 0) {
    const rows = ticketRows(prefix, i.standupLabel);
    if (f.prefixMore > 0) rows.push({ kind: "more", id: "more", text: `↓ ${f.prefixMore} more · keep typing` });
    out.push({ id: "tickets-prefix", title: "TICKETS", hint: "starts with", rows });
  }
  if (close.length > 0) out.push({ id: "tickets-close", title: "TICKETS", hint: "one character off", rows: ticketRows(close, i.standupLabel) });
  const title = f.hits.filter((h) => h.tier === "title");
  if (title.length > 0) {
    const rows = ticketRows(title, i.standupLabel);
    if (f.titleMore > 0) rows.push({ kind: "more", id: "more-title", text: `↓ ${f.titleMore} more · keep typing` });
    out.push({ id: "tickets-title", title: "TICKETS", hint: "title", rows });
  }
  return out;
}

function recentSection(i: ModelInput, count: number): Section[] {
  if (i.recents.length === 0) return [];
  return [{ id: "recent", title: "RECENT TICKETS", rows: ticketRows(i.recents.slice(0, count), i.standupLabel) }];
}

function commandRows(hits: CommandHit[]): Row[] {
  return hits.map((hit) => ({ kind: "command", id: `c:${hit.command.id}`, hit }));
}

function commandSection(i: ModelInput, limit: number): Section[] {
  const hits = searchCommands(i.commands, i.text, i.ctx, i.usage);
  return hits.length === 0 ? [] : [{ id: "commands", title: "COMMANDS", rows: commandRows(limit > 0 ? hits.slice(0, limit) : hits) }];
}

function personRows(i: ModelInput): Row[] {
  const rows: { row: Row; score: number }[] = [];
  for (const p of i.people) {
    const m = fuzzy(i.text, p.displayName);
    if (!m) continue;
    const here = p.standups.find((s) => s.standupId === i.ctx.standupId) ?? p.standups[0];
    if (!here) continue;
    rows.push({
      score: m.score + (here.standupId === i.ctx.standupId ? 1 : 0),
      row: {
        kind: "person",
        id: `p:${p.accountId}`,
        accountId: p.accountId,
        name: p.displayName,
        initials: p.initials,
        active: here.active,
        standupId: here.standupId,
        standupLabel: i.standupLabel(here.standupId),
        indexes: m.indexes,
        unassigned: false,
      },
    });
  }
  return rows.sort((a, b) => b.score - a.score).map((x) => x.row);
}

function peopleSection(i: ModelInput, limit: number, withUnassigned: boolean): Section[] {
  const rows = personRows(i);
  const out: Section[] = [];
  if (rows.length > 0) out.push({ id: "people", title: "PEOPLE", hint: i.mode === "@" ? "go to their person view" : undefined, rows: limit > 0 ? rows.slice(0, limit) : rows });
  if (withUnassigned && i.ctx.standupId !== null) {
    const n = i.ctx.view?.counts.unassigned ?? 0;
    out.push({
      id: "also",
      title: "ALSO",
      rows: [
        {
          kind: "person",
          id: "p:unassigned",
          accountId: "unassigned",
          name: "Unassigned",
          initials: "··",
          active: n,
          standupId: i.ctx.standupId,
          standupLabel: "",
          indexes: [],
          unassigned: true,
        },
      ],
    });
  }
  return out;
}

/** Arranges everything into the sections the dialog draws, in order. Empty sections are omitted (ux-spec §15.2). */
export function buildSections(i: ModelInput): Section[] {
  if (i.parent) {
    const kids = i.parent.children?.(i.ctx) ?? [];
    const hits = searchCommands(kids, i.text, i.ctx);
    return hits.length === 0 ? [] : [{ id: "nested", title: String(typeof i.parent.title === "function" ? i.parent.title(i.ctx) : i.parent.title).replace(/…$/, "").toUpperCase(), rows: commandRows(hits) }];
  }
  const empty = i.text.trim() === "";
  switch (i.mode) {
    case ">":
      return commandSection(i, 0);
    case "#": {
      if (empty) return recentSection(i, 8);
      const t = ticketSections(i);
      return t.length > 0 ? t : [];
    }
    case "@":
      return peopleSection(i, 0, true);
    default:
      break;
  }
  if (empty) {
    const sug = suggestedCommands(i.commands, i.ctx, i.usage);
    return [...recentSection(i, RECENT_SHOWN), ...(sug.length > 0 ? [{ id: "commands", title: "COMMANDS", rows: commandRows(sug) }] : [])];
  }
  // The find result is only fresh for a query worth asking about, so no guard is needed here. With a digit the query can
  // name a ticket: tickets first. Letters only: commands and people first, then the tickets whose titles match.
  const tickets = ticketSections(i);
  const commands = commandSection(i, MAX_COMMANDS);
  const people = peopleSection(i, MAX_PEOPLE, false);
  return hasDigit(i.text) ? [...tickets, ...commands, ...people] : [...commands, ...people, ...tickets];
}

/** The rows the keyboard moves over, in display order. */
export const flatten = (sections: Section[]): Row[] => sections.flatMap((s) => s.rows).filter(selectable);

/** The ticket the ticket commands act on while typing: the best KEY hit of the current query (exact tier first). A title hit
 *  does not count: typing "resync" must not offer "Refresh MOB-12 from Jira" because some summary contains the word. */
export function matchedTicket(find: TicketFind | null, findFor: string, text: string, standupId: number | null): { issueId: number; key: string; standupId: number } | null {
  if (!find || findFor !== text) return null;
  const hit = find.hits.find((h) => h.tier === "exact") ?? find.hits.find((h) => h.tier !== "title");
  return hit ? { issueId: hit.issueId, key: hit.key, standupId: hit.standupId || standupId || 0 } : null;
}
