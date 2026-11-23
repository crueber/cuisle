package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// RequireLiveStandupTx returns apperr not_found unless the standup exists and is not archived.
func RequireLiveStandupTx(ctx context.Context, tx *sql.Tx, standupID int64) error {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM standup WHERE id = ? AND deleted_at IS NULL`, standupID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.New(apperr.NotFound, "That standup no longer exists.")
	}
	return dbErr(err, "The data could not be read.")
}

// ApplyBoardSnapshotTx refreshes the standup's board snapshot (board_name, board_meta and, when the kind is
// compatible with the standup's scope, board_type).
func ApplyBoardSnapshotTx(ctx context.Context, tx *sql.Tx, standupID int64, b model.Board) error {
	meta := "{}"
	if len(b.Meta) > 0 {
		raw, err := json.Marshal(b.Meta)
		if err != nil {
			return dbErr(err, "The board could not be saved.")
		}
		meta = string(raw)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE standup SET board_name = ?, board_meta = ? WHERE id = ?`,
		b.Name, meta, standupID); err != nil {
		return dbErr(err, "The board could not be saved.")
	}
	if b.Kind == "scrum" || b.Kind == "kanban" {
		// kanban has no sprint scope (table CHECK): never flip a sprint-scoped standup to kanban here.
		if _, err := tx.ExecContext(ctx,
			`UPDATE standup SET board_type = ? WHERE id = ? AND (? <> 'kanban' OR scope = 'board')`,
			b.Kind, standupID, b.Kind); err != nil {
			return dbErr(err, "The board could not be saved.")
		}
	}
	return nil
}

// RecordConfigTx opens a standup_config_history row at t when fingerprint differs from the open row's, closing that
// row (valid_to = t). It reports whether a row was written. History is never deleted. A re-apply at the same instant
// as the open row rewrites that row in place instead of colliding with its key.
func RecordConfigTx(ctx context.Context, tx *sql.Tx, standupID int64, configJSON []byte, fingerprint string, t time.Time) (bool, error) {
	T := store.Ms(t)
	var from int64
	var fp string
	err := tx.QueryRowContext(ctx,
		`SELECT valid_from, fingerprint FROM standup_config_history WHERE standup_id = ? AND valid_to IS NULL`,
		standupID).Scan(&from, &fp)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, dbErr(err, "The configuration history could not be read.")
	case fp == fingerprint:
		return false, nil
	case from >= T:
		_, err := tx.ExecContext(ctx,
			`UPDATE standup_config_history SET fingerprint = ?, config_json = ? WHERE standup_id = ? AND valid_from = ?`,
			fingerprint, string(configJSON), standupID, from)
		return err == nil, dbErr(err, "The configuration history could not be saved.")
	default:
		if _, err := tx.ExecContext(ctx,
			`UPDATE standup_config_history SET valid_to = ? WHERE standup_id = ? AND valid_from = ?`,
			T, standupID, from); err != nil {
			return false, dbErr(err, "The configuration history could not be saved.")
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO standup_config_history (standup_id, valid_from, valid_to, fingerprint, config_json)
		 VALUES (?, ?, NULL, ?, ?)`, standupID, T, fingerprint, string(configJSON)); err != nil {
		return false, dbErr(err, "The configuration history could not be saved.")
	}
	return true, nil
}

// WriteSyncStateTx records a successful sync: needs_sync = 0, last_success_at = t, last_full_at = t for full runs only,
// history_from = t on the first success only, backlog_total and active_sprint_ids. last_attempt_at is StartRun's.
func WriteSyncStateTx(ctx context.Context, tx *sql.Tx, standupID int64, t time.Time, full bool, backlogTotal int, activeSprintIDs []string) error {
	var lastFull any
	if full {
		lastFull = store.Ms(t)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO standup_sync_state (standup_id, needs_sync, last_success_at, last_full_at, history_from,
		   backlog_total, active_sprint_ids)
		 VALUES (?, 0, ?, ?, ?, ?, ?)
		 ON CONFLICT(standup_id) DO UPDATE SET needs_sync = 0, last_success_at = excluded.last_success_at,
		   last_full_at = COALESCE(excluded.last_full_at, standup_sync_state.last_full_at),
		   history_from = COALESCE(standup_sync_state.history_from, excluded.history_from),
		   backlog_total = excluded.backlog_total, active_sprint_ids = excluded.active_sprint_ids`,
		standupID, store.Ms(t), lastFull, store.Ms(t), backlogTotal, encodeStrings(activeSprintIDs)); err != nil {
		return dbErr(err, "The sync state could not be saved.")
	}
	return nil
}
