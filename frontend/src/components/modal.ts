// Shared modal dialog focus helpers (Tab trapping + focus restoration).
import { useEffect } from "preact/hooks";
import type { RefObject } from "preact";

const FOCUSABLE = "button:not([disabled]), a[href], input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex='-1'])";

/** Keeps Tab and Shift+Tab inside an aria-modal dialog. */
export function trapTab(e: KeyboardEvent, dialog: HTMLElement | null): void {
  if (!dialog) return;
  const items = Array.from(dialog.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((el) => el.tabIndex >= 0);
  e.preventDefault();
  if (items.length === 0) {
    dialog.focus();
    return;
  }
  const i = items.indexOf(document.activeElement as HTMLElement);
  const next = e.shiftKey ? (i <= 0 ? items.length - 1 : i - 1) : i < 0 || i === items.length - 1 ? 0 : i + 1;
  items[next]?.focus();
}

/** Moves focus into ref on mount and restores focus to the opener on unmount (if still in the document). */
export function useDialogFocus(ref: RefObject<HTMLElement>, onUnmount?: () => void): void {
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    ref.current?.focus();
    return () => {
      onUnmount?.();
      if (opener && opener !== document.body && document.contains(opener)) opener.focus();
    };
  }, []);
}
