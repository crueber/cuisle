// FROZEN SIGNATURES (ux-spec §8). Implementation: WP-15.
import { goConfigure, route } from "./state/route";
import { cycleStandup, nextSpeaker, prevSpeaker, switchByNumber, toggleBoard } from "./state/standup";
import { startSync } from "./state/sync";
import { leaveConfigure } from "./state/config";
import { keyLegendOpen, switcherOpen, undoLatestToast } from "./state/ui";
import { tabStandups } from "./state/app";
import { ticketSession } from "./state/ticket";

const EDITABLE = 'input, textarea, select, [role="combobox"]';

/** True for input, textarea, select, contenteditable and anything inside [role=combobox]. */
export function isEditableTarget(t: EventTarget | null): boolean {
  if (typeof Element === "undefined" || !(t instanceof Element)) return false;
  if (t.closest(EDITABLE)) return true;
  const editable = t.closest("[contenteditable]");
  return editable !== null && editable.getAttribute("contenteditable") !== "false";
}

/** Handles one keydown per the ux-spec §8 map; returns true when handled (caller preventDefaults). */
export function handleGlobalKey(e: KeyboardEvent): boolean {
  // The ticket modal has its own key map (ux-spec §14.10); nothing here (R, J/K, arrows, digits, Undo) acts behind it.
  if (ticketSession.value !== null) return false;
  // Ctrl/Cmd+Z runs a visible Undo toast; inside inputs the native text undo wins.
  if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "z") {
    return !isEditableTarget(e.target) && undoLatestToast();
  }
  if (e.ctrlKey || e.metaKey || e.altKey) return false;
  if (isEditableTarget(e.target)) return false;

  const r = route.value;
  const onStandup = r.view === "standup";

  if (e.key === "Escape") {
    if (keyLegendOpen.value) {
      keyLegendOpen.value = false;
      return true;
    }
    if (switcherOpen.value) {
      switcherOpen.value = false;
      return true;
    }
    if (r.view === "configure") {
      void leaveConfigure();
      return true;
    }
    return false;
  }

  if (!onStandup) return false;
  const current = r.standupId;

  switch (e.key) {
    case "ArrowUp":
      prevSpeaker();
      return true;
    case "ArrowDown":
      nextSpeaker();
      return true;
    case "ArrowLeft":
      return cycleStandup(-1);
    case "ArrowRight":
      return cycleStandup(1);
    case "b":
    case "B":
      toggleBoard();
      return true;
    case "r":
    case "R":
      void startSync(current, e.shiftKey ? "full" : "incremental");
      return true;
    case ",":
      goConfigure(null);
      return true;
    case "/":
      switcherOpen.value = true;
      return true;
    case "?":
      keyLegendOpen.value = true;
      return true;
    default:
      break;
  }

  if (/^[1-9]$/.test(e.key)) {
    const n = Number(e.key);
    if (n > tabStandups().length) return false;
    void switchByNumber(n);
    return true;
  }
  return false;
}

/** Installs the keydown listener on target; returns the uninstaller. */
export function installGlobalKeys(target: Pick<Window, "addEventListener" | "removeEventListener"> = window): () => void {
  const listener = (e: KeyboardEvent) => {
    if (handleGlobalKey(e)) e.preventDefault();
  };
  target.addEventListener("keydown", listener);
  return () => target.removeEventListener("keydown", listener);
}
