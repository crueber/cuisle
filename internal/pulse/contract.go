// Package pulse holds the pure functions that turn synced facts into pulse inputs (jira-sync.md §5).
// Every function takes asOf instead of "now" and ignores events after it. No I/O, no time.Now().
//
// FROZEN CONTRACT: the types in this file and the exported signatures in the other files. File ownership:
//
//	WP-04 segments*.go lane*.go blocked*.go
//	WP-05 marks*.go label*.go speakers*.go
//
// Imports allowed: model, stdlib.
package pulse

import (
	"time"

	"github.com/crueber/cuisle/internal/model"
)

// Seg is a contiguous lane segment in day indexes: 0 ≤ From < To ≤ Days.
type Seg struct {
	Lane string `json:"lane"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

// Mark is one day with activity (only days with a non-zero count are emitted).
type Mark struct {
	Day      int `json:"day"`
	Commits  int `json:"commits"`
	PRs      int `json:"prs"`
	Comments int `json:"comments"`
}

// Span is an inclusive range of day indexes.
type Span struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// AxisDay describes one day column of the track.
type AxisDay struct {
	Date    string `json:"date"`  // YYYY-MM-DD (local)
	Label   string `json:"label"` // day of month, e.g. "23"
	Weekend bool   `json:"weekend"`
	Monday  bool   `json:"monday"`
}

// Window is the N-day pulse window ending today (the local date of asOf).
// Starts[k] = local midnight of (today − (N−1) + k), k = 0…N (DST-safe, built with time.Date in Loc).
type Window struct {
	Days   int
	Loc    *time.Location
	AsOf   time.Time
	Starts []time.Time // len Days+1
}

// Interval is one stretch of the status timeline, already mapped to a lane.
type Interval struct {
	StatusID string
	Lane     string // mapped lane; unmapped/hidden/backlog are drawn as "todo" (jira-sync.md §5.2)
	RealLane string // the mapped lane before the drawing substitution ("" when unmapped)
	From     time.Time
	To       time.Time // asOf for the last interval
}

// Ticket is everything pulse needs about one issue (state as of asOf; history may contain later events, which every
// function must ignore).
type Ticket struct {
	CreatedAt     time.Time
	UpdatedAt     time.Time
	StatusID      string               // current (as of asOf)
	Flagged       bool                 // current (as of asOf)
	StatusChanges []model.StatusChange // oldest first
	FlagChanges   []model.FlagChange
	Comments      []model.Comment
	Dev           []model.DevEvent
}

// SpeakerInput feeds SpeakerOrder.
type SpeakerInput struct {
	Speakers           []model.SpeakerConfig // by Position (included and excluded)
	ReshuffleDaily     bool
	Seed               int64           // shuffle seed for today (DailySeed or a Shuffle-button seed)
	UnassignedPosition string          // first|last|skip
	HasUnassigned      bool            // there are unassigned tickets
	WithTickets        map[string]bool // account ids that have tickets on the board
}

// SpeakerSlot is one entry of the effective speaking order.
type SpeakerSlot struct {
	ID         string // account id or model.UnassignedID
	Included   bool
	Unassigned bool
}

// LabelInput feeds Label (jira-sync.md §5.8).
type LabelInput struct {
	LaneLabel     string // e.g. "In Progress"
	DaysInLane    int
	PrevLaneLabel string // "" when no previous lane inside the window
	PrevDays      int
	Commits       int
	PRs           int
	Comments      int
	Days          int // window length N
	BlockedDays   int
	Done          bool
}
