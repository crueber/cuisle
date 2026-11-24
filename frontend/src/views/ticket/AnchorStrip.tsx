// AnchorStrip: the pinned nav of the ticket content column (ux-spec §14.4 "Anchor strip"; props.ts AnchorStripProps).
// Draws exactly the entries it is given; `active` is a prop. No api calls, no state.
import type { AnchorStripProps } from "./props";
import "./AnchorStrip.css";

export function AnchorStrip({ sections, active, onJump }: AnchorStripProps) {
  return (
    <nav class="anchor" aria-label="Sections">
      {sections.map((s) => (
        <button
          key={s.id}
          type="button"
          class="anchor-item"
          aria-current={s.id === active ? "true" : undefined}
          onClick={() => onJump(s.id)}
        >
          {s.label}
          {s.count !== "" ? <span class="anchor-count"> {s.count}</span> : null}
        </button>
      ))}
    </nav>
  );
}
