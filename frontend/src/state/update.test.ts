// state/update (ADR 0016): mirrors Go's update status; the only requests are Status (local), Check now and Install.
import { afterEach, describe, expect, it } from "vitest";
import { setApi } from "../api";
import { setEventBus } from "../api/events";
import { createMockApi } from "../api/mock";
import { handleGlobalKey } from "../keys";
import { route } from "./route";
import {
  NO_UPDATE, installUpdate, installUpdateEvents, installing, loadUpdateStatus, updateAvailable, updateDialogOpen, updateStatus,
} from "./update";

const AVAILABLE = {
  channel: "forgejo", currentVersion: "v1.2.0",
  available: { version: "v1.3.0", pageUrl: "https://forge.example/r/v1.3.0", publishedAt: 1, canInstall: true },
};

afterEach(() => {
  updateStatus.value = NO_UPDATE;
  updateDialogOpen.value = false;
});

describe("state/update", () => {
  it("loads the status without a check", async () => {
    const mock = createMockApi({ update: AVAILABLE });
    setApi(mock);
    await loadUpdateStatus();
    expect(updateAvailable()).toBe(true);
    expect(mock.calls.map((c) => c.method)).toEqual(["update.status"]);
  });

  it("the default mock build has no channel: nothing is offered", async () => {
    setApi(createMockApi());
    await loadUpdateStatus();
    expect(updateStatus.value.channel).toBe("");
    expect(updateAvailable()).toBe(false);
  });

  it("follows update:status events through an install", async () => {
    const mock = createMockApi({ update: AVAILABLE });
    setApi(mock);
    setEventBus(mock.bus);
    const off = installUpdateEvents();
    await loadUpdateStatus();
    await installUpdate();
    expect(updateStatus.value.phase).toBe("restarting");
    expect(installing()).toBe(true);
    await installUpdate(); // already installing: no second call
    expect(mock.calls.filter((c) => c.method === "update.install")).toHaveLength(1);
    off();
  });

  it("a rejected install without a status event still shows a failure", async () => {
    const mock = createMockApi({ update: AVAILABLE, failures: { "update.install": { code: "conflict", message: "An update check or install is already running." } } });
    setApi(mock);
    await loadUpdateStatus();
    await installUpdate();
    expect(updateStatus.value.phase).toBe("failed");
    expect(updateStatus.value.error).toBe("An update check or install is already running.");
  });

  it("global keys do nothing behind the Update dialog", () => {
    route.value = { view: "standup", standupId: 1, person: null };
    updateDialogOpen.value = true;
    expect(handleGlobalKey(new KeyboardEvent("keydown", { key: "r" }))).toBe(false);
    expect(handleGlobalKey(new KeyboardEvent("keydown", { key: "b" }))).toBe(false);
  });
});
