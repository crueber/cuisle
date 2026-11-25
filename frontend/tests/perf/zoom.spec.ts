import { test, expect } from "@playwright/test";
import { RUNS, p95, throttleCpu, timeKey, waitForStandupScreen } from "./measure";

// F15 (ux-spec §16.3): a zoom step re-runs the layout maths and repaints the screen within the existing switch budgets,
// mock mode, 4x CPU throttle, p95 of 20 runs, ≤ 50 ms to paint (the standup-switch budget). The watched text is the
// control's level, which changes on every press; no network, no IPC.
//
// The two levels alternated are chosen so that every timed press is a real REFLOW, not a rescale:
//   - whole board at 1920 x 1080: 100% <-> 90%, which is 2 <-> 3 board columns (the lanes are re-cut and re-drawn);
//   - person view: 110% <-> 125%, which is "compact rows, side headings" <-> "tight rows, side headings" (the density
//     ladder changes step, so every row re-renders at another height).
// Each run checks the layout really changed (the columns or the density tag), so a no-op cannot pass as fast.

// The levels below are the ones that reflow on a 1920 x 1080 window.
test.use({ viewport: { width: 1920, height: 1080 } });

const PAINT_BUDGET_MS = 50;
const LEVEL = ".zoom-level";

const CASES = [
  { view: "whole board", prepare: "b", start: "", first: "-", second: "=", layout: ".board-col" },
  { view: "person view", prepare: "", start: "=", first: "=", second: "-", layout: ".person-density-tag" },
] as const;

for (const c of CASES) {
  test(`zoom reflow on the ${c.view}: p95 <= ${PAINT_BUDGET_MS} ms, no network`, async ({ page, context }) => {
    await throttleCpu(context, page);
    await waitForStandupScreen(page);
    if (c.prepare) await page.keyboard.press(c.prepare);
    await page.locator(LEVEL).waitFor();
    await page.waitForLoadState("networkidle");
    if (c.start) await page.keyboard.press(c.start); // person view: 100% -> 110%, then 110% <-> 125%
    const requests: string[] = [];
    page.on("request", (r) => {
      if (!new URL(r.url()).pathname.startsWith("/assets/")) requests.push(r.url());
    });
    const layout = () => page.locator(c.layout).evaluateAll((els) => els.map((e) => (e.textContent ?? "").length + ":" + e.className).join("|") + `#${els.length}`);

    // Untimed warm-up pair, then 20 measured presses alternating the two levels.
    await timeKey(page, c.first, LEVEL);
    const a = await layout();
    await timeKey(page, c.second, LEVEL);
    const b = await layout();
    expect(a, "the two levels must lay out differently, or this times a rescale").not.toBe(b);
    const samples: number[] = [];
    for (let i = 0; i < RUNS; i++) samples.push(await timeKey(page, i % 2 === 0 ? c.first : c.second, LEVEL));
    const p = p95(samples);
    test.info().annotations.push({ type: "p95 ms", description: p.toFixed(1) });
    expect(requests, "a zoom step must not hit the network or IPC").toEqual([]);
    expect(p, `zoom step p95 ${p.toFixed(1)} ms over budget ${PAINT_BUDGET_MS} ms`).toBeLessThanOrEqual(PAINT_BUDGET_MS);
  });
}
