import { test, expect, type Page } from "@playwright/test";
import { RUNS, p95, throttleCpu } from "./measure";
import { NOW_1421, TIMEZONE, openApp, seed, stubMedia } from "../ticketHelpers";

// Architecture §6.1, mock mode, 4x CPU throttle, p95 of 20 runs:
//   ticket modal open (click → painted)         <= 100 ms, and ONLY TicketService.Get (local) reaches the facade; no HTTP
//   ticket step (J/K → painted)                 <= 50 ms
//   optimistic write feedback (F → flag flips)  <= 32 ms
// The facade call log is the mock's own (window.__CUISLE_MOCK_CALLS__), so "zero facade network calls" is asserted on the
// real call list: after warm-up, the only method seen while opening and stepping is ticket.get.

const OPEN_BUDGET_MS = 100;
const STEP_BUDGET_MS = 50;
const WRITE_BUDGET_MS = 32;

type Trigger = { kind: "click"; selector: string } | { kind: "key"; key: string };

/** Fires the trigger, then resolves at the first animation frame after the watched DOM value changed (rejects on timeout). */
function timeDom(page: Page, trigger: Trigger, watch: string, attr: "text" | string): Promise<number> {
  return page.evaluate(
    ([t, sel, a]) =>
      new Promise<number>((resolve, reject) => {
        const read = () =>
          Array.from(document.querySelectorAll(sel))
            .map((e) => (a === "text" ? (e.textContent ?? "") : (e.getAttribute(a) ?? "")))
            .join("|");
        const before = read();
        const timer = setTimeout(() => {
          mo.disconnect();
          reject(new Error(`no change of ${sel} (${a}) within 2000 ms`));
        }, 2000);
        const mo = new MutationObserver(() => {
          if (read() === before) return;
          mo.disconnect();
          clearTimeout(timer);
          requestAnimationFrame(() => resolve(performance.now() - t0));
        });
        mo.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true });
        const t0 = performance.now();
        if (t.kind === "click") {
          (document.querySelector(t.selector) as HTMLElement).click();
        } else {
          (document.querySelector(".tm") as HTMLElement).dispatchEvent(new KeyboardEvent("keydown", { key: t.key, bubbles: true, cancelable: true }));
        }
      }),
    [trigger, watch, attr] as const,
  );
}

async function closeModal(page: Page): Promise<void> {
  await page.evaluate(() => (document.querySelector(".tm") as HTMLElement).dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })));
  await page.waitForSelector(".tm", { state: "detached" });
}

const callsSince = (page: Page) => page.evaluate(() => (window as unknown as { __CUISLE_MOCK_CALLS__: { method: string }[] }).__CUISLE_MOCK_CALLS__.map((c) => c.method));
const resetCalls = (page: Page) => page.evaluate(() => ((window as unknown as { __CUISLE_MOCK_CALLS__: unknown[] }).__CUISLE_MOCK_CALLS__.length = 0));

/** Marks the person-view row of MOB-1421 (the golden: every ADF node, 4 attachments, 4 comments) so a timed click can find it. */
const tagRow = (page: Page) =>
  page.evaluate(() => {
    const key = Array.from(document.querySelectorAll(".person-view .ticket-key")).find((e) => e.textContent?.trim() === "MOB-1421");
    key!.closest("button")!.setAttribute("data-perf-row", "");
  });

test.use({ timezoneId: TIMEZONE, viewport: { width: 1920, height: 1080 } });

