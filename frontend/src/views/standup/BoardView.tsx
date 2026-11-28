// Whole-board screen body (ux-spec §6). Go computes the data; this file only arranges it (ADR 0008).
// Grouped by Assignee (§6.2–6.4, the default) or by Status (§6.5, F13).
import "../../styles/person.css";
import "../../styles/board.css";
import { useEffect, useRef, useState } from "preact/hooks";
import type { SpeakerView, StandupView, TicketView, ViewOptions } from "../../api/types";
import { Segmented } from "../../components/Segmented";
import {
  boardColumns,
  boardTitleWidth,
  doneBacklogLayout,
  laneRowHeight,
  listSectionHeight,
  splitLanes,
  statusLayout,
  statusSections,
  type BoardGroup,
  type LaneInput,
  type StatusSection,
} from "../../layout/board";
import { atEnd as isAtEnd, boardBelow, statusBelow, type BoardSection, type StatusBelowSection } from "../../layout/below";
import { boardLegendMode, legendExtra, railMode } from "../../layout/responsive";
import { contentScale } from "../../state/app";
import { boardGroupOf, buildBoardLanes, setBoardGroup, type BoardLane } from "../../state/standup";
import { BacklogList } from "./BacklogList";
import { DoneList } from "./DoneList";
import { Legend } from "./Legend";
import { MoreBar } from "./MoreBar";
import { PulseTrack } from "./PulseTrack";
import { StatusSectionHeader } from "./StatusSectionHeader";
import { TypeIcon } from "./TicketRow";
import { ZoomControl, ZOOM_CONTROL_PX } from "./ZoomControl";
import type { BoardViewProps } from "./props";
import { TrackBackdrop } from "./TrackBackdrop";
import { axisMondays, laneTrackWidth, realTrackWidth, ROW_ASSIGNEE_PX, ROW_GAP_PX, ROW_ICON_PX } from "./BoardView.geometry";
import { openIssue } from "./BoardView.rows";

/** Vertical space the lanes must leave free (title row, axis row, paddings), in CSS px. */
const LANE_CHROME_H = 180;
/** The more bar sits over the bottom 36px of the visible board (ux-spec §6.4). */
const MORE_BAR_H = 36;
/** Rough px width of one mono key character, for the title-width rule. */
const KEY_CH_PX = 8;
/** A status-mode column narrower than this lists Done and Backlog in one grid column instead of two. */
const TWO_GRID_COLS_MIN_W = 480;

type LaneBox = BoardLane;

const isUnassigned = (s: SpeakerView) => s.unassigned || s.id === "unassigned";

/** Nearest ancestor that scrolls vertically (the standup main area in the app). */
function scrollerOf(el: HTMLElement): HTMLElement | null {
  for (let p = el.parentElement; p; p = p.parentElement) {
    const overflow = getComputedStyle(p).overflowY;
    if (overflow === "auto" || overflow === "scroll") return p;
  }
  return null;
}

function subtitleHead(view: StandupView): string {
  if (view.boardType === "kanban") return "Kanban · continuous flow";
  if (view.sprint) return `${view.sprint.name} · ${view.sprint.endsLabel}`;
  return view.boardName;
}

interface RowProps {
  t: TicketView;
  k: number; // text scale: inline px are design px times this
  rowH: number;
  trackW: number;
  pulseDays: number;
  options: ViewOptions;
  /** Status mode only (§6.5): the assignee column, and BLOCKED after the title. */
  who?: { initials: string; name: string; none: boolean };
}

function BoardRow({ t, k, rowH, trackW, pulseDays, options, who }: RowProps) {
  const days = Math.max(1, t.daysInLane);
  const alert = t.overLimit || t.blocked;
  return (
    <button type="button" class={who ? "board-row board-row--status" : "board-row"} style={`height:${rowH * k}px`} onClick={() => openIssue(t.key)}>
      <TypeIcon type={t.type} />
      <span class="board-row-key">{t.key}</span>
      {who ? (
        <span class="board-row-title board-row-title--status">
          <span class="board-row-title-text" title={t.summary}>{t.summary}</span>
          {t.blocked ? <span class="ticket-blocked">BLOCKED</span> : null}
        </span>
      ) : (
        <span class="board-row-title">{t.summary}</span>
      )}
      {who ? (
        <span class={who.none ? "board-assignee board-assignee--none" : "board-assignee"} role="img" title={who.name} aria-label={who.name}>
          {who.initials}
        </span>
      ) : null}
      <PulseTrack pulse={t.pulse} days={pulseDays} width={trackW} rowH={rowH} lane options={options} scale={k} />
      <span class="board-days" data-alert={String(alert)}>{`${days}d`}</span>
    </button>
  );
}

