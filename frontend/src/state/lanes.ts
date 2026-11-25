// Custom-lane colours (ux-spec §9.4 Status mapping). Go assigns each lane the user names a palette index (LaneInfo.hue,
// 1..8); elements that carry data-lane also carry data-hue so the shared CSS rule paints them from the extended lane
// palette tokens. Built-in lanes have no hue and keep their own data-lane rules. Nothing here invents lane data: the
// hues come from the current standup's view and the open Configure form.
import { computed } from "@preact/signals";
import { config } from "./config";
import { currentView } from "./standup";

const hues = /* @__PURE__ */ computed(() => {
  const m = new Map<string, number>();
  for (const l of currentView.value?.lanes ?? []) if (l.hue) m.set(l.lane, l.hue);
  for (const l of config.value?.laneOptions ?? []) if (l.hue) m.set(l.lane, l.hue);
  return m;
});

/** The palette index of a custom lane, for data-hue; undefined (no attribute) for built-in lanes. */
export function hueOf(lane: string): number | undefined {
  return hues.value.get(lane);
}
