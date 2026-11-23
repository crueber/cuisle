// Implementation: WP-24. Decorative backdrop behind the rows (ux-spec §7 Backdrop).
import type { TrackBackdropProps } from "./props";

export function TrackBackdrop({ axis, width, shadeWeekends, todayLine }: TrackBackdropProps) {
  return (
    <div class="track-backdrop" aria-hidden="true" style={`width:${width}px;--days:${axis.length}`}>
      {shadeWeekends
        ? axis.map((d, i) =>
            d.weekend ? <span key={d.date} class="track-weekend" data-weekend="" style={`--x:${i}`} /> : null,
          )
        : null}
      <span class="track-today" data-today-line={String(todayLine)} style={`width:${todayLine}px`} />
    </div>
  );
}
