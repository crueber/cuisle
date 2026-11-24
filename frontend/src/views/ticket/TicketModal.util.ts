// Small helpers shared by the ticket modal shell components (WP-T7). No network, no state.
import { useEffect, useState } from "preact/hooks";
import { api, toAppError } from "../../api";
import { showToast } from "../../state/ui";

/** True once `active` has been true for `ms` (the "saving…" / "refreshing…" 250 ms rule); false the moment it stops. */
export function useAfter(active: boolean, ms: number): boolean {
  const [late, setLate] = useState(false);
  useEffect(() => {
    if (!active) {
      setLate(false);
      return;
    }
    const t = setTimeout(() => setLate(true), ms);
    return () => clearTimeout(t);
  }, [active, ms]);
  return active && late;
}

/** Local time as the spec writes it: H:mm, hour not padded ("9:41"). */
export function clock(ms: number): string {
  const d = new Date(ms);
  return `${d.getHours()}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** "6s" for a wait in ms, rounded up to whole seconds. */
export const seconds = (ms: number): string => `${Math.max(1, Math.ceil(ms / 1000))}s`;

const ISSUE_URL = /\/browse\/([A-Z][A-Z0-9_]+-\d+)(?:[/?#]|$)/;

/** Opens Jira (or any link the user clicked) in the OS browser. The webview never navigates (AGENTS §7). */
export function openLink(url: string, fallbackKey = ""): void {
  const key = ISSUE_URL.exec(url)?.[1] ?? (url === "" ? fallbackKey : "");
  if (key !== "") {
    openIssueKey(key);
    return;
  }
  if (!/^(https?:|mailto:)/i.test(url)) return;
  api()
    .app.openUrl(url)
    .catch(() => showToast({ message: "Couldn't open the link.", timeoutMs: 6000 }));
}

export function openIssueKey(key: string): void {
  api()
    .app.openIssue(key)
    .catch((e: unknown) => showToast({ message: toAppError(e).message, timeoutMs: 6000 }));
}
