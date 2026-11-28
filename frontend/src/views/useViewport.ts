// The window size in design px: the real size divided by the text scale, so Large (TV) at 1920 decides layout like
// Standard at 1536 (ux-spec §11). Re-read on resize only (no timers). `scale` is for multiplying inline px back.
// `cvw`/`cvh` are the same for the standup screen content below the header, which also divides by the standup's zoom
// (ux-spec §16.3): at 150% a 1920 × 1080 window lays out like 1280 wide. `cscale` is text scale × zoom.
import { signal } from "@preact/signals";
import { contentViewport, unscale } from "../layout/responsive";
import { contentScale, textScale } from "../state/app";

// A single shared resize listener ticks a signal so multiple mounted views don't each attach their own listener,
// while still reading live window.innerWidth/innerHeight (so jsdom tests that mutate innerWidth without dispatching
// resize see the exact size immediately).
const resizeTick = signal(0);
let listening = false;
function ensureResizeListener(): void {
  if (listening || typeof window === "undefined") return;
  listening = true;
  window.addEventListener("resize", () => {
    resizeTick.value++;
  });
}

function liveSize(): { w: number; h: number } {
  if (typeof window === "undefined") return { w: 1440, h: 900 };
  return { w: window.innerWidth, h: window.innerHeight };
}

/** Window size scaled only by textScale (for header/modal — never re-renders on standup zoom changes). */
export function useWindowViewport(): { vw: number; vh: number; scale: number } {
  ensureResizeListener();
  void resizeTick.value;
  const size = liveSize();
  const scale = textScale.value;
  return { vw: unscale(size.w, scale), vh: unscale(size.h, scale), scale };
}

export function useViewport(): { vw: number; vh: number; scale: number; cvw: number; cvh: number; cscale: number } {
  ensureResizeListener();
  void resizeTick.value;
  const size = liveSize();
  const scale = textScale.value;
  const cscale = contentScale.value;
  const c = contentViewport(size.w, size.h, scale, cscale);
  return { vw: unscale(size.w, scale), vh: unscale(size.h, scale), scale, cvw: c.vw, cvh: c.vh, cscale };
}
