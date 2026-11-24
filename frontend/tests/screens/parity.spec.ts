// Source of the bootstrap payload: tests/fixtures/bootstrap_mob.json, a copy of internal/app/testdata/
// bootstrap_mob.golden.json (see tests/fixtures/README.md for the regeneration steps).
import { test, expect, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

// Screenshot parity (testing §5, ux-spec §13): person, board and configure at the four mockup sizes, plus a dark
// variant at 1920×1080. Mock mode (`vite build --mode mock` + preview). Baselines carry Playwright's platform suffix
// (see tests/screens/README.md).

const BOOT_FILE = fileURLToPath(new URL("../fixtures/bootstrap_mob.json", import.meta.url));

interface BootFixture {
  settings: Record<string, string>;
  view: { generatedAt: number };
}

const boot = JSON.parse(readFileSync(BOOT_FILE, "utf8")) as BootFixture;

const SIZES = [
  { w: 1920, h: 1080 },
  { w: 1440, h: 900 },
  { w: 1280, h: 720 },
  { w: 1024, h: 640 },
] as const;

const SHOT = { maxDiffPixelRatio: 0.02, animations: "disabled" } as const;

/** Fixed "now" for the header's "synced N min ago" text: the fixture's generatedAt plus one minute. */
const FROZEN_NOW = boot.view.generatedAt + 60_000;

/**
 * Seeds the boot payload and a frozen Date.now, loads the app (it opens on the whole board, since the golden has no
 * remembered person), and waits for the screen and fonts. Each test waits for its own view's content.
 */
async function open(page: Page, payload: BootFixture): Promise<void> {
  await page.addInitScript(
    ([p, now]) => {
      (window as unknown as { __CUISLE_BOOT__: unknown }).__CUISLE_BOOT__ = p;
      Date.now = () => now;
    },
    [payload, FROZEN_NOW] as const,
  );
  await page.goto("/");
  await page.locator(".sf-screen").waitFor();
  await page.evaluate(() => document.fonts.ready);
}

for (const { w, h } of SIZES) {
  test.describe(`${w}x${h}`, () => {
    test.use({ viewport: { width: w, height: h } });

    test("person view (first speaker)", async ({ page }) => {
      await open(page, boot);
      await page.keyboard.press("ArrowDown");
      await expect(page.locator(".person-view .ticket-key").first()).toBeVisible();
      await expect(page).toHaveScreenshot(`person-${w}x${h}.png`, SHOT);
    });

    test("whole board", async ({ page }) => {
      await open(page, boot);
      await page.keyboard.press("b");
      await expect(page.locator(".board-row-key").first()).toBeVisible();
      await expect(page).toHaveScreenshot(`board-${w}x${h}.png`, SHOT);
    });

    test("configure", async ({ page }) => {
      await open(page, boot);
      await page.keyboard.press(",");
      await expect(page.locator(".cfg-screen")).toBeVisible();
      await page.locator('.cfg-grid[data-balanced="true"]').waitFor();
      await expect(page).toHaveScreenshot(`configure-${w}x${h}.png`, { ...SHOT, fullPage: false });
    });
  });
}

test.describe("dark theme at 1920x1080", () => {
  test.use({ viewport: { width: 1920, height: 1080 } });
  const dark: BootFixture = { ...boot, settings: { ...boot.settings, theme: "dark" } };

  test("person view (dark)", async ({ page }) => {
    await open(page, dark);
    await page.keyboard.press("ArrowDown");
    await expect(page.locator(".person-view .ticket-key").first()).toBeVisible();
    await expect(page).toHaveScreenshot("person-dark-1920x1080.png", SHOT);
  });

  test("whole board (dark)", async ({ page }) => {
    await open(page, dark);
    await page.keyboard.press("b");
    await expect(page.locator(".board-row-key").first()).toBeVisible();
    await expect(page).toHaveScreenshot("board-dark-1920x1080.png", SHOT);
  });
});
