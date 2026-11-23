// FROZEN CONTRACT: prop interfaces of the shared components (architecture §4.6, ux-spec §9.3, §10).
// Implementations: Icon → WP-10, Combobox → WP-11, the rest → WP-12.
import type { ComponentChildren } from "preact";

export type IconName =
  | "story" | "bug" | "task" | "spike" | "other" // issue types (subtask/epic use "other")
  | "sliders" | "resync" | "close" | "plus" | "chevron-down" | "chevron-up" | "board";

export interface IconProps {
  name: IconName;
  size?: number; // px, default 18
  strokeWidth?: number; // default 2.4
  title?: string; // when set: role="img" + <title>; else aria-hidden
  class?: string;
}

export interface ComboboxProps<T> {
  id: string;
  label: string; // accessible name (visually hidden unless showLabel)
  showLabel?: boolean;
  value: T | null;
  getKey: (o: T) => string;
  getLabel: (o: T) => string;
  renderOption?: (o: T, state: { active: boolean; selected: boolean }) => ComponentChildren;
  options?: T[]; // static: case-insensitive, prefix matches first, then substring
  load?: (query: string, signal: AbortSignal) => Promise<T[]>; // async: debounced 150 ms, stale dropped
  onSelect: (o: T) => void;
  placeholder?: string;
  minChars?: number; // default 0
  clearable?: boolean;
  onClear?: () => void;
  disabled?: boolean;
  emptyText?: string; // default "No matches"
  variant?: "field" | "menu"; // menu = button-triggered (standup switcher)
  menuLabel?: ComponentChildren; // button content in menu variant
  clearOnSelect?: boolean; // add-person
  invalid?: boolean;
  describedBy?: string;
}

export interface SegmentedOption<V extends string> {
  value: V;
  label: string;
  disabled?: boolean;
}

export interface SegmentedProps<V extends string> {
  id: string;
  label: string; // radiogroup name
  value: V;
  options: SegmentedOption<V>[];
  onChange: (v: V) => void;
}

export interface SwitchProps {
  id: string;
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
  description?: string;
}

export interface StepperProps {
  id: string;
  label: string;
  value: number;
  min: number;
  max: number;
  step?: number; // default 1
  suffix?: string; // e.g. "d"
  zeroLabel?: string; // e.g. "no limit"
  onChange: (v: number) => void;
}

export interface CheckboxProps {
  id: string;
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
}

export interface TextFieldProps {
  id: string;
  label: string;
  value: string;
  onInput: (v: string) => void; // every keystroke (caller debounces via save.ts)
  onCommit?: (v: string) => void; // blur or Enter
  placeholder?: string;
  error?: string; // inline validation message (aria-invalid + aria-describedby)
  hint?: string;
  multiline?: boolean;
  mono?: boolean;
  type?: "text" | "url" | "email" | "password";
  autoFocus?: boolean;
  maxLength?: number;
}

export interface ToastProps {
  message: string;
  action?: { label: string; run: () => void };
  onDismiss: () => void;
}

export interface SavedIndicatorProps {
  state: "idle" | "saving" | "saved" | "settled" | "failed";
  onRetry: () => void;
}

export interface BannerProps {
  kind: "offline" | "auth_missing" | "auth_invalid" | "recovered" | "error";
  message: string;
  action?: { label: string; run: () => void };
  onDismiss?: () => void;
}

export interface KeyLegendProps {
  open: boolean;
  onClose: () => void;
  showCommits: boolean;
  showPRs: boolean;
  showComments: boolean;
  devActivity: string; // ok|unavailable|unknown
}
