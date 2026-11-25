// Package model holds Cuisle's provider-neutral domain types (architecture.md §2.1).
//
// FROZEN CONTRACT (Wave 0). Only the orchestrator changes this file. Stdlib only.
// IDs are the provider's ids as strings. Times are time.Time here; the database stores Unix ms UTC.
package model

import (
	"encoding/json"
	"time"
)

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

// WorkingLanes are the four built-in lanes a standup walks (Done is always last and not part of the walk). A standup
// can add custom working lanes (IsCustomLane); use IsWorkingLane to test a lane id.
var WorkingLanes = []string{LaneTodo, LaneProg, LaneReview, LaneQA}

// Custom lanes (lanes the user names in Configure › Status mapping) have ids "c1", "c2", … per standup, never reused
// after a delete. They are working lanes. Each has a colour index 1..LaneHues from the extended lane palette.
const (
	CustomLanePrefix = "c"
	LaneHues         = 8  // size of the extended lane palette (design/tokens.json color.*.lane.x1..x8)
	MaxCustomLanes   = 12 // per standup
	MaxLaneLabel     = 40 // runes
)

// IsCustomLane reports whether lane is a custom lane id ("c" followed by a positive number without leading zeros).
func IsCustomLane(lane string) bool {
	if len(lane) < 2 || lane[0] != 'c' || lane[1] == '0' {
		return false
	}
	for i := 1; i < len(lane); i++ {
		if lane[i] < '0' || lane[i] > '9' {
			return false
		}
	}
	return true
}

// IsWorkingLane reports whether lane is a working lane: one of the four built-in walk lanes or a custom lane.
func IsWorkingLane(lane string) bool {
	switch lane {
	case LaneTodo, LaneProg, LaneReview, LaneQA:
		return true
	}
	return IsCustomLane(lane)
}

// IsShownLane reports whether tickets in lane appear on the standup: working lanes and done.
func IsShownLane(lane string) bool { return lane == LaneDone || IsWorkingLane(lane) }

// DefaultLaneLabel is the built-in label of a lane id ("" for a custom lane, which always has its own label).
func DefaultLaneLabel(lane string) string {
	switch lane {
	case LaneTodo:
		return "To Do"
	case LaneProg:
		return "In Progress"
	case LaneReview:
		return "In Review"
	case LaneQA:
		return "QA"
	case LaneDone:
		return "Done"
	case LaneBacklog:
		return "Backlog"
	case LaneHidden:
		return "Hidden"
	}
	return ""
}

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

// Comment is one comment on an issue. Since ADR 0015 the body is stored locally; it is never exported, logged or put
// into crash output (AGENTS.md invariant 12). Sources without Capabilities.Content leave Body empty (Raw nil).
type Comment struct {
	ID         string
	At         time.Time   // created
	UpdatedAt  time.Time   // = At until edited; "edited" when after At (zero from metadata-only sources = not edited)
	AuthorID   string      // "" = unknown/anonymous
	Body       RichText    // zero (Raw nil) when the source did not return a body
	Visibility *Visibility // nil = visible to everyone
}

// Visibility restricts who can see a comment in the provider.
type Visibility struct {
	Type  string // "role"|"group"
	Value string // e.g. "Developers"
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
	Title  string // pr: pull request title ("" = unknown; rows synced before migration 0004)
	Branch string // pr: source branch name ("" = unknown)
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
	Comments        []Comment // complete list when Capabilities.Content (bodies included), else possibly truncated metadata
	CommentTotal    int
	People          []Person      // assignees and comment authors seen
	Sprints         []Sprint      // sprints referenced by the issue's sprint field
	Content         *IssueContent // nil when !Capabilities.Content
}

// ── Ticket content (ADR 0015; architecture.md §2.1) ──

// RichTextADF is the neutral rich-text format: Atlassian Document Format JSON. Other sources convert to it.
const RichTextADF = "adf"

// RichText is rich content as received (parsed only at view-build time by internal/adf).
type RichText struct {
	Format string          `json:"format"` // "adf"
	Raw    json.RawMessage `json:"raw"`    // the document JSON verbatim; nil = empty
}

// IsEmpty reports whether there is no content.
func (r RichText) IsEmpty() bool { return len(r.Raw) == 0 || string(r.Raw) == "null" }

// Attachment is attachment metadata. Bytes live in internal/media, never in the model.
type Attachment struct {
	LocalID     int64 // attachment row id (the /media/{id} id); set by the cache, 0 from sources
	ID          string
	Name        string
	Mime        string
	Bytes       int64
	AuthorID    string
	CreatedAt   time.Time
	MediaFileID string // id used by rich-text media nodes; "" if unknown (jira-sync.md §2.14)
	Width       int    // 0 = unknown
	Height      int
}

// Field kinds (FieldDef.Kind, FieldValue.Kind; data-model.md field_def.kind CHECK).
const (
	FieldText       = "text"
	FieldParagraph  = "paragraph"
	FieldNumber     = "number"
	FieldDate       = "date"
	FieldDateTime   = "datetime"
	FieldURL        = "url"
	FieldOption     = "option"
	FieldOptions    = "options"
	FieldCascade    = "cascade"
	FieldUser       = "user"
	FieldUsers      = "users"
	FieldGroup      = "group"
	FieldGroups     = "groups"
	FieldVersion    = "version"
	FieldVersions   = "versions"
	FieldComponent  = "component"
	FieldComponents = "components"
	FieldLabels     = "labels"
	FieldProject    = "project"
	FieldTeam       = "team"
	FieldUnknown    = "unknown"
)

