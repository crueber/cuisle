import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { waitForStandupScreen } from "../perf/measure";

// testing §5 and ux-spec §5.5–5.6: "nothing hidden". At 1024×640 the person view DOM contains every ticket of that
// person in the fixture, and the more bar text matches what is actually below the fold.

const FIXTURE = fileURLToPath(new URL("../../src/api/fixtures/view_mob.json", import.meta.url));

interface FixtureTicket {
  key: string;
  assignee: string | null;
}
interface FixtureView {
  tickets: FixtureTicket[];
  speakers: { id: string; name: string }[];
}

const view = JSON.parse(readFileSync(FIXTURE, "utf8")) as FixtureView;

test.use({ viewport: { width: 1024, height: 640 } });

test("person view at 1024x640 contains every ticket of the selected person", async ({ page }) => {
  await waitForStandupScreen(page);
  // The app opens on the remembered person (Iris Calloway in the mock fixture); assert that before counting.
  const name = (await page.locator(".person-name").textContent())?.trim() ?? "";
  const speaker = view.speakers.find((s) => s.name === name);
  expect(speaker, `speaker ${name} is in the fixture`).toBeDefined();

  const expected = view.tickets.filter((t) => t.assignee === speaker!.id).map((t) => t.key);
  expect(expected.length).toBeGreaterThan(0);

  const rendered = await page.locator(".person-view .ticket-key").allTextContents();
  const missing = expected.filter((k) => !rendered.includes(k));
  expect(missing, `tickets of ${name} missing from the person view DOM`).toEqual([]);
});

test("more bar lane counts match the rows actually below the fold at 1024x640", async ({ page }) => {
  await waitForStandupScreen(page);
  const bar = page.locator(".more-bar").first();
  await expect(bar).toBeVisible();

  // Per lane group: rows whose top is past the visible bottom of the person scroller, less the bar's own height
  // (the bar overlays the bottom edge). Lanes with zero rows below are omitted, as the summary does.
  const belowByLane = await page.evaluate(() => {
    const scroller = document.querySelector<HTMLElement>(".person-scroll");
    const barEl = document.querySelector<HTMLElement>(".more-bar");
    if (!scroller || !barEl) return null;
    const fold = scroller.getBoundingClientRect().bottom - barEl.getBoundingClientRect().height;
    const out: Record<string, number> = {};
    document.querySelectorAll<HTMLElement>(".person-group").forEach((g) => {
      const label = g.getAttribute("aria-label") ?? "";
      const n = [...g.querySelectorAll<HTMLElement>(".ticket-key")].filter(
        (k) => k.getBoundingClientRect().top >= fold - 1,
      ).length;
      if (n > 0) out[label] = n;
    });
    return out;
  });
  expect(belowByLane, "person scroller and more bar present").not.toBeNull();

  const summary = ((await bar.locator(".more-bar-summary").textContent()) ?? "").trim();
  const isEnd = (await bar.locator(".more-bar-title").textContent())?.trim() === "End of list";
  const counts = belowByLane ?? {};
  if (Object.keys(counts).length === 0) {
    expect(isEnd, "nothing below the fold, so the bar says End of list").toBe(true);
    return;
  }
  expect(isEnd, "rows below the fold, so the bar must not say End of list").toBe(false);
  // Summary format (layout/below.ts): "2 To Do · 6 Done below".
  const parsed: Record<string, number> = {};
  for (const part of summary.replace(/ below$/, "").split(" · ")) {
    const m = /^(\d+) (.+)$/.exec(part.trim());
    expect(m, `summary part "${part}" is "<count> <lane>"`).not.toBeNull();
    parsed[m![2]!] = Number(m![1]);
  }
  expect(parsed, `more bar "${summary}" vs DOM rows below the fold`).toEqual(counts);
});
