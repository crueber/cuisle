// The Done section of the whole board (ux-spec §6.3). Every ticket is listed; nothing is collapsed (AGENTS invariant 1).
import type { DoneListProps } from "./props";
import { openIssue } from "./BoardView.rows";
import { TypeIcon } from "./TicketRow";

export function DoneList({ tickets, label, cols, speakers }: DoneListProps) {
  const rows = Math.max(1, Math.ceil(tickets.length / cols));
  return (
    <section class="board-list board-list--done">
      <header class="board-list-head">
        <h2 class="board-list-title">Done</h2>
        <span class="board-list-count">{`${tickets.length} ${label}`}</span>
      </header>
      <div class="board-list-grid" style={`--cols:${cols};--rows:${rows}`}>
        {tickets.map((t) => {
          const initials = speakers.find((s) => s.id === t.assignee)?.initials ?? "··";
          return (
            <button type="button" class="board-list-row" key={t.key} onClick={() => openIssue(t.key)}>
              <TypeIcon type={t.type} done />
              <span class="board-list-key">{t.key}</span>
              <span class="board-list-text">{t.summary}</span>
              <span class="board-initials">{initials}</span>
            </button>
          );
        })}
      </div>
    </section>
  );
}
