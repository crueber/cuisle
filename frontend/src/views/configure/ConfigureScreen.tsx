// ConfigureScreen: frame, rail, jump bar, section layout, save indicator, Done/Esc (ux-spec §9.1, §9.2, §9.5;
// architecture §5.3; AGENTS.md invariants 3 and 4).
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { SavedIndicator } from "../../components/SavedIndicator";
import { toAppError } from "../../api/errors";
import { standups } from "../../state/app";
import { config, createStandup, deleteStandup, duplicateStandup, leaveConfigure, loadConfig } from "../../state/config";
import { goConfigure, route } from "../../state/route";
import { flush, retryFailed, saveState } from "../../state/save";
import { paletteOpen } from "../../state/palette";
import { keyLegendOpen, showBanner } from "../../state/ui";
import { updateDialogOpen } from "../../state/update";
import { configureColumns, mainPadding } from "../../layout/responsive";
import { useViewport } from "../useViewport";
import { JumpBar } from "./JumpBar";
import { StandupList } from "./StandupList";
import type { StandupConfig } from "../../api/types";
import type { AppSectionProps, SectionProps } from "./props";
import { ConnectionSection } from "./sections/Connection";
import { DisplaySection } from "./sections/Display";
import { ExportSection } from "./sections/Export";
import { PersonViewSection } from "./sections/PersonView";
import { PulseSection } from "./sections/Pulse";
import { SourceSection } from "./sections/Source";
import { StorageSection } from "./sections/Storage";
import { TicketFieldsSection } from "./sections/TicketFields";
import { SpeakersSection } from "./sections/Speakers";
import { StatusMappingSection } from "./sections/StatusMapping";
import { WholeBoardSection } from "./sections/WholeBoard";
import "../../styles/configure.css";

type SectionId =
  | "sec-source" | "sec-speakers" | "sec-person" | "sec-pulse" | "sec-board"
  | "sec-mapping" | "sec-export" | "sec-display" | "sec-storage" | "sec-connection" | "sec-ticket-fields";

const LABELS: Record<SectionId, string> = {
  "sec-source": "Source",
  "sec-speakers": "Speakers",
  "sec-person": "Person view",
  "sec-pulse": "Pulse",
  "sec-board": "Whole board",
  "sec-mapping": "Status mapping",
  "sec-export": "Export",
  "sec-display": "Display",
  "sec-storage": "Storage",
  "sec-connection": "Connection",
  "sec-ticket-fields": "Ticket fields",
};

const ORDER: SectionId[] = [
  "sec-source", "sec-speakers", "sec-person", "sec-pulse", "sec-board", "sec-mapping", "sec-export", "sec-display", "sec-storage", "sec-connection",
  "sec-ticket-fields",
];

/** Column membership per ux-spec §9.2 (column-major DOM order) until the sections are measured and balanced. */
const COLUMNS: Record<1 | 2 | 3, SectionId[][]> = {
  3: [
    ["sec-source", "sec-person", "sec-export"],
    ["sec-speakers", "sec-display", "sec-storage", "sec-connection"],
    ["sec-pulse", "sec-board", "sec-mapping", "sec-ticket-fields"],
  ],
  2: [
    ["sec-source", "sec-speakers", "sec-board", "sec-export"],
    ["sec-person", "sec-pulse", "sec-mapping", "sec-display", "sec-storage", "sec-connection", "sec-ticket-fields"],
  ],
  1: [ORDER],
};

const JUMP_SECTIONS = ORDER.map((id) => ({ id, label: LABELS[id] }));

/**
 * Balances sections across columns by measured height: tallest first, each into the currently shortest column (ties to
 * the leftmost), then each column keeps the usual ORDER top to bottom. Columns end within one section of each other.
 * Returns null when any height is unknown (0, e.g. before layout or in jsdom), so the static COLUMNS table applies.
 * `gap` is the vertical space between sections.
 */
