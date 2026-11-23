// WP-38F: text fields flush on blur (and Enter) instead of waiting out the 400 ms debounce.
import { cleanup, fireEvent, render, screen } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../../api";
import type { StandupConfig } from "../../../api/types";
import { config } from "../../../state/config";
import { resetSaveQueue } from "../../../state/save";
import { SourceSection } from "./Source";

const cfg = {
  id: 7, rev: 1, name: "Svc", space: { id: "1", key: "S", name: "S" }, board: { id: "2", name: "B", type: "scrum" }, scope: "sprint", extraFilter: "",
  filterCheck: null, draft: false, needsSync: false,
} as unknown as StandupConfig;

const update = vi.fn(async () => ({ rev: 2, savedAt: 1, needsSync: false }));
const validateFilter = vi.fn(async () => ({ ok: true, errors: [] }));

beforeEach(() => {
  vi.useFakeTimers();
  resetSaveQueue();
  update.mockClear();
  validateFilter.mockClear();
  config.value = cfg;
  setApi({ config: { update }, connection: { validateFilter }, standup: { view: async () => ({ standupId: 7, rev: 2 }) } } as unknown as Api);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("Source text fields", () => {
  it("Name saves at once on blur, before the debounce would have fired", async () => {
    render(<SourceSection config={cfg} columns={3} />);
    fireEvent.input(screen.getByLabelText("Name"), { target: { value: "Renamed" } });
    expect(update).not.toHaveBeenCalled();
    fireEvent.blur(screen.getByLabelText("Name"));
    await vi.advanceTimersByTimeAsync(0);
    expect(update).toHaveBeenCalledWith(7, { name: "Renamed" });
  });

  it("Extra filter flushes and validates on commit", async () => {
    render(<SourceSection config={cfg} columns={3} />);
    fireEvent.input(screen.getByLabelText("Extra filter"), { target: { value: "labels = x" } });
    fireEvent.keyDown(screen.getByLabelText("Extra filter"), { key: "Enter" });
    await vi.advanceTimersByTimeAsync(0);
    expect(update).toHaveBeenCalledWith(7, { extraFilter: "labels = x" });
    expect(validateFilter).toHaveBeenCalledWith(7, "labels = x");
  });
});
