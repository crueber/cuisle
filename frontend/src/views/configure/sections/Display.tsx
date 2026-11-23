// Configure > Display section (app-wide; ux-spec §9.4 Display, §13; architecture §6.4 app_setting keys).
// Every control saves at once through state/app.setAppSetting (string values). Makes no network calls.
import type { Theme, TextSize } from "../../../api/types";
import { Segmented } from "../../../components/Segmented";
import { Stepper } from "../../../components/Stepper";
import { setAppSetting, settings } from "../../../state/app";
import type { AppSectionProps } from "../props";
import "./Display.css";

type SyncPolicy = "stale" | "always" | "never";

const THEMES: { value: Theme; label: string }[] = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "System" },
];
const TEXT_SIZES: { value: TextSize; label: string }[] = [
  { value: "standard", label: "Standard" },
  { value: "large", label: "Large (TV)" },
];
const POLICIES: { value: SyncPolicy; label: string }[] = [
  { value: "stale", label: "When stale" },
  { value: "always", label: "Always" },
  { value: "never", label: "Never" },
];

const DEFAULT_STALE_HOURS = 6;

function staleHours(raw: string | undefined): number {
  const n = Number.parseInt(raw ?? "", 10);
  if (Number.isNaN(n)) return DEFAULT_STALE_HOURS;
  return Math.min(48, Math.max(1, n));
}

export function DisplaySection(props: AppSectionProps) {
  void props;
  const s = settings.value;
  const theme: Theme = s.theme === "light" || s.theme === "dark" ? s.theme : "system";
  const textSize: TextSize = s.text_size === "large" ? "large" : "standard";
  const policy: SyncPolicy = s.sync_on_open === "always" || s.sync_on_open === "never" ? s.sync_on_open : "stale";

  return (
    <section class="cfg-section disp" aria-labelledby="disp-title">
      <header class="cfg-section-head">
        <h2 id="disp-title" class="cfg-section-title">
          Display
        </h2>
        <span class="cfg-section-sub">App-wide · saved as you change it</span>
      </header>

      <div class="cfg-field">
        <span class="cfg-field-label">Theme</span>
        <div class="cfg-field-control">
          <Segmented<Theme> id="disp-theme" label="Theme" value={theme} options={THEMES} onChange={(v) => setAppSetting("theme", v)} />
        </div>
      </div>

      <div class="cfg-field">
        <span class="cfg-field-label">Text size</span>
        <div class="cfg-field-control">
          <Segmented<TextSize>
            id="disp-text-size"
            label="Text size"
            value={textSize}
            options={TEXT_SIZES}
            onChange={(v) => setAppSetting("text_size", v)}
          />
        </div>
      </div>

      <div class="cfg-field">
        <span class="cfg-field-label">Sync on open</span>
        <div class="cfg-field-control">
          <Segmented<SyncPolicy>
            id="disp-sync-on-open"
            label="Sync on open"
            value={policy}
            options={POLICIES}
            onChange={(v) => setAppSetting("sync_on_open", v)}
          />
          {policy === "stale" ? (
            <Stepper
              id="disp-stale-hours"
              label="Stale after"
              value={staleHours(s.sync_stale_hours)}
              min={1}
              max={48}
              suffix="h"
              onChange={(v) => setAppSetting("sync_stale_hours", String(v))}
            />
          ) : null}
        </div>
      </div>
    </section>
  );
}
