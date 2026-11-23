// Implementation: WP-24. Positions Go-computed pulse data in day units (ux-spec §7); draws no colours inline.
import { textScale } from "../../state/app";
import { markBoxes, segBoxes, spanBoxes, trackGeometry } from "../../layout/track";
import type { PulseTrackProps } from "./props";

export function PulseTrack({ pulse, days, width, rowH, lane, done, options }: PulseTrackProps) {
  const geo = trackGeometry(rowH, lane);
  // Geometry is in design px; the text scale (Large/TV) multiplies what is emitted. `width` is already real px.
  const k = textScale.value;
  const devOk = options.devActivity !== "unavailable";
  const showMarks = !lane && !done;
  const marks = showMarks
    ? markBoxes(pulse.marks, {
        showCommits: options.showCommits && devOk,
        showPRs: options.showPRs && devOk,
        showComments: options.showComments,
        segTop: geo.segTop,
      })
    : [];
  const cls = ["pulse-track", lane ? "is-lane" : "", done ? "is-done" : ""].filter(Boolean).join(" ");

  return (
    <span role="img" aria-label={pulse.label} class={cls} style={`width:${width}px;height:${rowH * k}px;--days:${days}`}>
      {segBoxes(pulse.segs).map((b, i) => (
        <span
          key={`seg-${i}`}
          class="pulse-seg"
          data-lane={b.lane}
          style={`--x:${b.x};--w:${b.w};top:${geo.segTop * k}px;height:${geo.segH * k}px`}
        />
      ))}
      {spanBoxes(pulse.blocked).map((b, i) => (
        <span
          key={`blk-${i}`}
          class="pulse-blocked"
          data-blocked=""
          style={`--x:${b.x};--w:${b.w};top:${(geo.segTop - 2) * k}px;height:${(geo.segH + 4) * k}px`}
        />
      ))}
      {marks.map((m) => {
        // Commit and comment boxes sit `bottom` px above the segment top; PR diamonds are centred `bottom` px above it.
        const top = m.kind === "pr" ? geo.segTop - m.bottom - m.height / 2 : geo.segTop - m.bottom - m.height;
        return (
          <span
            key={`${m.kind}-${m.day}`}
            class="pulse-mark"
            data-mark={m.kind}
            data-level={m.kind === "commit" ? String(m.level) : undefined}
            style={`--x:${m.day};top:${top * k}px;height:${m.height * k}px`}
          />
        );
      })}
    </span>
  );
}
