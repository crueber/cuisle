// Captures the README screenshots from the real app (Go + fake Jira + the production frontend). The default fake
// Jira serves the invented "Saffron" sample (design/reference/sample-data.json): no real company, person or ticket.
//
//   (cd frontend && ./node_modules/.bin/vite build)
//   go build -tags server -o /tmp/cuisle-server ./cmd/cuisle
//   CUISLE_DATA_DIR=$(mktemp -d) CUISLE_FAKE_JIRA=default /tmp/cuisle-server &
//   node scripts/readme/shoot.mjs            → docs/screenshots/*.png
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const require = createRequire(join(here, "../../frontend/package.json"));
const { chromium } = require("playwright");
const out = join(here, "../../docs/screenshots");
const URL = process.env.CUISLE_URL ?? "http://localhost:8080";
const W = 1600;
const H = 1000;

const browser = await chromium.launch();
const errors = [];

async function page(scheme) {
  const p = await browser.newPage({ viewport: { width: W, height: H }, colorScheme: scheme, deviceScaleFactor: 2 });
  p.on("pageerror", (e) => errors.push(e.message));
  await p.goto(URL);
  await p.waitForSelector(".sf-header");
  await p.waitForTimeout(800);
  return p;
}

async function waitSynced(p) {
  for (let i = 0; i < 180; i++) {
    const label = await p.locator(".sf-header__sync").innerText().catch(() => "");
    const rows = await p.locator(".board-row").count();
    if (!/syncing/i.test(label) && rows > 0) return;
    await p.waitForTimeout(1000);
  }
  throw new Error("the first sync did not finish");
}

const light = await page("light");
// First run: sync every standup so each tab has data (R syncs the open one).
for (let n = 1; n <= 5; n++) {
  await light.keyboard.press(String(n));
  await light.waitForTimeout(400);
  await light.keyboard.press("r");
  await light.waitForTimeout(1500);
  await waitSynced(light);
}
await light.keyboard.press("1");
await light.waitForTimeout(600);

await light.keyboard.press("b");
await light.waitForTimeout(600);
await light.screenshot({ path: join(out, "board.png") });

// ↓ steps to the first speaker (↑ ↓ speakers, ← → standups).
await light.keyboard.press("ArrowDown");
await light.waitForTimeout(600);
await light.screenshot({ path: join(out, "person.png") });

// The ticket modal: MOB-1429 (blocked; hand-written description and comments in the fake Jira's content overlay).
await light.locator(".person-view button", { hasText: "MOB-1429" }).first().click();
await light.waitForSelector('[role="dialog"] .anchor');
await light.waitForTimeout(800);
await light.screenshot({ path: join(out, "ticket.png") });
await light.keyboard.press("Escape");
await light.waitForTimeout(400);

await light.keyboard.press(",");
await light.waitForSelector('.cfg-grid[data-balanced="true"]');
await light.waitForTimeout(800);
await light.screenshot({ path: join(out, "configure.png") });
await light.keyboard.press("Escape");

const dark = await page("dark");
await dark.keyboard.press("ArrowDown");
await dark.keyboard.press("ArrowDown");
await dark.waitForTimeout(600);
await dark.screenshot({ path: join(out, "person-dark.png") });

await browser.close();
if (errors.length) {
  console.error("page errors:", errors);
  process.exit(1);
}
console.log("wrote", ["board", "person", "ticket", "configure", "person-dark"].map((n) => `docs/screenshots/${n}.png`).join(", "));
