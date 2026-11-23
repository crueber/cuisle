// Package settings is app settings and standup configuration CRUD (data-model.md §2.1–2.3, architecture.md §5.3).
// Every standup mutation runs in one store.WriteTx and bumps standup.rev (BumpRevTx).
//
// FROZEN CONTRACT: the types and helpers in this file and the exported signatures in the other files. Ownership:
//
//	WP-16 standup*.go app_settings*.go uistate*.go
//	WP-17 lanes*.go columns*.go speakers*.go
//
// Imports allowed: store, model, apperr, stdlib.
package settings

import (
	"context"
	"database/sql"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// Store is the settings service over the database.
type Store struct {
	DB  *store.DB
	Now func() time.Time
}

func New(db *store.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{DB: db, Now: now}
}

// Ack is returned by every autosave mutation.
type Ack struct {
	Rev       int64
	SavedAt   time.Time
	NeedsSync bool
}

// Standup is one standup row (+ sync-state flags).
type Standup struct {
	ID               int64
	ConnectionID     int64 // 0 = none
	Name             string
	Position         int
	SpaceID          string
	SpaceKey         string
	SpaceName        string
	BoardID          string
	BoardName        string
	BoardType        string // scrum|kanban|""
	BoardMeta        map[string]any
	Scope            string
	ExtraFilter      string
	ExtraFilterOK    *bool // nil = not validated
	ExtraFilterError string
	FilterCheckedAt  time.Time // updated_at of the last SetFilterCheck (zero = never)
	Rev              int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        time.Time // zero = live
	NeedsSync        bool
}

// Draft reports whether no board is chosen yet.
func (s Standup) Draft() bool { return s.BoardID == "" }

// Settings is the standup_settings row.
type Settings struct {
	RowsMode           string
	UnassignedPosition string
	ReshuffleDaily     bool
	ShuffleSeed        int64
	ShuffleDate        string
	PulseDays          int
	ShowCommits        bool
	ShowPRs            bool
	ShowComments       bool
	ShadeWeekends      bool
	DoneWindow         string
	BacklogRows        int
}

// Speaker is one standup_speaker row.
type Speaker struct {
	AccountID   string
	DisplayName string
	Position    int
	Included    bool
	Manual      bool
	UserTouched bool
	LastSeenAt  time.Time
	CreatedAt   time.Time
}

// Patch is the autosave patch (nil = unchanged). It is also the bound StandupPatch type.
type Patch struct {
	Name               *string `json:"name,omitempty"`
	Scope              *string `json:"scope,omitempty"`
	ExtraFilter        *string `json:"extraFilter,omitempty"`
	RowsMode           *string `json:"rowsMode,omitempty"`
	UnassignedPosition *string `json:"unassignedPosition,omitempty"`
	ReshuffleDaily     *bool   `json:"reshuffleDaily,omitempty"`
	PulseDays          *int    `json:"pulseDays,omitempty"`
	ShowCommits        *bool   `json:"showCommits,omitempty"`
	ShowPRs            *bool   `json:"showPRs,omitempty"`
	ShowComments       *bool   `json:"showComments,omitempty"`
	ShadeWeekends      *bool   `json:"shadeWeekends,omitempty"`
	DoneWindow         *string `json:"doneWindow,omitempty"`
	BacklogRows        *int    `json:"backlogRows,omitempty"`
}

// UIState is the persisted window/navigation state.
type UIState struct {
	LastStandupID int64            `json:"lastStandupId"` // 0 = none
	LastView      string           `json:"lastView"`      // standup|configure
	LastPerson    map[int64]string `json:"lastPerson"`    // standup id → accountId|"unassigned"|"" (whole board)
	Window        *WindowState     `json:"window"`        // nil = never saved
}

type WindowState struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	W         int  `json:"w"`
	H         int  `json:"h"`
	Maximised bool `json:"maximised"`
}

// UIStatePatch is the bound patch type (nil = unchanged). StandupID+LastPerson set one standup's last person
// (LastPerson "" = whole board).
type UIStatePatch struct {
	LastStandupID *int64       `json:"lastStandupId,omitempty"`
	LastView      *string      `json:"lastView,omitempty"`
	StandupID     *int64       `json:"standupId,omitempty"`
	LastPerson    *string      `json:"lastPerson,omitempty"`
	Window        *WindowState `json:"window,omitempty"`
}

// App setting keys and defaults (architecture.md §6.4).
const (
	KeyTheme                = "theme"
	KeyTextSize             = "text_size"
	KeySyncOnOpen           = "sync_on_open"
	KeySyncStaleHours       = "sync_stale_hours"
	KeyHistoryRetentionDays = "history_retention_days"
)

var AppDefaults = map[string]string{
	KeyTheme: "system", KeyTextSize: "standard", KeySyncOnOpen: "stale", KeySyncStaleHours: "6",
	KeyHistoryRetentionDays: "0",
}

// DefaultLanes is the walk order and limits of a new standup ("right to left").
var DefaultLanes = []model.LaneConfig{
	{Lane: model.LaneQA, Position: 0, LimitDays: 3},
	{Lane: model.LaneReview, Position: 1, LimitDays: 2},
	{Lane: model.LaneProg, Position: 2, LimitDays: 5},
	{Lane: model.LaneTodo, Position: 3, LimitDays: 10},
}

// BumpRevTx increments standup.rev and updated_at for a live standup; 0 rows → apperr not_found.
func BumpRevTx(ctx context.Context, tx *sql.Tx, standupID int64, now time.Time) (int64, error) {
	var rev int64
	err := tx.QueryRowContext(ctx,
		`UPDATE standup SET rev = rev + 1, updated_at = ? WHERE id = ? AND deleted_at IS NULL RETURNING rev`,
		store.Ms(now), standupID).Scan(&rev)
	if err == sql.ErrNoRows {
		return 0, apperr.New(apperr.NotFound, "That standup no longer exists.")
	}
	if err != nil {
		return 0, apperr.Wrap(apperr.DB, "Could not save.", err)
	}
	return rev, nil
}

// SetNeedsSyncTx marks the standup as needing a (full) sync.
func SetNeedsSyncTx(ctx context.Context, tx *sql.Tx, standupID int64) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO standup_sync_state (standup_id, needs_sync) VALUES (?, 1)
		 ON CONFLICT(standup_id) DO UPDATE SET needs_sync = 1`, standupID)
	if err != nil {
		return apperr.Wrap(apperr.DB, "Could not save.", err)
	}
	return nil
}

// NeedsSyncTx reads standup_sync_state.needs_sync (absent row = true).
func NeedsSyncTx(ctx context.Context, tx *sql.Tx, standupID int64) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT needs_sync FROM standup_sync_state WHERE standup_id = ?`, standupID).Scan(&n)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, apperr.Wrap(apperr.DB, "Could not read settings.", err)
	}
	return n == 1, nil
}
