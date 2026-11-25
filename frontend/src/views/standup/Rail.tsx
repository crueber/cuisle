// Speaker rail (ux-spec §2.2): full (208px, names) or compact (76px, initials). Shows included speakers only, in the order given; it never
// hides people and does no repositioning (the view already places Unassigned first, last or skipped).
import { Icon } from "../../components/Icon";
import { tabStandups } from "../../state/app";
import { findKeyLabel } from "../../state/palette";
import type { RailProps } from "./props";

export function Rail(props: RailProps) {
  const { mode, speakers, selected, onSelect } = props;
  const full = mode === "full";
  const people = speakers.filter((s) => s.included);
  const standupCount = Math.max(1, tabStandups().length);
  // The full rail shows first names; two people sharing one get their full names so they stay distinguishable.
  const firsts = new Map<string, number>();
  for (const s of people) firsts.set(s.first, (firsts.get(s.first) ?? 0) + 1);

  return (
    <nav aria-label="Speakers" class={`sf-rail sf-rail--${mode}`}>
      <button
        type="button"
        class="sf-rail__item sf-rail__whole"
        aria-pressed={selected === null ? "true" : "false"}
        aria-label={full ? undefined : "Whole board"}
        title={full ? undefined : "Whole board"}
        onClick={() => onSelect(null)}
      >
        {full ? "Whole board" : <Icon name="board" size={22} />}
      </button>
      {full ? <div class="sf-rail__caps">Speaking order</div> : null}
      {people.map((s) => {
        const pressed = selected === s.id ? "true" : "false";
        const cls = s.unassigned ? "sf-rail__item sf-rail__item--unassigned" : "sf-rail__item";
        if (full) {
          return (
            <button
              key={s.id}
              type="button"
              class={cls}
              aria-pressed={pressed}
              title={s.name}
              onClick={() => onSelect(s.id)}
            >
              <span class="sf-rail__first">{(firsts.get(s.first) ?? 0) > 1 ? s.name : s.first}</span>{" "}
              <span class="sf-rail__count">{s.active}</span>
            </button>
          );
        }
        return (
          <button
            key={s.id}
            type="button"
            class={cls}
            aria-pressed={pressed}
            aria-label={`${s.unassigned ? "Unas." : s.initials} ${s.active} — ${s.name}`}
            title={s.name}
            onClick={() => onSelect(s.id)}
          >
            <span class="sf-rail__initials">{s.unassigned ? "Unas." : s.initials}</span>{" "}
            <span class="sf-rail__count">{s.active}</span>
          </button>
        );
      })}
      {full ? (
        <div class="sf-rail__hint">
          <span>↑ ↓ speaker</span> <span>← → standup</span> <span>B board ⇄ person</span> <span>G group by</span> <span>+ − zoom</span> <span>{`1–${standupCount} jump`}</span> <span>{findKeyLabel()} find</span>
        </div>
      ) : null}
    </nav>
  );
}
