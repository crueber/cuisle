// TicketHeader: the modal's sticky header (ux-spec §14.3) with the Refresh states (§14.12).
// Row 1: type icon, key (click copies), parent crumb, meta text, stepper, Refresh, Open in Jira, close.
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
export function trackWidth(modalW: number): number {
  return modalW >= 1320 ? 600 : modalW >= 1216 ? 440 : 300;
}

export function TicketHeader(props: TicketHeaderProps) {
  const { detail, geometry, position, backKey, refresh, onPrev, onNext, onBack, onRefresh, onOpenInJira, onOpenParent, onCopyKey, onClose } = props;
  const { row, parent } = detail;
  const k = textScale.value;
  const wide = geometry.modalW >= 1216; // below 1280 the parent name drops and Refresh / Open in Jira go icon-only
  const busy = refresh.phase === "refreshing" || refresh.phase === "waiting";
  const refreshingText = useAfter(refresh.phase === "refreshing", 250);
  const days = Math.max(1, row.daysInLane);
  const alert = row.overLimit || row.blocked;
  const w = trackWidth(geometry.modalW);

  let meta: { text: string; alert: boolean };
  if (refresh.phase === "failed") meta = { text: `Couldn't refresh · ${refresh.reason}`, alert: true };
  else if (refresh.phase === "waiting") meta = { text: `waiting for Jira (rate limit) · ${seconds(refresh.waitMs)}`, alert: false };
  else if (refresh.phase === "done" && refresh.at > 0) meta = { text: `refreshed ${clock(refresh.at)} · just this ticket`, alert: false };
  else meta = { text: detail.syncedAt > 0 ? `as of ${clock(detail.syncedAt)} · synced` : "not synced yet", alert: false };

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
        {parent ? (
          <button type="button" class="th-parent" onClick={onOpenParent}>
            <span class="th-in">in</span>
            <Icon name="spike" size={14} class="th-parent-icon" />
            <span class="th-parent-key">{parent.key}</span>
            {wide ? <span class="th-parent-name">{parent.summary}</span> : null}
          </button>
        ) : null}
        <span class="th-spacer" />
        <span class="th-meta" data-alert={meta.alert ? "true" : "false"}>
          {meta.text}
        </span>
        {refreshingText ? <span class="th-meta">refreshing…</span> : null}
        <span class="th-stepper">
          <button type="button" class="th-btn th-step" aria-label="Previous ticket" disabled={position.index <= 1} onClick={onPrev}>
            <Icon name="chevron-down" size={18} class="th-chev th-chev-prev" />
          </button>
          <span class="th-pos">{`${position.index} / ${position.total}`}</span>
          <button type="button" class="th-btn th-step" aria-label="Next ticket" disabled={position.index === 0 || position.index >= position.total} onClick={onNext}>
            <Icon name="chevron-down" size={18} class="th-chev th-chev-next" />
          </button>
        </span>
        <button
          type="button"
          class="th-btn th-action th-refresh"
          aria-label="Refresh this ticket"
          aria-busy={busy ? "true" : undefined}
          disabled={busy || refresh.cooldownMs > 0}
          onClick={onRefresh}
        >
          <Icon name="resync" size={18} class="th-spin" />
          {wide ? <span>Refresh</span> : null}
          {refresh.cooldownMs > 0 ? <span class="th-cooldown">{seconds(refresh.cooldownMs)}</span> : null}
        </button>
        <button type="button" class="th-btn th-action" aria-label="Open in Jira" onClick={onOpenInJira}>
          <Icon name="board" size={18} />
          {wide ? <span>Open in Jira</span> : null}
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