interface LaneProps {
  box: LaneBox;
  k: number;
  rowH: number;
  trackW: number;
  pulseDays: number;
  options: ViewOptions;
  onSelect: (id: string) => void;
}

function Lane({ box, k, rowH, trackW, pulseDays, options, onSelect }: LaneProps) {
  const { speaker, tickets } = box;
  const unassigned = isUnassigned(speaker);
  return (
    <section role="group" aria-label={speaker.name} class="board-lane" data-anchor={`lane:${speaker.id}`}>
      <button
        type="button"
        class={unassigned ? "board-lane-head board-lane-head--unassigned" : "board-lane-head"}
        data-lane-first={speaker.first}
        onClick={() => onSelect(speaker.id)}
      >
        <span class="board-lane-name">{speaker.name}</span>
        {unassigned ? (
          <span class="board-lane-alert" data-alert="true">{`${tickets.length} need an owner`}</span>
        ) : (
          <span class="board-lane-meta">{`${tickets.length} active · ${speaker.done} done`}</span>
        )}
        <span class={unassigned ? "board-lane-rule board-lane-rule--dashed" : "board-lane-rule"} aria-hidden="true" />
      </button>
      {tickets.map((t) => (
        <BoardRow key={t.key} t={t} k={k} rowH={rowH} trackW={trackW} pulseDays={pulseDays} options={options} />
      ))}
    </section>
  );
}

/** One status section (§6.5): the header, then its rows in the sorted order. */
function StatusLane({ section, compact, colW, speakerMap, ...row }: { section: StatusSection; compact: boolean; colW: number; speakerMap: Map<string, SpeakerView> } & Omit<RowProps, "t" | "who">) {
  return (
    <section role="group" aria-label={section.label} class="board-lane board-sec" data-sec="lane" data-sec-label={section.label} data-anchor={`sec:${section.lane}`}>
      <StatusSectionHeader section={section} compact={compact} colW={colW} />
      {section.tickets.map((t) => {
        const s = t.assignee === "" ? undefined : speakerMap.get(t.assignee);
        const who = t.assignee === "" ? { initials: "··", name: "Unassigned", none: true } : { initials: s?.initials ?? "··", name: s?.name ?? t.assignee, none: false };
        return <BoardRow key={t.key} t={t} who={who} {...row} />;
      })}
    </section>
  );
}

type SeqItem = { kind: "lane"; section: StatusSection } | { kind: "done" } | { kind: "backlog" };

