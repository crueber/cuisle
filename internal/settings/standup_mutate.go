package settings

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// ackTx builds the Ack of a standup mutation whose new rev is rev.
func (s *Store) ackTx(ctx context.Context, tx *sql.Tx, id, rev int64) (Ack, error) {
	needs, err := NeedsSyncTx(ctx, tx, id)
	if err != nil {
		return Ack{}, err
	}
	return Ack{Rev: rev, SavedAt: s.Now(), NeedsSync: needs}, nil
}

// notFound reports a zero-row result of a mutation that needs a matching row.
func notFound(res sql.Result) (bool, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return false, dbErr(err)
	}
	return n == 0, nil
}

// Duplicate copies standup (name "<name> (copy)", position right after the original), settings, lanes, column map,
// status lanes and speakers; needs_sync=1.
func (s *Store) Duplicate(ctx context.Context, id int64) (int64, error) {
	var newID int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		src, err := loadLiveTx(ctx, tx, id)
		if err != nil {
			return err
		}
		const suffix = " (copy)"
		base := []rune(src.Name)
		if max := 80 - utf8.RuneCountInString(suffix); len(base) > max {
			base = base[:max]
		}
		now := store.Ms(s.Now())
		res, err := tx.ExecContext(ctx,
			`INSERT INTO standup (connection_id, name, position, space_id, space_key, space_name, board_id, board_name,
			        board_type, board_meta, scope, extra_filter, extra_filter_ok, extra_filter_error, rev, created_at, updated_at)
			 SELECT connection_id, ?, 0, space_id, space_key, space_name, board_id, board_name,
			        board_type, board_meta, scope, extra_filter, extra_filter_ok, extra_filter_error, 1, ?, ?
			 FROM standup WHERE id = ?`,
			string(base)+suffix, now, now, id)
		if err != nil {
			return dbErr(err)
		}
		if newID, err = res.LastInsertId(); err != nil {
			return dbErr(err)
		}
		copies := []string{
			`INSERT INTO standup_settings (standup_id, rows_mode, unassigned_position, reshuffle_daily, shuffle_seed,
			        shuffle_date, pulse_days, show_commits, show_prs, show_comments, shade_weekends, done_window, backlog_rows)
			 SELECT ?, rows_mode, unassigned_position, reshuffle_daily, shuffle_seed, shuffle_date, pulse_days,
			        show_commits, show_prs, show_comments, shade_weekends, done_window, backlog_rows
			 FROM standup_settings WHERE standup_id = ?`,
			`INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days)
			 SELECT ?, lane, walk_position, limit_days FROM standup_lane WHERE standup_id = ?`,
			`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto, lane_order)
			 SELECT ?, column_name, position, lane, auto, lane_order FROM standup_column_map WHERE standup_id = ?`,
			`INSERT INTO standup_speaker_removed (standup_id, account_id, removed_at)
			 SELECT ?, account_id, removed_at FROM standup_speaker_removed WHERE standup_id = ?`,
			`INSERT INTO standup_status_lane (standup_id, status_id, status_name, column_name, lane)
			 SELECT ?, status_id, status_name, column_name, lane FROM standup_status_lane WHERE standup_id = ?`,
		}
		for _, q := range copies {
			if _, err := tx.ExecContext(ctx, q, newID, id); err != nil {
				return dbErr(err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, included, added_manually,
			        user_touched, last_seen_at, created_at)
			 SELECT ?, account_id, display_name, position, included, added_manually, user_touched, last_seen_at, ?
			 FROM standup_speaker WHERE standup_id = ?`, newID, now, id); err != nil {
			return dbErr(err)
		}
		if err := SetNeedsSyncTx(ctx, tx, newID); err != nil {
			return err
		}
		// place the copy right after the original and rewrite the live order densely
		order, err := liveOrderTx(ctx, tx)
		if err != nil {
			return err
		}
		rest := withoutID(order, newID)
		next := make([]int64, 0, len(rest)+1)
		for _, v := range rest {
			next = append(next, v)
			if v == id {
				next = append(next, newID)
			}
		}
		return rewriteOrderTx(ctx, tx, next)
	})
	if err != nil {
		return 0, err
	}
	return newID, nil
}

// CopySettingsFrom copies settings, lanes and limits from another standup (not source, mapping or speakers).
func (s *Store) CopySettingsFrom(ctx context.Context, id, fromID int64) (Ack, error) {
	var ack Ack
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		target, err := loadLiveTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := loadLiveTx(ctx, tx, fromID); err != nil {
			return err
		}
		from, err := readSettingsTx(ctx, tx, fromID)
		if err != nil {
			return err
		}
		lanes, err := readLanesTx(ctx, tx, fromID)
		if err != nil {
			return err
		}
		cur, err := readSettingsTx(ctx, tx, id)
		if err != nil {
			return err
		}
		next := from
		next.ShuffleSeed, next.ShuffleDate = cur.ShuffleSeed, cur.ShuffleDate // per-standup state, not copied
		if target.BoardType == "kanban" && next.DoneWindow == "sprint" {
			next.DoneWindow = "14d" // sprint is invalid on a kanban board
		}
		rev, err := BumpRevTx(ctx, tx, id, s.Now())
		if err != nil {
			return err
		}
		if err := writeSettingsTx(ctx, tx, id, next); err != nil {
			return err
		}
		if err := writeLanesTx(ctx, tx, id, lanes); err != nil {
			return err
		}
		ack, err = s.ackTx(ctx, tx, id, rev)
		return err
	})
	if err != nil {
		return Ack{}, err
	}
	return ack, nil
}

// DiscardDraft hard-deletes a standup with no space chosen; otherwise a no-op.
func (s *Store) DiscardDraft(ctx context.Context, id int64) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var space string
		err := tx.QueryRowContext(ctx, `SELECT COALESCE(space_id, '') FROM standup WHERE id = ?`, id).Scan(&space)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // nothing to discard
		}
		if err != nil {
			return dbErr(err)
		}
		if space != "" {
			return nil
		}
		// A standup that has been synced is never hard-deleted (its history stays); the guard also covers the
		// window between the read above and the delete.
		res, err := tx.ExecContext(ctx,
			`DELETE FROM standup WHERE id = ? AND NOT EXISTS (SELECT 1 FROM sync_run WHERE standup_id = ?)`, id, id)
		if err != nil {
			return dbErr(err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return nil
		}
		order, err := liveOrderTx(ctx, tx)
		if err != nil {
			return err
		}
		return rewriteOrderTx(ctx, tx, order)
	})
}

// Archive sets deleted_at (soft delete; never purged) and re-densifies positions of live standups.
func (s *Store) Archive(ctx context.Context, id int64) (Ack, error) {
	var ack Ack
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		rev, err := BumpRevTx(ctx, tx, id, s.Now()) // bumps while still live
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE standup SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, store.Ms(s.Now()), id); err != nil {
			return dbErr(err)
		}
		order, err := liveOrderTx(ctx, tx)
		if err != nil {
			return err
		}
		if err := rewriteOrderTx(ctx, tx, order); err != nil {
			return err
		}
		ack, err = s.ackTx(ctx, tx, id, rev)
		return err
	})
	if err != nil {
		return Ack{}, err
	}
	return ack, nil
}

// Restore clears deleted_at and appends the standup at the end of the live order.
func (s *Store) Restore(ctx context.Context, id int64) (Ack, error) {
	var ack Ack
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE standup SET deleted_at = NULL WHERE id = ? AND deleted_at IS NOT NULL`, id)
		if err != nil {
			return dbErr(err)
		}
		missing, err := notFound(res)
		if err != nil {
			return err
		}
		if missing {
			return apperr.New(apperr.NotFound, "That standup no longer exists.")
		}
		rev, err := BumpRevTx(ctx, tx, id, s.Now())
		if err != nil {
			return err
		}
		order, err := liveOrderTx(ctx, tx)
		if err != nil {
			return err
		}
		if err := rewriteOrderTx(ctx, tx, append(withoutID(order, id), id)); err != nil {
			return err
		}
		ack, err = s.ackTx(ctx, tx, id, rev)
		return err
	})
	if err != nil {
		return Ack{}, err
	}
	return ack, nil
}

