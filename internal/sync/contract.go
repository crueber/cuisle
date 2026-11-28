// Package sync is the provider-neutral sync orchestrator over source.Source (jira-sync.md §4, architecture.md §5.4).
// Plan → fetch into memory → one atomic apply. No timers, no polling, no retry loops.
//
// FROZEN CONTRACT: this file. Ownership:
//
//	WP-29 orchestrator*.go plan*.go hydrate*.go progress*.go   (runs, phases 0–6, progress, cancel)
//	WP-30 apply*.go (+ internal/cache/apply*.go)               (phase 7 atomic apply)
//	WP-T6 oneissue*.go content*.go media*.go                    (one-issue apply, content and media phases; ADR 0015)
//
// Imports allowed: source (never source/jira), cache, settings, secrets, model, store, apperr, stdlib.
package sync

import (
	"context"
	"io"
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

	// Ticket modal (ADR 0015). ticket:write_failed is emitted only by the app (internal/app).
	EvTicketUpdated = "ticket:updated"
	EvMediaReady    = "media:ready"
	EvSyncThrottled = "sync:throttled" // the per-connection limiter paused or slowed (jira-sync.md §3.1)
)

// TicketUpdated causes and changed-part names (architecture.md §3.3).
const (
	CauseWrite = "write"
	CauseSync  = "sync"

	ChangedStatus      = "status"
	ChangedAssignee    = "assignee"
	ChangedFlag        = "flag"
	ChangedComments    = "comments"
	ChangedDescription = "description"
	ChangedFields      = "fields"
	ChangedAttachments = "attachments"
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
	PhaseMedia  = "media" // lazy media phase, runs after sync:finished (jira-sync.md §2.14); source.PhaseContent precedes apply

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
	Phase     string `json:"phase"` // config|scan|issues|changelog|comments|content|dev|apply|media
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

// TicketUpdated: after a light write's refresh is applied (cause write, emitted by the app) and after a sync apply for
// every hydrated issue (cause sync). The frontend acts only if that issue's modal is open.
type TicketUpdated struct {
	IssueID    int64    `json:"issueId"`
	Key        string   `json:"key"`
	StandupIDs []int64  `json:"standupIds"` // standups that have the issue as a current member
	Cause      string   `json:"cause"`      // write|sync
	Changed    []string `json:"changed"`    // Changed* names
}

// MediaReady: an image landed in the cache after the record apply (media phase or EnsureMedia).
type MediaReady struct {
	AttachmentID int64  `json:"attachmentId"`
	IssueID      int64  `json:"issueId"`
	Cached       string `json:"cached"` // full|thumb
}

// SyncThrottled: the per-connection limiter paused (429) or slowed (near-limit signal). Bridged to this event by the
// app (the limiter lives in source/jira, which may not import sync).
type SyncThrottled struct {
	ResumeAt int64  `json:"resumeAt"` // Unix ms; 0 = slowed, not paused
	Reason   string `json:"reason"`   // "rate_limited"|"near_limit"
}

// MediaStore is the part of internal/media's Cache that the media phase needs. sync may not import media
// (architecture.md §2 table), so the app adapts media.Cache to it. Nil Options.Media = no media phase.
type MediaStore interface {
	// Put stores an original (deriving the thumbnail) and returns the cache state: "full"|"thumb"|"none".
	Put(ctx context.Context, att model.Attachment, connectionID int64, r io.Reader) (cached string, err error)
	// PutThumb stores a provider-made thumbnail.
	PutThumb(ctx context.Context, att model.Attachment, connectionID int64, r io.Reader) error
	// Evict trims originals to the cap (after the media phase).
	Evict(ctx context.Context) (freedBytes int64, err error)
}

// IssueApplied is what ApplyIssue changed. The caller emits ticket:updated and data:changed from it.
type IssueApplied struct {
	IssueID       int64
	Key           string
	StandupIDs    []int64  // standups whose rev was bumped (current members)
	Changed       []string // Changed* names
	StatusChanged bool     // transition_cache row dropped
	Skipped       bool     // stored row was newer (incoming updated older): issue row untouched, change rows merged
}

// IssueDelta is the known effect of a successful write, applied to the issue row only when the follow-up refresh
// failed (jira-sync.md §4.8). Nil = unchanged. No change-table rows are invented.
type IssueDelta struct {
	StatusID   *string
	AssigneeID *string // "" = unassigned
	Flagged    *bool
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
	// Media receives image bytes in the lazy media phase (ADR 0015). Nil = no media phase.
	Media MediaStore
}

// Orchestrator runs syncs. At most one run per standup, at most MaxParallel at once.
type Orchestrator struct {
	opts Options
	mu   gosync.Mutex
	runs map[int64]*run // by standup id
	last map[int64]Status
	sem  chan struct{}
	wg   waitGroup

	// Guarded by mu.
	closed       bool                            // set by Shutdown: Start refuses
	paused       int                             // incremented by PauseAndCancel: Start and mediaContext refuse
	starting     map[int64]chan struct{}         // closed when a reserved run has its id (or failed to get one)
	mediaCancels map[*context.CancelFunc]struct{} // active post-sync media phases, cancelled by Shutdown and PauseAndCancel
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
