// Package cache reads and writes the provider-neutral record store (data-model.md §2.2, §2.4–2.7, §4).
//
// FROZEN CONTRACT: the types in this file and exported signatures elsewhere. Ownership:
//
//	WP-20 everything except apply*.go (read side, connection, runs, reference caches, trim)
//	WP-30 apply*.go (record upserts + membership/spans + config history + sync state, called by sync's apply)
//
// Imports allowed: store, model, apperr, stdlib.
package cache

import "time"

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
