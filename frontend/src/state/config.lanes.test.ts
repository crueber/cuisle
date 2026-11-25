// Custom lanes from Configure › Status mapping, against the in-browser mock API: creating and deleting replace the
// config with the server's reply (Go picks ids and colours); renaming shows at once and autosaves after the debounce.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { api, setApi, wailsApi } from "../api";
import { createMockApi, type MockApi } from "../api/mock";
import { config, createLane, deleteLane, laneError, loadConfig, renameLane } from "./config";
import { flush } from "./save";

let mock: MockApi;
beforeEach(async () => {
  mock = createMockApi();
  setApi(mock);
  await loadConfig(1);
});
afterEach(() => {
  config.value = null;
  setApi(wailsApi);
});

const custom = () => config.value?.laneOptions.filter((o) => o.custom) ?? [];

describe("lane actions", () => {
  it("createLane maps the column and takes the server's lane (id and hue)", async () => {
    createLane("Design", "In Progress");
    await flush();
    expect(custom()).toEqual([expect.objectContaining({ lane: "c1", label: "Design", hue: 1, columns: ["In Progress"] })]);
    expect(config.value?.columns.find((c) => c.columnName === "In Progress")?.lane).toBe("c1");
    expect(mock.calls.filter((c) => c.method === "config.createLane")).toHaveLength(1);
  });

  it("renameLane shows at once, saves once after typing, and a blank commit restores the built-in label", async () => {
    renameLane("review", "Code");
    renameLane("review", "Code Review");
    expect(config.value?.laneOptions.find((o) => o.lane === "review")?.label).toBe("Code Review");
    await flush();
    expect(mock.calls.filter((c) => c.method === "config.renameLane")).toHaveLength(1);
    expect((await api().config.get(1)).laneOptions.find((o) => o.lane === "review")?.label).toBe("Code Review");

    renameLane("review", "");
    expect(config.value?.laneOptions.find((o) => o.lane === "review")?.label).toBe("");
    renameLane("review", "", true);
    expect(config.value?.laneOptions.find((o) => o.lane === "review")?.label).toBe("In Review");
  });

  it("a rejected create shows its message until the next lane action, and changes nothing", async () => {
    createLane("QA", "In Progress");
    await flush();
    expect(laneError(1)).toMatch(/already a lane called QA/);
    expect(custom()).toEqual([]);
    createLane("Design", "");
    await flush();
    expect(laneError(1)).toBeUndefined();
    expect(custom()).toHaveLength(1);
  });

  it("deleteLane moves the columns and drops the lane", async () => {
    createLane("Design", "In Progress");
    deleteLane("c1", "prog");
    await flush();
    expect(custom()).toEqual([]);
    expect(config.value?.columns.find((c) => c.columnName === "In Progress")?.lane).toBe("prog");
  });
});
