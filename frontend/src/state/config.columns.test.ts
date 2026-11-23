// moveColumn: order of columns that share a lane (Configure > Status mapping > Order within sections), against the
// in-browser mock API. The list reorders at once; every move reaches the API in order.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { api, setApi, wailsApi } from "../api";
import { createMockApi } from "../api/mock";
import { config, loadConfig, moveColumn, setColumnLane } from "./config";
import { flush } from "./save";

const orderOf = (lane: string) =>
  (config.value?.columns ?? [])
    .filter((c) => c.lane === lane)
    .sort((a, b) => a.order - b.order)
    .map((c) => c.columnName);

beforeEach(async () => {
  setApi(createMockApi());
  await loadConfig(1);
});
afterEach(() => {
  config.value = null;
  setApi(wailsApi);
});

describe("moveColumn", () => {
  it("reorders a shared lane at once and persists every move in order", async () => {
    setColumnLane("Review", "prog"); // Review joins In Progress, at the end
    await flush();
    await loadConfig(1);
    expect(orderOf("prog")).toEqual(["In Progress", "Review"]);

    moveColumn("Review", 0);
    expect(orderOf("prog")).toEqual(["Review", "In Progress"]); // optimistic
    moveColumn("In Progress", 0);
    moveColumn("Review", 0);
    await flush();

    const saved = await api().config.get(1);
    const persisted = saved.columns.filter((c) => c.lane === "prog").sort((a, b) => a.order - b.order).map((c) => c.columnName);
    expect(persisted).toEqual(["Review", "In Progress"]);
    expect(orderOf("prog")).toEqual(persisted);
  });

  it("ignores an unknown column", () => {
    const before = config.value;
    moveColumn("Nope", 0);
    expect(config.value).toBe(before);
  });
});
