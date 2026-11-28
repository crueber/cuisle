// Subtasks and Linked-issues sections of the ticket content column (ux-spec §14.4 "Subtasks and links";
// props.ts IssueRowsProps). A row is one button; the parent decides modal (synced) or browser (not synced).
import type { IssueRow } from "../../api/types";
import { hueOf } from "../../state/lanes";
import { Icon, issueTypeIcon } from "../../components/Icon";
import type { IssueRowsProps } from "./props";
import "./IssueRows.css";

function Row({ row, onOpen }: { row: IssueRow; onOpen: (row: IssueRow) => void }) {
  return (
    <li class="ir-row" data-done={row.done ? "true" : undefined}>
      <button type="button" class="ir-btn" onClick={() => onOpen(row)}>
        <span class="ir-type">
          <Icon name={issueTypeIcon(row.typeKind)} size={16} />
        </span>
        <span class="ir-key">{row.key}</span>
        <span class="ir-summary">{row.summary}</span>
        <span class="ir-status" data-lane={row.statusLozenge} data-hue={hueOf(row.statusLozenge)}>
          {row.statusName}
        </span>
        {row.assigneeInitials ? <span class="ir-assignee">{row.assigneeInitials}</span> : null}
      </button>
    </li>
  );
}

export function IssueRows({ subtasks, subtasksDone, linkGroups, onOpen }: IssueRowsProps) {
  const groups = linkGroups.filter((g) => (g.rows ?? []).length > 0);
  const linkCount = groups.reduce((n, g) => n + (g.rows ?? []).length, 0);
  return (
    <>
      {subtasks.length > 0 ? (
        <section class="tk-section" data-section="subtasks">
          <h2 class="tk-head">
            Subtasks <span class="tk-count">{`${subtasksDone}/${subtasks.length}`}</span>
          </h2>
          <ul class="ir-list" aria-label="Subtasks">
            {subtasks.map((row) => (
              <Row key={row.key} row={row} onOpen={onOpen} />
            ))}
          </ul>
        </section>
      ) : null}

      {groups.length > 0 ? (
        <section class="tk-section" data-section="links">
          <h2 class="tk-head">
            Links <span class="tk-count">{linkCount}</span>
          </h2>
          {groups.map((g) => (
            <div class="ir-group" key={g.label}>
              <span class="ir-group-label">{g.label}</span>
              <ul class="ir-list" aria-label={g.label}>
                {(g.rows ?? []).map((row) => (
                  <Row key={`${g.label}-${row.key}`} row={row} onOpen={onOpen} />
                ))}
              </ul>
            </div>
          ))}
        </section>
      ) : null}
    </>
  );
}
