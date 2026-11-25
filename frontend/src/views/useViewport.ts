// The window size in design px: the real size divided by the text scale, so Large (TV) at 1920 decides layout like
// Standard at 1536 (ux-spec §11). Re-read on resize only (no timers). `scale` is for multiplying inline px back.
// `cvw`/`cvh` are the same for the standup screen content below the header, which also divides by the standup's zoom
// (ux-spec §16.3): at 150% a 1920 × 1080 window lays out like 1280 wide. `cscale` is text scale × zoom.
import { useEffect, useState } from "preact/hooks";
import { contentViewport, unscale } from "../layout/responsive";
import { contentScale, textScale } from "../state/app";

export function useViewport(): { vw: number; vh: number; scale: number; cvw: number; cvh: number; cscale: number } {
  const [size, setSize] = useState(() => ({ w: window.innerWidth, h: window.innerHeight }));
  useEffect(() => {
    const onResize = () => setSize({ w: window.innerWidth, h: window.innerHeight });
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  const scale = textScale.value;
  const cscale = contentScale.value;
  const c = contentViewport(size.w, size.h, scale, cscale);
  return { vw: unscale(size.w, scale), vh: unscale(size.h, scale), scale, cvw: c.vw, cvh: c.vh, cscale };
}
