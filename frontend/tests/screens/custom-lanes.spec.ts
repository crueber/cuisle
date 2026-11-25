import { test, expect, type Page } from "@playwright/test";
import { NOW_1421, TIMEZONE, openApp, seed, stubMedia } from "../ticketHelpers";

// Lanes the user names (ux-spec §9.4 Status mapping): typing a new name in a column's lane box and choosing
// "Create lane …" adds a working lane; the person view then has a section for it, coloured from the extended palette.
// Screenshots: Configure › Status mapping with the new lane, and Iris's person view with its section. The layout rule
// (nothing scrolls sideways) is checked with the most lanes a standup can have, each with a 40-character name.

test.use({ timezoneId: TIMEZONE });

async function openConfigure(page: Page): Promise<void> {
  await page.keyboard.press(",");
  await page.locator(".cfg-screen").waitFor();
  await page.locator("#sec-mapping .cfg-section").waitFor();
  await page.locator('.cfg-grid[data-balanced="true"]').waitFor();
}

async function createLane(page: Page, column: string, name: string): Promise<void> {
  const box = page.getByRole("combobox", { name: `Lane for ${column}` });
  // Typed like a person (select the current lane, type over it); WebKit's fill() at narrow widths raced the field's sync.
  await box.click();
  await box.press("ControlOrMeta+a");
  await page.keyboard.type(name);
  // A name no lane has: the create option is the only option and already active, so Enter creates it.
  await expect(page.getByRole("option", { name: `Create lane “${name}”` })).toBeVisible();
  await box.press("Enter");
  await expect(box).toHaveValue(name);
}

async function backToStandup(page: Page): Promise<void> {
  await page.keyboard.press("Escape");
  await page.locator(".sf-screen .ticket-key").first().waitFor();
}

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

test.describe("custom lanes", () => {
  test.use({ viewport: { width: 1920, height: 1080 } });

  test.beforeEach(async ({ page }) => {
    await seed(page, NOW_1421);
    await stubMedia(page);
    await openApp(page);
  });

  test("Configure and the person view show a lane the user named", async ({ page }) => {
    await openConfigure(page);
    await createLane(page, "Review", "Peer Review");
    const mapping = page.locator("#sec-mapping");
    await expect(mapping.locator('.cfg-swatch[data-lane="c1"][data-hue="1"]').first()).toBeVisible();
    await expect(page.getByLabel("Name of the Peer Review lane")).toHaveValue("Peer Review");
    await page.waitForLoadState("networkidle");
    await mapping.scrollIntoViewIfNeeded();
    await expect(mapping).toHaveScreenshot("configure-custom-lane.png", { animations: "disabled", maxDiffPixelRatio: 0.02 });

    await backToStandup(page);
    const section = page.getByRole("group", { name: "Peer Review" });
    await expect(section).toBeVisible();
    await expect(section.locator('.pulse-seg[data-lane="c1"][data-hue="1"]').first()).toBeVisible();
    await expect(page.locator('.legend .legend-swatch[data-lane="c1"]')).toBeVisible();
    await page.waitForLoadState("networkidle");
    await expect(page).toHaveScreenshot("person-custom-lane.png", { animations: "disabled", maxDiffPixelRatio: 0.02 });

    // Whole board grouped by status (F13, ux-spec §6.5): the custom lane is its own section, swatch in its colour.
    await page.keyboard.press("b");
    await page.locator(".board-row-key").first().waitFor();
    await page.keyboard.press("g");
    const head = page.locator('.board-sec-head[data-sec-head="c1"]');
    await expect(head).toBeVisible();
    await expect(head.locator(".board-sec-name")).toHaveText("Peer Review");
    await expect(head.locator('.board-sec-swatch[data-hue="1"]')).toBeVisible();
    await page.waitForLoadState("networkidle");
    await expect(page).toHaveScreenshot("board-status-custom-lane.png", { animations: "disabled", maxDiffPixelRatio: 0.02 });
  });
});

for (const [w, h] of [[1920, 1080], [1280, 720], [1024, 640]] as const) {
  test(`${w}x${h}: twelve long-named lanes never scroll sideways`, async ({ page }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width: w, height: h });
    await seed(page, NOW_1421);
    await stubMedia(page);
    await openApp(page);
    await openConfigure(page);
    const columns = ["To Do", "In Progress", "Review", "QA"];
    for (let i = 1; i <= 12; i++) {
      const name = `Lane ${String(i).padStart(2, "0")} ${"with a very long name".padEnd(31, "x")}`; // 40 characters
      await createLane(page, columns[i % columns.length]!, name);
    }
    await page.waitForLoadState("networkidle");
    expect(await sidewaysScrollers(page)).toEqual([]);

    await backToStandup(page);
    expect(await sidewaysScrollers(page)).toEqual([]);
    // A legend this long folds into the "? Key" button, and long section names stay inside the side-heading gutter.
    await expect(page.locator(".person-view .legend-key")).toBeVisible();
    const overlaps = await page.evaluate(() =>
      Array.from(document.querySelectorAll<HTMLElement>(".person-group.is-side")).flatMap((g) => {
        const head = g.querySelector(".person-group-head")?.getBoundingClientRect();
        const rows = g.querySelector(".person-group-rows")?.getBoundingClientRect();
        const h2 = g.querySelector(".person-group-head h2") as HTMLElement | null;
        return head && rows && h2 && h2.scrollWidth > h2.clientWidth + 1 ? [h2.textContent] : head && rows && head.right > rows.left + 1 ? [h2?.textContent] : [];
      }),
    );
    expect(overlaps).toEqual([]);
    await page.keyboard.press("b");
    await page.locator(".board-row-key").first().waitFor();
    expect(await sidewaysScrollers(page)).toEqual([]);
  });
}

// Regressions found in WebKit (the app's webview): run with --browser=webkit when touching the Combobox.
for (const [w, h] of [[1920, 1080], [1280, 720]] as const) {
  test.describe(`${w}x${h} lane combobox`, () => {
    test.use({ viewport: { width: w, height: h } });
    test.beforeEach(async ({ page }) => {
      await seed(page, NOW_1421);
      await stubMedia(page);
      await openApp(page);
      await openConfigure(page);
    });

    test("clicking Create lane creates it", async ({ page }) => {
      const box = page.getByRole("combobox", { name: "Lane for Review" });
      await box.click();
      await box.press("ControlOrMeta+a");
      await page.keyboard.type("Peer Review");
      await page.getByRole("option", { name: "Create lane “Peer Review”" }).click();
      await expect(box).toHaveValue("Peer Review");
      await expect(page.locator('.cfg-swatch[data-lane="c1"][data-hue="1"]').first()).toBeVisible();
    });

    // A single input event (paste, fill) right after the form loads must not be overwritten by the field's deferred
    // sync to its committed label (Combobox typedRef).
    test("one input event right after load filters the list", async ({ page }) => {
      const box = page.getByRole("combobox", { name: "Lane for In Progress" });
      await box.fill("QA");
      await expect(page.getByRole("option")).toHaveText(["QA"]);
      await expect(box).toHaveValue("QA");
    });
  });
}