// Move places a live standup at toPosition (clamped) and rewrites dense positions.
func (s *Store) Move(ctx context.Context, id int64, toPosition int) (Ack, error) {
	var ack Ack
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		rev, err := BumpRevTx(ctx, tx, id, s.Now())
		if err != nil {
			return err
		}
		order, err := liveOrderTx(ctx, tx)
		if err != nil {
			return err
		}
		rest := withoutID(order, id)
		at := min(max(toPosition, 0), len(rest))
		next := make([]int64, 0, len(order))
		next = append(next, rest[:at]...)
		next = append(next, id)
		next = append(next, rest[at:]...)
		if err := rewriteOrderTx(ctx, tx, next); err != nil {
			return err
		}
		ack, err = s.ackTx(ctx, tx, id, rev)
		return err
	})
	if err != nil {
		return Ack{}, err
	}
	return ack, nil
}

// Update applies a patch with validation (apperr validation + Field = JSON field name): name 1–80 chars trimmed;
// scope sprint|board (kanban → board only); extraFilter without ORDER BY; rowsMode fit|roomy; unassignedPosition
// first|last|skip; pulseDays 7|14|21|28; doneWindow sprint|14d|yesterday (sprint rejected for kanban); backlogRows 0–50.
// scope/extraFilter changes set needs_sync and clear the filter check; Ack.NeedsSync reports the flag.
func (s *Store) Update(ctx context.Context, id int64, p Patch) (Ack, error) {
	var ack Ack
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		cur, err := loadLiveTx(ctx, tx, id)
		if err != nil {
			return err
		}
		se, err := readSettingsTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := validateStandupFields(cur, p); err != nil {
			return err
		}
		next, nextSe := applyStandupPatch(cur, se, p)
		if next.Scope != cur.Scope || next.ExtraFilter != cur.ExtraFilter {
			next.ExtraFilterOK = nil
			next.ExtraFilterError = ""
			if err := SetNeedsSyncTx(ctx, tx, id); err != nil {
				return err
			}
		}
		rev, err := BumpRevTx(ctx, tx, id, s.Now())
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE standup SET name = ?, scope = ?, extra_filter = ?, extra_filter_ok = ?, extra_filter_error = ? WHERE id = ?`,
			next.Name, next.Scope, next.ExtraFilter, nullBool(next.ExtraFilterOK), nullStr(next.ExtraFilterError), id); err != nil {
			return dbErr(err)
		}
		if err := writeSettingsTx(ctx, tx, id, nextSe); err != nil {
			return err
		}
		ack, err = s.ackTx(ctx, tx, id, rev)
		return err
	})
	if err != nil {
		return Ack{}, err
	}
	return ack, nil
}

// SetSpace records the space snapshot and clears board, board meta, column map and status lanes; needs_sync.
func (s *Store) SetSpace(ctx context.Context, id int64, space model.Space) (Ack, error) {
	var ack Ack
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		rev, err := BumpRevTx(ctx, tx, id, s.Now())
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE standup SET space_id = ?, space_key = ?, space_name = ?, board_id = NULL, board_name = NULL,
			        board_type = NULL, board_meta = '{}' WHERE id = ?`,
			space.ID, space.Key, space.Name, id); err != nil {
			return dbErr(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM standup_status_lane WHERE standup_id = ?`, id); err != nil {
			return dbErr(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM standup_column_map WHERE standup_id = ?`, id); err != nil {
			return dbErr(err)
		}
		if err := SetNeedsSyncTx(ctx, tx, id); err != nil {
			return err
		}
		ack, err = s.ackTx(ctx, tx, id, rev)
		return err
	})
	if err != nil {
		return Ack{}, err
	}
	return ack, nil
}

// SetFilterCheck stores the async extra-filter validation result.
func (s *Store) SetFilterCheck(ctx context.Context, id int64, ok bool, errs []string) error {
	msg := ""
	if !ok {
		msg = strings.Join(errs, "; ")
	}
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE standup SET extra_filter_ok = ?, extra_filter_error = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
			bi(ok), nullStr(msg), store.Ms(s.Now()), id)
		if err != nil {
			return dbErr(err)
		}
		missing, err := notFound(res)
		if err != nil {
			return err
		}
		if missing {
			return apperr.New(apperr.NotFound, "That standup no longer exists.")
		}
		return nil
	})
}

// SetConnectionAll points every standup without a connection at connID (after the first Connect).
func (s *Store) SetConnectionAll(ctx context.Context, connID int64) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE standup SET connection_id = ? WHERE connection_id IS NULL`, connID)
		return dbErr(err)
	})
}
