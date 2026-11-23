// Configure jump bar (ux-spec §9.2: sticky pill links under the title when there are 1 or 2 columns).
import type { JumpBarProps } from "./props";

export function JumpBar({ sections, onJump }: JumpBarProps) {
  return (
    <nav class="cfg-jump" aria-label="Sections">
      {sections.map((s) => (
        <button key={s.id} type="button" class="cfg-pill" onClick={() => onJump(s.id)}>
          {s.label}
        </button>
      ))}
    </nav>
  );
}
