import type { BrowserContext, Page } from "@playwright/test";

// Shared helpers for the perf budgets (architecture §6.1). Not a spec file.

/**
 * Chromium CPU throttle used by every budget: 4x slowdown relative to a developer machine, per architecture §6.1.
 * Shared CI runners are already about 2x slower than that reference, so CI sets PERF_CPU_THROTTLE=2 to keep the
 * effective slowdown comparable (scripts/publish/ci.yml). The budgets themselves never change.
 */
export const CPU_THROTTLE = Number(process.env.PERF_CPU_THROTTLE) > 0 ? Number(process.env.PERF_CPU_THROTTLE) : 4;

/** Measured runs per budget (testing §5). */
export const RUNS = 20;

/** How long a timed interaction may take to change the DOM before the run counts as a failure. */
const CHANGE_TIMEOUT_MS = 2000;

/**
 * Nearest-rank p95: for 20 samples this is the 19th smallest value.
 * Pure function so the rule is documented in one place.
 */
export function p95(samples: readonly number[]): number {
  if (samples.length === 0) throw new Error("p95 of an empty sample set");
  const sorted = [...samples].sort((a, b) => a - b);
  const rank = Math.ceil(0.95 * sorted.length);
  return sorted[rank - 1]!;
}

/** Applies the CPU throttle through CDP (Chromium only; the budgets are Chromium-measured). */
export async function throttleCpu(context: BrowserContext, page: Page): Promise<void> {
  const cdp = await context.newCDPSession(page);
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: CPU_THROTTLE });
}

/**
 * Dispatches a keydown on window (the global key listener's target), waits for a DOM mutation under #app whose
 * `watch` text actually changed, then resolves at the next animation frame. The result is dispatch → that frame, so it
 * covers the handler, the async view lookup, Preact's render and one frame. A run that changes nothing rejects, so a
 * no-op key cannot produce a fast number.
 *
 * `watch` is a selector whose textContent is read before and after; it must change on every press.
 */
export function timeKey(page: Page, key: string, watch: string): Promise<number> {
  return page.evaluate(
    ([k, sel, timeoutMs]) =>
      new Promise<number>((resolve, reject) => {
        const read = () =>
          Array.from(document.querySelectorAll(sel))
            .map((e) => e.textContent ?? "")
            .join("|");
        const before = read();
        const root = document.getElementById("app");
        if (!root) {
          reject(new Error("no #app root"));
          return;
        }
        const t0 = performance.now();
        let settled = false;
        const timer = setTimeout(() => {
          if (settled) return;
          settled = true;
          mo.disconnect();
          reject(new Error(`key ${k}: no state change in ${timeoutMs} ms (watch ${sel})`));
        }, timeoutMs);
        const mo = new MutationObserver(() => {
          if (settled || read() === before) return;
          settled = true;
          clearTimeout(timer);
          mo.disconnect();
          requestAnimationFrame(() => resolve(performance.now() - t0));
        });
        mo.observe(root, { subtree: true, childList: true, characterData: true, attributes: true });
        window.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }));
      }),
    [key, watch, CHANGE_TIMEOUT_MS] as const,
  );
}

/**
 * Sets an input's value and dispatches `input` (what typing does), timed like timeKey. The watched list is any
 * [role=option] under document.body (the Combobox list may be portalled). Rejects when the options do not change.
 */
export function timeInput(page: Page, selector: string, value: string): Promise<number> {
  return page.evaluate(
    ([sel, v, timeoutMs]) =>
      new Promise<number>((resolve, reject) => {
        const el = document.querySelector<HTMLInputElement>(sel);
        if (!el) {
          reject(new Error(`no input for ${sel}`));
          return;
        }
        const read = () =>
          Array.from(document.querySelectorAll('[role="option"]'))
            .map((e) => e.textContent ?? "")
            .join("|");
        const before = read();
        const t0 = performance.now();
        let settled = false;
        const timer = setTimeout(() => {
          if (settled) return;
          settled = true;
          mo.disconnect();
          reject(new Error(`input "${v}": options did not change in ${timeoutMs} ms`));
        }, timeoutMs);
        const mo = new MutationObserver(() => {
          if (settled || read() === before) return;
          settled = true;
          clearTimeout(timer);
          mo.disconnect();
          requestAnimationFrame(() => resolve(performance.now() - t0));
        });
        mo.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true });
        el.value = v;
        el.dispatchEvent(new Event("input", { bubbles: true }));
      }),
    [selector, value, CHANGE_TIMEOUT_MS] as const,
  );
}

/** Waits until the standup screen is painted (first ticket row rendered). */
export async function waitForStandupScreen(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator(".sf-screen .ticket-key").first().waitFor();
}
