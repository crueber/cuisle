// Package sync is the provider-neutral sync orchestrator over source.Source (jira-sync.md §4, architecture.md §5.4).
// Plan → fetch into memory → one atomic apply. No timers, no polling, no retry loops.
//
// FROZEN CONTRACT: this file. Ownership:
//
//	WP-29 orchestrator*.go plan*.go hydrate*.go progress*.go   (runs, phases 0–6, progress, cancel)
//	WP-30 apply*.go (+ internal/cache/apply*.go)               (phase 7 atomic apply)
//
// Imports allowed: source (never source/jira), cache, settings, secrets, model, store, apperr, stdlib.
package sync

import (
	"context"
	"log/slog"
	gosync "sync"
	"time"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/store"
)

// Emitter is implemented by the app over Wails events.
type Emitter interface {
	Emit(name string, payload any)
}

// Clock abstracts time for tests.
type Clock interface {
	Now() time.Time
	Location() *time.Location
}

// Event names (architecture.md §3.3).
const (
	EvSyncStarted    = "sync:started"
	EvSyncProgress   = "sync:progress"
	EvSyncFinished   = "sync:finished"
	EvDataChanged    = "data:changed"
	EvConnectionAuth = "connection:auth"
)

// Modes, triggers, phases, statuses.
const (
	ModeIncremental = "incremental"
	ModeFull        = "full"

	TriggerManual = "manual"
	TriggerOpen   = "open"
	TriggerConfig = "config"

	PhaseConfig = "config"
	PhaseScan   = "scan"
	PhaseApply  = "apply"

	StatusOK        = "ok"
	StatusPartial   = "partial"
	StatusCancelled = "cancelled"
	StatusFailed    = "failed"
)

// Event payloads (registered as typed Wails events by the app).
type SyncStarted struct {
	RunID     int64  `json:"runId"`
	StandupID int64  `json:"standupId"`
	Mode      string `json:"mode"`
	Trigger   string `json:"trigger"`
}

type SyncProgress struct {
	RunID     int64  `json:"runId"`
	StandupID int64  `json:"standupId"`
	Phase     string `json:"phase"` // config|scan|issues|changelog|comments|dev|apply
	Done      int    `json:"done"`
	Total     int    `json:"total"`
}

type SyncStats struct {
	Issues   int   `json:"issues"`
	Requests int   `json:"requests"`
	Ms       int64 `json:"ms"`
}

type SyncFinished struct {
	RunID        int64     `json:"runId"`
	StandupID    int64     `json:"standupId"`
	Status       string    `json:"status"` // ok|partial|cancelled|failed
	ErrorCode    string    `json:"errorCode,omitempty"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	Warnings     []string  `json:"warnings"`
	FinishedAt   int64     `json:"finishedAt"`
	Stats        SyncStats `json:"stats"`
}

type DataChanged struct {
	StandupID int64  `json:"standupId"` // 0 = all
	What      string `json:"what"`      // view|config|standups
}

type ConnectionAuth struct {
	State string `json:"state"` // ok|invalid|missing
}

// Status is the live state of one standup's sync (the app maps it to view.SyncStatus).
type Status struct {
	StandupID     int64
	RunID         int64
	State         string // idle|running|failed
	Mode          string
	Phase         string
	Done, Total   int
	LastSuccessAt time.Time
	LastAttemptAt time.Time
	LastStatus    string
	Warnings      []string
	ErrorCode     string
	ErrorMessage  string
	NeedsSync     bool
}

type Options struct {
	DB         *store.DB
	Keyring    secrets.Keyring
	Emitter    Emitter
	Clock      Clock
	Logger     *slog.Logger
	SourceOpts source.Options
	// OpenSource defaults to source.Open.
	OpenSource func(conn model.Connection, secret string, opts source.Options) (source.Source, error)
	// AfterSync is called after a successful (ok|partial) apply, outside the transaction; sink delivery hook. May be nil.
	AfterSync func(ctx context.Context, standupID, runID int64)
	// MaxParallel runs (default 2). HydrateConcurrency (default 4) bounds concurrent DevActivity calls.
	MaxParallel        int
	HydrateConcurrency int
	// ProgressInterval coalesces progress events (default 100 ms ⇒ ≤ 10/s).
	ProgressInterval time.Duration
}

// Orchestrator runs syncs. At most one run per standup, at most MaxParallel at once.
type Orchestrator struct {
	opts Options
	mu   gosync.Mutex
	runs map[int64]*run // by standup id
	last map[int64]Status
	sem  chan struct{}
	wg   gosync.WaitGroup

	// Guarded by mu (unexported additions; the exported API is unchanged).
	closed   bool                    // set by Shutdown: Start refuses
	starting map[int64]chan struct{} // closed when a reserved run has its id (or failed to get one)
}

// run is the state of one in-flight run (WP-29 may add fields in orchestrator.go via an embedded struct if needed).
type run struct {
	id        int64
	standupID int64
	mode      string
	trigger   string
	started   time.Time
	cancel    context.CancelFunc
	done      chan struct{}
	status    Status
}

// Fetched is everything phases 0–6 gathered in memory, handed to apply (phase 7).
type Fetched struct {
	RunID        int64
	StandupID    int64
	ConnectionID int64
	StartedAt    time.Time // T for spans and config history
	Mode         string    // effective mode after promotion
	Board        model.BoardConfig
	BoardChanged bool // column→status set differs from the cached one
	Sprints      []model.Sprint
	Statuses     []model.Status
	Types        []model.IssueType
	People       []model.Person // assignees + comment authors seen
	SourceMeta   map[string]any
	DevState     string                       // ok|unavailable|unknown
	Members      []Member                     // scanned membership, board role wins over backlog
	Details      []model.IssueDetail          // hydrated issues
	Dev          map[string]model.DevActivity // by source id, only changed/fetched ones
	DevChecked   []string                     // source ids whose dev activity was checked this run
	BacklogTotal int
	Warnings     []string
	Partial      bool // comment/dev per-issue failures
}

// Member is one scanned member.
type Member struct {
	SourceID  string
	Key       string
	Role      string // board|backlog
	RankIndex int
	UpdatedAt time.Time
}

// ApplyResult reports what the apply changed.
type ApplyResult struct {
	Rev           int64
	ConfigChanged bool // column map or speakers changed (emit data:changed what=config)
	Hydrated      int
}
