// KeyLegend: the "?" popover with the ux-spec §8 keyboard map and the §7 pulse legend.
// Esc closes through a document keydown listener that exists only while open.
import { useEffect } from "preact/hooks";
import type { KeyLegendProps } from "./props";

const KEYS: [string[], string][] = [
  [["↑", "↓"], "Previous / next speaker (whole board after the last)"],
  [["←", "→"], "Previous / next standup"],
  [["B"], "Whole board; press again to return to the person"],
  [["1–9"], "Switch standup by position"],
  [["R"], "Resync incremental, from Jira"],
  [["Shift+R"], "Resync full, from Jira"],
  [["/"], "Focus the standup switcher"],
  [[","], "Open Configure"],
  [["?"], "Show this key legend"],
  [["Esc"], "Close this popover; in Configure, Done"],
  [["Enter"], "On a focused ticket, open it in Jira"],
];

const LANES: [string, string][] = [
  ["todo", "To Do"],
  ["prog", "In Progress"],
  ["review", "In Review"],
  ["qa", "QA"],
];

export function KeyLegend({ open, onClose, showCommits, showPRs, showComments, devActivity }: KeyLegendProps) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  const devAvailable = devActivity !== "unavailable";

  return (
    <div class="keylegend" role="dialog" aria-label="Keys and pulse legend">
      <div class="keylegend-head">
        <span class="keylegend-title">Keys</span>
        <button type="button" class="keylegend-close" aria-label="Close" onClick={() => onClose()}>
          <span aria-hidden="true">×</span>
        </button>
      </div>
      <ul class="keylegend-keys">
        {KEYS.map(([caps, text]) => (
          <li key={text} class="keylegend-row">
            <span class="keylegend-caps">
              {caps.map((c) => (
                <kbd key={c}>{c}</kbd>
              ))}
            </span>
            {" "}<span class="keylegend-text">{text}</span>
          </li>
        ))}
      </ul>
      <div class="keylegend-title">Pulse</div>
      <ul class="keylegend-pulse">
        {LANES.map(([lane, name]) => (
          <li key={lane} class="keylegend-row">
            <span class="legend-swatch" data-lane={lane} aria-hidden="true" />
            {" "}<span class="keylegend-text">{name}</span>
          </li>
        ))}
        <li class="keylegend-row">
          <span class="legend-swatch legend-swatch-blocked" aria-hidden="true" />
          {" "}<span class="keylegend-text">Blocked (hatched)</span>
        </li>
        <li class="keylegend-row">
          <span class="legend-chip" aria-hidden="true">
            3d
          </span>
          {" "}<span class="keylegend-text">Over limit (filled chip)</span>
        </li>
        {showCommits && devAvailable ? (
          <li class="keylegend-row">
            <span class="legend-tick" aria-hidden="true" />
            {" "}<span class="keylegend-text">Commit tick, taller for more commits that day</span>
          </li>
        ) : null}
        {showPRs && devAvailable ? (
          <li class="keylegend-row">
            <span class="legend-diamond" aria-hidden="true" />
            {" "}<span class="keylegend-text">PR activity that day</span>
          </li>
        ) : null}
        {showComments ? (
          <li class="keylegend-row">
            <span class="legend-comment" aria-hidden="true" />
            {" "}<span class="keylegend-text">Comment that day</span>
          </li>
        ) : null}
      </ul>
    </div>
  );
}
