// The command palette with the ticket finder (F14, ux-spec §15). One dialog: a combobox input with a mode chip or a
// breadcrumb, a listbox of sections (tickets, commands, people), and a footer of key hints. It is a lazy chunk: App loads
// it on first open (the entry bundle keeps only state/palette). The palette keeps no state between opens except the
// recents and the command usage, which are app settings.
//
// Network (AGENTS invariant 2): typing never calls Jira. TicketService.FindByKey and StandupService.People are local;
// the only request this dialog can make is the explicit "isn't synced" fetch row (TicketService.FetchByKey, one request
// through the limiter's interactive lane). Commands such as Resync act on their own triggers.
import { useEffect, useRef, useState } from "preact/hooks";
import { api, toAppError } from "../../api";
import type { PersonHit, TicketFind, TicketHit } from "../../api/types";
import { Icon } from "../../components/Icon";
import { openIssueKey } from "../ticket/TicketModal.util";
import { goPersonIn, openTicketIn } from "../../commands/actions";
import "../../commands/builtin";
import { buildCtx } from "../../commands/context";
import { allCommands, type CommandHit } from "../../commands/registry";
import type { Command } from "../../commands/types";
import { bumpUsage, readUsage } from "../../commands/usage";
import { tabStandups } from "../../state/app";
import { adoptOpener, closePalette, isMac, paletteOpen, recentTicketIds, restoreFocus, type PaletteMode } from "../../state/palette";
import { views } from "../../state/standup";
import { applyPrefix, buildSections, flatten, matchedTicket, wantsTickets, type Row, type Section } from "./model";
import { PaletteRow } from "./Rows";
import "./palette.css";

type FetchState = { phase: "idle" | "fetching" | "error"; message: string };
const IDLE: FetchState = { phase: "idle", message: "" };

/** The inline text of a failed fetch (ux-spec §15.3). */
export function fetchErrorText(code: string, key: string, fallback: string): string {
  switch (code) {
    case "offline":
      return "Couldn't reach Jira · offline";
    case "not_found":
      return `No issue ${key} in Jira`;
    case "forbidden":
      return "You don't have access";
    case "rate_limited":
      return "Jira is busy · try again in a moment";
    case "auth_invalid":
    case "auth_missing":
      return "Jira rejected the token · reconnect from Configure";
    default:
      return fallback || "Couldn't fetch that ticket";
  }
}

const MODE_NAMES: Record<string, string> = { ">": "commands", "#": "tickets", "@": "people" };
const PLACEHOLDERS: Record<string, string> = { ">": "Run a command", "#": "Find a ticket by key or title", "@": "Find a person" };

