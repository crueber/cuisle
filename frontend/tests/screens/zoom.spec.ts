// F15 zoom (ux-spec §16): screenshots of the board at 80 / 100 / 150 / 175 % and the person view at 150 % on a 1920 x 1080
// window (compare with design/mockups/zoom-*-1920x1080.png by looking: the mockups are a guide, the spec wins), the
// header never zooming, ⌘/Ctrl with + = − 0 never page-zooming, scroll anchoring, and the no-sideways-scroll rule
// (AGENTS invariant 16) at every one of the nine zoom levels. Mock mode against the golden bootstrap payload.
import { test, expect, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { NOW_1421, TIMEZONE, openApp, openTicket, seed, stubMedia } from "../ticketHelpers";

interface BootFixture {
  settings: Record<string, string>;
  view: { generatedAt: number };
}
const boot = JSON.parse(readFileSync(fileURLToPath(new URL("../fixtures/bootstrap_mob.json", import.meta.url)), "utf8")) as BootFixture;
const FROZEN_NOW = boot.view.generatedAt + 60_000;
const SHOT = { maxDiffPixelRatio: 0.02, animations: "disabled" } as const;
const STEPS = [70, 80, 90, 100, 110, 125, 150, 175, 200] as const;

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

/** Presses + or − until the level shown on the control is `to`. */
async function zoomTo(page: Page, to: number): Promise<void> {
  for (let i = 0; i < 12; i++) {
    const now = Number((await page.locator(".zoom-level").textContent())!.replace("%", ""));
    if (now === to) return;
    await page.keyboard.press(to > now ? "+" : "-");
    await page.waitForTimeout(30);
  }
  throw new Error(`could not reach ${to}%`);
}

const level = (page: Page) => page.locator(".zoom-level");

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

test.describe("screenshots at 1920x1080", () => {
  test.use({ viewport: { width: 1920, height: 1080 } });

  for (const z of [80, 100, 150, 175]) {
    test(`whole board at ${z}%`, async ({ page }) => {
      await open(page);
      await page.keyboard.press("b");
      await page.locator(".board-row-key").first().waitFor();
      await zoomTo(page, z);
      await page.waitForTimeout(1400); // the 1.2 s toast has gone
      await expect(page).toHaveScreenshot(`zoom-board-${z}-1920x1080.png`, SHOT);
    });
  }

  test("person view at 150%", async ({ page }) => {
    await open(page);
    await page.keyboard.press("ArrowDown");
    await expect(page.locator(".person-view .ticket-key").first()).toBeVisible();
    await zoomTo(page, 150);
    await page.waitForTimeout(1400);
    await expect(page).toHaveScreenshot("zoom-person-150-1920x1080.png", SHOT);
  });
});

test.describe("what zoom does at 1920x1080", () => {
  test.use({ viewport: { width: 1920, height: 1080 } });

  test("the board has 3, 2, 2, 1 columns at 80, 100, 150, 175 %", async ({ page }) => {
    await open(page);
    await page.keyboard.press("b");
    await page.locator(".board-row-key").first().waitFor();
    const cols = async () => page.locator(".board-col").count();
    await zoomTo(page, 80);
    expect(await cols()).toBe(3);
    await zoomTo(page, 100);
    expect(await cols()).toBe(2);
    await zoomTo(page, 150);
    expect(await cols()).toBe(2);
    await zoomTo(page, 175);
    expect(await cols()).toBe(1);
  });

  test("the person view at 150% has side headings; the toast says so, then goes after about 1.2 s", async ({ page }) => {
    await open(page);
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("=");
    await page.keyboard.press("=");
    await page.keyboard.press("=");
    await expect(level(page)).toHaveText("150%");
    await expect(page.locator(".person-group").first()).toHaveAttribute("data-heading", "side");
    const toast = page.locator(".zoom-toast");
    await expect(toast).toHaveText("150% · side headings");
    await expect(page.locator(".zoom-toast-slot")).toHaveAttribute("role", "status");
    await expect(toast).toBeHidden({ timeout: 2500 });
  });

  test("the middle button is ink-filled when zoomed and resets to 100%; 0 does too", async ({ page }) => {
    await open(page);
    await page.keyboard.press("b");
    await expect(level(page)).toHaveAttribute("data-zoomed", "false");
    await page.keyboard.press("+");
    await expect(level(page)).toHaveAttribute("data-zoomed", "true");
    const fill = await level(page).evaluate((e) => getComputedStyle(e).backgroundColor);
    expect(fill).not.toBe("rgba(0, 0, 0, 0)");
    await level(page).click();
    await expect(level(page)).toHaveText("100%");
    await page.keyboard.press("+");
    await page.keyboard.press("+");
    await page.keyboard.press("0");
    await expect(level(page)).toHaveText("100%");
  });

  test("the header never zooms: same height and wordmark size at 70% and 200%", async ({ page }) => {
    await open(page);
    await page.keyboard.press("b");
    const head = () =>
      page.evaluate(() => {
        const h = document.querySelector<HTMLElement>(".sf-header")!;
        const w = document.querySelector<HTMLElement>(".sf-header__wordmark")!;
        return { h: h.getBoundingClientRect().height, font: getComputedStyle(w).fontSize };
      });
    const base = await head();
    await zoomTo(page, 200);
    expect(await head()).toEqual(base);
    await zoomTo(page, 70);
    expect(await head()).toEqual(base);
  });

  test("⌘/Ctrl with + = − 0 zoom the screen and are prevented, so the webview never page-zooms", async ({ page }) => {
    await open(page);
    await page.keyboard.press("b");
    await page.evaluate(() => {
      const w = window as unknown as { __prevented: boolean[] };
      w.__prevented = [];
      window.addEventListener("keydown", (e) => {
        if (["=", "-", "0", "+"].includes(e.key)) w.__prevented.push(e.defaultPrevented); // not the Meta / Control presses
      });
    });
    const headH = () => page.evaluate(() => document.querySelector<HTMLElement>(".sf-header")!.getBoundingClientRect().height);
    const h0 = await headH();
    await page.keyboard.press("Meta+=");
    await expect(level(page)).toHaveText("110%");
    await page.keyboard.press("Control+=");
    await expect(level(page)).toHaveText("125%");
    await page.keyboard.press("Meta+-");
    await expect(level(page)).toHaveText("110%");
    await page.keyboard.press("Control+0");
    await expect(level(page)).toHaveText("100%");
    expect(await page.evaluate(() => (window as unknown as { __prevented: boolean[] }).__prevented)).toEqual([true, true, true, true]);
    expect(await headH()).toBe(h0);
    // the browser's own page zoom is untouched
    expect(await page.evaluate(() => window.visualViewport?.scale ?? 1)).toBe(1);
    expect(await page.evaluate(() => window.innerWidth)).toBe(1920);
  });

  test("zoom is per standup: it survives a switch and the person view shares it", async ({ page }) => {
    await open(page);
    await page.keyboard.press("b");
    await zoomTo(page, 125);
    await page.keyboard.press("ArrowDown"); // to a person
    await expect(level(page)).toHaveText("125%");
    await page.keyboard.press("b");
    await expect(level(page)).toHaveText("125%");
  });

  test("keeps the topmost fully visible lane at the top when the zoom changes", async ({ page }) => {
    await open(page);
    await page.keyboard.press("b");
    await page.locator(".board-row-key").first().waitFor();
    await page.keyboard.press("+"); // 110%
    await page.evaluate(() => {
      const m = document.querySelector<HTMLElement>(".sf-main")!;
      m.scrollTop = 260;
    });
    await page.waitForTimeout(100);
    const topLane = () =>
      page.evaluate(() => {
        const m = document.querySelector<HTMLElement>(".sf-main")!;
        const top = m.getBoundingClientRect().top;
        let best: { key: string; y: number } | null = null;
        document.querySelectorAll<HTMLElement>("[data-anchor]").forEach((el) => {
          const y = el.getBoundingClientRect().top - top;
          if (y >= -1 && (!best || y < best.y)) best = { key: el.dataset.anchor!, y };
        });
        return best as { key: string; y: number } | null;
      });
    const before = await topLane();
    expect(before).not.toBeNull();
    await page.keyboard.press("+"); // 125%
    await page.waitForTimeout(100);
    const after = await topLane();
    expect(after?.key).toBe(before!.key);
    expect(Math.abs(after!.y)).toBeLessThanOrEqual(2); // at the top of the scroll area
  });

  test("the toast keeps one size at every zoom (toasts keep the text size scale only)", async ({ page }) => {
    await open(page);
    await page.keyboard.press("b");
    const toastFont = async () => (await page.locator(".zoom-toast").evaluate((e) => getComputedStyle(e).fontSize));
    await page.keyboard.press("-");
    const small = await toastFont();
    await page.waitForTimeout(1400);
    await zoomTo(page, 175);
    const large = await toastFont();
    expect(small).toBe(large);
    expect(small).toBe("15px");
  });

  test("past the ladder the more bar takes over: at 200% nothing is hidden, the lists scroll", async ({ page }) => {
    await open(page);
    await page.keyboard.press("b");
    await zoomTo(page, 200);
    await expect(page.locator(".more-bar")).toBeVisible();
    // every ticket is still in the DOM
    expect(await page.locator(".board-row").count()).toBeGreaterThanOrEqual(20);
  });
});

for (const [w, h] of [
  [1920, 1080],
  [1280, 720],
] as const) {
  test.describe(`nothing scrolls sideways at every zoom, ${w}x${h}`, () => {
    test.use({ viewport: { width: w, height: h } });

    for (const z of STEPS) {
      test(`${z}%: person view, whole board, board by status`, async ({ page }) => {
        await open(page);
        await page.keyboard.press("ArrowDown");
        await page.locator(".person-view .ticket-key").first().waitFor();
        await zoomTo(page, z);
        expect(await sidewaysScrollers(page), `person ${z}%`).toEqual([]);
        await page.keyboard.press("b");
        await page.locator(".board-row-key").first().waitFor();
        expect(await sidewaysScrollers(page), `board ${z}%`).toEqual([]);
        await page.keyboard.press("g");
        await page.locator(".board-sec").first().waitFor();
        expect(await sidewaysScrollers(page), `board by status ${z}%`).toEqual([]);
        // nothing pokes out of the main area to the right
        const over = await page.evaluate(() => {
          const main = document.querySelector<HTMLElement>(".sf-main")!.getBoundingClientRect();
          return Array.from(document.querySelectorAll<HTMLElement>(".board-row, .board-controls, .board-lane-head, .board-sec-head"))
            .filter((e) => e.getBoundingClientRect().right > main.right + 1 || e.getBoundingClientRect().left < main.left - 1)
            .map((e) => e.className);
        });
        expect(over, `board by status ${z}% overflow`).toEqual([]);
      });
    }
  });
}

// Zoom is for the standup screen only (§16.1): the ticket modal, which opens over it, keeps the text size scale.
test.describe("the ticket modal never zooms, 1920x1080", () => {
  test.use({ viewport: { width: 1920, height: 1080 }, timezoneId: TIMEZONE });

  test("the dialog and its header pulse track are the same size at 100% and 150%", async ({ page }) => {
    await seed(page, NOW_1421);
    await stubMedia(page);
    await openApp(page);
    const geo = () =>
      page.evaluate(() => {
        const d = document.querySelector<HTMLElement>('[role="dialog"]')!;
        const t = d.querySelector<HTMLElement>(".pulse-track")!;
        const r = d.getBoundingClientRect();
        const tr = t.getBoundingClientRect();
        return { dialogW: r.width, dialogH: r.height, trackW: tr.width, trackH: tr.height, font: getComputedStyle(d).fontSize };
      });
    await openTicket(page, "MOB-1421");
    const at100 = await geo();
    expect(at100.trackH).toBeGreaterThan(0);
    await page.keyboard.press("Escape");
    await page.getByRole("dialog").waitFor({ state: "detached" });
    await zoomTo(page, 150);
    await openTicket(page, "MOB-1421");
    expect(await geo()).toEqual(at100);
  });
});
