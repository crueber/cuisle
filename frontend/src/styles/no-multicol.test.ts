// CSS multi-column is banned: in the app's WebKit webview a multi-column box inside a scroller fragments into extra
// columns past the right edge (Configure scrolled sideways). Use a grid of explicit column containers instead.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

function cssFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n);
    if (statSync(p).isDirectory()) return cssFiles(p);
    return p.endsWith(".css") ? [p] : [];
  });
}

describe("stylesheets", () => {
  it("useNoCssMultiColumn", () => {
    const bad = cssFiles(resolve(__dirname, "..")).filter((f) => /(^|[;{\s])(column-count|column-width|columns)\s*:|break-(before|after):\s*column/m.test(readFileSync(f, "utf8")));
    expect(bad).toEqual([]);
  });
});
