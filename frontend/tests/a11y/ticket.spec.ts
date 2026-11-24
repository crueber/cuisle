import { test, expect, type Page } from "@playwright/test";
import { fileURLToPath } from "node:url";
import { NOW_1421, NOW_1429, TIMEZONE, jumpTo, openApp, openTicket, seed, stubMedia } from "../ticketHelpers";

// testing §5: zero axe violations on the ticket modal, its Status combobox and the lightbox, in both themes. Axe runs with
// its defaults (no rule is disabled or filtered), over the whole page: the modal sits over the standup screen, so the
// screen behind is scanned too.

const AXE_PATH = fileURLToPath(new URL("../../node_modules/axe-core/axe.min.js", import.meta.url));

type Theme = "light" | "dark";
const THEMES: readonly Theme[] = ["light", "dark"];

interface AxeViolation {
  id: string;
  impact?: string | null;
  help: string;
  nodes: { target: string[] }[];
}
interface AxeWindow {
  axe: { run: (context?: unknown, options?: unknown) => Promise<{ violations: AxeViolation[] }> };
}

async function setTheme(page: Page, theme: Theme): Promise<void> {
  await page.evaluate((t) => {
    document.documentElement.dataset.theme = t;
  }, theme);
  await page.evaluate(() => new Promise<void>((r) => requestAnimationFrame(() => r())));
}

async function scanAndExpect(page: Page, label: string): Promise<void> {
  await page.addScriptTag({ path: AXE_PATH });
  const violations = await page.evaluate(async () => {
    const result = await (window as unknown as AxeWindow).axe.run();
    return result.violations.map((v) => ({ id: v.id, impact: v.impact ?? null, help: v.help, nodes: v.nodes.map((n) => n.target.join(" ")) }));
  });
  const report = violations.map((v) => `${v.id} (${v.impact ?? "n/a"}): ${v.help}\n    ${v.nodes.join("\n    ")}`);
  expect(violations, `${label}: axe violations\n${report.join("\n")}`).toEqual([]);
}

test.use({ timezoneId: TIMEZONE, viewport: { width: 1920, height: 1080 } });

for (const theme of THEMES) {
  test.describe(`axe, ticket modal, ${theme} theme`, () => {
    test("MOB-1421 (every ADF node, every field kind)", async ({ page }) => {
      await seed(page, NOW_1421);
      await stubMedia(page);
      await openApp(page);
      await setTheme(page, theme); // before opening: the textColor clamp reads the theme when the modal renders
      await openTicket(page, "MOB-1421");
      await scanAndExpect(page, `ticket modal (${theme})`);
    });

    test("MOB-1429: comments, failed comment, open status combobox", async ({ page }) => {
      await seed(page, NOW_1429, { failures: { "ticket.addComment": { code: "offline", message: "couldn't reach Jira (offline)", times: 1 } } });
      await stubMedia(page);
      await openApp(page);
      await setTheme(page, theme); // before opening: the textColor clamp reads the theme when the modal renders
      await openTicket(page, "MOB-1429");
      const dialog = page.getByRole("dialog", { name: "Ticket MOB-1429" });
      await jumpTo(page, /comments/i);
      const composer = dialog.getByRole("textbox", { name: "Comment" });
      await composer.fill("Raised in standup.");
      await composer.press("Control+Enter");
      await expect(dialog.getByText(/Not sent/)).toBeVisible();
      await composer.press("Escape");
      await page.keyboard.press("s");
      await expect(page.getByRole("listbox")).toBeVisible();
      await scanAndExpect(page, `ticket modal with combobox and failed comment (${theme})`);
    });

    test("lightbox", async ({ page }) => {
      await seed(page, NOW_1421);
      await stubMedia(page);
      await openApp(page);
      await setTheme(page, theme); // before opening: the textColor clamp reads the theme when the modal renders
      await openTicket(page, "MOB-1421");
      await page.getByRole("dialog", { name: "Ticket MOB-1421" }).locator(".att-tile").first().click();
      await expect(page.getByRole("dialog", { name: "Image viewer" })).toBeVisible();
      await page.waitForFunction(() => Array.from(document.images).every((i) => i.complete));
      await scanAndExpect(page, `lightbox (${theme})`);
    });
  });
}