export function BoardView(props: BoardViewProps) {
  const { view, mainW, vh, onSelectPerson } = props;
  // mainW and vh are design px (window / content scale: text size × zoom); k multiplies the inline px back (ux-spec §11, §16).
  const k = contentScale.value;
  const cols = boardColumns(mainW);
  const group: BoardGroup = boardGroupOf(view.standupId);
  const status = group === "status";
  // Below 1360 (the compact rail) the status headers shorten their wording (§6.5).
  const compact = props.vw !== undefined && railMode(props.vw) === "compact";

  const keyCh = (view.tickets ?? []).reduce((m, t) => Math.max(m, t.key.length), 7);
  const colW = (mainW - 40 * (cols - 1)) / cols;
  const keyPx = keyCh * KEY_CH_PX;
  const titleW = boardTitleWidth(mainW, colW, keyPx) * k;
  const counts = view.counts;
  const doneTickets = (view.tickets ?? []).filter((t) => t.lane === "done");
  const backlog = view.backlog ?? [];
  const bottom = doneBacklogLayout(mainW);
  const speakers = view.speakers ?? [];
  const speakerMap = new Map(speakers.map((s) => [s.id, s] as const));

  // The track takes whatever the row has left; the axis runs over the same span (plus the gap before the chip).
  const trackW = status
    ? realTrackWidth(laneTrackWidth(colW, keyPx, titleW / k, ROW_ASSIGNEE_PX), k, 5)
    : realTrackWidth(laneTrackWidth(colW, keyPx, titleW / k), k);
  const mondays = axisMondays(view.axis, (trackW + ROW_GAP_PX) / k);
  const trackLeft = `calc(${ROW_ICON_PX * k}px + var(--key-w) + var(--title-w) + ${3 * ROW_GAP_PX}px + var(--assignee-w, 0px))`;

  // Assignee mode: the lanes cut, in speaking order, into columns. Status mode: the sections, Done and Backlog as one sequence cut once.
  let rowH: number;
  let assigneeColumns: LaneBox[][] = [];
  let statusColumns: SeqItem[][] = [];
  let doneGridCols = 1;
  if (status) {
    const sections = statusSections(view);
    doneGridCols = colW >= TWO_GRID_COLS_MIN_W ? 2 : 1;
    const seq: SeqItem[] = [...sections.map((section): SeqItem => ({ kind: "lane", section })), { kind: "done" }];
    const lists = [listSectionHeight(doneTickets.length, doneGridCols)];
    if (backlog.length > 0) {
      seq.push({ kind: "backlog" });
      lists.push(listSectionHeight(backlog.length, doneGridCols, view.backlogTotal > backlog.length));
    }
    const layout = statusLayout(sections.map((s) => s.tickets.length), lists, cols, vh - LANE_CHROME_H);
    rowH = layout.rowH;
    statusColumns = layout.columns.map((idx) => idx.flatMap((i) => (seq[i] ? [seq[i] as SeqItem] : [])));
  } else {
    const lanes = buildBoardLanes(view);
    const inputs: LaneInput[] = lanes.map((l) => ({ id: l.speaker.id, rows: l.tickets.length, unassigned: isUnassigned(l.speaker) }));
    rowH = laneRowHeight(inputs, cols, vh - LANE_CHROME_H);
    const balanced = splitLanes(inputs, cols, rowH);
    const byId = new Map(lanes.map((l) => [l.speaker.id, l] as const));
    assigneeColumns = balanced.columns.map((col) =>
      col.flatMap((i) => {
        const box = byId.get(i.id);
        return box ? [box] : [];
      }),
    );
  }

  const rootRef = useRef<HTMLDivElement>(null);
  const [more, setMore] = useState({ visible: false, atEnd: false, summary: "" });
  // Scroll and resize are throttled to one animation frame. The scroll container is the nearest ancestor that scrolls.
  useEffect(() => {
    const root = rootRef.current;
    const scroller = root ? scrollerOf(root) : null;
    if (!root || !scroller) return;
    let frame: number | null = null;
    const measure = () => {
      const box = scroller.getBoundingClientRect();
      // Header bottom edge in content px (as belowFold uses row bottoms): a header the bar covers counts as below.
      const contentBottom = (node: Element) => node.getBoundingClientRect().bottom - box.top + scroller.scrollTop;
      const foldY = scroller.clientHeight - MORE_BAR_H * k;
      let summary: string;
      if (status) {
        // Status mode: every section in on-screen order. Lanes count their rows past the fold; Done and Backlog list by heading.
        const sections: StatusBelowSection[] = [];
        root.querySelectorAll<HTMLElement>("[data-sec]").forEach((el) => {
          if (el.dataset.sec === "lane") {
            sections.push({ label: el.dataset.secLabel ?? "", rows: Array.from(el.querySelectorAll(".board-row")).map(contentBottom) });
          } else {
            const head = el.querySelector(".board-list-title");
            if (head) sections.push({ label: el.dataset.secLabel ?? "", rows: [], top: contentBottom(head) });
          }
        });
        summary = statusBelow(sections, foldY, scroller.scrollTop).summary;
      } else {
        const sections: BoardSection[] = Array.from(root.querySelectorAll<HTMLElement>(".board-lane-head")).map((h) => ({
          label: h.dataset.laneFirst ?? "",
          top: contentBottom(h),
        }));
        root.querySelectorAll<HTMLElement>(".board-list-title").forEach((h) => {
          const title = (h.textContent ?? "").trim();
          if (title === "Done") sections.push({ label: `Done (${view.counts.done})`, top: contentBottom(h) });
          if (title === "Backlog") sections.push({ label: `Backlog (${view.counts.backlog})`, top: contentBottom(h) });
        });
        summary = boardBelow(sections, foldY, scroller.scrollTop).summary;
      }
      const next = {
        visible: scroller.scrollHeight > scroller.clientHeight,
        atEnd: isAtEnd(scroller.scrollTop, scroller.clientHeight, scroller.scrollHeight),
        summary,
      };
      setMore((prev) => (prev.visible === next.visible && prev.atEnd === next.atEnd && prev.summary === next.summary ? prev : next));
    };
    const schedule = () => {
      if (frame !== null) return;
      frame = requestAnimationFrame(() => {
        frame = null;
        measure();
      });
    };
    measure();
    scroller.addEventListener("scroll", schedule, { passive: true });
    window.addEventListener("resize", schedule);
    // The screen sits under display:none while Configure is open (0 px tall); measure again when it comes back.
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(schedule);
    ro?.observe(scroller);
    return () => {
      ro?.disconnect();
      scroller.removeEventListener("scroll", schedule);
      window.removeEventListener("resize", schedule);
      if (frame !== null) cancelAnimationFrame(frame);
    };
  }, [view, vh, mainW, k, group, props.vw]);

  const axis = (
    <div class="board-axis">
      <span class="board-axis-label">{`last ${view.pulseDays} days`}</span>
      <div class="board-axis-track" style={`--days:${view.axis.length}`}>
        {mondays.map((m) => (
          <span class="board-axis-mon" key={m.x} style={`--x:${m.x}`}>{m.text}</span>
        ))}
        <span class="board-axis-today">today</span>
      </div>
    </div>
  );
  const backdrop = (
    <div class="board-backdrop" aria-hidden="true">
      <TrackBackdrop axis={view.axis} width={trackW} shadeWeekends={view.options.shadeWeekends} todayLine={2} />
    </div>
  );
  const rowProps = { k, rowH, trackW, pulseDays: view.pulseDays, options: view.options };
  const colsStyle = `--cols:${cols};--key-w:calc(${keyCh}ch * var(--scale));--title-w:${titleW}px;--track-left:${trackLeft}${status ? `;--assignee-w:${ROW_ASSIGNEE_PX * k + ROW_GAP_PX}px` : ""}`;

  return (
    <div class="board" ref={rootRef}>
      <header class="board-head">
        <div class="board-title-block">
          <h1 class="board-title">Whole board</h1>
          <p class="board-sub">
            {subtitleHead(view)} · {counts.active} active · <span class="board-sub-alert" data-alert="true">{`${counts.unassigned} unassigned`}</span> · {counts.done} done · {view.backlogTotal >= 0 ? view.backlogTotal : counts.backlog} backlog
          </p>
        </div>
        <div class="board-controls">
          <div class="board-group">
            <Segmented<BoardGroup>
              toggle
              id="board-group"
              label="Group the board by"
              value={group}
              options={[
                { value: "assignee", label: "Assignee" },
                { value: "status", label: "Status" },
              ]}
              onChange={(g) => setBoardGroup(view.standupId, g)}
            />
            <kbd class="board-group-kbd" aria-hidden="true">G</kbd>
          </div>
          <ZoomControl note={cols === 1 ? "1 column" : `${cols} columns`} />
          {/* Board lane tracks draw no marks (§6.2), so the board never lists the commit / PR / comment keys: the short legend. */}
          <Legend mode={boardLegendMode(mainW, legendExtra(view.lanes ?? []), ZOOM_CONTROL_PX)} options={view.options} lanes={view.lanes ?? []} />
        </div>
      </header>

      <div class="board-cols" style={colsStyle}>
        {status
          ? statusColumns.map((col, i) => (
              <div class="board-col" data-column={String(i + 1)} key={i}>
                {axis}
                <div class="board-col-lanes">
                  {backdrop}
                  {col.map((item) => {
                    if (item.kind === "lane") {
                      return <StatusLane key={item.section.lane} section={item.section} compact={compact} colW={colW} speakerMap={speakerMap} {...rowProps} />;
                    }
                    if (item.kind === "done") {
                      return (
                        <div class="board-sec-list" key="done" data-sec="done" data-sec-label={`Done (${counts.done})`}>
                          <DoneList tickets={doneTickets} label={view.options.doneLabel} cols={doneGridCols} speakers={speakers} />
                        </div>
                      );
                    }
                    return (
                      <div class="board-sec-list" key="backlog" data-sec="backlog" data-sec-label={`Backlog (${counts.backlog})`}>
                        <BacklogList items={backlog} total={view.backlogTotal} rows={backlog.length} cols={doneGridCols} />
                      </div>
                    );
                  })}
                </div>
              </div>
            ))
          : assigneeColumns.map((col, i) => (
              <div class="board-col" data-column={String(i + 1)} key={i}>
                {axis}
                <div class="board-col-lanes">
                  {backdrop}
                  {col.map((box) => (
                    <Lane
                      key={box.speaker.id}
                      box={box}
                      k={k}
                      rowH={rowH}
                      trackW={trackW}
                      pulseDays={view.pulseDays}
                      options={view.options}
                      onSelect={onSelectPerson}
                    />
                  ))}
                </div>
              </div>
            ))}
      </div>

      {status ? null : (
        <div class={bottom.sideBySide ? "board-bottom" : "board-bottom board-bottom--stacked"}>
          <DoneList tickets={doneTickets} label={view.options.doneLabel} cols={bottom.doneCols} speakers={speakers} />
          <BacklogList items={backlog} total={view.backlogTotal} rows={backlog.length} cols={bottom.backlogCols} />
        </div>
      )}
      <MoreBar visible={more.visible} atEnd={more.atEnd} summary={more.summary} />
    </div>
  );
}
