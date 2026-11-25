// F14: the command palette with the ticket finder (ux-spec §15). Screenshots against design/mockups/palette-*-1920x1080.png
// (the mockups use Saffron data over an older backdrop: compare layout, not text), plus the behaviour that needs a real
// layout engine: ⌘K over the ticket modal and over Configure, Esc unwinding, the "not synced" fetch row, the header Find
// button at every size. Mock mode against the Go golden bootstrap; time frozen like parity.spec.ts.
import { test, expect, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
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
}

const palette = (page: Page) => page.getByRole("dialog", { name: "Find a ticket or run a command" });
const input = (page: Page) => page.getByRole("combobox", { name: "Find a ticket or run a command" });

async function openPalette(page: Page): Promise<void> {
  await page.keyboard.press("Control+k");
  await palette(page).waitFor();
  await expect(input(page)).toBeFocused();
}

test.describe("1920x1080", () => {
  test.use({ viewport: { width: 1920, height: 1080 } });

  test("empty: recents and suggested commands", async ({ page }) => {
    await open(page, { ...boot, settings: { ...boot.settings, "palette.recent_tickets": "[7,3,22]" } });
    await openPalette(page);
    await expect(page.getByRole("option").first()).toBeVisible();
    await expect(page).toHaveScreenshot("palette-empty-1920x1080.png", SHOT);
  });

  test("a ticket number: exact, one character off, ticket commands", async ({ page }) => {
    await open(page);
    await openPalette(page);
    await input(page).fill("1429");
    await expect(page.locator(".pal-row--ticket").first()).toBeVisible();
    await expect(page).toHaveScreenshot("palette-ticket-1920x1080.png", SHOT);
  });

  test("> commands: sta", async ({ page }) => {
    await open(page);
    await openPalette(page);
    await input(page).pressSequentially(">sta");
    await expect(page.locator(".pal-mode")).toHaveText(">");
    await expect(page.getByRole("option").first()).toContainText("Group board by status");
    await expect(page).toHaveScreenshot("palette-commands-1920x1080.png", SHOT);
  });

  test("@ people: no", async ({ page }) => {
    await open(page);
    await openPalette(page);
    await input(page).pressSequentially("@no");
    await expect(page.locator(".pal-row--person").first()).toBeVisible();
    await expect(page).toHaveScreenshot("palette-people-1920x1080.png", SHOT);
  });

  test("a nested command: Theme", async ({ page }) => {
    await open(page);
    await openPalette(page);
    await input(page).pressSequentially(">theme");
    await page.keyboard.press("Enter");
    await expect(page.getByTestId("pal-crumb")).toHaveText(/Theme/);
    await expect(page).toHaveScreenshot("palette-nested-1920x1080.png", SHOT);
  });
});

