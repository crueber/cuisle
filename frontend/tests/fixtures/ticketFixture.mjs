// The mock facade serves this file for MOB-1421. The hand-built ticket_mob1421.json (the mockup data) stays for the
// component unit tests (AdfDoc, FieldRail, TicketHeader, TicketContent, Lightbox), which assert its exact content.
// Builds src/api/fixtures/ticket_mob1421.golden.json from the Go golden internal/app/testdata/ticket_mob1421.golden.json
// (WP-T9, F12 freeze decision 7). The golden is the real stack's TicketDetail for MOB-1421 over the fake Jira; the mock's
// standup views (src/api/fixtures/view_mob.json) name the same issue differently, so the transform maps only the ids:
//   local issue ids   +100   (golden 7 -> mock 107; subtasks, links and ADF inline cards follow: 11 -> 111, 12 -> 112 ...)
//   row.sourceId      "10099" + golden id    (10007 -> 10106, as the mock view has it)
//   account ids       "u-iris" -> "iris"     (the mock's speakers)
// Times are kept: the golden's clock is the same calendar day (2026-11-23) as the mock's, and a shift would move local
// midnights (the "new since standup" bound). The base URL is already https://example.atlassian.net in both.
// Run from frontend/:  node tests/fixtures/ticketFixture.mjs   (writes src/api/fixtures/ticket_mob1421.golden.json)
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const ID_OFFSET = 100;

function walk(v, key) {
  if (Array.isArray(v)) return v.map((x) => walk(x, key));
  if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, walk(x, k)]));
  if (key === "issueId" && typeof v === "number" && v > 0) return v + ID_OFFSET;
  if ((key === "accountId" || key === "assignee") && typeof v === "string" && v.startsWith("u-")) return v.slice(2);
  return v;
}

export function mockTicketFromGolden(golden) {
  const out = walk(golden, "");
  out.row.id = golden.row.id + ID_OFFSET;
  out.row.sourceId = String(10099 + golden.row.id);
  return out;
}

if (process.argv[1] && process.argv[1].endsWith("ticketFixture.mjs")) {
  const here = dirname(fileURLToPath(import.meta.url));
  const GOLDEN = resolve(here, "../../../internal/app/testdata/ticket_mob1421.golden.json");
  const OUT = resolve(here, "../../src/api/fixtures/ticket_mob1421.golden.json");
  const golden = JSON.parse(readFileSync(GOLDEN, "utf8"));
  writeFileSync(OUT, JSON.stringify(mockTicketFromGolden(golden), null, 2) + "\n");
  console.log("wrote", OUT);
}
