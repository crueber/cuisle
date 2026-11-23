// The Backlog section of the whole board (ux-spec §6.3). Hidden entirely when rows is 0.
import type { BacklogListProps } from "./props";
import { openIssue } from "./BoardView.rows";
import { TypeIcon } from "./TicketRow";

export function BacklogList({ items, total, rows, cols }: BacklogListProps) {
  if (rows === 0) return null;
  const more = total - items.length;
  const gridRows = Math.max(1, Math.ceil(items.length / cols));
  return (
    <section class="board-list board-list--backlog">
      <header class="board-list-head">
        <h2 class="board-list-title">Backlog</h2>
        <span class="board-list-count">{`top ${rows} by rank`}</span>
      </header>
      <div class="board-list-grid" style={`--cols:${cols};--rows:${gridRows}`}>
        {items.map((b) => (
          <button type="button" class="board-list-row board-list-row--backlog" key={b.key} onClick={() => openIssue(b.key)}>
            <TypeIcon type={b.type} />
            <span class="board-list-key board-list-key--muted">{b.key}</span>
            <span class="board-list-text">{b.summary}</span>
          </button>
        ))}
      </div>
      {total > items.length ? (
        <p class="board-footnote">{`Showing the top ${rows} as set in Configure · ${more} more ranked below in Jira`}</p>
      ) : null}
    </section>
  );
}
