// Package model holds Cuisle's provider-neutral domain types (architecture.md §2.1).
//
// FROZEN CONTRACT (Wave 0). Only the orchestrator changes this file. Stdlib only.
// IDs are the provider's ids as strings. Times are time.Time here; the database stores Unix ms UTC.
package model

import "time"

// Lane ids (architecture.md conventions).
const (
	LaneBacklog = "backlog"
	LaneTodo    = "todo"
	LaneProg    = "prog"
	LaneReview  = "review"
	LaneQA      = "qa"
	LaneDone    = "done"
	LaneHidden  = "hidden"
)

// WorkingLanes are the four lanes a standup walks (Done is always last and not part of the walk).
var WorkingLanes = []string{LaneTodo, LaneProg, LaneReview, LaneQA}

// Membership roles.
const (
	RoleBoard   = "board"
	RoleBacklog = "backlog"
)

// UnassignedID is the pseudo speaker id for unassigned tickets.
const UnassignedID = "unassigned"

type Connection struct {
	ID      int64
	Source  string // "jira"
	BaseURL string // https://acme.atlassian.net (no trailing slash)
	Account string // login (Jira: email)
}

// Account is the authenticated user.
type Account struct {
	ID          string
	DisplayName string
}

type Person struct {
	AccountID   string
	DisplayName string
	Active      bool
	Kind        string // "user"|"app"|"customer"
}

type Space struct {
	ID, Key, Name string
	Meta          map[string]any
}

type Board struct {
	ID, Name string
	Kind     string // normalised "scrum"|"kanban"; RawType keeps the provider's value
	RawType  string // Jira: scrum|kanban|simple
	SpaceID  string
	Meta     map[string]any // Jira: {"filterId": 9001, "subQuery": "…", "rankFieldId": "…"}
}

type Column struct {
	Name      string
	Position  int
	StatusIDs []string
}

type Status struct {
	ID, Name    string
	Category    string // "new"|"indeterminate"|"done"|"undefined"
	BlockedHint bool   // name matches (?i)\b(blocked|impeded|on hold)\b
}

type IssueType struct {
	ID, Name string
	Kind     string // story|bug|task|spike|subtask|epic|other
}

type Sprint struct {
	ID, BoardID, Name, State   string    // State: future|active|closed
	StartAt, EndAt, CompleteAt time.Time // zero = unknown
}

type BoardConfig struct {
	Board   Board
	Columns []Column
}

type Issue struct {
	SourceID, Key, SpaceID, Summary, TypeID, StatusID string
	AssigneeID                                        string // "" = unassigned
	Flagged                                           bool
	Rank                                              string
	ParentKey                                         string
	SprintIDs                                         []string
	CreatedAt, UpdatedAt, ResolvedAt                  time.Time
	Meta                                              map[string]any
}

type StatusChange struct {
	ID       string
	At       time.Time
	From, To string // status ids
}

type AssigneeChange struct {
	ID       string
	At       time.Time
	From, To string // account ids, "" = unassigned
}

type FlagChange struct {
	ID      string
	At      time.Time
	Flagged bool // state after the change
}

// Comment is metadata only, never the body.
type Comment struct {
	ID       string
	At       time.Time
	AuthorID string
}

const (
	DevCommit = "commit"
	DevPR     = "pr"
)

type DevEvent struct {
	Kind   string // "commit"|"pr"
	ID     string // commit SHA, or PR key "<repo>#<id>"
	Repo   string
	At     time.Time
	Status string // pr: OPEN|MERGED|DECLINED|UNKNOWN
	Merge  bool   // commit: merge commit
}

// IssueDetail is what Source.Hydrate returns for one issue.
type IssueDetail struct {
	Issue
	Type            IssueType
	Status          Status
	Assignee        *Person
	StatusChanges   []StatusChange
	AssigneeChanges []AssigneeChange
	FlagChanges     []FlagChange
	Comments        []Comment
	CommentTotal    int
	People          []Person // assignees and comment authors seen
	Sprints         []Sprint // sprints referenced by the issue's sprint field
}

type DevActivity struct {
	Fingerprint string
	Changed     bool
	Events      []DevEvent
}

// ── Effective configuration (what the view builder reads; also the config_json of standup_config_history) ──

type LaneConfig struct {
	Lane      string // todo|prog|review|qa
	Position  int    // walk position 0..3
	LimitDays int    // 0 = no limit
}

type ColumnLane struct {
	ColumnName string
	Position   int
	Lane       string // backlog|todo|prog|review|qa|done|hidden
	Auto       bool
	LaneOrder  int // order among the columns of the same lane (0-based, dense); tickets in a lane follow it
}

type StatusLane struct {
	StatusID, StatusName, ColumnName, Lane string
}

type SpeakerConfig struct {
	AccountID string
	Name      string
	Position  int
	Included  bool
	Manual    bool // added via "Add someone from this space"
}

type EffectiveConfig struct {
	StandupID          int64
	Rev                int64
	Name               string
	SpaceID            string
	SpaceKey           string
	SpaceName          string
	BoardID            string
	BoardName          string
	BoardType          string // "scrum"|"kanban"|"" (draft)
	Scope              string // "sprint"|"board"
	ExtraFilter        string
	Lanes              []LaneConfig    // walk order (sorted by Position)
	Columns            []ColumnLane    // board column order
	StatusLanes        []StatusLane    // status id → lane
	Speakers           []SpeakerConfig // by Position
	UnassignedPosition string          // "first"|"last"|"skip"
	ReshuffleDaily     bool
	ShuffleSeed        int64
	ShuffleDate        string // local YYYY-MM-DD the seed belongs to
	PulseDays          int    // 7|14|21|28
	ShowCommits        bool
	ShowPRs            bool
	ShowComments       bool
	ShadeWeekends      bool
	RowsMode           string // "fit"|"roomy"
	DoneWindow         string // "sprint"|"14d"|"yesterday"
	BacklogRows        int
	ActiveSprintIDs    []string
}

// Member is one issue in a standup snapshot, with its state as of the snapshot time.
type Member struct {
	LocalID    int64 // issue.id
	Issue      Issue // state as of AsOf (status, assignee, flagged)
	TypeKind   string
	StatusName string
	Role       string // "board"|"backlog"
	RankIndex  int
}

// IssueHistory holds every recorded event for one issue, ≤ AsOf, oldest first.
type IssueHistory struct {
	StatusChanges   []StatusChange
	AssigneeChanges []AssigneeChange
	FlagChanges     []FlagChange
	Comments        []Comment
	Dev             []DevEvent
}

// BoardSnapshot is what view.Build consumes; produced by cache.LoadCurrent or history.LoadAsOf.
type BoardSnapshot struct {
	StandupID      int64
	AsOf           time.Time // its Location() is the viewer's timezone
	Live           bool
	Config         EffectiveConfig
	Sprints        []Sprint                // active as of AsOf
	Members        []Member                // board role in rank order, then backlog role in rank order
	History        map[string]IssueHistory // by Issue.SourceID
	People         map[string]Person       // by AccountID
	Statuses       map[string]Status       // by status id (BlockedHint used for blocked spans)
	BacklogTotal   int                     // -1 unknown
	IssueURLPrefix string                  // URL = IssueURLPrefix + key (Jira: "<base>/browse/")
	DevActivity    string                  // "ok"|"unavailable"|"unknown"
	LastSyncedAt   time.Time               // zero = never
	Warnings       []string                // warnings of the last finished sync run
}
