// Package export builds the cuisle.export.v1 document (architecture.md §3.5). Additive-only within v1.
// No secrets, no comment bodies, no e-mail addresses.
//
// FROZEN CONTRACT: the types here. Implementation of export.go: WP-09. Imports allowed: view, model, stdlib.
package export

import "time"

const Schema = "cuisle.export.v1"

type Document struct {
	Schema      string     `json:"schema"`
	GeneratedAt string     `json:"generatedAt"` // RFC 3339 UTC
	Generator   Generator  `json:"generator"`
	Kind        string     `json:"kind"` // current|range
	Source      SourceRef  `json:"source"`
	Standup     StandupRef `json:"standup"`
	Snapshots   []Snapshot `json:"snapshots"`
}

type Generator struct {
	Name    string `json:"name"` // "Cuisle"
	Version string `json:"version"`
}

type SourceRef struct {
	Kind    string `json:"kind"`
	BaseURL string `json:"baseUrl"`
}

type StandupRef struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Space SpaceRef `json:"space"`
	Board BoardRef `json:"board"`
	Scope string   `json:"scope"`
}

type SpaceRef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type BoardRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type Snapshot struct {
	AsOf      string     `json:"asOf"`     // RFC 3339 UTC
	Date      string     `json:"date"`     // local YYYY-MM-DD
	Timezone  string     `json:"timezone"` // IANA name
	Live      bool       `json:"live"`
	SyncedAt  *string    `json:"syncedAt"` // null = never synced
	Sprint    *SprintRef `json:"sprint"`   // null for kanban / none
	PulseDays int        `json:"pulseDays"`
	Window    DateRange  `json:"window"`
	Lanes     []LaneRef  `json:"lanes"`
	People    []Person   `json:"people"`
	Tickets   []Ticket   `json:"tickets"`
	Backlog   []Backlog  `json:"backlog"`
	Counts    Counts     `json:"counts"`
}

type SprintRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	StartAt string `json:"startAt"`
	EndAt   string `json:"endAt"`
}

type DateRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type LaneRef struct {
	Lane      string `json:"lane"`  // todo|prog|review|qa|done|c<N> (a lane the user named)
	Label     string `json:"label"` // the configured label (renamed built-in lanes and custom lanes included)
	LimitDays int    `json:"limitDays"`
	Kind      string `json:"kind"`   // working|done (additive in v1)
	Custom    bool   `json:"custom"` // a lane the user named (additive in v1)
}

type Person struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Included bool   `json:"included"`
	Active   int    `json:"active"`
	Done     int    `json:"done"`
}

type Ticket struct {
	Key        string  `json:"key"`
	SourceID   string  `json:"sourceId"`
	URL        string  `json:"url"`
	Summary    string  `json:"summary"`
	Type       string  `json:"type"`
	Assignee   *string `json:"assignee"` // null = unassigned
	Lane       string  `json:"lane"`
	Status     string  `json:"status"`
	DaysInLane int     `json:"daysInLane"`
	OverLimit  bool    `json:"overLimit"`
	Blocked    bool    `json:"blocked"`
	DoneAt     *string `json:"doneAt"` // RFC 3339 or null
	Pulse      Pulse   `json:"pulse"`
	LaneLabel  string  `json:"laneLabel"` // the lane's configured label (additive in v1)
}

type Pulse struct {
	Segments []DatedSeg  `json:"segments"`
	Marks    []DatedMark `json:"marks"`
	Blocked  []DateRange `json:"blocked"`
}

type DatedSeg struct {
	Lane string `json:"lane"`
	From string `json:"from"` // inclusive dates
	To   string `json:"to"`
}

type DatedMark struct {
	Date     string `json:"date"`
	Commits  int    `json:"commits"`
	PRs      int    `json:"prs"`
	Comments int    `json:"comments"`
}

type Backlog struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Type    string `json:"type"`
	URL     string `json:"url"`
}

type Counts struct {
	Active       int `json:"active"`
	Unassigned   int `json:"unassigned"`
	Done         int `json:"done"`
	Backlog      int `json:"backlog"`
	BacklogTotal int `json:"backlogTotal"`
}

// Meta is what the document needs beyond the views.
type Meta struct {
	GeneratedAt time.Time
	Version     string // buildinfo version
	Kind        string // current|range
	SourceKind  string
	BaseURL     string
	StandupID   int64
	Name        string
	SpaceKey    string
	SpaceName   string
	BoardID     string
	BoardName   string
	BoardKind   string
	Scope       string
	Timezone    *time.Location
}
