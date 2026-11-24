// TicketHeader: the modal's sticky header (ux-spec §14.3) with the Refresh states (§14.12).
// Row 1: type icon, key (click copies), then ⚡︎H:mm (tooltip has the detail), Refresh, an icon-only Jira button, close.
// No stepper (J/K step from the keyboard) and no parent crumb (the parent lives in the field rail).
// Row 2: the summary (wraps, never ellipsised). Row 3: lane name, the 21-day pulse track, days chip, limit.
import { Icon } from "../../components/Icon";
import { textScale } from "../../state/app";
import { PulseTrack } from "../standup/PulseTrack";
import { TrackBackdrop } from "../standup/TrackBackdrop";
import { TypeIcon } from "../standup/TicketRow";
import type { TicketHeaderProps } from "./props";
import { clock, seconds, useAfter } from "./TicketModal.util";
import "./TicketHeader.css";

/** Track widths of ux-spec §14.3 at the four table geometries (modal 1320 / 1240 / 1216 / 1000 wide). */
/** U+26A1 followed by U+FE0E so it draws as a text glyph, not the colour emoji. */
const BOLT = "\u26A1\uFE0E";

export function trackWidth(modalW: number): number {
  return modalW >= 1320 ? 600 : modalW >= 1216 ? 440 : 300;
}

export function TicketHeader(props: TicketHeaderProps) {
  const { detail, geometry, backKey, refresh, onBack, onRefresh, onOpenInJira, onCopyKey, onClose } = props;
  const { row } = detail;
  const k = textScale.value;
  const wide = geometry.modalW >= 1216; // below 1280 Refresh goes icon-only
  const busy = refresh.phase === "refreshing" || refresh.phase === "waiting";
  const refreshingText = useAfter(refresh.phase === "refreshing", 250);
  const days = Math.max(1, row.daysInLane);
  const alert = row.overLimit || row.blocked;
  const w = trackWidth(geometry.modalW);

  // Compact "⚡︎9:41" (U+26A1 + U+FE0E, text presentation); the detail is the tooltip. Failures and waits stay spelled out.
  const synced = detail.syncedAt > 0 ? clock(detail.syncedAt) : "";
  let meta: { text: string; alert: boolean; tip?: string };
  if (refresh.phase === "failed") meta = { text: `Couldn't refresh · ${refresh.reason}`, alert: true };
  else if (refresh.phase === "waiting") meta = { text: `waiting for Jira (rate limit) · ${seconds(refresh.waitMs)}`, alert: false };
  else if (refresh.phase === "done" && refresh.at > 0)
    meta = {
      text: `${BOLT}${clock(refresh.at)}`,
      alert: false,
      tip: `Refreshed just this ticket at ${clock(refresh.at)}${synced ? ` · board synced ${synced}` : ""}`,
    };
  else if (synced) meta = { text: `${BOLT}${synced}`, alert: false, tip: `Synced at ${synced} (this ticket's data is as of that sync)` };
  else meta = { text: "not synced yet", alert: false };

  return (
    <div class="th" data-short={geometry.short ? "true" : "false"}>
      <div class="th-row1">
        {backKey !== "" ? (
          <button type="button" class="th-back" onClick={onBack}>
            {`← ${backKey}`}
          </button>
        ) : null}
        <TypeIcon type={row.type} />
        <button type="button" class="th-key" title="Copy key" onClick={onCopyKey}>
          {row.key}
        </button>
        <span class="th-spacer" />
        <span class="th-meta" data-alert={meta.alert ? "true" : "false"} title={meta.tip}>
          {meta.text}
        </span>
        {refreshingText ? <span class="th-meta">refreshing…</span> : null}
        <button
          type="button"
          class="th-btn th-action th-refresh"
          aria-label="Refresh this ticket"
          title="Refresh this ticket (R)"
          aria-busy={busy ? "true" : undefined}
          disabled={busy || refresh.cooldownMs > 0}
          onClick={onRefresh}
        >
          <Icon name="resync" size={18} class="th-spin" />
          {wide ? <span>Refresh</span> : null}
          {refresh.cooldownMs > 0 ? <span class="th-cooldown">{seconds(refresh.cooldownMs)}</span> : null}
        </button>
        <button type="button" class="th-btn th-jira" aria-label="Open in Jira" title="Open in Jira (O)" onClick={onOpenInJira}>
          <Icon name="external" size={18} />
        </button>
        <button type="button" class="th-btn th-close" aria-label="Close" onClick={onClose}>
          <Icon name="close" size={24} />
        </button>
      </div>

      <h2 class="th-summary">{row.summary}</h2>

      <div class="th-row3">
        <span class="th-lane">
          <span class="th-swatch" data-lane={row.lane} aria-hidden="true" />
          <span class="th-lane-name">{row.statusName}</span>
        </span>
        <span class="th-track" style={{ width: `${w * k}px` }}>
          {!geometry.short ? (
            <span class="th-mondays" aria-hidden="true">
              {detail.axis.map((d, i) =>
                d.monday ? (
                  <span class="th-monday" key={d.date} style={{ left: `${(i / detail.axis.length) * 100}%` }}>
                    {d.label}
                  </span>
                ) : null,
              )}
            </span>
          ) : null}
          <span class="th-track-box">
            <TrackBackdrop axis={detail.axis} width={w * k} shadeWeekends={true} todayLine={3} />
            <PulseTrack
              pulse={row.pulse}
              days={detail.pulseDays}
              width={w * k}
              rowH={geometry.short ? 28 : 26}
              options={{ showCommits: true, showPRs: true, showComments: true, devActivity: detail.dev ? "ok" : "unavailable" }}
            />
          </span>
        </span>
        <span class="th-days" data-alert={alert ? "true" : "false"}>{`${days}d`}</span>
        {row.blocked ? <span class="th-blocked">{detail.blockedDays > 0 ? `BLOCKED ${detail.blockedDays}D` : "BLOCKED"}</span> : null}
        <span class="th-spacer" />
        {detail.limitDays > 0 ? <span class="th-limit">{`limit ${detail.limitDays}d · ${detail.pulseDays} days`}</span> : null}
      </div>
    </div>
  );
}
