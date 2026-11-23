package cache

// WP-30 defines here the exported *Tx helpers that internal/sync's apply step needs (data-model.md §2.5–2.7,
// jira-sync.md §4.6 steps 1, 3–5, 7–9): upsert reference rows, upsert issues and their change/comment/dev rows by
// natural key (never delete), rewrite standup_issue and maintain standup_issue_span + issue.removed_at, config history
// by fingerprint (ConfigJSON below), and the standup_sync_state update.
//
// The helpers live in apply.go (config document), apply_reference.go, apply_issue.go, apply_member.go and
// apply_state.go. All take the caller's *sql.Tx: sync runs the whole apply in one store.WriteTx.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/crueber/cuisle/internal/model"
)

type configShow struct {
	Commits  bool `json:"commits"`
	PRs      bool `json:"prs"`
	Comments bool `json:"comments"`
}

type configStatusLane struct {
	StatusID   string `json:"statusId"`
	StatusName string `json:"statusName"`
	ColumnName string `json:"columnName"`
	Lane       string `json:"lane"`
}

type configSpeaker struct {
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
	Position  int    `json:"position"`
	Included  bool   `json:"included"`
}

// configDoc is the config_json of data-model.md §2.6: everything the view builder reads from config. Field order is
// fixed, so equal configs always serialise to equal bytes (the fingerprint depends on it).
type configDoc struct {
	Scope              string             `json:"scope"`
	ExtraFilter        string             `json:"extraFilter"`
	DoneWindow         string             `json:"doneWindow"`
	BacklogRows        int                `json:"backlogRows"`
	PulseDays          int                `json:"pulseDays"`
	ShadeWeekends      bool               `json:"shadeWeekends"`
	Show               configShow         `json:"show"`
	RowsMode           string             `json:"rowsMode"`
	WalkOrder          []string           `json:"walkOrder"`
	Limits             map[string]int     `json:"limits"`
	StatusLanes        []configStatusLane `json:"statusLanes"`
	Speakers           []configSpeaker    `json:"speakers"`
	UnassignedPosition string             `json:"unassignedPosition"`
	ActiveSprintIDs    []string           `json:"activeSprintIds"`
}

// ConfigJSON returns the canonical config_json of data-model.md §2.6 for cfg and its sha256 hex fingerprint.
func ConfigJSON(cfg model.EffectiveConfig) ([]byte, string) {
	lanes := append([]model.LaneConfig(nil), cfg.Lanes...)
	sort.SliceStable(lanes, func(i, j int) bool { return lanes[i].Position < lanes[j].Position })
	walk := make([]string, 0, len(lanes))
	limits := make(map[string]int, len(lanes))
	for _, l := range lanes {
		walk = append(walk, l.Lane)
		limits[l.Lane] = l.LimitDays
	}

	sl := make([]configStatusLane, 0, len(cfg.StatusLanes))
	for _, s := range cfg.StatusLanes {
		sl = append(sl, configStatusLane{StatusID: s.StatusID, StatusName: s.StatusName, ColumnName: s.ColumnName, Lane: s.Lane})
	}
	sort.SliceStable(sl, func(i, j int) bool { return sl[i].StatusID < sl[j].StatusID })

	sp := make([]configSpeaker, 0, len(cfg.Speakers))
	for _, s := range cfg.Speakers {
		sp = append(sp, configSpeaker{AccountID: s.AccountID, Name: s.Name, Position: s.Position, Included: s.Included})
	}
	sort.SliceStable(sp, func(i, j int) bool { return sp[i].Position < sp[j].Position })

	doc := configDoc{
		Scope: cfg.Scope, ExtraFilter: cfg.ExtraFilter, DoneWindow: cfg.DoneWindow, BacklogRows: cfg.BacklogRows,
		PulseDays: cfg.PulseDays, ShadeWeekends: cfg.ShadeWeekends,
		Show:     configShow{Commits: cfg.ShowCommits, PRs: cfg.ShowPRs, Comments: cfg.ShowComments},
		RowsMode: cfg.RowsMode, WalkOrder: walk, Limits: limits, StatusLanes: sl, Speakers: sp,
		UnassignedPosition: cfg.UnassignedPosition, ActiveSprintIDs: append([]string{}, cfg.ActiveSprintIDs...),
	}
	// A struct of strings, ints, bools and slices of those cannot fail to marshal.
	b, err := json.Marshal(doc)
	if err != nil {
		b = []byte("{}")
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:])
}
