import { test, expect, type Page } from "@playwright/test";
import { NOW_1421, TIMEZONE, openApp, openTicket, seed, stubMedia } from "../ticketHelpers";

// App rule (AGENTS invariant 16): nothing in Cuisle scrolls sideways — not the window, not a screen, not a panel, not a
// box inside the ticket modal. Every element that could scroll horizontally must have no horizontal overflow.
// Configure once did in the app's WebKit webview (CSS multi-column fragmented past the right edge) while Chromium looked
// fine, so run this locally with `--browser=webkit` too when touching layout.

const SIZES = [
  [1920, 1080],
  [1800, 1190],
  [1440, 900],
  [1280, 720],
  [1024, 640],
] as const;

test.use({ timezoneId: TIMEZONE });

/** Every element whose content is wider than its box and that would scroll (or is the page itself). */
async function sidewaysScrollers(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out: string[] = [];
    const root = document.scrollingElement as HTMLElement;
    if (root.scrollWidth > root.clientWidth + 1) out.push(`page sw=${root.scrollWidth} cw=${root.clientWidth}`);
    for (const el of Array.from(document.querySelectorAll<HTMLElement>("body *"))) {
      const ox = getComputedStyle(el).overflowX;
      if ((ox === "auto" || ox === "scroll") && el.scrollWidth > el.clientWidth + 1) {
        out.push(`${el.tagName.toLowerCase()}.${String(el.className).trim().replace(/\s+/g, ".")} sw=${el.scrollWidth} cw=${el.clientWidth}`);
      }
    }
    return out;
  });
}

for (const [w, h] of SIZES) {
  test.describe(`${w}x${h}`, () => {
    test.use({ viewport: { width: w, height: h } });

    test.beforeEach(async ({ page }) => {
      await seed(page, NOW_1421);
      await stubMedia(page);
      await openApp(page);
    });

    test("person view never scrolls sideways", async ({ page }) => {
      expect(await sidewaysScrollers(page)).toEqual([]);
    });

    test("whole board never scrolls sideways", async ({ page }) => {
      await page.keyboard.press("b");
      await page.locator(".board-row-key").first().waitFor();
      expect(await sidewaysScrollers(page)).toEqual([]);
    });

    test("configure never scrolls sideways", async ({ page }) => {
      await page.keyboard.press(",");
      await page.locator(".cfg-screen").waitFor();
      await page.locator("#sec-mapping .cfg-section").waitFor(); // the sections render once the config has loaded
      await page.locator('.cfg-grid[data-balanced="true"]').waitFor();
      await page.waitForLoadState("networkidle");
      expect(await sidewaysScrollers(page)).toEqual([]);
      // The columns balance themselves: no column is longer than another by more than its tallest section.
      await page.waitForTimeout(300);
      const spread = await page.evaluate(() => {
        const cols = Array.from(document.querySelectorAll<HTMLElement>(".cfg-col"));
        const hs = cols.map((c) => c.getBoundingClientRect().height);
        const tallest = Math.max(...Array.from(document.querySelectorAll<HTMLElement>(".cfg-anchor")).map((a) => a.offsetHeight));
        return { n: cols.length, diff: Math.max(...hs) - Math.min(...hs), tallest };
      });
      if (spread.n > 1) expect(spread.diff).toBeLessThanOrEqual(spread.tallest + 24);
    });

    test("ticket modal (code block, table, long text) never scrolls sideways", async ({ page }) => {
      await openTicket(page, "MOB-1421");
      expect(await sidewaysScrollers(page)).toEqual([]);
    });
  });
}
