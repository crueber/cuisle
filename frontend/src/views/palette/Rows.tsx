// The palette's rows (F14, ux-spec §15.3-15.5): ticket, command, person and the "not synced" fetch row. All 48px, all
// options of the listbox. Colours are tokens; lanes come from the data (TicketHit.lane, the view's lane labels).
import type { ComponentChildren } from "preact";
import type { TicketHit } from "../../api/types";
import { Icon } from "../../components/Icon";
import type { IconName } from "../../components/props";
import type { Command, Ctx } from "../../commands/types";
import type { CommandHit } from "../../commands/registry";
import { hueOf } from "../../state/lanes";
import type { Row } from "./model";
import { PaletteIcon } from "./icons";

/** Text with the matched characters inked (ink background, paper text). */
export function Hl({ text, indexes }: { text: string; indexes: readonly number[] }) {
  if (indexes.length === 0) return <>{text}</>;
  const on = new Set(indexes);
  const parts: ComponentChildren[] = [];
  let run = "";
  let marked = false;
  const flush = () => {
    if (run !== "") parts.push(marked ? <mark class="pal-hl">{run}</mark> : run);
    run = "";
  };
  [...text].forEach((ch, i) => {
    const m = on.has(i);
    if (m !== marked) flush();
    marked = m;
    run += ch;
  });
  flush();
  return <>{parts}</>;
}

const typeIcon = (t: string): IconName => (t === "story" || t === "bug" || t === "task" || t === "spike" ? t : "other");

interface RowProps {
  row: Row;
  index: number;
  selected: boolean;
  onPick: () => void;
  onHover: () => void;
  ctx: Ctx;
  laneLabel: (hit: TicketHit) => string;
  fetch: { phase: "idle" | "fetching" | "error"; message: string };
}

const optionProps = (p: RowProps, extra: { disabled?: boolean } = {}) => ({
  id: `pal-opt-${p.index}`,
  role: "option" as const,
  "aria-selected": p.selected,
  "aria-disabled": extra.disabled ? true : undefined,
  "data-selected": p.selected ? "true" : "false",
  onMouseMove: p.onHover,
  onClick: p.onPick,
});

function TicketOption(p: RowProps & { row: Extract<Row, { kind: "ticket" }> }) {
  const { hit, standupLabel } = p.row;
  const done = hit.done;
  return (
    <div {...optionProps(p)} class="pal-row pal-row--ticket" data-done={done ? "true" : "false"}>
      <Icon name={typeIcon(hit.type)} size={20} strokeWidth={2} class={`ticket-icon type-${typeIcon(hit.type)}`} />
      <span class="pal-key">
        <Hl text={hit.key} indexes={hit.match} />
      </span>
      <span class="pal-title" title={hit.summary}>
        {hit.summary}
        {hit.removed ? <span class="pal-off">not on board</span> : null}
      </span>
      <span class="pal-lane">
        {hit.lane !== "" ? <span class="legend-swatch pal-sw" data-lane={hit.lane} data-hue={hueOf(hit.lane)} aria-hidden="true" /> : null}
        <span>{p.laneLabel(hit)}</span>
      </span>
      <span class="pal-who" data-unassigned={hit.assignee === "" ? "true" : "false"} title={hit.assignee === "" ? "Unassigned" : undefined}>
        {hit.assignee === "" ? "··" : hit.assigneeInitials}
      </span>
      <span class="pal-days" data-alert={hit.overLimit ? "true" : "false"}>
        {done || hit.lane === "" ? "" : `${Math.max(1, hit.daysInLane)}d`}
      </span>
      <span class="pal-standup">{standupLabel}</span>
    </div>
  );
}

function CommandOption(p: RowProps & { row: Extract<Row, { kind: "command" }> }) {
  const h: CommandHit = p.row.hit;
  const c: Command = h.command;
  const nested = c.children !== undefined || c.mode !== undefined;
  const disabled = h.disabled !== "";
  const checked = c.checked?.(p.ctx) === true;
  const detail = disabled ? h.disabled : h.detail;
  return (
    <div {...optionProps(p, { disabled })} class="pal-row pal-row--command" data-disabled={disabled ? "true" : "false"}>
      <PaletteIcon name={c.icon ?? "other"} class="pal-ico" />
      <span class="pal-name">
        <Hl text={h.title} indexes={h.indexes} />
      </span>
      {detail ? <span class="pal-detail">{detail}</span> : null}
      <span class="pal-fill" />
      {checked ? <PaletteIcon name="check" size={18} class="pal-check" /> : null}
      <span class="pal-cat">{c.category}</span>
      <span class="pal-kbd">{c.shortcut ? <kbd>{c.shortcut}</kbd> : null}</span>
      <span class="pal-chev">{nested ? <PaletteIcon name="chevron-right" size={18} /> : null}</span>
    </div>
  );
}

function PersonOption(p: RowProps & { row: Extract<Row, { kind: "person" }> }) {
  const r = p.row;
  return (
    <div {...optionProps(p)} class="pal-row pal-row--person" data-unassigned={r.unassigned ? "true" : "false"}>
      {r.unassigned ? <PaletteIcon name="person" class="pal-ico" /> : <span class="pal-initials">{r.initials}</span>}
      <span class="pal-name">
        <Hl text={r.name} indexes={r.indexes} />
      </span>
      <span class="pal-detail">{r.unassigned ? `${r.active} need an owner` : `${r.active} active`}</span>
      <span class="pal-fill" />
      <span class="pal-cat">{r.unassigned ? "Navigate" : "Person"}</span>
      <span class="pal-standup">{r.standupLabel}</span>
    </div>
  );
}

function FetchOption(p: RowProps & { row: Extract<Row, { kind: "fetch" }> }) {
  const { key } = p.row;
  const f = p.fetch;
  return (
    <div {...optionProps(p)} class="pal-row pal-row--fetch" data-state={f.phase}>
      <PaletteIcon name="download" class="pal-ico" />
      <span class="pal-fetch">
        <strong class="pal-key">{key}</strong>{" "}
        {f.phase === "fetching" ? (
          <span>Fetching from Jira…</span>
        ) : f.phase === "error" ? (
          <span class="pal-fetch-error">{f.message}</span>
        ) : (
          <>
            <span>isn't synced in Cuisle</span> <span class="pal-detail">↵ fetches just this ticket from Jira (one request), then opens it</span>
          </>
        )}
      </span>
    </div>
  );
}

export function PaletteRow(p: RowProps) {
  switch (p.row.kind) {
    case "ticket":
      return <TicketOption {...p} row={p.row} />;
    case "command":
      return <CommandOption {...p} row={p.row} />;
    case "person":
      return <PersonOption {...p} row={p.row} />;
    case "fetch":
      return <FetchOption {...p} row={p.row} />;
    default:
      return null;
  }
}
