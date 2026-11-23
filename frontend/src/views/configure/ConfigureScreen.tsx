// ConfigureScreen: frame, rail, jump bar, section layout, save indicator, Done/Esc (ux-spec §9.1, §9.2, §9.5;
// architecture §5.3; AGENTS.md invariants 3 and 4).
import { useEffect } from "preact/hooks";
import { SavedIndicator } from "../../components/SavedIndicator";
import { toAppError } from "../../api/errors";
import { standups } from "../../state/app";
import { config, createStandup, deleteStandup, duplicateStandup, leaveConfigure, loadConfig } from "../../state/config";
import { goConfigure, route } from "../../state/route";
import { flush, retryFailed, saveState } from "../../state/save";
import { showBanner } from "../../state/ui";
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
import { SpeakersSection } from "./sections/Speakers";
import { StatusMappingSection } from "./sections/StatusMapping";
import { WholeBoardSection } from "./sections/WholeBoard";
import "../../styles/configure.css";

type SectionId =
  | "sec-source" | "sec-speakers" | "sec-person" | "sec-pulse" | "sec-board"
  | "sec-mapping" | "sec-export" | "sec-display" | "sec-connection";

const LABELS: Record<SectionId, string> = {
  "sec-source": "Source",
  "sec-speakers": "Speakers",
  "sec-person": "Person view",
  "sec-pulse": "Pulse",
  "sec-board": "Whole board",
  "sec-mapping": "Status mapping",
  "sec-export": "Export",
  "sec-display": "Display",
  "sec-connection": "Connection",
};

const ORDER: SectionId[] = [
  "sec-source", "sec-speakers", "sec-person", "sec-pulse", "sec-board", "sec-mapping", "sec-export", "sec-display", "sec-connection",
];

/** Column membership per ux-spec §9.2 (column-major DOM order). */
const COLUMNS: Record<1 | 2 | 3, SectionId[][]> = {
  3: [
    ["sec-source", "sec-person", "sec-export"],
    ["sec-speakers", "sec-display", "sec-connection"],
    ["sec-pulse", "sec-board", "sec-mapping"],
  ],
  2: [
    ["sec-source", "sec-speakers", "sec-board", "sec-export"],
    ["sec-person", "sec-pulse", "sec-mapping", "sec-display", "sec-connection"],
  ],
  1: [ORDER],
};

const JUMP_SECTIONS = ORDER.map((id) => ({ id, label: LABELS[id] }));

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
    case "sec-connection": return <ConnectionSection {...app} />;
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

  // Every visit and every selection loads fresh data (Done clears it); leaving flushes pending text saves.
  useEffect(() => {
    if (selectedId !== null) {
      void loadConfig(selectedId).catch((e: unknown) =>
        showBanner({ kind: "error", message: toAppError(e).message, dismissible: true }),
      );
    }
    return () => void flush();
  }, [r]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !e.defaultPrevented) void leaveConfigure();
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
              {/* One flat list under one parent, keyed by section id: a change of column count only moves the nodes,
                  so section state (an open Reconnect form, typed text) survives. Columns are CSS columns with a
                  forced break before the first section of each column. */}
              <div class={`cfg-grid cfg-cols-${columns}`}>
                {COLUMNS[columns].flatMap((ids) => ids.map((id, i) => ({ id, start: i === 0 }))).map(({ id, start }) => (
                  <div key={id} id={id} class={start ? "cfg-anchor cfg-col-start" : "cfg-anchor"}>
                    {renderSection(id, columns, current)}
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
