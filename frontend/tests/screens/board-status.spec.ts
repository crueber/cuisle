// F13: whole board grouped by status (ux-spec §6.5). Screenshots against design/mockups/board-status-*.png and
// board-assignee-toggle-1920x1080.png, plus the behaviour that needs a real layout engine: the G key, the switch, the
// per-standup choice surviving a standup switch, and the more bar naming sections (the J/K order is unit-tested in state/ticketSteps.status.test.ts: the mock serves only two ticket bodies).
// Mock mode against the Go golden bootstrap (tests/fixtures/bootstrap_mob.json); time frozen like parity.spec.ts.
import { test, expect, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const BOOT_FILE = fileURLToPath(new URL("../fixtures/bootstrap_mob.json", import.meta.url));
interface BootFixture {
  settings: Record<string, string>;
  view: { generatedAt: number };
}
const boot = JSON.parse(readFileSync(BOOT_FILE, "utf8")) as BootFixture;
const SHOT = { maxDiffPixelRatio: 0.02, animations: "disabled" } as const;
const FROZEN_NOW = boot.view.generatedAt + 60_000;

async function open(page: Page, payload: BootFixture = boot): Promise<void> {
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
  await page.keyboard.press("b");
  await page.locator(".board-row-key").first().waitFor();
}

const statusBoard = (page: Page) => page.locator(".board-sec");

for (const { w, h } of [
  { w: 1920, h: 1080 },
  { w: 1440, h: 900 },
  { w: 1280, h: 720 },
  { w: 1024, h: 640 },
] as const) {
  test.describe(`${w}x${h}`, () => {
    test.use({ viewport: { width: w, height: h } });

    test("whole board grouped by status", async ({ page }) => {
      await open(page);
      await page.keyboard.press("g");
      await expect(statusBoard(page).first()).toBeVisible();
      await expect(page).toHaveScreenshot(`board-status-${w}x${h}.png`, SHOT);
    });
  });
}

test.describe("1920x1080 assignee toggle", () => {
  test.use({ viewport: { width: 1920, height: 1080 } });

  test("the switch in the title row (assignee mode)", async ({ page }) => {
    await open(page);
    await expect(page.getByRole("button", { name: "Assignee", pressed: true })).toBeVisible();
    await expect(page).toHaveScreenshot("board-assignee-toggle-1920x1080.png", SHOT);
  });
});

test.describe("dark theme at 1920x1080", () => {
  test.use({ viewport: { width: 1920, height: 1080 } });

  test("whole board grouped by status (dark)", async ({ page }) => {
    await open(page, { ...boot, settings: { ...boot.settings, theme: "dark" } });
    await page.keyboard.press("g");
    await expect(statusBoard(page).first()).toBeVisible();
    await expect(page).toHaveScreenshot("board-status-dark-1920x1080.png", SHOT);
  });
});

test.describe("behaviour", () => {
  test.use({ viewport: { width: 1920, height: 1080 } });

  test("G toggles the grouping, the switch does too, and a person view ignores G", async ({ page }) => {
    await open(page);
    await expect(statusBoard(page)).toHaveCount(0);
    await page.keyboard.press("g");
    await expect(page.getByRole("button", { name: "Status", pressed: true })).toBeVisible();
    await expect(statusBoard(page).first()).toBeVisible();
    await page.keyboard.press("G");
    await expect(page.getByRole("button", { name: "Assignee", pressed: true })).toBeVisible();
    await page.getByRole("button", { name: "Status" }).click();
    await expect(statusBoard(page).first()).toBeVisible();
    // On a person view G does nothing; going back to the board still shows the status grouping.
    await page.keyboard.press("ArrowDown");
    await page.locator(".person-view .ticket-key").first().waitFor();
    await page.keyboard.press("g");
    await expect(page.locator(".person-view")).toBeVisible();
    await page.keyboard.press("b");
    await expect(statusBoard(page).first()).toBeVisible();
  });

  test("the status sections follow the mockup: QA, In Review, In Progress | To Do, Done, Backlog", async ({ page }) => {
    await open(page);
    await page.keyboard.press("g");
    const names = (col: string) => page.locator(`[data-column="${col}"] .board-sec-name, [data-column="${col}"] .board-list-title`).allTextContents();
    expect(await names("1")).toEqual(["QA", "In Review", "In Progress"]);
    expect(await names("2")).toEqual(["To Do", "Done", "Backlog"]);
    // a header carries its lane swatch and the alert counts
    await expect(page.locator('[data-sec-head="qa"]')).toContainText("7 tickets");
    await expect(page.locator('[data-sec-head="qa"]')).toContainText("over limit");
    // an unassigned row has the dashed box
    await expect(page.locator(".board-assignee--none").first()).toHaveText("··");
  });

  test("the more bar names sections at a short window; nothing is hidden", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 720 });
    await open(page);
    await page.keyboard.press("g");
    const bar = page.locator(".more-bar");
    await expect(bar).toBeVisible();
    await expect(bar).toContainText(/In Progress \(\d+\)/);
    await expect(bar).toContainText("below");
    // every active ticket is in the DOM
    expect(await page.locator(".board-sec .board-row").count()).toBe(40);
  });
});
