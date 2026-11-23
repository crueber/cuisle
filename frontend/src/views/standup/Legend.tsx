// Implementation: WP-24. Colour key for the track (ux-spec §3 legend rule). Modes: full, short, button ("? Key").
import { keyLegendOpen } from "../../state/ui";
import type { LegendProps } from "./props";

export function Legend({ mode, options, lanes }: LegendProps) {
  if (mode === "button") {
    return (
      <button
        type="button"
        class="legend-key"
        onClick={() => {
          keyLegendOpen.value = true;
        }}
      >
        ? Key
      </button>
    );
  }
  const full = mode === "full";
  const devOk = options.devActivity !== "unavailable";
  // Colour key reads in board-flow order (To Do first), the reverse of the speaking walk order.
  const walk = lanes.filter((l) => l.lane !== "done").reverse();
  return (
    <div class="legend">
      {walk.map((l) => (
        <span key={l.lane} class="legend-item">
          <span class="legend-swatch" data-lane={l.lane} aria-hidden="true" />
          <span>{l.label}</span>
        </span>
      ))}
      <span class="legend-item">
        <span class="legend-blocked" aria-hidden="true" />
        <span>blocked</span>
      </span>
      {full && options.showCommits && devOk ? (
        <span class="legend-item">
          <span class="legend-commit" aria-hidden="true" />
          <span>commit</span>
        </span>
      ) : null}
      {full && options.showPRs && devOk ? (
        <span class="legend-item">
          <span class="legend-pr" aria-hidden="true" />
          <span>PR</span>
        </span>
      ) : null}
      {full && options.showComments ? (
        <span class="legend-item">
          <span class="legend-comment" aria-hidden="true" />
          <span>comment</span>
        </span>
      ) : null}
      <span class="legend-item">
        <span class="legend-over" aria-hidden="true">
          6d
        </span>
        <span>over limit</span>
      </span>
    </div>
  );
}
