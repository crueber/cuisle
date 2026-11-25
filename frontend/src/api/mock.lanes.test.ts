// The mock mirrors Go's custom-lane rules (settings lanes.go): ids c<N> never reused, rotating hues, labels unique
// ignoring case, delete moves a lane's columns, and the view lists custom lanes in walk order with their labels.
import { describe, expect, it } from "vitest";
import { AppError } from "./errors";
import { createMockApi } from "./mock";

async function setup() {
  const api = createMockApi();
  const cfg = await api.config.get(1);
  const col = cfg.columns.find((c) => c.lane === "prog")!.columnName;
  return { api, id: cfg.id, col };
}

async function code(p: Promise<unknown>): Promise<string> {
  try {
    await p;
    return "ok";
  } catch (e) {
    return e instanceof AppError ? `${e.code}:${e.field}` : String(e);
  }
}

describe("mock custom lanes", () => {
  it("creates a lane for a column: id, hue, option order, walk end, and the view", async () => {
    const { api, id, col } = await setup();
    const cfg = await api.config.createLane(id, " Design ", col);
    const design = cfg.laneOptions.find((o) => o.custom)!;
    expect(design).toMatchObject({ lane: "c1", label: "Design", hue: 1, kind: "working", columns: [col] });
    expect(cfg.laneOptions.map((o) => o.lane)).toEqual(["backlog", "todo", "prog", "review", "qa", "c1", "done", "hidden"]);
    expect(cfg.lanes.at(-1)?.lane).toBe("c1");
    expect(cfg.columns.find((c) => c.columnName === col)?.lane).toBe("c1");
    expect(cfg.needsSync).toBe(true);
    const view = await api.standup.view(id);
    expect(view.lanes.map((l) => l.lane)).toContain("c1");
    expect(view.lanes.at(-1)?.lane).toBe("done");
  });

  it("reuses a lane with the same name and rejects clashing names", async () => {
    const { api, id } = await setup();
    await api.config.createLane(id, "Design", "");
    const again = await api.config.createLane(id, "design", "");
    expect(again.laneOptions.filter((o) => o.custom)).toHaveLength(1);
    expect(await code(api.config.createLane(id, "in review", ""))).toBe("validation:label");
    expect(await code(api.config.createLane(id, "  ", ""))).toBe("validation:label");
  });

  it("renames built-in lanes, Done and custom lanes; the view follows", async () => {
    const { api, id } = await setup();
    await api.config.createLane(id, "Design", "");
    await api.config.renameLane(id, "review", "Code Review");
    await api.config.renameLane(id, "done", "Shipped");
    await api.config.renameLane(id, "c1", "Discovery");
    const view = await api.standup.view(id);
    const labels = Object.fromEntries(view.lanes.map((l) => [l.lane, l.label]));
    expect(labels).toMatchObject({ review: "Code Review", done: "Shipped", c1: "Discovery" });
    await api.config.renameLane(id, "review", "");
    expect((await api.config.get(id)).laneOptions.find((o) => o.lane === "review")?.label).toBe("In Review");
    expect(await code(api.config.renameLane(id, "hidden", "Parked"))).toBe("validation:lane");
  });

  it("deletes a lane, moving its columns; ids are never reused", async () => {
    const { api, id, col } = await setup();
    await api.config.createLane(id, "Design", col);
    expect(await code(api.config.deleteLane(id, "c1", ""))).toBe("validation:moveTo");
    const cfg = await api.config.deleteLane(id, "c1", "qa");
    expect(cfg.columns.find((c) => c.columnName === col)?.lane).toBe("qa");
    expect(cfg.laneOptions.some((o) => o.custom)).toBe(false);
    expect(cfg.lanes.map((l) => l.lane)).not.toContain("c1");
    const next = await api.config.createLane(id, "Blocked", "");
    expect(next.laneOptions.find((o) => o.custom)).toMatchObject({ lane: "c2", hue: 1 }); // c1's freed colour
  });

  it("walk order must list every working lane, custom ones included", async () => {
    const { api, id } = await setup();
    await api.config.createLane(id, "Design", "");
    expect(await code(api.config.setWalkOrder(id, ["qa", "review", "prog", "todo"]))).toBe("validation:lanes");
    await api.config.setWalkOrder(id, ["todo", "c1", "prog", "review", "qa"]);
    expect((await api.config.get(id)).lanes.map((l) => l.lane)).toEqual(["todo", "c1", "prog", "review", "qa"]);
    await api.config.setLaneLimit(id, "c1", 7);
    expect((await api.config.get(id)).lanes.find((l) => l.lane === "c1")?.limitDays).toBe(7);
  });
});
