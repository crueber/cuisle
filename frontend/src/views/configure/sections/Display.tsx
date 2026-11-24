// Configure > Display section (app-wide; ux-spec §9.4 Display, §13; architecture §6.4 app_setting keys).
// Every control saves at once through state/app.setAppSetting (string values). The only network call is the explicit
// Check now button (ADR 0016); the Updates row shows the version and the last check from state/update.
import type { Theme, TextSize } from "../../../api/types";
import { Segmented } from "../../../components/Segmented";
import { Stepper } from "../../../components/Stepper";
import { Switch } from "../../../components/Switch";
import { setAppSetting, settings } from "../../../state/app";
import { checkError, checkNow, checkingNow, updateStatus } from "../../../state/update";
import { ageText } from "../../standup/headerText";
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

/** "last checked 3h ago" / "not checked yet" for the Updates row (now from Date.now() at render). */
export function lastCheckedText(lastCheckedAt: number, now: number): string {
  return lastCheckedAt > 0 ? `last checked ${ageText(now, lastCheckedAt)} ago` : "not checked yet";
}

function UpdatesField() {
  const u = updateStatus.value;
  const version = u.currentVersion || "dev";
  const daily = settings.value["update.check_daily"] !== "off";
  const busy = checkingNow.value;
  if (u.channel === "") return null; // local and dev builds never check (ADR 0016): no Updates row at all
  const failure = checkError.value || u.lastError;
  const result = failure
    ? failure
    : u.available
      ? `Cuisle ${u.available.version} is available: use Update in the standup header.`
      : u.lastCheckedAt > 0
        ? "You're up to date."
        : "";
  return (
    <div class="cfg-field">
      <span class="cfg-field-label">Updates</span>
      <div class="cfg-field-control disp-updates">
        <Switch
          id="disp-update-daily"
          label="Check for updates daily"
          description={`Once a day Cuisle asks ${u.channel === "github" ? "GitHub" : "Forgejo"} whether there's a newer release. Nothing about you or your Jira data is sent.`}
          checked={daily}
          onChange={(on) => setAppSetting("update.check_daily", on ? "on" : "off")}
        />
        <div class="disp-update-row">
          <span class="disp-update-line">
            Cuisle {version} · {lastCheckedText(u.lastCheckedAt, Date.now())}
          </span>
          <button
            type="button"
            class="disp-btn"
            disabled={busy}
            onClick={() => {
              void checkNow();
            }}
          >
            {busy ? "Checking…" : "Check now"}
          </button>
        </div>
        {result ? (
          <p class="disp-update-result" data-alert={failure ? "true" : "false"} role="status">
            {result}
          </p>
        ) : null}
      </div>
    </div>
  );
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

      <UpdatesField />
    </section>
  );
}
