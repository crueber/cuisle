// The command contract (F14, ux-spec §15.4). Every user action is registered once and used by the palette; the key map and
// the key legend can read the same entries (id, title, shortcut) later.
import type { StandupTab, StandupView, SyncStatus, UpdateStatus } from "../api/types";
import type { BoardGroup } from "../layout/board";
import type { Route } from "../state/route";
import type { PaletteMode } from "../state/palette";

export type Category = "Navigate" | "Sync" | "View" | "Display" | "Ticket" | "Configure" | "App";

/** The ticket the ticket commands act on: the one open in the modal, else the best match of the current query. */
export interface TicketRef {
  issueId: number;
  key: string;
  standupId: number;
}

/** What a command can see when it is listed, checked and run. Built fresh by commands/context.ts. */
export interface Ctx {
  route: Route;
  /** The current standup (on the standup screen or Configure), else null. */
  standupId: number | null;
  onStandup: boolean;
  /** The whole board is showing (no person selected). */
  onBoard: boolean;
  view: StandupView | null;
  tabs: StandupTab[];
  group: BoardGroup;
  sync: SyncStatus | undefined;
  /** "synced 1 min ago" and friends. */
  syncText: string;
  theme: "light" | "dark" | "system";
  textSize: "standard" | "large";
  /** The theme in effect right now (system resolved). */
  resolvedTheme: "light" | "dark";
  update: UpdateStatus;
  authInvalid: boolean;
  ticket: TicketRef | null;
  /** The dialog's handle for commands that open a list instead of acting. */
  ui: { setMode(mode: PaletteMode): void };
}

export interface Command {
  id: string; // "sync.resync"
  title: string | ((c: Ctx) => string);
  category: Category;
  /** Aliases searched too ("refresh", "pull" for Resync; "dark" for Theme). */
  keywords?: string[];
  /** Display only: "R", "⇧R", "G", "1–9", ",". */
  shortcut?: string;
  detail?: (c: Ctx) => string | undefined;
  /** true = runnable; a string = the reason it is disabled (shown muted in the row). */
  available?: (c: Ctx) => true | string;
  /** Extension: false hides the command altogether (Cancel sync while idle, Reconnect Jira when auth is fine). */
  visible?: (c: Ctx) => boolean;
  run?: (c: Ctx) => void | Promise<void>;
  /** Nested commands (› chevron); mutually exclusive with run and mode. */
  children?: (c: Ctx) => Command[];
  /** Extension: choosing it opens a section of the palette (`@` people) instead of closing; shows the › chevron. */
  mode?: PaletteMode;
  /** Extension: ✓ when this is the current choice (children of Theme, Switch standup…). */
  checked?: (c: Ctx) => boolean;
  /** Extension: the input's placeholder while this command's children are listed ("Choose a theme"). */
  placeholder?: string;
  /** Extension: an icon name from views/palette/icons. */
  icon?: string;
}