export function balanceColumns(order: readonly SectionId[], heights: ReadonlyMap<string, number>, n: number, gap: number): SectionId[][] | null {
  if (n <= 1) return null;
  if (order.some((id) => !(heights.get(id) ?? 0))) return null;
  const rank = new Map(order.map((id, i) => [id, i]));
  const byHeight = [...order].sort((a, b) => (heights.get(b) ?? 0) - (heights.get(a) ?? 0) || (rank.get(a) ?? 0) - (rank.get(b) ?? 0));
  const cols: SectionId[][] = Array.from({ length: n }, () => []);
  const h = new Array<number>(n).fill(0);
  for (const id of byHeight) {
    let c = 0;
    for (let i = 1; i < n; i++) if ((h[i] ?? 0) < (h[c] ?? 0)) c = i;
    const col = cols[c] ?? [];
    h[c] = (h[c] ?? 0) + (col.length ? gap : 0) + (heights.get(id) ?? 0);
    col.push(id);
  }
  for (const col of cols) col.sort((a, b) => (rank.get(a) ?? 0) - (rank.get(b) ?? 0));
  // The column holding the first section in ORDER goes first, so Source stays top-left.
  cols.sort((a, b) => (rank.get(a[0] as SectionId) ?? 99) - (rank.get(b[0] as SectionId) ?? 99));
  return cols;
}

/** How uneven the columns must get (px) before a height change rebalances them. */
const REBALANCE_PX = 160;

/** Tallest column minus shortest column, for an assignment and measured section heights. */
function spread(cols: SectionId[][], heights: ReadonlyMap<string, number>): number {
  const hs = cols.map((c) => c.reduce((a, id, i) => a + (i ? SECTION_GAP : 0) + (heights.get(id) ?? 0), 0));
  return Math.max(...hs) - Math.min(...hs);
}

/** Vertical space between sections (--space-6, `.cfg-anchor + .cfg-anchor`). */
const SECTION_GAP = 24;

const same = (a: SectionId[][], b: SectionId[][]) => a.length === b.length && a.every((c, i) => c.join() === (b[i] ?? []).join());

/** Configure rail width (ux-spec §9.1): 280px at vw ≥ 1360, else 230px. */
function railWidth(vw: number): number {
  return vw >= 1360 ? 280 : 230;
}

function columnsFor(vw: number, vh: number): 1 | 2 | 3 {
  return configureColumns(vw - railWidth(vw) - 2 * mainPadding(vw, vh).x);
}

function renderSection(id: SectionId, columns: 1 | 2 | 3, cfg: StandupConfig) {
  const p: SectionProps = { config: cfg, columns };
  const app: AppSectionProps = { columns };
  switch (id) {
    case "sec-source": return <SourceSection {...p} />;
    case "sec-speakers": return <SpeakersSection {...p} />;
    case "sec-person": return <PersonViewSection {...p} />;
    case "sec-pulse": return <PulseSection {...p} />;
    case "sec-board": return <WholeBoardSection {...p} />;
    case "sec-mapping": return <StatusMappingSection {...p} />;
    case "sec-export": return <ExportSection {...p} />;
    case "sec-display": return <DisplaySection {...app} />;
    case "sec-storage": return <StorageSection {...app} />;
    case "sec-connection": return <ConnectionSection {...app} />;
    case "sec-ticket-fields": return <TicketFieldsSection {...app} />;
  }
}

