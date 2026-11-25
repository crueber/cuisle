// Command usage counts (F14, ux-spec §15.7): app setting palette.command_usage, a JSON object of command id to count.
// They rank the empty palette's suggestions and break ties between equal fuzzy scores.
import { setAppSetting, settings } from "../state/app";
import type { Usage } from "./registry";

const USAGE_KEY = "palette.command_usage";

/** The saved counts; a missing or corrupt setting reads as none. */
export function readUsage(): Usage {
  try {
    const v: unknown = JSON.parse(settings.value[USAGE_KEY] || "{}");
    if (typeof v !== "object" || v === null || Array.isArray(v)) return {};
    const out: Record<string, number> = {};
    for (const [k, n] of Object.entries(v)) if (typeof n === "number" && Number.isFinite(n) && n > 0) out[k] = Math.floor(n);
    return out;
  } catch {
    return {};
  }
}

/** Counts one run of a command and saves the setting. Nested choices count under their own id. */
export function bumpUsage(id: string): void {
  const u = { ...readUsage() };
  u[id] = (u[id] ?? 0) + 1;
  setAppSetting(USAGE_KEY, JSON.stringify(u));
}