export function PaletteDialog() {
  const start = paletteOpen.value;
  const [mode, setMode] = useState<PaletteMode>(start?.mode ?? "");
  const [text, setText] = useState("");
  const [stack, setStack] = useState<Command[]>([]);
  const [sel, setSel] = useState(0);
  const [find, setFind] = useState<TicketFind | null>(null);
  const [findFor, setFindFor] = useState("");
  const [recents, setRecents] = useState<TicketHit[]>([]);
  const [people, setPeople] = useState<PersonHit[]>([]);
  const [fetching, setFetching] = useState<FetchState>(IDLE);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);
  const seq = useRef(0);
  const parent = stack.length > 0 ? stack[stack.length - 1]! : null;

  const ctx0 = buildCtx();
  const current = ctx0.standupId;
  const wantTickets = parent === null && (mode === "" || mode === "#") && wantsTickets(text);

  // Focus goes to the input, and back to the opener (the modal, a button, the page) when the palette closes.
  // The opener is captured when the palette is opened (state/palette.ts); this only fills it in when nothing had focus then.
  useEffect(() => {
    adoptOpener(document.activeElement, dialogRef.current);
    inputRef.current?.focus();
    return restoreFocus;
  }, []);

  // Local reads, once per open: the recent tickets and everyone who speaks anywhere.
  useEffect(() => {
    const ids = recentTicketIds();
    if (ids.length > 0) {
      api()
        .ticket.byIds(ids, current ?? 0)
        .then(setRecents)
        .catch(() => undefined);
    }
    api()
      .standup.people()
      .then(setPeople)
      .catch(() => undefined);
  }, []);

  // The ticket finder: local only, answered in a few ms, so no debounce; a stale answer is dropped.
  useEffect(() => {
    if (!wantTickets) {
      setFind(null);
      setFindFor("");
      return;
    }
    const mine = ++seq.current;
    api()
      .ticket.find(text, current ?? 0, 20)
      .then((r) => {
        if (mine !== seq.current) return;
        setFind(r);
        setFindFor(text);
      })
      .catch(() => {
        if (mine === seq.current) setFind(null);
      });
  }, [text, wantTickets, current]);

  const matched = matchedTicket(find, findFor, text, current);
  const ctx = buildCtx({ matched, setMode: (m) => enterMode(m) });
  const usage = readUsage();
  const labelOf = (id: number): string => {
    const t = tabStandups().find((x) => x.id === id);
    return t ? t.spaceKey || t.name : "";
  };
  const sections: Section[] = buildSections({ mode, parent, text, ctx, usage, commands: allCommands(), find, findFor, recents, people, standupLabel: labelOf });
  const rows: Row[] = flatten(sections);
  const total = rows.length;
  const at = Math.min(sel, Math.max(0, total - 1));
  const selected = rows[at];

  // Keep the selected option in view inside the scrolling list.
  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>(`#pal-opt-${at}`);
    el?.scrollIntoView?.({ block: "nearest" });
  }, [at, sections]);

  function enterMode(m: PaletteMode) {
    setMode(m);
    setText("");
    setSel(0);
    setFetching(IDLE);
    inputRef.current?.focus();
  }

  const laneLabel = (hit: TicketHit): string => {
    const lane = views.value.get(hit.standupId)?.lanes?.find((l) => l.lane === hit.lane);
    return (lane?.label ?? hit.statusName).toUpperCase();
  };

  async function doFetch(key: string) {
    if (current === null || fetching.phase === "fetching") return;
    setFetching({ phase: "fetching", message: "" });
    try {
      const detail = await api().ticket.fetchByKey(current, key);
      closePalette();
      await openTicketIn(current, detail.issueId);
    } catch (e) {
      const err = toAppError(e);
      setFetching({ phase: "error", message: fetchErrorText(err.code, key, err.message) });
    }
  }

  function runCommand(hit: CommandHit) {
    const c = hit.command;
    if (hit.disabled !== "") return;
    if (c.children) {
      setStack([...stack, c]);
      setText("");
      setSel(0);
      return;
    }
    if (c.mode !== undefined) {
      enterMode(c.mode);
      return;
    }
    bumpUsage(c.id);
    const run = c.run;
    closePalette();
    if (run) void Promise.resolve(run(buildCtx({ matched }))).catch(() => undefined);
  }

  function activate(row: Row | undefined) {
    if (!row) return;
    switch (row.kind) {
      case "ticket": {
        const hit = row.hit;
        closePalette();
        void openTicketIn(hit.standupId || current || 0, hit.issueId);
        break;
      }
      case "person":
        closePalette();
        void goPersonIn(row.standupId, row.accountId);
        break;
      case "command":
        runCommand(row.hit);
        break;
      case "fetch":
        void doFetch(row.key);
        break;
      default:
        break;
    }
  }

  const unwind = () => {
    if (parent) {
      setStack(stack.slice(0, -1));
      setText("");
      setSel(0);
    } else if (mode !== "") {
      enterMode("");
    } else {
      closePalette();
    }
  };

  const onKeyDown = (e: KeyboardEvent) => {
    e.stopPropagation(); // nothing behind the palette (the modal, the global keys, the legend) sees a key
    if (e.isComposing) return;
    const meta = e.metaKey || e.ctrlKey;
    if (meta && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "k") {
      e.preventDefault();
      closePalette();
      return;
    }
    switch (e.key) {
      case "Escape":
        e.preventDefault();
        unwind();
        return;
      case "ArrowDown":
      case "ArrowUp": {
        e.preventDefault();
        if (total === 0) return;
        setSel((at + (e.key === "ArrowDown" ? 1 : -1) + total) % total);
        return;
      }
      case "Home":
        e.preventDefault();
        setSel(0);
        return;
      case "End":
        e.preventDefault();
        setSel(Math.max(0, total - 1));
        return;
      case "PageDown":
        e.preventDefault();
        setSel(Math.min(Math.max(0, total - 1), at + 8));
        return;
      case "PageUp":
        e.preventDefault();
        setSel(Math.max(0, at - 8));
        return;
      case "Enter": {
        e.preventDefault();
        if (meta) {
          const key = selected?.kind === "ticket" ? selected.hit.key : selected?.kind === "fetch" ? selected.key : null;
          if (key) {
            closePalette();
            openIssueKey(key);
          }
          return;
        }
        activate(selected);
        return;
      }
      case "ArrowRight": {
        const el = inputRef.current!;
        const nested = selected?.kind === "command" && (selected.hit.command.children !== undefined || selected.hit.command.mode !== undefined);
        if (nested && el.selectionStart === el.value.length && el.selectionEnd === el.value.length) {
          e.preventDefault();
          activate(selected);
        }
        return;
      }
      case "Backspace":
        if (text === "" && (parent || mode !== "")) {
          e.preventDefault();
          unwind();
        }
        return;
      case "Tab":
        e.preventDefault(); // focus stays in the input
        return;
      default:
    }
  };

  const onInput = (e: Event) => {
    const r = applyPrefix((e.currentTarget as HTMLInputElement).value, mode, parent !== null);
    if (r.mode !== mode) setMode(r.mode);
    setText(r.text);
    setSel(0);
    if (fetching.phase !== "idle") setFetching(IDLE);
  };

  const placeholder = parent ? (parent.placeholder ?? "Choose an option") : (PLACEHOLDERS[mode] ?? "Find a ticket by key or title, or run a command");
  const chip = parent ? `${String(typeof parent.title === "function" ? parent.title(ctx) : parent.title).replace(/…$/, "")}` : null;
  const needDigits = mode === "#" && parent === null && text.trim() !== "" && !wantsTickets(text);
  const empty = total === 0 && !needDigits && (text.trim() !== "" || mode !== "");
  let n = -1;

  return (
    <div
      class="pal-scrim"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) closePalette();
      }}
    >
      <div
        class="pal"
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label="Find a ticket or run a command"
        onKeyDown={onKeyDown}
        onMouseDown={(e) => {
          if (e.target !== inputRef.current) e.preventDefault(); // clicks inside never take focus from the input
        }}
      >
        <div class="pal-input">
          <Icon name="search" size={22} strokeWidth={2.4} class="pal-input__icon" />
          {chip !== null ? (
            <span class="pal-crumb" data-testid="pal-crumb">
              {chip}
              <span aria-hidden="true"> ›</span>
            </span>
          ) : mode !== "" ? (
            <button
              type="button"
              class="pal-mode"
              tabIndex={-1}
              aria-label={`Searching ${MODE_NAMES[mode]} only. Remove`}
              onMouseDown={(e) => {
                e.preventDefault();
                enterMode("");
              }}
            >
              {mode}
            </button>
          ) : null}
          <input
            ref={inputRef}
            class="pal-field"
            type="text"
            role="combobox"
            aria-label="Find a ticket or run a command"
            aria-expanded="true"
            aria-controls="pal-list"
            aria-autocomplete="list"
            aria-activedescendant={selected ? `pal-opt-${at}` : undefined}
            autocomplete="off"
            autocapitalize="off"
            spellcheck={false}
            placeholder={placeholder}
            value={text}
            onInput={onInput}
          />
          <kbd class="pal-esc" aria-hidden="true">
            Esc
          </kbd>
        </div>
        <div class="pal-list" id="pal-list" role="listbox" aria-label="Results" ref={listRef}>
          {sections.map((s) => (
            <div class="pal-sec" role="group" aria-labelledby={`pal-h-${s.id}`} key={s.id}>
              <div class="pal-sec__head" id={`pal-h-${s.id}`}>
                <span>{s.title}</span>
                {s.hint ? <span class="pal-sec__hint">{s.hint}</span> : null}
              </div>
              {s.rows.map((r) => {
                if (r.kind === "more" || r.kind === "note") {
                  return (
                    <div class="pal-more" role="presentation" key={r.id}>
                      {r.text}
                    </div>
                  );
                }
                n++;
                const index = n;
                return (
                  <PaletteRow
                    key={r.id}
                    row={r}
                    index={index}
                    selected={index === at}
                    onPick={() => activate(r)}
                    onHover={() => setSel(index)}
                    ctx={ctx}
                    laneLabel={laneLabel}
                    fetch={r.kind === "fetch" ? fetching : IDLE}
                  />
                );
              })}
            </div>
          ))}
          {needDigits ? <div class="pal-empty">Type a ticket key or a few letters of its title, like 1429, mob-1429 or recipe card.</div> : null}
          {empty ? <div class="pal-empty">Nothing matches “{text}”.</div> : null}
        </div>
        {parent ? (
          <div class="pal-note">⌫ on an empty field goes back to all commands · the choice saves immediately, as in Configure → Display</div>
        ) : null}
        <div class="pal-foot">
          <span class="pal-hints">
            <span>
              <kbd>↑</kbd>
              <kbd>↓</kbd> choose
            </span>
            <span>
              <kbd>↵</kbd> run / open
            </span>
            <span>
              <kbd>{isMac() ? "⌘" : "Ctrl"}</kbd>
              <kbd>↵</kbd> open in Jira
            </span>
          </span>
          <span class="pal-legend">
            <span>
              <b>&gt;</b> commands
            </span>
            <span>
              <b>#</b> tickets
            </span>
            <span>
              <b>@</b> people
            </span>
          </span>
        </div>
        <div class="pal-sr" role="status" aria-live="polite">
          {total === 1 ? "1 result" : `${total} results`}
        </div>
      </div>
    </div>
  );
}
