import { expect, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { deflateSync } from "node:zlib";

// Shared by the ticket modal specs (screens, a11y, perf). Not a spec file.

interface FixtureTicket {
  asOf: number;
}
const readTicket = (name: string): FixtureTicket =>
  JSON.parse(readFileSync(fileURLToPath(new URL(`../src/api/fixtures/${name}`, import.meta.url)), "utf8")) as FixtureTicket;

/** Frozen Date.now for the golden MOB-1421 (the detail's asOf plus a minute) and the hand-built MOB-1429. */
export const NOW_1421 = readTicket("ticket_mob1421.golden.json").asOf + 60_000;
export const NOW_1429 = readTicket("ticket_mob1429.json").asOf + 60_000;

/** The zone the screenshots are taken in, so the times in the baselines do not depend on the machine. */
export const TIMEZONE = "America/Chicago";

export interface MockHooks {
  failures?: Record<string, { code: string; message?: string; times?: number }>;
  holdMs?: Record<string, number>;
}

/** Pins Date.now and (optionally) the mock facade's options before the app starts. */
export async function seed(page: Page, now: number, options?: MockHooks): Promise<void> {
  await page.addInitScript(
    ([n, o]) => {
      Date.now = () => n;
      if (o) (window as unknown as { __CUISLE_MOCK_OPTIONS__: unknown }).__CUISLE_MOCK_OPTIONS__ = o;
    },
    [now, options ?? null] as const,
  );
}

/** Loads the mock app on Iris's person view (the mockups' background) and waits for the screen and fonts. */
export async function openApp(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator(".sf-screen .ticket-key").first().waitFor();
  await page.evaluate(() => document.fonts.ready);
  // The whole board, then the first speaker: Iris (the rail may be icon-only at narrow widths, so no clicking by name).
  await page.keyboard.press("b");
  await page.keyboard.press("ArrowRight");
  await expect(page.locator(".person-name")).toHaveText(/Iris/);
}

/** Clicks a person-view row by its key and waits for the modal's content. */
export async function openTicket(page: Page, key: string): Promise<void> {
  await page.locator(".person-view button", { hasText: key }).first().click();
  const dialog = page.getByRole("dialog", { name: `Ticket ${key}` });
  await dialog.waitFor();
  await dialog.locator(".anchor").waitFor();
  await page.waitForLoadState("networkidle");
  await page.evaluate(() => document.fonts.ready);
}

// ── a stand-in for /media/{id}[/thumb] (the real route is served by the Go asset middleware; vite preview has none) ──
const crcTable = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});
function crc32(buf: Buffer): number {
  let c = 0xffffffff;
  for (const b of buf) c = crcTable[(c ^ b) & 0xff]! ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}
function chunk(type: string, data: Buffer): Buffer {
  const head = Buffer.alloc(8);
  head.writeUInt32BE(data.length, 0);
  head.write(type, 4, "ascii");
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(Buffer.concat([head.subarray(4), data])), 0);
  return Buffer.concat([head, data, crc]);
}
/** A w x h PNG with a warm diagonal gradient (the mockups' placeholder screenshots are orange cards). */
export function gradientPng(w: number, h: number): Buffer {
  const stride = w * 3 + 1;
  const raw = Buffer.alloc(stride * h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const t = (x / w + y / h) / 2;
      const o = y * stride + 1 + x * 3;
      raw[o] = Math.round(236 - 40 * t);
      raw[o + 1] = Math.round(160 - 60 * t);
      raw[o + 2] = Math.round(92 - 40 * t);
    }
  }
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8;
  ihdr[9] = 2;
  const sig = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
  return Buffer.concat([sig, chunk("IHDR", ihdr), chunk("IDAT", deflateSync(raw)), chunk("IEND", Buffer.alloc(0))]);
}

/** Serves a gradient PNG for every /media/... request, so image tiles and the lightbox have pixels. */
export async function stubMedia(page: Page): Promise<void> {
  const full = gradientPng(1200, 720);
  const thumb = gradientPng(320, 200);
  await page.route("**/media/**", (route) =>
    route.fulfill({ status: 200, contentType: "image/png", body: route.request().url().endsWith("/thumb") ? thumb : full }),
  );
}

/** Clicks an anchor-strip entry and waits for the smooth scroll to settle (the content column stops moving). */
export async function jumpTo(page: Page, name: RegExp): Promise<void> {
  await page.locator(".anchor").getByText(name).click();
  await page.evaluate(
    () =>
      new Promise<void>((resolve) => {
        const el = document.querySelector(".tm-content") as HTMLElement;
        let last = -1;
        let still = 0;
        const tick = () => {
          still = el.scrollTop === last ? still + 1 : 0;
          last = el.scrollTop;
          if (still >= 5) resolve();
          else requestAnimationFrame(tick);
        };
        tick();
      }),
  );
}
