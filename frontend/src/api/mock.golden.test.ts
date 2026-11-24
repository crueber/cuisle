// WP-T9 tests: the mock's MOB-1421 is the Go golden (internal/app/testdata/ticket_mob1421.golden.json) with only its ids
// normalised (tests/fixtures/ticketFixture.mjs), so mock and Go agree. If this fails, the golden changed:
// run `node tests/fixtures/ticketFixture.mjs` from frontend/ and review the diff.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { mockTicketFromGolden } from "../../tests/fixtures/ticketFixture.mjs";
import { createMockApi } from "./mock";
import type { TicketDetail } from "./types";

const read = (rel: string) => JSON.parse(readFileSync(resolve(process.cwd(), rel), "utf8")) as Record<string, unknown>;
const golden = read("../internal/app/testdata/ticket_mob1421.golden.json");
const fixture = read("src/api/fixtures/ticket_mob1421.golden.json");

describe("the mock's MOB-1421 is the Go golden", () => {
  it("fixtures/ticket_mob1421.golden.json equals the transform of the Go golden", () => {
    expect(fixture).toEqual(mockTicketFromGolden(golden));
  });

  it("only ids differ from the golden: same keys, rail, ADF and comments", () => {
    const g = golden as unknown as TicketDetail;
    const f = fixture as unknown as TicketDetail;
    expect(Object.keys(f)).toEqual(Object.keys(g));
    expect(f.row.key).toBe(g.row.key);
    expect(f.row.id).toBe(g.row.id + 100);
    expect(f.rail.map((r) => r.id)).toEqual(g.rail.map((r) => r.id));
    expect(f.comments).toHaveLength(g.comments.length);
    expect(f.description).toEqual(JSON.parse(JSON.stringify(g.description).replace(/"accountId":"u-/g, '"accountId":"').replace(/"issueId":8\b/, '"issueId":108')));
  });

  it("ticket.get serves it for the mock's MOB-1421 (issue 107) and ids line up with the views", async () => {
    const mock = createMockApi();
    const d = await mock.ticket.get(1, 107);
    expect(d.row.key).toBe("MOB-1421");
    expect(d.row.id).toBe(107);
    const view = await mock.standup.view(1);
    const row = view.tickets.find((t) => t.id === 107);
    expect(row?.key).toBe("MOB-1421");
    expect(row?.sourceId).toBe(d.row.sourceId);
    expect(row?.assignee).toBe(d.row.assignee);
    for (const s of d.subtasks) expect(view.tickets.some((t) => t.id === s.issueId && t.key === s.key)).toBe(true);
  });
});
