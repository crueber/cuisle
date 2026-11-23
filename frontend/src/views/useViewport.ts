// The window size in design px: the real size divided by the text scale, so Large (TV) at 1920 decides layout like
// Standard at 1536 (ux-spec §11). Re-read on resize only (no timers). `scale` is for multiplying inline px back.
import { useEffect, useState } from "preact/hooks";
import { unscale } from "../layout/responsive";
import { textScale } from "../state/app";

export function useViewport(): { vw: number; vh: number; scale: number } {
  const [size, setSize] = useState(() => ({ w: window.innerWidth, h: window.innerHeight }));
  useEffect(() => {
    const onResize = () => setSize({ w: window.innerWidth, h: window.innerHeight });
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  const scale = textScale.value;
  return { vw: unscale(size.w, scale), vh: unscale(size.h, scale), scale };
}
