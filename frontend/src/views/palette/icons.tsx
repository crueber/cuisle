// Palette-only icons (F14): the glyphs the shared Icon set does not have. They live in the lazy palette chunk so the
// entry bundle does not carry them. Same drawing rules as components/Icon: 24-unit box, stroke only, currentColor.
import { Icon } from "../../components/Icon";
import type { IconName } from "../../components/props";

const EXTRA: Record<string, readonly string[]> = {
  person: ["M12 4a4 4 0 1 0 0 8 4 4 0 0 0 0-8z", "M4.5 21a7.5 7.5 0 0 1 15 0"],
  layers: ["M12 3l9 5-9 5-9-5z", "M3 13l9 5 9-5"],
  columns: ["M4 4h16v16H4z", "M10 4v10", "M16 4v6"],
  text: ["M5 7V5h14v2", "M12 5v14", "M9 19h6"],
  check: ["M5 12.5l4.5 4.5L19 7"],
  "chevron-right": ["M9 6l6 6-6 6"],
  sun: ["M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8z", "M12 2.5v2M12 19.5v2M4.6 4.6l1.4 1.4M18 18l1.4 1.4M2.5 12h2M19.5 12h2M4.6 19.4L6 18M18 6l1.4-1.4"],
  minus: ["M5 12h14"],
  copy: ["M9 9h11v11H9z", "M5 15V5a1 1 0 0 1 1-1h10"],
};

const CORE = new Set<string>(["story", "bug", "task", "spike", "other", "sliders", "resync", "close", "plus", "board", "external", "download", "search"]);

export function PaletteIcon({ name, size = 20, class: cls }: { name: string; size?: number; class?: string }) {
  const extra = EXTRA[name];
  if (!extra) return <Icon name={(CORE.has(name) ? name : "other") as IconName} size={size} strokeWidth={2} class={cls} />;
  return (
    <svg class={cls} width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      {extra.map((d) => (
        <path d={d} />
      ))}
    </svg>
  );
}
