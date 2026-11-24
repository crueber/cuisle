// Renders the Cuisle DMG artwork (icons sit at x 151 and 388, row centre y 190: wails3 tool package defaults) from design/icon/cuisle-icon.svg (run on a Mac or anywhere with Playwright):
//   node scripts/icon/render.mjs   → build/darwin/dmg-file-icon.png (1254²) and build/darwin/dmg-background.png (540×380)
// Then: wails3 generate icons -input build/darwin/dmg-file-icon.png -macfilename build/darwin/dmg-file-icon.icns
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "../..");
const require = createRequire(join(root, "frontend/package.json"));
const { chromium } = require("playwright");
const svg = readFileSync(join(root, "design/icon/cuisle-icon.svg"), "utf8");
const mono = readFileSync(join(root, "frontend/src/assets/fonts/plex-mono-700.woff2")).toString("base64");
const sans = readFileSync(join(root, "frontend/src/assets/fonts/plex-sans-condensed-600.woff2")).toString("base64");

const b = await chromium.launch();

// 1. The DMG file icon: the app icon, large, on transparent.
{
  const p = await b.newPage({ viewport: { width: 1254, height: 1254 } });
  await p.setContent(`<html><body style="margin:0;background:transparent">${svg.replace("<svg", '<svg width="1254" height="1254"')}</body></html>`);
  await p.screenshot({ path: join(root, "build/darwin/dmg-file-icon.png"), omitBackground: true });
}

// 2. The DMG window background (540×380): paper, the wordmark, a pulse line from the app to Applications.
{
  const p = await b.newPage({ viewport: { width: 540, height: 380 }, deviceScaleFactor: 1 });
  await p.setContent(`<html><head><style>
    @font-face { font-family: M; src: url(data:font/woff2;base64,${mono}); font-weight: 700; }
    @font-face { font-family: S; src: url(data:font/woff2;base64,${sans}); font-weight: 600; }
    body { margin: 0; width: 540px; height: 380px; background: #F6F4EE; position: relative; overflow: hidden; }
    .mark { position: absolute; top: 34px; width: 100%; text-align: center; font: 700 22px/1 M; letter-spacing: .32em; color: #16181D; }
    .pulse { position: absolute; left: 214px; width: 104px; top: 186px; height: 8px; display: flex; gap: 3px; }
    .pulse i { flex: 1; border-radius: 2px; }
    .arrow { position: absolute; left: 318px; top: 181px; width: 0; height: 0; border: 9px solid transparent; border-left: 12px solid #5E8C6A; }
    .hint { position: absolute; bottom: 34px; width: 100%; text-align: center; font: 600 15px/1 S; color: #5F636A; }
    .rule { position: absolute; top: 72px; left: 220px; width: 100px; height: 2px; background: #16181D; }
  </style></head><body>
    <div class="mark">CUISLE</div><div class="rule"></div>
    <div class="pulse"><i style="background:#8C8676"></i><i style="background:#3D6FC4"></i><i style="background:#7A55B8"></i><i style="background:#C08A2A"></i><i style="background:#5E8C6A"></i></div>
    <div class="arrow"></div>
    <div class="hint">Drag Cuisle to Applications</div>
  </body></html>`);
  await p.evaluate(() => document.fonts.ready);
  await p.screenshot({ path: join(root, "build/darwin/dmg-background.png") });
}
await b.close();
console.log("wrote build/darwin/dmg-file-icon.png, build/darwin/dmg-background.png");
