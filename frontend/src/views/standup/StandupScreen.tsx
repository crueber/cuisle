// The standup screen frame (ux-spec §2): header, speaker rail, and the main area holding the person or board view.
// Layout maths comes from layout/responsive on the window size divided by the text scale, and below the header by
// text scale × zoom (useViewport); re-read on resize (no timers).
import "../../styles/standup-frame.css";
import { mainPadding, mainWidth, railMode } from "../../layout/responsive";
import type { SpeakerView } from "../../api/types";
import { useLayoutEffect } from "preact/hooks";
import { route } from "../../state/route";
import { restoreAnchor, zoomOf } from "../../state/zoom";
import { currentView, personGroups, selectPerson } from "../../state/standup";
import { syncStatus } from "../../state/sync";
import { BoardView } from "./BoardView";
import { Header } from "./Header";
import { PersonView } from "./PersonView";
import { Rail } from "./Rail";
import { useViewport } from "../useViewport";

/** Unassigned is always reachable by id, even when the view lists no speaker entry for it. */
const UNASSIGNED: SpeakerView = {
  id: "unassigned",
  name: "Unassigned",
  first: "Unassigned",
  initials: "··",
  unassigned: true,
  included: true,
  active: 0,
  done: 0,
};

export function StandupScreen() {
  // vw drives the header (text scale only); cvw/cvh drive everything below it (text scale × zoom, ux-spec §16.3).
  const { vw, cvw, cvh } = useViewport();
  const r = route.value;
  const zoom = r.view === "standup" ? zoomOf(r.standupId) : 100;
  // After a zoom reflow, the lane or group that was at the top is put back at the top (captured by setZoom).
  useLayoutEffect(restoreAnchor, [zoom]);
  const view = currentView.value;
  const wanted = r.view === "standup" ? r.person : null;
  const person: SpeakerView | null =
    view && wanted !== null
      ? (view.speakers ?? []).find((s) => s.id === wanted) ?? (wanted === "unassigned" ? UNASSIGNED : null)
      : null;
  const status = r.view === "standup" ? syncStatus.value.get(r.standupId) : undefined;
  const firstSync = view !== undefined && view !== null && view.lastSyncedAt <= 0;
  const mainW = mainWidth(cvw);
  const pad = mainPadding(cvw, cvh);
  const z = zoom / 100;

  return (
    <div class="sf-screen">
      <Header vw={vw} />
      <div class="sf-body" style={{ "--zoom": z }}>
        <Rail
          mode={railMode(cvw)}
          speakers={view?.speakers ?? []}
          selected={person ? person.id : null}
          onSelect={(p) => selectPerson(p)}
        />
        <main class="sf-main" style={{ paddingLeft: pad.x * z, paddingRight: pad.x * z, paddingTop: pad.top * z, paddingBottom: pad.bottom * z }}>
          {firstSync ? (
            <p class="sf-notice" role="status">
              {status?.state === "running"
                ? `First sync in progress (${status.phase}${status.total > 0 ? ` ${status.done}/${status.total}` : ""}). Tickets appear when it finishes.`
                : "Not synced yet. Press R (or ↻) to fetch this board from Jira."}
            </p>
          ) : null}
          {view && person ? (
            <PersonView view={view} person={person} groups={personGroups.value} mainW={mainW} vh={cvh} />
          ) : view ? (
            <BoardView view={view} mainW={mainW} vh={cvh} vw={cvw} onSelectPerson={(p) => selectPerson(p)} />
          ) : null}
        </main>
      </div>
    </div>
  );
}
