// Implementation: WP-24. "Scroll for more" bar and its end state (ux-spec §5.6). Renders nothing when not visible.
import type { MoreBarProps } from "./props";

export function MoreBar({ visible, atEnd, summary }: MoreBarProps) {
  if (!visible) return null;
  return (
    <div class="more-bar" aria-live="polite">
      {atEnd ? (
        <>
          <span class="more-bar-title">End of list</span>
          <span class="more-bar-summary">every ticket is shown above</span>
        </>
      ) : (
        <>
          <span class="more-bar-title">⌄ Scroll for more</span>
          <span class="more-bar-summary">{summary}</span>
        </>
      )}
    </div>
  );
}
