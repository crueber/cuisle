import { test, expect } from "@playwright/test";
// Configure jump bar (ux-spec §9.2): when stuck, it sits flush with the top of the scrolling main; nothing shows above it.
import { NOW_1421, TIMEZONE, openApp, seed } from "../ticketHelpers";
test.use({ timezoneId: TIMEZONE });
for (const [w, h] of [[1440, 900], [1280, 720], [1024, 640]] as const) {
  test(`configure jump bar sticks flush to the top at ${w}x${h}`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await seed(page, NOW_1421); await openApp(page);
    await page.keyboard.press(","); await page.locator('.cfg-grid[data-balanced="true"]').waitFor();
    await page.locator(".cfg-main").evaluate((m) => { m.scrollTop = 600; });
    const r = await page.evaluate(() => ({ main: document.querySelector(".cfg-main")!.getBoundingClientRect().top, bar: document.querySelector(".cfg-jump")!.getBoundingClientRect().top }));
    expect(Math.abs(r.bar - r.main)).toBeLessThanOrEqual(1);
  });
}
