// FROZEN SIGNATURES (architecture §4.3). Implementation: WP-15. Module-level signals; no providers.
import { batch, computed, signal } from "@preact/signals";
import { api } from "../api/index";
import type { Bootstrap, BuildInfo, Connection, StandupTab, Theme, TextSize } from "../api/types";
import { enqueue } from "./save";
import { initialRoute, route } from "./route";
import { syncStatus } from "./sync";
import { seedBoardGroups, views } from "./standup";
import { currentZoom, seedZooms } from "./zoom";

export const settings = signal<Record<string, string>>({});
export const connection = signal<Connection | null>(null);
export const standups = signal<StandupTab[]>([]); // live standups in rail order (drafts included; tabs filter configured)
export const build = signal<BuildInfo | null>(null);
export const recovered = signal(false); // DB recovered → settings-loss banner
/** The text scale (--scale in tokens.css: Large/TV = 1.25) that layout maths must divide the window size by. */
export const textScale = computed(() => (settings.value.text_size === "large" ? 1.25 : 1));
/** The standup screen content scale (ux-spec §16.1): the text scale times the standup's zoom. The header keeps textScale. */
export const contentScale = computed(() => textScale.value * (currentZoom() / 100));

/** Seeds every store from the boot object (window.__CUISLE_BOOT__ or AppService.Bootstrap()). */
export function initFromBoot(boot: Bootstrap): void {
  const s: Record<string, string> = {};
  for (const [k, v] of Object.entries(boot.settings ?? {})) if (v !== undefined) s[k] = v;
  batch(() => {
    settings.value = s;
    connection.value = boot.connection;
    standups.value = boot.standups;
    build.value = boot.build;
    recovered.value = boot.recovered;
    views.value = boot.view ? new Map([[boot.view.standupId, boot.view]]) : new Map();
    syncStatus.value = new Map(boot.sync.map((st) => [st.standupId, st]));
    seedBoardGroups(boot.ui?.boardGroup);
    seedZooms(boot.ui?.zoom);
    applyAppearance(themeOf(s), textSizeOf(s));
    route.value = initialRoute(boot);
  });
}

const configuredTabs = computed(() => standups.value.filter((t) => t.configured).sort((a, b) => a.position - b.position));

/** Configured standups (tabs and 1–9 shortcuts), in position order. */
export function tabStandups(): StandupTab[] {
  return configuredTabs.value;
}

function themeOf(s: Record<string, string>): Theme {
  const t = s.theme;
  return t === "light" || t === "dark" ? t : "system";
}

function textSizeOf(s: Record<string, string>): TextSize {
  return s.text_size === "large" ? "large" : "standard";
}

// ── appearance ──────────────────────────────────────────────────────────────────────────────────
let wantedTheme: Theme = "system";
let systemQuery: MediaQueryList | null = null;

function resolvedTheme(): "light" | "dark" {
  if (wantedTheme !== "system") return wantedTheme;
  return systemQuery?.matches ? "dark" : "light";
}

function paintTheme(): void {
  document.documentElement.dataset.theme = resolvedTheme();
}

/** Watches the OS scheme once; the listener only repaints when the theme is "system". */
function watchSystemTheme(): void {
  if (systemQuery || typeof window.matchMedia !== "function") return;
  systemQuery = window.matchMedia("(prefers-color-scheme: dark)");
  systemQuery.addEventListener("change", () => {
    if (wantedTheme === "system") paintTheme();
  });
}

/** Sets <html data-theme> (system → matchMedia, kept in sync with OS changes) and <html data-text>. */
export function applyAppearance(theme: Theme, textSize: TextSize): void {
  wantedTheme = theme;
  if (theme === "system") watchSystemTheme();
  document.documentElement.dataset.text = textSize;
  paintTheme();
}

/** Autosaves an app setting (key app:<key>, immediate) and applies theme/text size to <html> at once. */
export function setAppSetting(key: string, value: string): void {
  settings.value = { ...settings.value, [key]: value };
  if (key === "theme" || key === "text_size") applyAppearance(themeOf(settings.value), textSizeOf(settings.value));
  enqueue({ key: `app:${key}`, run: () => api().app.setAppSetting(key, value) });
}

/** Re-reads StandupService.List into `standups`. */
export async function refreshStandups(): Promise<void> {
  standups.value = await api().standup.list();
}
