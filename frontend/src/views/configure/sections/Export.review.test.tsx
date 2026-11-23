// WP-38F: Export section fixes: edits survive a rail switch, deletes go through the save queue in order, and a saved
// secret leaves the field. Uses the real save queue and real components.
import { act, cleanup, fireEvent, render, screen } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { setApi, type Api } from "../../../api";
import { createMemoryBus, setEventBus } from "../../../api/events";
import type { SinkConfig, SinkInput, SinkKind, StandupConfig } from "../../../api/types";
import { resetSaveQueue } from "../../../state/save";
import { ExportSection } from "./Export";

const KIND = {
  kind: "file", displayName: "File (JSON)",
  schema: [{ key: "dir", label: "Folder", type: "path", default: "", options: null, help: "" }, { key: "token", label: "Token", type: "secret", default: "", options: null, help: "" }],
} as unknown as SinkKind;
const SINK = { id: 5, kind: "file", name: "Nightly", standupId: 3, config: { dir: "/tmp/x" }, hasSecret: false, sendOn: "manual", enabled: true, lastDelivery: null } as unknown as SinkConfig;
const cfg = (id: number) => ({ id }) as unknown as StandupConfig;

let save: Mock<(i: SinkInput) => Promise<SinkConfig>>;
let del: Mock<(id: number) => Promise<void>>;

beforeEach(() => {
  vi.useFakeTimers();
  resetSaveQueue();
  setEventBus(createMemoryBus());
  save = vi.fn(async (i: SinkInput) => ({ ...SINK, id: i.id || 5, name: i.name, hasSecret: i.secret !== null } as SinkConfig));
  del = vi.fn(async () => {});
  setApi({ sink: { kinds: async () => [KIND], list: async () => [SINK], save, delete: del } } as unknown as Api);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

async function mount(id = 3) {
  const utils = render(<ExportSection config={cfg(id)} columns={3} />);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
  return utils;
}
const type = (label: string, value: string) => fireEvent.input(screen.getByLabelText(label), { target: { value } });

describe("Export section", () => {
  it("an edit still saves when the rail switches to another standup before the debounce fires", async () => {
    const { rerender } = await mount(3);
    type("Name", "Renamed");
    rerender(<ExportSection config={cfg(4)} columns={3} />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(500);
    });
    expect(save).toHaveBeenCalledTimes(1);
    expect(save.mock.calls[0]![0]).toMatchObject({ id: 5, name: "Renamed", standupId: 3 });
  });

  it("a delete waits for the row's save in flight, then deletes the id that save returned", async () => {
    let release!: (s: SinkConfig) => void;
    save.mockImplementationOnce(() => new Promise<SinkConfig>((r) => (release = r)));
    await mount(3);
    type("Name", "Renamed");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(500);
    });
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(del).not.toHaveBeenCalled();
    await act(async () => {
      release({ ...SINK, id: 5 });
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(del).toHaveBeenCalledWith(5);
  });

  it("clears the secret field once the secret is saved, and does not send it again", async () => {
    await mount(3);
    type("Token", "s3cret");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(500);
    });
    expect(save.mock.calls[0]![0].secret).toBe("s3cret");
    expect((screen.getByLabelText("Token") as HTMLInputElement).value).toBe("");
    type("Folder", "/tmp/y");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(500);
    });
    expect(save.mock.calls[1]![0].secret).toBeNull();
  });
});