test.describe("ticket modal budgets (mock, 4x CPU)", () => {
  test("open p95 <= 100 ms, only ticket.get reaches the facade, no HTTP", async ({ page, context }) => {
    await seed(page, NOW_1421);
    await stubMedia(page);
    await openApp(page);
    await page.waitForLoadState("networkidle");
    await throttleCpu(context, page);
    await tagRow(page);
    // Warm-up: one open/close so the modal's code and fonts are in memory.
    await timeDom(page, { kind: "click", selector: "[data-perf-row]" }, ".tm .th-key", "text");
    await closeModal(page);

    const requests: string[] = [];
    page.on("request", (r) => {
      const p = new URL(r.url()).pathname;
      if (!p.startsWith("/assets/") && !p.startsWith("/media/")) requests.push(r.url());
    });
    await resetCalls(page);
    const samples: number[] = [];
    for (let i = 0; i < RUNS; i++) {
      samples.push(await timeDom(page, { kind: "click", selector: "[data-perf-row]" }, ".tm .th-key", "text"));
      await closeModal(page);
    }
    const p = p95(samples);
    test.info().annotations.push({ type: "p95 ms", description: p.toFixed(1) });
    console.log(`PERF ${test.info().title}: p95 ${p.toFixed(1)} ms, max ${Math.max(...samples).toFixed(1)} ms`);
    expect(new Set(await callsSince(page))).toEqual(new Set(["ticket.get"]));
    expect(requests, "opening a ticket must not touch the network").toEqual([]);
    expect(p, `modal open p95 ${p.toFixed(1)} ms over budget ${OPEN_BUDGET_MS} ms`).toBeLessThanOrEqual(OPEN_BUDGET_MS);
  });

  test("step (J/K) p95 <= 50 ms, only ticket.get reaches the facade", async ({ page, context }) => {
    await seed(page, NOW_1421);
    await stubMedia(page);
    await openApp(page);
    await page.waitForLoadState("networkidle");
    await throttleCpu(context, page);
    await tagRow(page);
    await timeDom(page, { kind: "click", selector: "[data-perf-row]" }, ".tm .th-key", "text");
    await timeDom(page, { kind: "key", key: "j" }, ".tm .th-key", "text"); // warm-up
    await resetCalls(page);
    const samples: number[] = [];
    for (let i = 0; i < RUNS; i++) {
      samples.push(await timeDom(page, { kind: "key", key: i % 2 === 0 ? "j" : "k" }, ".tm .th-key", "text"));
    }
    const p = p95(samples);
    test.info().annotations.push({ type: "p95 ms", description: p.toFixed(1) });
    console.log(`PERF ${test.info().title}: p95 ${p.toFixed(1)} ms, max ${Math.max(...samples).toFixed(1)} ms`);
    expect(new Set(await callsSince(page))).toEqual(new Set(["ticket.get"]));
    expect(p, `step p95 ${p.toFixed(1)} ms over budget ${STEP_BUDGET_MS} ms`).toBeLessThanOrEqual(STEP_BUDGET_MS);
  });

  test("optimistic write (F toggles the flag) p95 <= 32 ms", async ({ page, context }) => {
    await seed(page, NOW_1421);
    await stubMedia(page);
    await openApp(page);
    await page.waitForLoadState("networkidle");
    await throttleCpu(context, page);
    await tagRow(page);
    await timeDom(page, { kind: "click", selector: "[data-perf-row]" }, ".tm .th-key", "text");
    await expect(page.locator('.tm [role="switch"]')).toBeEnabled();
    const settle = async () => {
      await expect(page.locator(".tm .fr-saving")).toHaveCount(0);
      await page.waitForTimeout(60);
    };
    await timeDom(page, { kind: "key", key: "f" }, '.tm [role="switch"]', "aria-checked"); // warm-up
    await settle();
    const samples: number[] = [];
    for (let i = 0; i < RUNS; i++) {
      samples.push(await timeDom(page, { kind: "key", key: "f" }, '.tm [role="switch"]', "aria-checked"));
      await settle();
    }
    const p = p95(samples);
    test.info().annotations.push({ type: "p95 ms", description: p.toFixed(1) });
    console.log(`PERF ${test.info().title}: p95 ${p.toFixed(1)} ms, max ${Math.max(...samples).toFixed(1)} ms`);
    expect(p, `optimistic write p95 ${p.toFixed(1)} ms over budget ${WRITE_BUDGET_MS} ms`).toBeLessThanOrEqual(WRITE_BUDGET_MS);
  });
});
