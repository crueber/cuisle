// Implementation: WP-10. Props are frozen in props.ts.
// Inline SVG (architecture §4.6), 24-unit viewBox, stroke-only, colour from currentColor (token-driven via CSS color).
import type { IconName, IconProps } from "./props";

// Each icon is a list of path data strings. Circles and rectangles are written as paths so every icon is one shape kind.
const SHAPES: Record<IconName, readonly string[]> = {
  // Issue types: the shapes the ticket rows have always drawn (TicketRow's TypeIcon renders these).
  story: ["M6 3h12v18l-6-4-6 4z"], // bookmark
  bug: ["M12 8a5 5 0 1 0 0 10 5 5 0 0 0 0-10z", "M12 8V4M4 13h3M17 13h3M6 8l2 2M18 8l-2 2"],
  task: ["M5 3h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z", "M8 12l3 3 5-6"], // check-square
  spike: ["M13 2L4 14h7l-1 8 9-12h-7z"], // bolt
  other: ["M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z"], // circle
  search: ["M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14z", "M20 20l-4.2-4.2"],
  sliders: ["M4 6h10", "M18 6h2", "M4 12h4", "M12 12h8", "M4 18h12", "M20 18h0", "M14 4v4", "M8 10v4", "M16 16v4"],
  resync: ["M20 11a8 8 0 0 0-14.5-4.5L3 9", "M3 4v5h5", "M4 13a8 8 0 0 0 14.5 4.5L21 15", "M21 20v-5h-5"],
  close: ["M6 6l12 12", "M18 6L6 18"],
  plus: ["M12 5v14", "M5 12h14"],
  "chevron-down": ["M6 9l6 6 6-6"],
  "chevron-up": ["M6 15l6-6 6 6"],
  external: ["M14 4h6v6", "M20 4l-9 9", "M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"], // box with an arrow out
  board: ["M3 3h7v7H3z", "M14 3h7v7h-7z", "M3 14h7v7H3z", "M14 14h7v7h-7z"],
  download: ["M12 4v11", "M7 10l5 5 5-5", "M5 20h14"], // arrow into a tray (the Update button)
};

export type IssueIconName = "story" | "bug" | "task" | "spike" | "other";

/** Maps a Jira issue type kind onto its shared Icon name (subtask/epic/unknown → "other"). */
export function issueTypeIcon(type: string): IssueIconName {
  return type === "story" || type === "bug" || type === "task" || type === "spike" ? type : "other";
}

export function Icon(props: IconProps) {
  const size = props.size ?? 18;
  const strokeWidth = props.strokeWidth ?? 2.4;
  const title = props.title || undefined;
  return (
    <svg
      class={props.class}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width={strokeWidth}
      stroke-linecap="round"
      stroke-linejoin="round"
      role={title ? "img" : undefined}
      aria-hidden={title ? undefined : "true"}
    >
      {title ? <title>{title}</title> : null}
      {SHAPES[props.name].map((d) => (
        <path d={d} />
      ))}
    </svg>
  );
}
