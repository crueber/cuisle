// Package history loads "as of" snapshots (ADR 0011). v1 ships the stub: LoadAsOf returns not_implemented.
// FROZEN CONTRACT: signatures. Implementation: WP-22. Imports allowed: cache, store, settings, model, apperr.
package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// LoadAsOf will rebuild a standup's snapshot as of t from spans, config history and change tables (data-model.md Q8).
// v1: returns apperr.NotImplemented.
func LoadAsOf(ctx context.Context, db *store.DB, standupID int64, t time.Time) (model.BoardSnapshot, error) {
	return model.BoardSnapshot{}, apperr.NotImplemented
}

// MemberAt is one reconstructed membership row.
type MemberAt struct {
	Key      string
	Role     string // board|backlog
	Lane     string // lane via the config history's status lanes ("" if unmapped)
	Assignee string // "" = unassigned
}

// Reconstruct is the test-only helper for the testing.md §3 reconstruction test: membership from standup_issue_span,
// status and assignee as of t from the change tables, lanes from standup_config_history as of t. Sorted by key.
func Reconstruct(ctx context.Context, db *store.DB, standupID int64, t time.Time) ([]MemberAt, error) {
	var out []MemberAt
	err := db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = reconstruct(ctx, tx, standupID, store.Ms(t))
		return err
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.DB, "The history could not be read.", err)
	}
	return out, nil
}

// reconstruct runs the Q8 logic inside one read transaction. tms is t in Unix ms.
func reconstruct(ctx context.Context, tx *sql.Tx, standupID, tms int64) ([]MemberAt, error) {
	lanes, err := laneMap(ctx, tx, standupID, tms)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT i.id, i.key, sp.role, i.status_id, i.assignee_account_id
		FROM standup_issue_span sp
		JOIN issue i ON i.id = sp.issue_id
		WHERE sp.standup_id = ? AND sp.valid_from <= ? AND (sp.valid_to IS NULL OR sp.valid_to > ?)`,
		standupID, tms, tms)
	if err != nil {
		return nil, fmt.Errorf("history: spans: %w", err)
	}
	type member struct {
		id        int64
		key, role string
		status    string
		assignee  sql.NullString
	}
	var members []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.id, &m.key, &m.role, &m.status, &m.assignee); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("history: scan span: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("history: close spans: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: spans: %w", err)
	}

	out := make([]MemberAt, 0, len(members))
	for _, m := range members {
		status, err := statusAt(ctx, tx, m.id, tms, m.status)
		if err != nil {
			return nil, err
		}
		assignee, err := assigneeAt(ctx, tx, m.id, tms, m.assignee)
		if err != nil {
			return nil, err
		}
		out = append(out, MemberAt{Key: m.key, Role: m.role, Lane: lanes[status], Assignee: assignee})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// statusAt returns the status id of an issue as of tms: the latest change at or before tms, else the first change's
// from-status, else the issue's current status (no changes at all).
func statusAt(ctx context.Context, tx *sql.Tx, issueID, tms int64, current string) (string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT changed_at, from_status_id, to_status_id FROM status_change
		WHERE issue_id = ? ORDER BY changed_at, history_id`, issueID)
	if err != nil {
		return "", fmt.Errorf("history: status changes: %w", err)
	}
	defer rows.Close()
	first, have, status := "", false, ""
	found := false
	for rows.Next() {
		var at int64
		var from, to string
		if err := rows.Scan(&at, &from, &to); err != nil {
			return "", fmt.Errorf("history: scan status change: %w", err)
		}
		if !have {
			first, have = from, true
		}
		if at <= tms {
			status, found = to, true
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("history: status changes: %w", err)
	}
	switch {
	case found:
		return status, nil
	case have:
		return first, nil
	default:
		return current, nil
	}
}

// assigneeAt returns the assignee account of an issue as of tms ("" = unassigned), with the same rule as statusAt.
func assigneeAt(ctx context.Context, tx *sql.Tx, issueID, tms int64, current sql.NullString) (string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT changed_at, from_account_id, to_account_id FROM assignee_change
		WHERE issue_id = ? ORDER BY changed_at, history_id`, issueID)
	if err != nil {
		return "", fmt.Errorf("history: assignee changes: %w", err)
	}
	defer rows.Close()
	var first, latest sql.NullString
	have, found := false, false
	for rows.Next() {
		var at int64
		var from, to sql.NullString
		if err := rows.Scan(&at, &from, &to); err != nil {
			return "", fmt.Errorf("history: scan assignee change: %w", err)
		}
		if !have {
			first, have = from, true
		}
		if at <= tms {
			latest, found = to, true
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("history: assignee changes: %w", err)
	}
	var v sql.NullString
	switch {
	case found:
		v = latest
	case have:
		v = first
	default:
		v = current
	}
	return v.String, nil
}

// laneMap returns statusId -> lane from the config history row covering tms ("" map if none).
func laneMap(ctx context.Context, tx *sql.Tx, standupID, tms int64) (map[string]string, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `
		SELECT config_json FROM standup_config_history
		WHERE standup_id = ? AND valid_from <= ? AND (valid_to IS NULL OR valid_to > ?)
		ORDER BY valid_from DESC LIMIT 1`, standupID, tms, tms).Scan(&raw)
	if err == sql.ErrNoRows {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("history: config: %w", err)
	}
	var cfg struct {
		StatusLanes []struct {
			StatusID string `json:"statusId"`
			Lane     string `json:"lane"`
		} `json:"statusLanes"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("history: config json: %w", err)
	}
	m := make(map[string]string, len(cfg.StatusLanes))
	for _, sl := range cfg.StatusLanes {
		m[sl.StatusID] = sl.Lane
	}
	return m, nil
}
