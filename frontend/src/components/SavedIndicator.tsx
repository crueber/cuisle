// SavedIndicator (ux-spec §9.1, architecture §5.3). Renders the state it is given; the 250 ms delay lives in save.ts.
// The status node is always rendered and is the same DOM node across states (live region).
import type { SavedIndicatorProps } from "./props";

export function SavedIndicator({ state, onRetry }: SavedIndicatorProps) {
  return (
    <span role="status" class="saved" data-state={state}>
      {state === "saving" ? "Saving…" : null}
      {state === "saved" ? "Saved" : null}
      {state === "settled" ? "All changes saved" : null}
      {state === "failed" ? (
        <>
          Not saved <span aria-hidden="true">·</span>{" "}
          <button type="button" class="saved-retry" onClick={() => onRetry()}>
            Retry
          </button>
        </>
      ) : null}
    </span>
  );
}
