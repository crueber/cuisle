// FROZEN CONTRACT: props of the Configure components (ux-spec §9). Owners: WP-26, WP-27, WP-28.
import type { StandupConfig, StandupTab } from "../../api/types";

export interface StandupListProps {
  standups: StandupTab[];
  selectedId: number | null;
  onSelect: (id: number) => void;
  onAdd: () => void;
}

export interface JumpBarProps {
  sections: { id: string; label: string }[];
  onJump: (id: string) => void;
}

/** Every section receives the loaded config; mutations go through state/config.ts. */
export interface SectionProps {
  config: StandupConfig;
  columns: 1 | 2 | 3;
}

/** Display + Connection sections are app-wide. */
export interface AppSectionProps {
  columns: 1 | 2 | 3;
}

export interface ConnectScreenProps {
  reconnect?: boolean; // shown from the banner, prefilled site/email
}
