// Implementation: WP-24. Person view (ux-spec §5): title row, axis row, groups in walk order, more bar.
// Owns the scroll position: the density and "below the fold" summary come from the layout functions (WP-13).
import { useEffect, useRef, useState } from "preact/hooks";
import { api, toAppError } from "../../api";
import { atEnd as isAtEnd, belowFold } from "../../layout/below";
import { chooseDensity, type GroupCount } from "../../layout/density";
import { legendMode, mainPadding, titleSizes, trackWidth } from "../../layout/responsive";
import { axisLabelMode } from "../../layout/track";
import { textScale } from "../../state/app";
import { showBanner } from "../../state/ui";
import "../../styles/person.css";
import { Legend } from "./Legend";
import { MoreBar } from "./MoreBar";
import type { PersonViewProps } from "./props";
import { TicketRow } from "./TicketRow";
import { TrackBackdrop } from "./TrackBackdrop";

/** The more bar sits over the bottom 36px of the visible list (ux-spec §5.6). */
const MORE_BAR_H = 36;
/** Header (64), axis row (30) and bottom padding (10) sit outside the scroll area. */
const FIXED_CHROME = 64 + 30 + 10;

function openIssue(key: string): void {
  api()
    .app.openIssue(key)
    .catch((e: unknown) => showBanner({ kind: "error", message: toAppError(e).message, dismissible: true }));
}

export function PersonView({ view, person, groups: allGroups, mainW, vh }: PersonViewProps) {
  // Only lanes that have tickets are shown (ux-spec §5.3).
  const groups = allGroups.filter((g) => g.tickets.length > 0);
  // vh and mainW are design px (the window divided by the text scale); `k` multiplies inline px back (ux-spec §11).
  const k = textScale.value;
  const tall = vh >= 800;
  // Scroll area height H (ux-spec §5.5 mockup formula): vh - 64 - padTop - titleH - 30 - 10.
  // mainPadding's top depends on vh only.
  const H = vh - FIXED_CHROME - mainPadding(0, vh).top - titleSizes(vh).personRow;
  const counts: GroupCount[] = groups.map((g) => ({ label: g.lane.label, count: g.tickets.length }));
  const { density, scroll } = chooseDensity(counts, H, view.options.rowsMode === "roomy" ? "roomy" : "fit");
  const keyCh = view.tickets.reduce((m, t) => Math.max(m, t.key.length), 0);
  const trackW = trackWidth(mainW) * k;
  const days = view.pulseDays;
  const labelAll = axisLabelMode(trackW / k, days) === "all";

  const scrollRef = useRef<HTMLDivElement>(null);
  const frame = useRef<number | null>(null);
  const [pos, setPos] = useState({ top: 0, visibleH: H * k, end: false });

  const measure = () => {
    const el = scrollRef.current;
    if (!el) return;
    setPos({
      top: el.scrollTop,
      visibleH: el.clientHeight > 0 ? el.clientHeight : H * k,
      end: isAtEnd(el.scrollTop, el.clientHeight, el.scrollHeight),
    });
  };
  // Scroll handling is throttled to one animation frame.
  const onScroll = () => {
    if (frame.current !== null) return;
    frame.current = requestAnimationFrame(() => {
      frame.current = null;
      measure();
    });
  };
  useEffect(() => {
    measure();
  }, [allGroups, density.step, vh, k]);
  // A new speaker or standup starts at the top (the scroller is reused, so its scrollTop would carry over).
  useEffect(() => {
    if (scrollRef.current) scrollRef.current.scrollTop = 0;
    measure();
  }, [view.standupId, person.id]);
  // The screen sits under display:none while Configure is open (0 px tall); measure again when it comes back.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(onScroll);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  useEffect(
    () => () => {
      if (frame.current !== null) cancelAnimationFrame(frame.current);
    },
    [],
  );

  const below = belowFold(counts, density, (pos.visibleH - MORE_BAR_H * k) / k, pos.top / k);
  const side = density.side;

  return (
    <div class={`person-view${tall ? " is-tall" : ""}`}>
      <div class="person-title">
        <div class="person-title-text">
          <h1 class="person-name">{person.name}</h1>
          <p class="person-counts">{`${person.active} active · ${person.done} done`}</p>
          {density.step !== "roomy" ? <span class="person-density-tag">{density.label}</span> : null}
        </div>
        <Legend mode={legendMode(mainW)} options={view.options} lanes={view.lanes} />
      </div>

      <div class="person-axis">
        <span class="person-axis-label">{`last ${days} days`}</span>
        <div class="person-axis-track" style={`width:${trackW}px;--days:${days}`}>
          {view.axis.map((d, i) =>
            labelAll || d.monday ? (
              <span
                key={d.date}
                class="person-axis-day"
                data-axis-label=""
                data-axis-kind={d.monday ? "monday" : d.weekend ? "weekend" : "day"}
                style={`--x:${i}`}
              >
                {d.label}
              </span>
            ) : null,
          )}
        </div>
        <span class="person-axis-spacer" aria-hidden="true" />
      </div>

      <div class="person-scroll" ref={scrollRef} onScroll={onScroll}>
        <div class="person-body">
          <TrackBackdrop axis={view.axis} width={trackW} shadeWeekends={view.options.shadeWeekends} todayLine={3} />
          {groups.map((g) => {
            const isDone = g.lane.lane === "done";
            return (
              <section
                key={g.lane.lane}
                role="group"
                aria-label={g.lane.label}
                data-heading={side ? "side" : "stacked"}
                class={`person-group${side ? " is-side" : ""}${isDone ? " is-done" : ""}`}
              >
                <div class="person-group-head" style={`height:${(side ? density.row : density.headCost) * k}px`}>
                  <h2>
                    {g.lane.label} <span class="person-group-count">{g.tickets.length}</span>
                  </h2>
                </div>
                <div class="person-group-rows">
                  {g.tickets.map((t) => (
                    <TicketRow
                      key={t.key}
                      ticket={t}
                      density={density}
                      trackW={trackW}
                      keyCh={keyCh}
                      days={days}
                      options={view.options}
                      onOpen={openIssue}
                    />
                  ))}
                </div>
              </section>
            );
          })}
        </div>
        {groups.length === 0 ? <p class="person-empty">Nothing on the board for this person.</p> : null}
        <MoreBar visible={scroll} atEnd={pos.end} summary={below.summary} />
      </div>
    </div>
  );
}
