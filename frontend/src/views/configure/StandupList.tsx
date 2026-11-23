// Configure rail: StandupList (ux-spec §9.1 Left rail, §9.3 ordered lists, §4 drafts).
import { Icon } from "../../components/Icon";
import { moveStandup } from "../../state/config";
import type { StandupTab } from "../../api/types";
import type { StandupListProps } from "./props";

function meta(t: StandupTab): string {
  const people = `${t.speakerCount} ${t.speakerCount === 1 ? "person" : "people"}`;
  return t.spaceKey ? `${t.spaceKey} · ${people}` : people;
}

export function StandupList({ standups, selectedId, onSelect, onAdd }: StandupListProps) {
  const last = standups.length - 1;
  return (
    <aside class="cfg-rail" aria-label="Standups">
      <div class="cfg-rail-head">STANDUPS · {standups.length}</div>
      <ul class="cfg-rail-list">
        {standups.map((t, i) => {
          const selected = t.id === selectedId;
          return (
            <li key={t.id} class="cfg-rail-item" aria-current={selected ? "true" : undefined} data-selected={selected ? "true" : undefined}>
              <button type="button" class="cfg-rail-select" onClick={() => onSelect(t.id)}>
                <span class="cfg-rail-num">{i + 1}</span>
                <span class="cfg-rail-text">
                  <span class="cfg-rail-name">{t.name}</span>
                  <span class="cfg-rail-meta">{meta(t)}</span>
                  {!t.configured ? <span class="cfg-tag">DRAFT</span> : null}
                  {t.needsSync ? <span class="cfg-rail-sync">needs sync</span> : null}
                </span>
              </button>
              <span class="cfg-rail-move">
                <button
                  type="button"
                  class="cfg-move"
                  aria-label={`Move ${t.name} up`}
                  disabled={i === 0}
                  onClick={() => void moveStandup(t.id, i - 1)}
                >
                  <Icon name="chevron-up" size={14} />
                </button>
                <button
                  type="button"
                  class="cfg-move"
                  aria-label={`Move ${t.name} down`}
                  disabled={i === last}
                  onClick={() => void moveStandup(t.id, i + 1)}
                >
                  <Icon name="chevron-down" size={14} />
                </button>
              </span>
            </li>
          );
        })}
      </ul>
      <button type="button" class="cfg-rail-add" onClick={() => onAdd()}>
        + New standup
      </button>
      <p class="cfg-rail-foot">Order here = tab order and the 1–N shortcuts.</p>
    </aside>
  );
}
