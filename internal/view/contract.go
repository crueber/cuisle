// Package view builds the view models the UI renders (architecture.md §3.2, §5.2, ADR 0008).
// Go computes every data-derived value; TypeScript only lays out.
//
// FROZEN CONTRACT: every type in this file (they generate frontend/src/bindings). Implementation of build.go: WP-31;
// ticket*.go (BuildTicket, the TicketDetail family below): WP-T6.
// Imports allowed: model, pulse, cache, settings, adf, stdlib. JSON names are lowerCamel; lists are never nil.
package view

import (
	"github.com/crueber/cuisle/internal/adf"
	"github.com/crueber/cuisle/internal/pulse"
)

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
	IssueID int64  `json:"issueId"` // local issue id: opens the ticket modal (F12)
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

// ── Ticket modal (ADR 0015, ux-spec §14, architecture.md §3.2 "Ticket types") ──

// TicketDetail is the ticket modal payload, built by BuildTicket from a snapshot and cache.Ticket (no network).
type TicketDetail struct {
	IssueID   int64 `json:"issueId"`
	StandupID int64 `json:"standupId"`
	AsOf      int64 `json:"asOf"`
	SyncedAt  int64 `json:"syncedAt"`  // issue hydrated_at (header "as of 9:41 · synced"); 0 = never
	UpdatedAt int64 `json:"updatedAt"` // the issue's provider updated time

	Row    TicketView    `json:"row"` // the board row (key, summary, type, lane, days, blocked, pulse over PulseDays)
	URL    string        `json:"url"`
	Parent *IssueRefView `json:"parent"` // nil = no parent

	PulseDays   int       `json:"pulseDays"` // header track length (21)
	Axis        []AxisDay `json:"axis"`      // len == PulseDays
	LimitDays   int       `json:"limitDays"` // lane limit, 0 = none
	BlockedDays int       `json:"blockedDays"`

	Description  *adf.Doc         `json:"description"` // nil when empty
	Attachments  []AttachmentView `json:"attachments"` // images first, then files, each by created desc
	Subtasks     []IssueRow       `json:"subtasks"`
	SubtasksDone int              `json:"subtasksDone"`
	LinkGroups   []LinkGroup      `json:"linkGroups"` // {Label "IS BLOCKED BY", Rows}
	Comments     []CommentView    `json:"comments"`   // oldest first; the frontend orders per ticket.comment_order
	NewSince     int64            `json:"newSince"`   // start of the previous working day (D3); 0 = none
	Draft        CommentInput     `json:"draft"`

	Rail     []RailGroup `json:"rail"` // state, details, custom, development, dates — in that order; empty groups omitted
	Dev      *DevSummary `json:"dev"`  // nil if none/unavailable
	Can      TicketCaps  `json:"can"`
	Warnings []string    `json:"warnings"` // Warn* below
}

// Ticket warnings (TicketDetail.Warnings).
const (
	WarnMediaUnresolved = "media_unresolved" // a media node could not be matched to an attachment
	WarnRefreshFailed   = "refresh_failed"   // a write succeeded but the follow-up refresh failed (known delta applied)
	WarnNotOnBoard      = "not_on_board"     // the issue left the standup ("No longer on this board", ux-spec §14.12)
)

// IssueRefView is a compact reference to another issue (the parent).
type IssueRefView struct {
	IssueID  int64  `json:"issueId"` // 0 = not synced (opens the browser)
	Key      string `json:"key"`
	Summary  string `json:"summary"`
	TypeKind string `json:"typeKind"`
}

// IssueRow is one subtask or linked-issue row.
type IssueRow struct {
	IssueID          int64  `json:"issueId"` // 0 = not synced → opens the browser
	Key              string `json:"key"`
	Summary          string `json:"summary"`
	TypeKind         string `json:"typeKind"`
	StatusName       string `json:"statusName"`
	StatusLozenge    string `json:"statusLozenge"` // todo|prog|review|qa|done
	AssigneeInitials string `json:"assigneeInitials"`
	URL              string `json:"url"`
	Done             bool   `json:"done"`
}

// LinkGroup is the links sharing one label.
type LinkGroup struct {
	Label string     `json:"label"` // upper case, e.g. "IS BLOCKED BY"
	Rows  []IssueRow `json:"rows"`
}

// AttachmentView is one attachment tile or file card.
type AttachmentView struct {
	ID             int64  `json:"id"` // attachment row id (/media/{id}, EnsureMedia, OpenAttachment)
	Name           string `json:"name"`
	Ext            string `json:"ext"` // upper case without the dot, e.g. "PDF"
	Mime           string `json:"mime"`
	Bytes          int64  `json:"bytes"`
	AuthorInitials string `json:"authorInitials"`
	AuthorName     string `json:"authorName"`
	CreatedAt      int64  `json:"createdAt"`
	Image          bool   `json:"image"`
	Width          int    `json:"width"` // 0 = unknown
	Height         int    `json:"height"`
	Cached         string `json:"cached"`   // full|thumb|none
	URL            string `json:"url"`      // /media/{id}
	ThumbURL       string `json:"thumbUrl"` // /media/{id}/thumb
}

// PersonRef is a person as the modal shows them.
type PersonRef struct {
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
	Initials  string `json:"initials"`
	Self      bool   `json:"self"` // the connected user
}

// CommentView is one stored comment.
type CommentView struct {
	ID         string    `json:"id"`
	Author     PersonRef `json:"author"`
	CreatedAt  int64     `json:"createdAt"`
	UpdatedAt  int64     `json:"updatedAt"`
	Edited     bool      `json:"edited"`
	Visibility string    `json:"visibility"` // "" or e.g. "Developers only"
	Body       adf.Doc   `json:"body"`
}

