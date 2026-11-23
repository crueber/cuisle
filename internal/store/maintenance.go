package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
)

const weeklyMaxAge = 7 * 24 * time.Hour

// WeeklyBackup writes VACUUM INTO <dir>/cuisle.db.bak-weekly when it is missing or older than 7 days at now and the
// DB holds history (any standup_sync_state.history_from set). Returns whether it wrote. Kept: 1.
func (db *DB) WeeklyBackup(ctx context.Context, now time.Time) (bool, error) {
	if db.Path == "" {
		return false, nil
	}
	var hasHistory bool
	if err := db.W.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM standup_sync_state WHERE history_from IS NOT NULL)").Scan(&hasHistory); err != nil {
		return false, apperr.Wrap(apperr.DB, "The weekly backup could not be checked.", err)
	}
	if !hasHistory {
		return false, nil
	}
	target := filepath.Join(filepath.Dir(db.Path), "cuisle.db.bak-weekly")
	fi, err := os.Stat(target)
	switch {
	case err == nil:
		if now.Sub(fi.ModTime()) <= weeklyMaxAge {
			return false, nil
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return false, apperr.Wrap(apperr.DB, "The weekly backup could not be checked.", err)
	}

	// Write beside the target and rename, so a crash never leaves a half-written weekly backup in place.
	tmp := target + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, apperr.Wrap(apperr.DB, "The weekly backup could not be written.", err)
	}
	if _, err := db.W.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		_ = os.Remove(tmp)
		return false, apperr.Wrap(apperr.DB, "The weekly backup could not be written.", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return false, apperr.Wrap(apperr.DB, "The weekly backup could not be written.", err)
	}
	return true, nil
}

// MarkInterruptedRuns sets sync_run rows still 'running' to 'failed' (error_code 'interrupted') and sink_outbox rows
// still 'delivering' to 'failed'. Called once at startup. Returns rows changed.
func (db *DB) MarkInterruptedRuns(ctx context.Context, now time.Time) (int, error) {
	var changed int64
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			"UPDATE sync_run SET status = 'failed', error_code = 'interrupted', finished_at = ? WHERE status = 'running'",
			Ms(now))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		changed += n

		res, err = tx.ExecContext(ctx,
			"UPDATE sink_outbox SET status = 'failed', updated_at = ? WHERE status = 'delivering'", Ms(now))
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		if err != nil {
			return err
		}
		changed += n
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(changed), nil
}
