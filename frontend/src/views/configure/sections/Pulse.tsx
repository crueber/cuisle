// Implementation: WP-27. Props are frozen in props.ts.
// Configure > Pulse (ux-spec §9.4 Pulse, §9.3 ordered lists). Mutations go through state/config.ts.
import type { Pulse, StandupView, WalkLane } from "../../../api/types";
import { useEffect, useRef, useState } from "preact/hooks";
import { Segmented } from "../../../components/Segmented";
import { Stepper } from "../../../components/Stepper";
import { Switch } from "../../../components/Switch";
import { connection } from "../../../state/app";
import { saveField, setLaneLimit } from "../../../state/config";
import { FieldError } from "../FieldError";
import { views } from "../../../state/standup";
import { PulseTrack } from "../../standup/PulseTrack";
import { TrackBackdrop } from "../../standup/TrackBackdrop";
import type { SectionProps } from "../props";
import "./Pulse.css";

const DAY_OPTIONS = [7, 14, 21, 28] as const;
const PREVIEW_WIDTH = 640; // px
const PREVIEW_ROW_H = 28; // px

/** Shown when the standup has never synced or its preview key is not among the tickets. */
const SAMPLE_PULSE: Pulse = {
  label: "Sample ticket",
  segs: [
    { lane: "prog", from: 0, to: 3 },
    { lane: "review", from: 3, to: 5 },
    { lane: "qa", from: 5, to: 6 },
  ],
  marks: [{ day: 1, commits: 2, prs: 1, comments: 0 }],
  blocked: [],
};

function isWalkLane(lane: string): lane is WalkLane {
  return lane === "todo" || lane === "prog" || lane === "review" || lane === "qa";
}

function previewPulse(view: StandupView | undefined): Pulse {
  if (!view || view.previewKey === "") return SAMPLE_PULSE;
  const ticket = view.tickets.find((t) => t.key === view.previewKey);
  return ticket ? ticket.pulse : SAMPLE_PULSE;
}

export function PulseSection({ config }: SectionProps) {
  const view = views.value.get(config.id);
  // The preview is as wide as its column (no horizontal scroll). PREVIEW_WIDTH is the fallback before measuring.
  const trackRef = useRef<HTMLDivElement>(null);
  const [previewW, setPreviewW] = useState(PREVIEW_WIDTH);
  useEffect(() => {
    const el = trackRef.current;
    if (!el) return;
    const measure = () => {
      const w = Math.floor(el.clientWidth);
      if (w > 0) setPreviewW(w);
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const devActivity = connection.value?.devActivity ?? "unknown";
  const walk = config.lanes.flatMap((l) => (isWalkLane(l.lane) ? [{ ...l, lane: l.lane }] : []));
  const axis = view && view.axis.length === config.pulseDays ? view.axis : [];

  return (
    <section class="cfg-section pulse" aria-labelledby="cfg-h-pulse">
      <header class="cfg-section-head">
        <h2 id="cfg-h-pulse" class="cfg-section-title">Pulse</h2>
        <span class="cfg-section-sub">the timeline track on every ticket</span>
      </header>

      {devActivity === "unavailable" && (
        <p class="pulse-warning" role="status">
          Commit and PR marks unavailable — Jira's development info isn't reachable for this site.
        </p>
      )}

      <div class="cfg-field">
        <span class="cfg-field-label">Days shown</span>
        <div class="cfg-field-control">
          <Segmented
            id="pulse-days"
            label="Days shown"
            value={String(config.pulseDays)}
            options={DAY_OPTIONS.map((n) => ({ value: String(n), label: String(n) }))}
            onChange={(v) => saveField("pulseDays", Number(v))}
          />
        </div>
      </div>

      <div class="cfg-field cfg-field-stack">
        <span class="cfg-field-label">
          Live preview
          <span class="cfg-hint">last {config.pulseDays} days → today</span>
        </span>
        <div class="pulse-preview-track" ref={trackRef}>
          <TrackBackdrop axis={axis} width={previewW} shadeWeekends={config.shadeWeekends} todayLine={3} />
          <PulseTrack
            pulse={previewPulse(view)}
            days={config.pulseDays}
            width={previewW}
            rowH={PREVIEW_ROW_H}
            options={{
              showCommits: config.showCommits,
              showPRs: config.showPRs,
              showComments: config.showComments,
              devActivity,
            }}
          />
        </div>
      </div>

      <div class="cfg-field cfg-field-stack">
        <span class="cfg-field-label">Marks</span>
        <div class="cfg-field-control pulse-marks">
          <Switch id="pulse-commits" label="Commit marks" checked={config.showCommits} onChange={(v) => saveField("showCommits", v)} />
          <Switch id="pulse-prs" label="PR marks" checked={config.showPRs} onChange={(v) => saveField("showPRs", v)} />
          <Switch id="pulse-comments" label="Comment marks" checked={config.showComments} onChange={(v) => saveField("showComments", v)} />
        </div>
      </div>

      <div class="cfg-field cfg-field-stack">
        <span class="cfg-field-label">
          Status limits
          <span class="cfg-hint">days before the counter turns red</span>
        </span>
        <div class="pulse-limits">
          {walk.map((l) => (
            <div key={l.lane} class="pulse-limit">
              <span class="pulse-limit-head">
                <span class="cfg-swatch" data-lane={l.lane} aria-hidden="true" />
                <span class="pulse-limit-name">{l.label}</span>
              </span>
              <Stepper
                id={`pulse-limit-${l.lane}`}
                label={`${l.label} limit`}
                value={l.limitDays}
                min={0}
                max={99}
                suffix="d"
                zeroLabel="none"
                onChange={(n) => setLaneLimit(l.lane, n)}
              />
              <FieldError standupId={config.id} field={`limit:${l.lane}`} />
            </div>
          ))}
        </div>
      </div>

      <div class="cfg-field">
        <span class="cfg-field-label">Weekends</span>
        <div class="cfg-field-control">
          <Segmented<"shade" | "plain">
            id="pulse-weekends"
            label="Weekends"
            value={config.shadeWeekends ? "shade" : "plain"}
            options={[
              { value: "shade", label: "Shade" },
              { value: "plain", label: "Plain" },
            ]}
            onChange={(v) => saveField("shadeWeekends", v === "shade")}
          />
        </div>
      </div>
    </section>
  );
}