// CommentInput is composer text with its mentions (drafts and AddComment).
type CommentInput struct {
	Text     string    `json:"text"`
	Mentions []Mention `json:"mentions"`
}

// Mention is an @-mention: a rune range of CommentInput.Text.
type Mention struct {
	Start     int    `json:"start"` // rune offsets, End exclusive
	End       int    `json:"end"`
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
}

// RailGroup is one group of the field rail. Fields without a value produce no row; their labels go to NotSet.
type RailGroup struct {
	ID     string    `json:"id"` // state|details|custom|development|dates
	Label  string    `json:"label"`
	Rows   []RailRow `json:"rows"`
	NotSet []string  `json:"notSet"` // labels of empty fields, in rail order
}

// Rail group ids (RailGroup.ID), in rail order.
const (
	RailState       = "state"
	RailDetails     = "details"
	RailCustom      = "custom"
	RailDevelopment = "development"
	RailDates       = "dates"
)

// Rail-only row kinds (RailRow.Kind values beyond the model field kinds) and the members each one sets. Every other
// row uses a model field kind (model.FieldKinds) with the member noted in ux-spec §14.6 (text → Text, number → Number,
// options/labels/versions/components/groups/cascade → Items, user/users → Users, paragraph → Rich, unknown → Raw,
// url → URL + URLLabel, date/datetime → Text "YYYY-MM-DD"/RFC 3339).
// The Development group renders from its rail rows; TicketDetail.Dev carries the same facts structured.
const (
	RailKindStatus   = "status"   // Edit "status"; Text = status name (the swatch uses TicketDetail.Row.Lane)
	RailKindFlag     = "flag"     // Edit "flag"; Text "Flagged"|"Not flagged"; Sub = blocked days ("3d") when flagged
	RailKindParent   = "parent"   // Text = parent key, Sub = parent summary; Mono
	RailKindSprint   = "sprint"   // Text = sprint name, Sub = "ends 1 Dec"
	RailKindTime     = "time"     // Text "14h of 24h", Progress = spent/estimate 0..1; Mono
	RailKindWatchers = "watchers" // Number; Mono
	RailKindBranch   = "branch"   // Text = most recent PR source branch; Mono; no row when unknown
	RailKindCommits  = "commits"  // Text "7 commits", Sub "last 2h"
	RailKindPR       = "pr"       // Label "#418", Text = PR title (the repository when unknown), Sub = repository, Items = [status OPEN|MERGED|DECLINED|UNKNOWN]
)

// RailRow is one field row. Kind is a model field kind or a RailKind*; only the members relevant to it are set.
type RailRow struct {
	FieldID  string      `json:"fieldId"`
	Label    string      `json:"label"`
	Kind     string      `json:"kind"`
	Edit     string      `json:"edit"` // ""|"status"|"assignee"|"flag"
	Text     string      `json:"text"`
	Number   *float64    `json:"number"`
	Items    []string    `json:"items"`
	Users    []PersonRef `json:"users"`
	Rich     *adf.Doc    `json:"rich"`
	Raw      string      `json:"raw"`
	Mono     bool        `json:"mono"`
	Sub      string      `json:"sub"`      // e.g. "ends 1 Dec"
	Progress *float64    `json:"progress"` // time tracking 0..1
	URL      string      `json:"url"`
	URLLabel string      `json:"urlLabel"` // e.g. "support.example.com › #48213"
}

// TransitionList is the Status combobox content.
type TransitionList struct {
	Items     []TransitionOption `json:"items"`
	FetchedAt int64              `json:"fetchedAt"`
	Offline   bool               `json:"offline"` // cached list shown disabled
}

// TransitionOption is one transition.
type TransitionOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ToStatusName string `json:"toStatusName"`
	ToLane       string `json:"toLane"` // lane of the target status in the standup (optimistic row update)
	NeedsScreen  bool   `json:"needsScreen"`
	URL          string `json:"url"` // issue URL for "↗ Jira"
}

// TicketCaps says which light writes the modal offers.
type TicketCaps struct {
	Transition bool   `json:"transition"`
	Assign     bool   `json:"assign"`
	Flag       bool   `json:"flag"`
	Comment    bool   `json:"comment"`
	Reason     string `json:"reason"` // "offline"|"auth"|"read_only"|""
}

// DevSummary is the rail's Development group.
type DevSummary struct {
	Branch       string  `json:"branch"`
	Commits      int     `json:"commits"`
	LastCommitAt int64   `json:"lastCommitAt"`
	PRs          []DevPR `json:"prs"`
}

// DevPR is one pull request.
type DevPR struct {
	ID     string `json:"id"`     // display id without "#", e.g. "421"
	Title  string `json:"title"`  // the PR title; the repository name when the title is not known
	Repo   string `json:"repo"`   // repository name
	Status string `json:"status"` // OPEN|MERGED|DECLINED|UNKNOWN
}

// TicketFieldPrefs is Configure › Ticket fields (per connection; ux-spec §14.8) and BuildTicket's rail order.
type TicketFieldPrefs struct {
	Pinned    []FieldPref   `json:"pinned"`    // pinned custom fields, in rail order
	Hidden    []FieldPref   `json:"hidden"`    // hidden fields
	Available []FieldOption `json:"available"` // custom fields that can be pinned (not pinned), A–Z
}

// FieldPref is one pinned or hidden field.
type FieldPref struct {
	FieldID  string `json:"fieldId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Position int    `json:"position"` // pinned order (0-based); -1 when only hidden
	Hidden   bool   `json:"hidden"`
}

// FieldOption is a field the "Pin a field…" combobox offers.
type FieldOption struct {
	FieldID string `json:"fieldId"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Custom  bool   `json:"custom"`
}