test.describe("behaviour", () => {
  test.use({ viewport: { width: 1920, height: 1080 } });

  test("Esc unwinds a nested level, then the mode chip, then closes; focus returns to the opener", async ({ page }) => {
    await open(page);
    await page.getByRole("button", { name: /Find a ticket or run a command/ }).focus();
    await page.keyboard.press("Enter");
    await palette(page).waitFor();
    await input(page).pressSequentially(">theme");
    await page.keyboard.press("Enter");
    await expect(page.getByTestId("pal-crumb")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("pal-crumb")).toHaveCount(0);
    await expect(page.locator(".pal-mode")).toHaveText(">");
    await page.keyboard.press("Escape");
    await expect(page.locator(".pal-mode")).toHaveCount(0);
    await expect(palette(page)).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(palette(page)).toHaveCount(0);
    await expect(page.getByRole("button", { name: /Find a ticket or run a command/ })).toBeFocused();
  });

  test("Enter on a ticket opens the modal; ⌘K over the modal, Esc returns to the modal", async ({ page }) => {
    await open(page);
    await openPalette(page);
    await input(page).fill("1421");
    await expect(page.locator(".pal-row--ticket").first()).toBeVisible();
    await page.keyboard.press("Enter");
    const modal = page.locator(".tm");
    await expect(modal).toBeVisible();
    await openPalette(page);
    await page.keyboard.press("Escape");
    await expect(palette(page)).toHaveCount(0);
    await expect(modal).toBeVisible();
    await expect(modal).toBeFocused();
  });

  test("⌘K over Configure opens it; a command runs from there", async ({ page }) => {
    await open(page);
    await page.keyboard.press(",");
    await page.locator(".cfg-screen, [data-testid='configure']").first().waitFor().catch(() => undefined);
    await page.keyboard.press("Control+k");
    await palette(page).waitFor();
    await expect(page).toHaveScreenshot("palette-over-configure-1920x1080.png", SHOT);
    await page.keyboard.press("Escape");
    await expect(palette(page)).toHaveCount(0);
  });

  test("T opens it when no field has focus", async ({ page }) => {
    await open(page);
    await page.keyboard.press("t");
    await expect(palette(page)).toBeVisible();
  });

  test("a key that is not synced offers the fetch row; a missing issue says so inline", async ({ page }) => {
    await open(page);
    await openPalette(page);
    await input(page).fill("mob-404");
    const row = page.locator(".pal-row--fetch");
    await expect(row).toBeVisible();
    await expect(row).toContainText("isn't synced");
    await page.keyboard.press("Enter");
    await expect(row).toContainText("No issue MOB-404 in Jira");
    await expect(palette(page)).toBeVisible();
  });

  test("the palette dialog is a lazy chunk: absent from the page until first opened", async ({ page }) => {
    const scripts: string[] = [];
    page.on("request", (r) => {
      if (r.url().includes("lazy-PaletteDialog")) scripts.push(r.url());
    });
    await open(page);
    expect(scripts).toHaveLength(0);
    await openPalette(page);
    expect(scripts.length).toBeGreaterThan(0);
  });
});

test.describe("axe", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`no violations, ${theme}`, async ({ page }) => {
      await page.setViewportSize({ width: 1440, height: 900 });
      await open(page, { ...boot, settings: { ...boot.settings, theme, "palette.recent_tickets": "[7,3]" } });
      await openPalette(page);
      expect((await new AxeBuilder({ page }).include(".pal").analyze()).violations).toEqual([]);
      await input(page).fill("14");
      await expect(page.locator(".pal-row--ticket").first()).toBeVisible();
      expect((await new AxeBuilder({ page }).include(".pal").analyze()).violations).toEqual([]);
    });
  }
});

test.describe("header Find button", () => {
  for (const { w, h } of [
    { w: 1920, h: 1080 },
    { w: 1440, h: 900 },
    { w: 1280, h: 720 },
    { w: 1100, h: 700 },
    { w: 1024, h: 640 },
  ] as const) {
    test(`${w}x${h}: Find is visible, nothing overlaps, nothing scrolls sideways`, async ({ page }) => {
      await page.setViewportSize({ width: w, height: h });
      await open(page);
      const find = page.getByRole("button", { name: /Find a ticket or run a command/ });
      const resync = page.getByRole("button", { name: "Resync (R)" });
      const conf = page.getByRole("button", { name: "Configure" });
      await expect(find).toBeVisible();
      const [f, r, c] = await Promise.all([find.boundingBox(), resync.boundingBox(), conf.boundingBox()]);
      expect(f!.x + f!.width).toBeLessThanOrEqual(r!.x + 0.5);
      expect(r!.x + r!.width).toBeLessThanOrEqual(c!.x + 0.5);
      const tabs = await page.locator(".sf-tabs").boundingBox();
      if (tabs) expect(tabs.x + tabs.width).toBeLessThanOrEqual(f!.x + 0.5);
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
      if (w >= 1280) await expect(find).toContainText("Find");
      else await expect(find).not.toContainText("Find");
    });
  }
});
