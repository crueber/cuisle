import { test, expect } from "@playwright/test";
import { RUNS, p95, throttleCpu, timeInput, waitForStandupScreen } from "./measure";

// Architecture §6.1: Combobox keystroke → filtered list, static options.
//   target ≤ 16 ms (reported as an annotation), fail > 32 ms (p95, 20 runs, 4x CPU).
//
// The app has no 500-option static list, and this WP does not add one to src. This spec therefore measures the
// largest static combobox that exists: the Configure › Status mapping lane picker (6 lane options). The 500-option case
// belongs in WP-11's unit tests for the filter (Combobox.filter.ts), not here.

const COMBOBOX_FAIL_MS = 32;
const COMBOBOX_TARGET_MS = 16;

// Each query changes the visible option set compared with the previous one (the list opens showing all options),
// so every keystroke must re-filter: "d" (Done), "p" (In Progress), "q" (QA), "b" (Backlog), "r" (In Review,
// In Progress). Cycled to RUNS; no two consecutive queries share a result set.
const QUERIES = ["d", "p", "q", "b", "r"];
const KEYSTROKES: string[] = Array.from({ length: RUNS }, (_, i) => QUERIES[i % QUERIES.length]!);

const INPUT = 'input[role="combobox"][id^="cfg-map-"]';

test("combobox keystroke to filtered list p95 <= 32 ms (target 16) at 4x CPU", async ({ page, context }) => {
  await throttleCpu(context, page);
  await waitForStandupScreen(page);
  await page.keyboard.press(",");
  // Status mapping lane pickers are input-variant comboboxes with id cfg-map-<column>.
  const input = page.locator(INPUT).first();
  await expect(input).toBeVisible();
  await input.click();
  // ArrowDown opens the list without typing (Combobox keyboard map); the list then shows every option.
  await page.keyboard.press("ArrowDown");
  await expect(input).toHaveAttribute("aria-expanded", "true");
  const allLabels = await page.locator('[role="option"]').allTextContents();
  expect(allLabels.length, "status mapping offers several lane options").toBeGreaterThan(1);

  const samples: number[] = [];
  for (const value of KEYSTROKES) {
    samples.push(await timeInput(page, INPUT, value));
    // The filtered list must be the prefix/substring match for the query (Combobox.filter.ts rule), not any list.
    const shown = await page.locator('[role="option"]').allTextContents();
    const want = allLabels.filter((l) => l.toLowerCase().includes(value.toLowerCase()));
    expect(shown.map((s) => s.trim()).sort(), `options for query "${value}"`).toEqual(
      want.map((s) => s.trim()).sort(),
    );
  }
  const p = p95(samples);
  test.info().annotations.push({ type: "p95 ms", description: p.toFixed(1) });
  if (p > COMBOBOX_TARGET_MS) {
    test.info().annotations.push({ type: "warning", description: `p95 ${p.toFixed(1)} ms above the 16 ms target` });
  }
  expect(p, `combobox p95 ${p.toFixed(1)} ms over fail limit ${COMBOBOX_FAIL_MS} ms`).toBeLessThanOrEqual(
    COMBOBOX_FAIL_MS,
  );
});
