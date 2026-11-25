// FROZEN SIGNATURES (ux-spec §8). Implementation: WP-15.
import { goConfigure, route } from "./state/route";
import { cycleStandup, nextSpeaker, prevSpeaker, switchByNumber, toggleBoard, toggleBoardGroup } from "./state/standup";
import { startSync } from "./state/sync";
import { leaveConfigure } from "./state/config";
import { keyLegendOpen, switcherOpen, undoLatestToast } from "./state/ui";
import { tabStandups } from "./state/app";
import { ticketSession } from "./state/ticket";
import { updateDialogOpen } from "./state/update";
import { closePalette, openPalette, paletteOpen } from "./state/palette";
import { resetZoom, zoomBy } from "./state/zoom";

const EDITABLE = 'input, textarea, select, [role="combobox"]';

/** True for input, textarea, select, contenteditable and anything inside [role=combobox]. */
export function isEditableTarget(t: EventTarget | null): boolean {
  if (typeof Element === "undefined" || !(t instanceof Element)) return false;
  if (t.closest(EDITABLE)) return true;
  const editable = t.closest("[contenteditable]");
  return editable !== null && editable.getAttribute("contenteditable") !== "false";
}

/** + = zoom in, − _ zoom out, 0 reset (ux-spec §16.2). Shift is allowed: + and _ are shifted keys. */
function zoomKeyOf(e: KeyboardEvent): "in" | "out" | "reset" | null {
  if (e.altKey) return null;
  if (e.key === "+" || e.key === "=") return "in";
  if (e.key === "-" || e.key === "_" || e.key === "\u2212") return "out";
  if (e.key === "0" && !e.shiftKey) return "reset";
  return null;
}

function runZoomKey(k: "in" | "out" | "reset"): boolean {
  return k === "reset" ? resetZoom() : zoomBy(k === "in" ? 1 : -1);
}

/** Handles one keydown per the ux-spec §8 map; returns true when handled (caller preventDefaults). */
export function handleGlobalKey(e: KeyboardEvent): boolean {
  // ⌘K / Ctrl+K opens the command palette everywhere (standup, Configure, over the ticket modal) and, in a field, still
  // wins over the field. It toggles: while the palette is open its own input handles keys first, so this only sees the
  // press when focus left the input.
  if ((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "k") {
    if (updateDialogOpen.value) return false;
    if (paletteOpen.value) closePalette();
    else openPalette();
    return true;
  }
  // ⌘/Ctrl with + = − 0: zoom the standup screen. Always swallowed (so the webview never applies its own page zoom, which
  // would also magnify the header), but it only zooms on the standup screen when nothing else owns the keys. Handled
  // before the modifier early-return below, like ⌘K.
  const zk = zoomKeyOf(e);
  if (zk && (e.metaKey || e.ctrlKey)) {
    const busy = paletteOpen.value || ticketSession.value !== null || updateDialogOpen.value || isEditableTarget(e.target);
    if (!busy) runZoomKey(zk);
    return true;
  }
  // The palette owns every key while it is open.
  if (paletteOpen.value) return false;
  // The ticket modal has its own key map (ux-spec §14.10); nothing here (R, J/K, arrows, digits, Undo) acts behind it.
  if (ticketSession.value !== null) return false;
  // Nor behind the Update dialog: it closes itself on Escape.
  if (updateDialogOpen.value) return false;
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

  // T: the finder, from the standup or Configure (no field has focus here and the ticket modal has its own keys).
  if ((e.key === "t" || e.key === "T") && r.view !== "connect") {
    openPalette();
    return true;
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
    case "g":
    case "G":
      // Group by Assignee / Status: whole board only; on a person view it does nothing (ux-spec §6.5, §8).
      return toggleBoardGroup();
    case "+":
    case "=":
    case "-":
    case "_":
    case "\u2212":
    case "0": {
      const k = zoomKeyOf(e);
      return k !== null && runZoomKey(k);
    }
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
