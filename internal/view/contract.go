// Package view builds the view models the UI renders (architecture.md §3.2, §5.2, ADR 0008).
// Go computes every data-derived value; TypeScript only lays out.
//
// FROZEN CONTRACT: every type in this file (they generate frontend/src/bindings). Implementation of build.go: WP-31.
// Imports allowed: model, pulse, cache, settings, stdlib. JSON names are lowerCamel; lists are never nil.
package view

import "github.com/crueber/cuisle/internal/pulse"

type PulseSeg = pulse.Seg
type PulseMark = pulse.Mark
type PulseSpan = pulse.Span
type AxisDay = pulse.AxisDay

type StandupView struct {
	StandupID    int64         `json:"standupId"`
	Rev          int64         `json:"rev"`
	GeneratedAt  int64         `json:"generatedAt"` // = AsOf for a live view
	Today        string        `json:"today"`       // local date of AsOf
	AsOf         int64         `json:"asOf"`
	Live         bool          `json:"live"`
	Name         string        `json:"name"`
	SpaceKey     string        `json:"spaceKey"`
	BoardName    string        `json:"boardName"`
	BoardType    string        `json:"boardType"` // scrum|kanban
	Scope        string        `json:"scope"`     // sprint|board
	Sprint       *SprintInfo   `json:"sprint"`    // nil for kanban / no active sprint
	PulseDays    int           `json:"pulseDays"`
	Axis         []AxisDay     `json:"axis"`
	Lanes        []LaneInfo    `json:"lanes"`    // walk order, then done
	Speakers     []SpeakerView `json:"speakers"` // effective speaking order, then excluded-with-tickets
	Tickets      []TicketView  `json:"tickets"`  // active + done-in-window, board rank order
	Backlog      []BacklogItem `json:"backlog"`
	BacklogTotal int           `json:"backlogTotal"` // -1 unknown
	Counts       Counts        `json:"counts"`
	Options      ViewOptions   `json:"options"`
	Sync         SyncStatus    `json:"sync"` // filled by the app layer (zero from Build)
	Warnings     []string      `json:"warnings"`
	PreviewKey   string        `json:"previewKey"`
	LastSyncedAt int64         `json:"lastSyncedAt"` // 0 = never
}

type SprintInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	StartAt   int64  `json:"startAt"`
	EndAt     int64  `json:"endAt"`
	DaysLeft  int    `json:"daysLeft"`
	EndsLabel string `json:"endsLabel"` // "ends today"|"ends tomorrow"|"ends in 3 days"|"ended 2 days ago"
}

type LaneInfo struct {
	Lane      string   `json:"lane"`
	Label     string   `json:"label"` // "To Do"|"In Progress"|"In Review"|"QA"|"Done"
	LimitDays int      `json:"limitDays"`
	Columns   []string `json:"columns"` // board column names mapped to this lane
}

type SpeakerView struct {
	ID         string `json:"id"` // accountId or "unassigned"
	Name       string `json:"name"`
	First      string `json:"first"`
	Initials   string `json:"initials"`
	Unassigned bool   `json:"unassigned"`
	Included   bool   `json:"included"`
	Active     int    `json:"active"`
	Done       int    `json:"done"`
}

type TicketView struct {
	ID         int64  `json:"id"`
	SourceID   string `json:"sourceId"`
	Key        string `json:"key"`
	Summary    string `json:"summary"`
	URL        string `json:"url"`
	Type       string `json:"type"`     // story|bug|task|spike|subtask|epic|other
	Assignee   string `json:"assignee"` // accountId, "" = unassigned
	Lane       string `json:"lane"`     // todo|prog|review|qa|done
	StatusName string `json:"statusName"`
	DaysInLane int    `json:"daysInLane"`
	OverLimit  bool   `json:"overLimit"`
	Blocked    bool   `json:"blocked"`
	DoneAt     int64  `json:"doneAt"`
	Pulse      Pulse  `json:"pulse"`
}

type Pulse struct {
	Segs    []PulseSeg  `json:"segs"`
	Marks   []PulseMark `json:"marks"`
	Blocked []PulseSpan `json:"blocked"`
	Label   string      `json:"label"`
}

type BacklogItem struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Type    string `json:"type"`
	URL     string `json:"url"`
}

type Counts struct {
	Active     int `json:"active"`
	Unassigned int `json:"unassigned"`
	Done       int `json:"done"`
	Backlog    int `json:"backlog"`
}

