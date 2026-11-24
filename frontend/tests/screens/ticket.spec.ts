import { test, expect } from "@playwright/test";
import { NOW_1421, NOW_1429, TIMEZONE, jumpTo, openApp, openTicket, seed, stubMedia } from "../ticketHelpers";

// Ticket modal screenshots (testing §5, ux-spec §14): the four mockup sizes, the edit states and the lightbox, in mock
// mode, compared by eye with design/mockups/ticket-*.png. The data differs (MOB-1421 here is the Go golden over the fake
// Jira, MOB-1429 is hand-built for the edit states); layout, typography and colour must match. Baselines are our own.

const SIZES = [
  { w: 1920, h: 1080 },
  { w: 1440, h: 900 },
  { w: 1280, h: 720 },
  { w: 1024, h: 640 },
] as const;

const SHOT = { maxDiffPixelRatio: 0.02, animations: "disabled" } as const;

test.use({ timezoneId: TIMEZONE });

for (const { w, h } of SIZES) {
  test.describe(`ticket ${w}x${h}`, () => {
    test.use({ viewport: { width: w, height: h } });

    test("MOB-1421, every ADF node", async ({ page }) => {
      await seed(page, NOW_1421);
      await stubMedia(page);
      await openApp(page);
      await openTicket(page, "MOB-1421");
      await expect(page).toHaveScreenshot(`ticket-${w}x${h}.png`, SHOT);
    });
  });
}

test.describe("ticket edit states and lightbox, 1920x1080", () => {
  test.use({ viewport: { width: 1920, height: 1080 } });

  test("MOB-1429: status list open, a sending comment and a failed one", async ({ page }) => {
    await seed(page, NOW_1429, {
      failures: { "ticket.addComment": { code: "offline", message: "couldn't reach Jira (offline)", times: 1 } },
      holdMs: { "ticket.addComment": 1500 },
    });
    await stubMedia(page);
    await openApp(page);
    await openTicket(page, "MOB-1429");
    const dialog = page.getByRole("dialog", { name: "Ticket MOB-1429" });
    await jumpTo(page, /comments/i);
    const composer = dialog.getByRole("textbox", { name: "Comment" });
    // The first send fails (offline) after the hold; the second is still in flight when the shot is taken.
    await composer.fill("Unblocked once #421 merges. I'll re-test on the Android beta.");
    await composer.press("Control+Enter");
    await expect(dialog.getByText(/Not sent/)).toBeVisible({ timeout: 5000 });
    await composer.fill("Raised in standup: moving this to the top of Iris's list once PLAT-444 lands.");
    await composer.press("Control+Enter");
    await expect(dialog.getByText(/sending/)).toBeVisible();
    await composer.press("Escape");
    await jumpTo(page, /comments/i);
    await page.keyboard.press("s");
    await expect(page.getByRole("listbox")).toBeVisible();
    await expect(page).toHaveScreenshot("ticket-edit-states-1920x1080.png", SHOT);
  });

  test("lightbox on the first image", async ({ page }) => {
    await seed(page, NOW_1421);
    await stubMedia(page);
    await openApp(page);
    await openTicket(page, "MOB-1421");
    const dialog = page.getByRole("dialog", { name: "Ticket MOB-1421" });
    await dialog.locator(".att-tile").first().click();
    const box = page.getByRole("dialog", { name: "Image viewer" });
    await expect(box).toBeVisible();
    await expect(box.locator("img").first()).toBeVisible();
    await page.waitForFunction(() => Array.from(document.images).every((i) => i.complete));
    await expect(page).toHaveScreenshot("ticket-lightbox-1920x1080.png", SHOT);
  });
});

// Header geometry (ux-spec §14.3): the Monday labels never touch the pulse marks, the key never wraps, there is no
// stepper, parent crumb or text "Open in Jira" label. These measure the real layout, which jsdom cannot.
for (const { w, h } of SIZES) {
  test.describe(`ticket header geometry ${w}x${h}`, () => {
    test.use({ viewport: { width: w, height: h } });

    for (const [key, now] of [
      ["MOB-1421", NOW_1421],
      ["MOB-1429", NOW_1429],
    ] as const) {
      test(`${key}: labels clear the marks, key on one line, no stepper or crumb`, async ({ page }) => {
        await seed(page, now);
        await stubMedia(page);
        await openApp(page);
        await openTicket(page, key);
        const dialog = page.getByRole("dialog", { name: `Ticket ${key}` });
        const head = dialog.locator(".th");
        await expect(head).toBeVisible();

        const m = await head.evaluate((el) => {
          const box = (e: Element) => {
            const r = e.getBoundingClientRect();
            return { l: r.left, r: r.right, t: r.top, b: r.bottom };
          };
          const keyEl = el.querySelector(".th-key") as HTMLElement;
          return {
            labels: Array.from(el.querySelectorAll(".th-monday")).map(box),
            marks: Array.from(el.querySelectorAll(".pulse-mark")).map(box),
            keyH: keyEl.getBoundingClientRect().height,
            keyFont: parseFloat(getComputedStyle(keyEl).fontSize),
            text: (el.textContent ?? "").replace(/\s+/g, " "),
          };
        });
        const short = h < 800;
        if (!short) expect(m.labels.length).toBeGreaterThan(0);
        for (const l of m.labels) {
          for (const k of m.marks) {
            const hit = l.l < k.r && k.l < l.r && l.t < k.b && k.t < l.b;
            expect(hit, `label ${JSON.stringify(l)} overlaps mark ${JSON.stringify(k)}`).toBe(false);
          }
        }
        expect(m.keyH).toBeLessThan(m.keyFont * 1.8); // one line
        expect(m.text).not.toMatch(/\d+ \/ \d+/);
        expect(m.text).not.toMatch(/in .*MOB-1200/);
        await expect(dialog.getByRole("button", { name: "Previous ticket" })).toHaveCount(0);
        await expect(dialog.getByRole("button", { name: "Open in Jira" })).toHaveText("");
      });
    }
  });
}
