import { test, expect } from "@playwright/test";
import { RUNS, p95, throttleCpu, timeKey, waitForStandupScreen } from "./measure";

// Architecture §6.1, mock mode, 4x CPU throttle, p95 of 20 runs.
//   standup switch (cached view) → painted  ≤ 50 ms
//   person switch (←/→)                      ≤ 32 ms, no IPC
//
// Each timed press must change the watched DOM (timeKey rejects otherwise), so a no-op key cannot pass as fast.
//
// Facade calls: MockApi is created inside main.tsx and is not exposed on window, so calls cannot be counted from
// outside without touching src. In mock mode every facade call is an in-memory promise, so there is no network and no
// IPC. The proxy asserted here is "zero network requests while switching" (embedded /assets/* excluded), plus the
// timing budget itself. A switch that waited on a fetch would show up as a request or as a time overrun.

const STANDUP_BUDGET_MS = 50;
const PERSON_BUDGET_MS = 32;

const TAB_WATCH = '.sf-tab[aria-pressed="true"] .sf-tab__num';
const PERSON_WATCH = ".person-name";

test.describe("switching budgets (mock, 4x CPU)", () => {
  test("standup switch by number key p95 <= 50 ms", async ({ page, context }) => {
    await throttleCpu(context, page);
    await waitForStandupScreen(page);
    // Untimed warm-up: 1 → 2 so the first measured switch is on a warm cache and module graph.
    await timeKey(page, "2", TAB_WATCH);
    // Measured: alternate 1 and 2, so every run is a real switch to a different cached standup.
    const samples: number[] = [];
    for (let i = 0; i < RUNS; i++) {
      samples.push(await timeKey(page, i % 2 === 0 ? "1" : "2", TAB_WATCH));
    }
    const p = p95(samples);
    test.info().annotations.push({ type: "p95 ms", description: p.toFixed(1) });
    expect(p, `standup switch p95 ${p.toFixed(1)} ms over budget ${STANDUP_BUDGET_MS} ms`).toBeLessThanOrEqual(
      STANDUP_BUDGET_MS,
    );
  });

  test("person switch by ArrowDown p95 <= 32 ms, no network", async ({ page, context }) => {
    await throttleCpu(context, page);
    await waitForStandupScreen(page);
    // Let boot-time requests (module chunks, first view) finish before counting.
    await page.waitForLoadState("networkidle");

    // Embedded assets (/assets/*: lazily loaded font faces, chunks) are not data calls; anything else is.
    const requests: string[] = [];
    page.on("request", (r) => {
      if (!new URL(r.url()).pathname.startsWith("/assets/")) requests.push(r.url());
    });

    // Untimed warm-up press, then RUNS measured presses. Each press moves to the next speaker or the whole board.
    await timeKey(page, "ArrowDown", PERSON_WATCH);
    const samples: number[] = [];
    for (let i = 0; i < RUNS; i++) {
      samples.push(await timeKey(page, "ArrowDown", PERSON_WATCH));
    }
    const p = p95(samples);
    test.info().annotations.push({ type: "p95 ms", description: p.toFixed(1) });
    expect(requests, "person switch must not hit the network or IPC").toEqual([]);
    expect(p, `person switch p95 ${p.toFixed(1)} ms over budget ${PERSON_BUDGET_MS} ms`).toBeLessThanOrEqual(
      PERSON_BUDGET_MS,
    );
  });
});