type ViewOptions struct {
	RowsMode      string `json:"rowsMode"` // fit|roomy
	ShowCommits   bool   `json:"showCommits"`
	ShowPRs       bool   `json:"showPRs"`
	ShowComments  bool   `json:"showComments"`
	ShadeWeekends bool   `json:"shadeWeekends"`
	DoneWindow    string `json:"doneWindow"` // sprint|14d|yesterday
	DoneLabel     string `json:"doneLabel"`
	DevActivity   string `json:"devActivity"` // ok|unavailable|unknown (unavailable hides commit/PR marks + legend)
}

type SyncStatus struct {
	StandupID     int64    `json:"standupId"`
	RunID         int64    `json:"runId"`
	State         string   `json:"state"` // idle|running|failed
	Mode          string   `json:"mode"`
	Phase         string   `json:"phase"`
	Done          int      `json:"done"`
	Total         int      `json:"total"`
	LastSuccessAt int64    `json:"lastSuccessAt"`
	LastAttemptAt int64    `json:"lastAttemptAt"`
	LastStatus    string   `json:"lastStatus"` // ok|partial|cancelled|failed|""
	Warnings      []string `json:"warnings"`
	ErrorCode     string   `json:"errorCode"`
	ErrorMessage  string   `json:"errorMessage"`
	NeedsSync     bool     `json:"needsSync"`
}

type StandupTab struct {
	ID           int64  `json:"id"`
	Rev          int64  `json:"rev"`
	Name         string `json:"name"`
	SpaceKey     string `json:"spaceKey"`
	Position     int    `json:"position"`
	ActiveCount  int    `json:"activeCount"`
	SpeakerCount int    `json:"speakerCount"`
	Configured   bool   `json:"configured"` // board chosen; false = DRAFT
	NeedsSync    bool   `json:"needsSync"`
	LastSyncedAt int64  `json:"lastSyncedAt"`
}

type StandupConfig struct {
	ID                 int64           `json:"id"`
	Rev                int64           `json:"rev"`
	Position           int             `json:"position"`
	Name               string          `json:"name"`
	Space              *SpaceOption    `json:"space"`
	Board              *BoardOption    `json:"board"`
	Scope              string          `json:"scope"`
	ExtraFilter        string          `json:"extraFilter"`
	FilterCheck        *FilterCheck    `json:"filterCheck"`
	Columns            []ColumnMapping `json:"columns"`
	Lanes              []LaneInfo      `json:"lanes"`
	UnmappedStatuses   []string        `json:"unmappedStatuses"`
	RowsMode           string          `json:"rowsMode"`
	Speakers           []SpeakerConfig `json:"speakers"`
	UnassignedPosition string          `json:"unassignedPosition"`
	ReshuffleDaily     bool            `json:"reshuffleDaily"`
	PulseDays          int             `json:"pulseDays"`
	ShowCommits        bool            `json:"showCommits"`
	ShowPRs            bool            `json:"showPRs"`
	ShowComments       bool            `json:"showComments"`
	ShadeWeekends      bool            `json:"shadeWeekends"`
	DoneWindow         string          `json:"doneWindow"`
	BacklogRows        int             `json:"backlogRows"`
	NeedsSync          bool            `json:"needsSync"`
	Draft              bool            `json:"draft"`
}

type ColumnMapping struct {
	ColumnName  string   `json:"columnName"`
	StatusNames []string `json:"statusNames"`
	Lane        string   `json:"lane"`
	Auto        bool     `json:"auto"`
	Order       int      `json:"order"` // position among the columns mapped to the same lane (0-based)
}

type SpeakerConfig struct {
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
	Initials  string `json:"initials"`
	Position  int    `json:"position"`
	Included  bool   `json:"included"`
	Manual    bool   `json:"manual"`
	Active    int    `json:"active"`
	Note      string `json:"note"` // e.g. "no tickets right now"
}

type SpaceOption struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type BoardOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // scrum|kanban
}

type FilterCheck struct {
	OK        bool     `json:"ok"`
	Errors    []string `json:"errors"`
	CheckedAt int64    `json:"checkedAt"`
}

// ConfigInput is what BuildConfig needs beyond the effective config.
type ConfigInput struct {
	Position         int
	FilterCheck      *FilterCheck
	NeedsSync        bool
	UnmappedStatuses []string
	ActiveByAccount  map[string]int // active tickets per speaker, from the current snapshot (nil = unknown/0)
}
