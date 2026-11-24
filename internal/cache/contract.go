// Package cache reads and writes the provider-neutral record store (data-model.md §2.2, §2.4–2.7, §4).
//
// FROZEN CONTRACT: the types in this file and exported signatures elsewhere. Ownership:
//
//	WP-20 everything except apply*.go (read side, connection, runs, reference caches, trim)
//	WP-30 apply*.go (record upserts + membership/spans + config history + sync state, called by sync's apply)
//	WP-T6 ticket*.go content*.go (LoadTicket, content/attachment/field upserts; ADR 0015)
//
// Imports allowed: store, model, apperr, stdlib.
package cache

import (
	"time"

	"github.com/crueber/cuisle/internal/model"
)

// ConnRow is the connection row (never the secret).
type ConnRow struct {
	ID               int64
	Source           string
	BaseURL          string
	Account          string
	AccountID        string
	DisplayName      string
	AuthState        string // unknown|ok|invalid|missing
	VerifiedAt       time.Time
	SourceMeta       map[string]any
	DevActivityState string // unknown|ok|unavailable
	DevCheckedAt     time.Time
	CreatedAt        time.Time
}

// TabRow is Q6: one row per live standup.
type TabRow struct {
	ID            int64
	Rev           int64
	Name          string
	SpaceKey      string
	Position      int
	Configured    bool
	NeedsSync     bool
	LastSuccessAt time.Time
	ActiveCount   int
	SpeakerCount  int // included speakers
}

// SyncState is the standup_sync_state row.
type SyncState struct {
	StandupID       int64
	NeedsSync       bool
	LastAttemptAt   time.Time
	LastSuccessAt   time.Time
	LastFullAt      time.Time
	HistoryFrom     time.Time
	BacklogTotal    int
	ActiveSprintIDs []string
}

// Run is a sync_run row.
type Run struct {
	ID             int64
	StandupID      int64
	Mode           string
	Trigger        string
	Status         string // running|ok|partial|cancelled|failed
	StartedAt      time.Time
	FinishedAt     time.Time
	IssuesScanned  int
	IssuesHydrated int
	Requests       int
	Throttled      int
	ErrorCode      string
	ErrorMessage   string
	Warnings       []string
}

// IssueStamp is what the scan diff needs about a cached issue.
type IssueStamp struct {
	LocalID        int64
	UpdatedAt      time.Time
	DevFingerprint string
	DevCheckedAt   time.Time
	LastStatusAt   time.Time // newest status_change.changed_at, zero if none
}

// ── Ticket modal (ADR 0015; data-model.md §2.5a, Q9) ──

// Ticket is everything view.BuildTicket needs for one issue, read by LoadTicketTx in one reader transaction with no
// network. Lists and maps are never nil.
type Ticket struct {
	LocalID      int64 // issue.id
	ConnectionID int64
	Issue        model.Issue // current row (status, assignee, flagged, parent key, sprint ids, times)
	TypeKind     string      // issue_type.kind (story|bug|task|spike|subtask|epic|other)
	HydratedAt   time.Time   // issue.hydrated_at ("synced" in the header)
	RemovedAt    time.Time   // issue.removed_at; non-zero = no longer on any board ("No longer on this board")

	Description      *model.RichText // nil = empty or not synced yet
	Environment      *model.RichText
	Watchers         int       // -1 unknown
	ContentUpdatedAt time.Time // issue_content.updated_at; zero = no content row yet

	Fields   []TicketField    // issue_field_value joined with field_def (current values; no row = not set)
	Registry []TicketFieldDef // the connection's field registry with rail preferences (drives "Not set" and order)

	Comments    []model.Comment    // deleted_at IS NULL, oldest first (created_at, comment_id)
	Attachments []TicketAttachment // removed_at IS NULL, images first, then created desc
	Links       []TicketLink       // relation 'link' then 'subtask', by label then position
	Parent      *TicketLink        // issue.parent_key resolved against local issues; nil when no parent

	Draft model.NewComment // ticket_draft; zero Text and empty Mentions when none

	History model.IssueHistory      // every status/assignee/flag change, comment time (incl. deleted) and dev event
	People  map[string]model.Person // accounts referenced (assignee, reporter, comment authors, user fields, mentions)
	Issues  map[string]TicketRef    // synced issues on this connection, by key, referenced by inline cards (resolver)
}

// TicketField is one current field value with its registry entry.
type TicketField struct {
	Value  model.FieldValue
	Name   string // field_def.name; "" if the registry lacks the field
	Custom bool
}

// TicketFieldDef is one registry entry with its per-connection rail preference (ticket_field_pref).
type TicketFieldDef struct {
	Def    model.FieldDef
	Pinned int // position among pinned custom fields (0-based); -1 = not pinned
	Hidden bool
}

// TicketAttachment is an attachment row with its media-cache state. Attachment.LocalID is the row id.
type TicketAttachment struct {
	model.Attachment
	IsImage    bool   // renderable inline (png/jpeg/gif/webp/bmp ≤ 25 MB)
	HasThumb   bool   // thumb_path IS NOT NULL
	HasFile    bool   // file_path IS NOT NULL (original cached)
	FetchError string // last media fetch error code; "" when fine
}

// TicketLink is an issue_link row (or the parent) with the other issue resolved against local issues.
type TicketLink struct {
	Relation        string // "link"|"subtask"|"parent"
	Label           string // "is blocked by", "blocks", … ("" for subtask/parent)
	Position        int
	OtherSourceID   string
	OtherKey        string
	OtherSummary    string // stored snapshot
	OtherStatusID   string
	OtherTypeID     string
	OtherAssigneeID string
	OtherTypeKind   string // issue_type.kind of OtherTypeID when known
	OtherIssueID    int64  // local id when the other issue is synced; 0 = not synced (opens the browser)
	LiveSummary     string // the synced issue's current summary ("" when not synced)
	LiveStatusID    string
	LiveAssigneeID  string // the synced issue's CURRENT assignee account ("" = unassigned or not synced)
}

// TicketRef is a synced issue as seen by the ADF resolver (inline/block cards).
type TicketRef struct {
	LocalID  int64
	Key      string
	Summary  string
	StatusID string
	TypeKind string
}
