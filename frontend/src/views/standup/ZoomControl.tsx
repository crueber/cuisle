// The zoom control in the title row of the person view and the Whole board (ux-spec §16.2): "− 100% +", the middle
// button showing the level and resetting it, ink-filled whenever the zoom is not 100%. A toast under it names what the
// layout did ("150% · 2 columns") for 1.2 s. Zoom is per standup and shared by both views.
import { useEffect, useRef, useState } from "preact/hooks";
import { route } from "../../state/route";
import { resetZoom, setZoom, stepFrom, zoomFlash, zoomOf, ZOOM_DEFAULT, ZOOM_STEPS } from "../../state/zoom";

/** Room the control takes in the title row (px): the short legend's threshold rises by it, so the legend gives way first. */
export const ZOOM_CONTROL_PX = 150;

const TOAST_MS = 1200;

export function ZoomControl({ note }: { note: string }) {
  const r = route.value;
  const flash = zoomFlash.value;
  const seen = useRef(flash.seq);
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (flash.seq === seen.current) return;
    seen.current = flash.seq;
    setShown(true);
    const t = setTimeout(() => setShown(false), TOAST_MS);
    return () => clearTimeout(t);
  }, [flash.seq]);
  if (r.view !== "standup") return null;
  const level = zoomOf(r.standupId);
  const min = level <= (ZOOM_STEPS[0] ?? 70);
  const max = level >= (ZOOM_STEPS[ZOOM_STEPS.length - 1] ?? 200);
  return (
    <div class="zoom">
      <div class="zoom-ctl" role="group" aria-label="Zoom">
        <button type="button" class="zoom-btn" aria-label="Zoom out (−)" disabled={min} onClick={() => setZoom(r.standupId, stepFrom(level, -1))}>
          <span aria-hidden="true">−</span>
        </button>
        <button type="button" class="zoom-btn zoom-level" aria-label={level === ZOOM_DEFAULT ? `Reset zoom to ${ZOOM_DEFAULT}% (0)` : `${level}%. Reset zoom to ${ZOOM_DEFAULT}% (0)`} data-zoomed={String(level !== ZOOM_DEFAULT)} onClick={() => resetZoom()}>
          {`${level}%`}
        </button>
        <button type="button" class="zoom-btn" aria-label="Zoom in (+)" disabled={max} onClick={() => setZoom(r.standupId, stepFrom(level, 1))}>
          <span aria-hidden="true">+</span>
        </button>
      </div>
      <div class="zoom-toast-slot" role="status">
        {shown ? <span class="zoom-toast">{`${flash.percent}% · ${note}`}</span> : null}
      </div>
    </div>
  );
}
