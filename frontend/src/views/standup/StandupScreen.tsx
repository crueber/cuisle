// The standup screen frame (ux-spec §2): header, speaker rail, and the main area holding the person or board view.
// Layout maths comes from layout/responsive on the window size divided by the text scale (useViewport); re-read on resize (no timers).
import "../../styles/standup-frame.css";
import { mainPadding, mainWidth, railMode } from "../../layout/responsive";
import type { SpeakerView } from "../../api/types";
import { route } from "../../state/route";
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
  const { vw, vh } = useViewport();
  const r = route.value;
  const view = currentView.value;
  const wanted = r.view === "standup" ? r.person : null;
  const person: SpeakerView | null =
    view && wanted !== null
      ? (view.speakers ?? []).find((s) => s.id === wanted) ?? (wanted === "unassigned" ? UNASSIGNED : null)
      : null;
  const status = r.view === "standup" ? syncStatus.value.get(r.standupId) : undefined;
  const firstSync = view !== undefined && view !== null && view.lastSyncedAt <= 0;
  const mainW = mainWidth(vw);
  const pad = mainPadding(vw, vh);

  return (
    <div class="sf-screen">
      <Header vw={vw} />
      <div class="sf-body">
        <Rail
          mode={railMode(vw)}
          speakers={view?.speakers ?? []}
          selected={person ? person.id : null}
          onSelect={(p) => selectPerson(p)}
        />
        <main class="sf-main" style={{ paddingLeft: pad.x, paddingRight: pad.x, paddingTop: pad.top, paddingBottom: pad.bottom }}>
          {firstSync ? (
            <p class="sf-notice" role="status">
              {status?.state === "running"
                ? `First sync in progress (${status.phase}${status.total > 0 ? ` ${status.done}/${status.total}` : ""}). Tickets appear when it finishes.`
                : "Not synced yet. Press R (or ↻) to fetch this board from Jira."}
            </p>
          ) : null}
          {view && person ? (
            <PersonView view={view} person={person} groups={personGroups.value} mainW={mainW} vh={vh} />
          ) : view ? (
            <BoardView view={view} mainW={mainW} vh={vh} onSelectPerson={(p) => selectPerson(p)} />
          ) : null}
        </main>
      </div>
    </div>
  );
}