// FieldKinds lists every field kind, in the order of the data-model CHECK constraint.
var FieldKinds = []string{FieldText, FieldParagraph, FieldNumber, FieldDate, FieldDateTime, FieldURL, FieldOption,
	FieldOptions, FieldCascade, FieldUser, FieldUsers, FieldGroup, FieldGroups, FieldVersion, FieldVersions,
	FieldComponent, FieldComponents, FieldLabels, FieldProject, FieldTeam, FieldUnknown}

// FieldDef is one entry of the per-connection field registry (field discovery, jira-sync.md §2.2).
type FieldDef struct {
	ID         string // "priority", "customfield_10042"
	Name       string
	Kind       string // one of FieldKinds
	Custom     bool
	CustomType string          // provider type key, e.g. com.atlassian.jira.plugin.system.customfieldtypes:select
	Schema     json.RawMessage // provider schema verbatim
}

// FieldValue is the current value of one non-empty field. Stored as issue_field_value.value_json (the JSON names
// below are that storage format; empty members are omitted).
type FieldValue struct {
	FieldID string          `json:"fieldId,omitempty"`
	Kind    string          `json:"kind,omitempty"`   // one of FieldKinds
	Text    string          `json:"text,omitempty"`   // text, option, url, date ("YYYY-MM-DD"), datetime (RFC 3339), group/project/team/version/component name
	Number  *float64        `json:"number,omitempty"` // number (story points, …)
	Items   []string        `json:"items,omitempty"`  // options, labels, versions, components, groups; cascade = [parent, child]
	Users   []string        `json:"users,omitempty"`  // account ids (user, users)
	Rich    *RichText       `json:"rich,omitempty"`   // paragraph
	Raw     json.RawMessage `json:"raw,omitempty"`    // unknown: provider value verbatim
}

// IssueLink is one link to another issue, direction applied ("is blocked by" vs "blocks").
type IssueLink struct {
	ID            string
	TypeName      string // provider link type name, e.g. "Blocks"
	Label         string // "is blocked by"
	Outward       bool
	OtherSourceID string
	OtherKey      string
	OtherSummary  string
	OtherStatusID string
	OtherTypeID   string
}

// IssueRef is a display snapshot of another issue (subtask, parent).
type IssueRef struct {
	SourceID   string
	Key        string
	Summary    string
	StatusID   string
	TypeID     string
	AssigneeID string
}

// IssueContent is the ticket content Hydrate/GetIssue return when Capabilities.Content.
type IssueContent struct {
	Description *RichText // nil = empty
	Environment *RichText
	Attachments []Attachment
	Fields      []FieldValue // every non-empty field value except those modelled on Issue; empties are implied by FieldDef
	Links       []IssueLink
	Subtasks    []IssueRef
	Parent      *IssueRef
	Watchers    int    // -1 unknown
	Hash        string // content hash; unchanged hash → no rewrite
}

// Transition is one workflow transition offered for an issue (Source.Transitions).
type Transition struct {
	ID           string
	Name         string
	ToStatusID   string
	ToStatusName string
	NeedsScreen  bool // a provider screen with required fields: never posted, the UI opens the browser
}

// Mention is an @-mention inside composer text: a rune range of Text.
// The JSON names are the ticket_draft.mentions_json storage format ([{start,end,accountId,name}]).
type Mention struct {
	Start     int    `json:"start"` // rune offset into NewComment.Text
	End       int    `json:"end"`   // exclusive
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
}

// NewComment is what the composer sends (Source.AddComment); adf.FromPlainText turns it into ADF.
type NewComment struct {
	Text     string
	Mentions []Mention
}

type DevActivity struct {
	Fingerprint string
	Changed     bool
	Events      []DevEvent
}

// ── Effective configuration (what the view builder reads; also the config_json of standup_config_history) ──

type LaneConfig struct {
	Lane      string // todo|prog|review|qa|c<N>
	Position  int    // walk position 0..len-1
	LimitDays int    // 0 = no limit
	Label     string // display label; "" = the built-in default (custom lanes always have one)
	Hue       int    // custom lanes: colour index 1..LaneHues; 0 for built-in lanes
}

type ColumnLane struct {
	ColumnName string
	Position   int
	Lane       string // backlog|todo|prog|review|qa|done|hidden|c<N>
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
	Lanes              []LaneConfig    // working lanes incl. custom, walk order (sorted by Position)
	DoneLabel          string          // display label of the Done lane; "" = "Done"
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

// LaneLabel returns the display label of lane in this config: the configured label, else the built-in default. A
// custom lane id the config no longer has (deleted) is returned unchanged.
func (c EffectiveConfig) LaneLabel(lane string) string {
	if lane == LaneDone && c.DoneLabel != "" {
		return c.DoneLabel
	}
	for _, l := range c.Lanes {
		if l.Lane == lane && l.Label != "" {
			return l.Label
		}
	}
	if d := DefaultLaneLabel(lane); d != "" {
		return d
	}
	return lane
}

// HasLane reports whether lane is a lane of this config: a built-in lane id or one of its custom lanes.
func (c EffectiveConfig) HasLane(lane string) bool {
	if !IsCustomLane(lane) {
		return DefaultLaneLabel(lane) != ""
	}
	for _, l := range c.Lanes {
		if l.Lane == lane {
			return true
		}
	}
	return false
}
