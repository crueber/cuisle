import { test, expect, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { waitForStandupScreen } from "../perf/measure";

const BOOT_FIXTURE = fileURLToPath(new URL("../../src/api/fixtures/app.json", import.meta.url));

// testing §5 and ux-spec §10: zero axe violations on the person view, the whole board, Configure (all sections
// expanded) and Connect, in both themes. Rules are not disabled or filtered: axe runs with its defaults.

const AXE_PATH = fileURLToPath(new URL("../../node_modules/axe-core/axe.min.js", import.meta.url));

type Theme = "light" | "dark";
const THEMES: readonly Theme[] = ["light", "dark"];

interface AxeNode {
  target: string[];
}
interface AxeViolation {
  id: string;
  impact?: string | null;
  help: string;
  nodes: AxeNode[];
}
interface AxeWindow {
  axe: { run: (context?: unknown, options?: unknown) => Promise<{ violations: AxeViolation[] }> };
}

async function setTheme(page: Page, theme: Theme): Promise<void> {
  await page.evaluate((t) => {
    document.documentElement.dataset.theme = t;
  }, theme);
  // Let the theme apply and the layout settle before scanning.
  await page.evaluate(() => new Promise<void>((r) => requestAnimationFrame(() => r())));
}

async function scanAndExpect(page: Page, label: string): Promise<void> {
  await page.addScriptTag({ path: AXE_PATH });
  const violations = await page.evaluate(async () => {
    const result = await (window as unknown as AxeWindow).axe.run();
    return result.violations.map((v) => ({
      id: v.id,
      impact: v.impact ?? null,
      help: v.help,
      nodes: v.nodes.map((n) => n.target.join(" ")),
    }));
  });
  const report = violations.map(
    (v) => `${v.id} (${v.impact ?? "n/a"}): ${v.help}\n    ${v.nodes.join("\n    ")}`,
  );
  expect(violations, `${label}: axe violations\n${report.join("\n")}`).toEqual([]);
}

for (const theme of THEMES) {
  test.describe(`axe, ${theme} theme`, () => {
    test("person view", async ({ page }) => {
      await waitForStandupScreen(page);
      await setTheme(page, theme);
      await scanAndExpect(page, `person view (${theme})`);
    });

    test("whole board", async ({ page }) => {
      await waitForStandupScreen(page);
      await page.keyboard.press("b");
      await expect(page.locator(".sf-screen").first()).toBeVisible();
      await setTheme(page, theme);
      await scanAndExpect(page, `whole board (${theme})`);
    });

    test("configure, all sections expanded", async ({ page }) => {
      await waitForStandupScreen(page);
      await page.keyboard.press(",");
      // Every section is wrapped in .cfg-anchor (ConfigureScreen ORDER: 11 sections). All must be mounted.
      const anchors = page.locator(".cfg-anchor");
      await expect(anchors.first()).toBeVisible();
      await page.evaluate(() => {
        document.querySelectorAll<HTMLDetailsElement>("details").forEach((d) => {
          d.open = true;
        });
      });
      const count = await anchors.count();
      expect(count, "all eleven configure sections are mounted").toBe(11);
      for (let i = 0; i < count; i++) {
        await anchors.nth(i).scrollIntoViewIfNeeded();
      }
      await setTheme(page, theme);
      await scanAndExpect(page, `configure (${theme}), ${count} sections`);
    });

    test("connect", async ({ page }) => {
      // First-run route (route.ts initialRoute): no standups and connection "missing" opens Connect. The boot object
      // is seeded from the mock fixture via window.__CUISLE_BOOT__ (main.tsx prefers it), so no src change is needed.
      const boot = JSON.parse(readFileSync(BOOT_FIXTURE, "utf8")) as Record<string, unknown> & {
        connection: Record<string, unknown>;
      };
      boot.standups = [];
      boot.sync = [];
      delete boot.view;
      boot.connection = { ...boot.connection, state: "missing" };
      await page.addInitScript((b) => {
        (window as unknown as { __CUISLE_BOOT__: unknown }).__CUISLE_BOOT__ = b;
      }, boot);
      await page.goto("/");
      await expect(page.locator(".connect-wordmark")).toBeVisible();
      await setTheme(page, theme);
      await scanAndExpect(page, `connect (${theme})`);
    });
  });
}
