// Test helper: a command context with sensible defaults (no signals read).
import { NO_UPDATE } from "../state/update";
import type { StandupTab } from "../api/types";
import type { Ctx } from "./types";

export const tab = (id: number, name: string, spaceKey: string, position: number): StandupTab => ({
  id,
  rev: 1,
  name,
  spaceKey,
  position,
  activeCount: 0,
  speakerCount: 0,
  configured: true,
  needsSync: false,
  lastSyncedAt: 0,
});

export function makeCtx(over: Partial<Ctx> = {}): Ctx {
  return {
    route: { view: "standup", standupId: 1, person: null },
    standupId: 1,
    onStandup: true,
    onBoard: true,
    view: null,
    tabs: [tab(1, "MOB", "MOB", 0), tab(2, "WEB", "WEB", 1), tab(3, "DATA", "DATA", 2)],
    group: "assignee",
    sync: undefined,
    syncText: "synced 1 min ago",
    theme: "system",
    textSize: "standard",
    resolvedTheme: "light",
    update: NO_UPDATE,
    authInvalid: false,
    ticket: null,
    ui: { setMode: () => undefined },
    ...over,
  };
}
