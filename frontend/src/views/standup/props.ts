// FROZEN CONTRACT: props of the standup-screen components (ux-spec §2, §4–§7). Owners: WP-23, WP-24, WP-25.
import type { BacklogItem, LaneInfo, Pulse, SpeakerView, StandupTab, StandupView, TicketView, ViewOptions } from "../../api/types";
import type { DensitySpec } from "../../layout/density";
import type { LegendMode, RailMode } from "../../layout/responsive";
import type { TicketGroup } from "../../state/standup";

export interface HeaderProps {
  vw: number; // unscaled CSS px
}

export interface TabsProps {
  tabs: StandupTab[]; // configured standups only
  activeId: number;
  availW: number;
  onSelect: (id: number) => void;
  onAdd: () => void;
}

export interface StandupSwitcherProps {
  tabs: StandupTab[];
  activeId: number;
  onSelect: (id: number) => void;
}

export interface RailProps {
  mode: RailMode;
  speakers: SpeakerView[]; // view.speakers (rail shows Included only; Unassigned per position)
  selected: string | null; // null = whole board
  onSelect: (person: string | null) => void;
}

export interface PersonViewProps {
  view: StandupView;
  person: SpeakerView;
  groups: TicketGroup[];
  mainW: number;
  vh: number;
}

export interface TicketRowProps {
  ticket: TicketView;
  density: DensitySpec;
  trackW: number;
  keyCh: number; // max key length across the view (ch units)
  days: number;
  options: ViewOptions;
  onOpen: (key: string) => void;
}

export interface PulseTrackProps {
  pulse: Pulse;
  days: number;
  width: number; // px
  rowH: number; // row height (or lane-row mode)
  lane?: boolean; // board lane rows: thin track, no marks
  done?: boolean; // done row: muted history, no marks
  options: Pick<ViewOptions, "showCommits" | "showPRs" | "showComments" | "devActivity">;
}

export interface TrackBackdropProps {
  axis: StandupView["axis"];
  width: number;
  shadeWeekends: boolean;
  todayLine: 2 | 3;
}

export interface MoreBarProps {
  visible: boolean;
  atEnd: boolean;
  summary: string;
}

export interface LegendProps {
  mode: LegendMode;
  options: ViewOptions;
  lanes: LaneInfo[];
}

export interface BoardViewProps {
  view: StandupView;
  mainW: number;
  vh: number;
  onSelectPerson: (person: string) => void;
}

export interface DoneListProps {
  tickets: TicketView[];
  label: string; // view.options.doneLabel
  cols: number;
  speakers: SpeakerView[];
}

export interface BacklogListProps {
  items: BacklogItem[];
  total: number; // -1 unknown
  rows: number; // configured backlog rows
  cols: number;
}