export function ConfigureScreen() {
  const { vw, vh } = useViewport();
  const r = route.value;
  const selectedId = r.view === "configure" ? r.standupId : null;
  const cfg = config.value;
  const list = standups.value;
  const columns = columnsFor(vw, vh);
  const current = cfg !== null && selectedId !== null && cfg.id === selectedId ? cfg : null;
  const position = selectedId === null ? -1 : list.findIndex((t) => t.id === selectedId);

  // Columns balance themselves by measured section height: when the standup or the column count changes, and again
  // whenever a section's height changes (speakers or statuses added, a form opened). A rebalance never happens while
  // focus is inside the sections, so nothing jumps to another column mid-edit; it waits for focus to leave.
  // Until measured, the static §9.2 table applies.
  const gridRef = useRef<HTMLDivElement>(null);
  const balanceKey = current ? `${columns}:${current.id}` : "";
  const [balanced, setBalanced] = useState<{ key: string; cols: SectionId[][] } | null>(null);
  const layout = balanced && balanced.key === balanceKey ? balanced.cols : COLUMNS[columns];
  const rebalance = (force: boolean) => {
    const grid = gridRef.current;
    if (!grid || !balanceKey) return;
    if (!force && grid.contains(document.activeElement)) return;
    const heights = new Map<string, number>();
    for (const el of Array.from(grid.querySelectorAll<HTMLElement>(".cfg-anchor"))) heights.set(el.id, el.offsetHeight);
    const cols = balanceColumns(ORDER, heights, columns, SECTION_GAP) ?? COLUMNS[columns];
    setBalanced((prev) => {
      if (!prev || prev.key !== balanceKey) return { key: balanceKey, cols };
      if (same(prev.cols, cols)) return prev;
      // A height change only moves sections when it clearly pays: the current columns are off by more than the
      // threshold and the new ones are better by at least that much. Small changes never shuffle the screen.
      const now = spread(prev.cols, heights);
      return now > REBALANCE_PX && now - spread(cols, heights) >= REBALANCE_PX ? { key: balanceKey, cols } : prev;
    });
  };
  const rebalanceRef = useRef(rebalance);
  rebalanceRef.current = rebalance;
  useLayoutEffect(() => {
    if (balanced?.key !== balanceKey) rebalanceRef.current(true);
  }, [balanceKey]);
  useEffect(() => {
    const grid = gridRef.current;
    if (!grid || typeof ResizeObserver === "undefined") return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const later = () => {
      clearTimeout(timer);
      timer = setTimeout(() => rebalanceRef.current(false), 150);
    };
    const ro = new ResizeObserver(later);
    for (const el of Array.from(grid.querySelectorAll(".cfg-anchor"))) ro.observe(el);
    grid.addEventListener("focusout", later);
    return () => {
      clearTimeout(timer);
      ro.disconnect();
      grid.removeEventListener("focusout", later);
    };
  }, [balanceKey, layout]);

  // Every visit and every selection loads fresh data (Done clears it); leaving flushes pending text saves.
  useEffect(() => {
    if (selectedId !== null) {
      void loadConfig(selectedId).catch((e: unknown) =>
        showBanner({ kind: "error", message: toAppError(e).message, dismissible: true }),
      );
    }
    return () => void flush();
  }, [r]);

  // Escape leaves Configure unless an open popup/combobox already handled it (defaultPrevented) or an overlay is open.
  // When focus is outside inputs and global keys are installed, keys.ts handles Escape first and preventDefaults it.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing || e.key !== "Escape") return;
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (paletteOpen.peek() || updateDialogOpen.peek() || keyLegendOpen.peek()) return;
      e.preventDefault();
      void leaveConfigure();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const jump = (id: string) => {
    document.getElementById(id)?.scrollIntoView({ block: "start" });
  };

  return (
    <div class="cfg-screen">
      <header class="cfg-header">
        <span class="cfg-wordmark">CUISLE</span>
        <span class="cfg-sep" aria-hidden="true">│</span>
        <span class="cfg-header-title">Configure</span>
        <span class="cfg-spacer" />
        <SavedIndicator state={saveState.value} onRetry={() => retryFailed()} />
        <button type="button" class="cfg-done" onClick={() => void leaveConfigure()}>
          Done
        </button>
      </header>
      <div class="cfg-body">
        <StandupList
          standups={list}
          selectedId={selectedId}
          onSelect={(id) => goConfigure(id)}
          onAdd={() => void createStandup()}
        />
        <main class="cfg-main">
          {current ? (
            <>
              <div class="cfg-title-row">
                <div class="cfg-title">
                  <h1 class="cfg-name">{current.name}</h1>
                  {position >= 0 ? (
                    <span class="cfg-pos">
                      standup {position + 1} of {list.length}
                    </span>
                  ) : null}
                  {current.draft ? <span class="cfg-tag">DRAFT</span> : null}
                </div>
                <div class="cfg-title-actions">
                  <button type="button" class="cfg-link" onClick={() => void duplicateStandup(current.id)}>
                    Duplicate
                  </button>
                  <button type="button" class="cfg-link cfg-link-alert" onClick={() => void deleteStandup(current.id)}>
                    Delete standup
                  </button>
                </div>
              </div>
              {columns < 3 ? <JumpBar sections={JUMP_SECTIONS} onJump={jump} /> : null}
              {/* Explicit column containers in a CSS grid. Not CSS multi-column: WebKit (the macOS and Linux webviews)
                  fragments a multi-column box inside the scrolling main into extra columns past the right edge, so
                  Configure scrolled sideways. A change of column count (crossing a width threshold) remounts the
                  sections; text fields have already autosaved by then. */}
              <div class={`cfg-grid cfg-cols-${columns}`} ref={gridRef} data-balanced={balanced?.key === balanceKey ? "true" : "false"}>
                {layout.map((ids, c) => (
                  <div key={`${columns}-${c}`} class="cfg-col">
                    {ids.map((id) => (
                      <div key={id} id={id} class="cfg-anchor">
                        {renderSection(id, columns, current)}
                      </div>
                    ))}
                  </div>
                ))}
              </div>
            </>
          ) : (
            <p class="cfg-empty">{selectedId === null ? "Create a standup to start." : "Loading…"}</p>
          )}
        </main>
      </div>
    </div>
  );
}
