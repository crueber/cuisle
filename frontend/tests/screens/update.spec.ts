import { test, expect } from "@playwright/test";
import { NOW_1421, TIMEZONE, UPDATE_AVAILABLE, openApp, seed, stubMedia } from "../ticketHelpers";

// The Update button in the standup header and its confirm dialog (ux-spec §2.1, ADR 0016), in mock mode with a
// channel build that has a newer release.

const SHOT = { maxDiffPixelRatio: 0.02, animations: "disabled" } as const;

test.use({ timezoneId: TIMEZONE });

for (const [w, h] of [[1920, 1080], [1280, 720], [1024, 640]] as const) {
  test(`header with the Update button ${w}x${h}`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await seed(page, NOW_1421, UPDATE_AVAILABLE);
    await stubMedia(page);
    await openApp(page);
    const header = page.locator("header.sf-header");
    const labels = await header.locator("button.sf-header__configure").evaluateAll((bs) => bs.map((b) => b.getAttribute("aria-label")));
    expect(labels[0]).toMatch(/^Find a ticket or run a command/); // F14
    expect(labels.slice(1)).toEqual(["Resync (R)", "Update to v1.3.0", "Configure"]);
    await expect(header.getByRole("button", { name: "Update to v1.3.0" })).toHaveAttribute("title", "Cuisle v1.3.0 is available");
    await expect(header).toHaveScreenshot(`header-update-${w}x${h}.png`, SHOT);
  });
}

test("update dialog 1920x1080", async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await seed(page, NOW_1421, UPDATE_AVAILABLE);
  await stubMedia(page);
  await openApp(page);
  await page.getByRole("button", { name: "Update to v1.3.0" }).click();
  const dialog = page.getByRole("dialog", { name: "Update Cuisle" });
  await expect(dialog).toBeVisible();
  await expect(page).toHaveScreenshot("update-dialog-1920x1080.png", SHOT);
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
});

test("configure display: updates row 1920x1080", async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await seed(page, NOW_1421, UPDATE_AVAILABLE);
  await stubMedia(page);
  await openApp(page);
  await page.keyboard.press(",");
  const display = page.locator("section.disp");
  await display.waitFor();
  await expect(display.getByRole("switch", { name: "Check for updates daily" })).toHaveAttribute("aria-checked", "true");
  await expect(display.getByRole("button", { name: "Check now" })).toBeVisible();
  await expect(display).toHaveScreenshot("display-updates-1920x1080.png", SHOT);
});

test("no Update button in the default (no channel) mock build", async ({ page }) => {
  await seed(page, NOW_1421);
  await stubMedia(page);
  await openApp(page);
  await expect(page.getByRole("button", { name: /^Update to/ })).toHaveCount(0);
});
