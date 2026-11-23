package cache

import (
	"context"
	"database/sql"
	"time"

	"github.com/crueber/cuisle/internal/store"
)

// StartRun inserts a running sync_run row and stamps standup_sync_state.last_attempt_at (own small tx).
func StartRun(ctx context.Context, w *sql.DB, standupID int64, mode, trigger string, startedAt time.Time) (int64, error) {
	var id int64
	err := withWriteTx(ctx, w, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO sync_run (standup_id, mode, trigger, status, started_at) VALUES (?, ?, ?, 'running', ?)`,
			standupID, mode, trigger, store.Ms(startedAt))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO standup_sync_state (standup_id, last_attempt_at) VALUES (?, ?)
			ON CONFLICT(standup_id) DO UPDATE SET last_attempt_at = excluded.last_attempt_at`,
			standupID, store.Ms(startedAt))
		return err
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// FinishRun writes the final status and counters of r.ID (own small tx).
// Only the outcome columns are written; standup, mode, trigger and started_at keep their StartRun values.
func FinishRun(ctx context.Context, w *sql.DB, r Run) error {
	return withWriteTx(ctx, w, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE sync_run
			   SET status = ?, finished_at = ?, issues_scanned = ?, issues_hydrated = ?, requests = ?, throttled = ?,
			       error_code = ?, error_message = ?, warnings = ?
			 WHERE id = ?`,
			r.Status, nullMs(r.FinishedAt), r.IssuesScanned, r.IssuesHydrated, r.Requests, r.Throttled,
			nullStr(r.ErrorCode), nullStr(r.ErrorMessage), encodeStrings(r.Warnings), r.ID)
		if err != nil {
			return err
		}
		return requireOneRow(res, "The sync run no longer exists.")
	})
}

// LogRun appends a sync_log line (no secrets, no issue content).
func LogRun(ctx context.Context, w *sql.DB, runID int64, at time.Time, level, phase, message string) error {
	_, err := w.ExecContext(ctx,
		`INSERT INTO sync_log (run_id, at, level, phase, message) VALUES (?, ?, ?, ?, ?)`,
		runID, store.Ms(at), level, phase, message)
	if err != nil {
		return dbErr(err, "The sync log could not be saved.")
	}
	return nil
}

// RecentRuns returns the newest n runs (all standups when standupID is 0), newest first.
func RecentRuns(ctx context.Context, q *sql.DB, standupID int64, n int) ([]Run, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, standup_id, mode, trigger, status, started_at, finished_at,
		       issues_scanned, issues_hydrated, requests, throttled, error_code, error_message, warnings
		  FROM sync_run
		 WHERE (? = 0 OR standup_id = ?)
		 ORDER BY started_at DESC, id DESC
		 LIMIT ?`, standupID, standupID, limitArg(n))
	if err != nil {
		return nil, dbErr(err, "The sync runs could not be read.")
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var started int64
		var finished sql.NullInt64
		var errCode, errMsg sql.NullString
		var warnings string
		if err := rows.Scan(&r.ID, &r.StandupID, &r.Mode, &r.Trigger, &r.Status, &started, &finished,
			&r.IssuesScanned, &r.IssuesHydrated, &r.Requests, &r.Throttled, &errCode, &errMsg, &warnings); err != nil {
			return nil, dbErr(err, "The sync runs could not be read.")
		}
		r.StartedAt = store.FromMs(started)
		r.FinishedAt = msTime(finished)
		r.ErrorCode = errCode.String
		r.ErrorMessage = errMsg.String
		r.Warnings = decodeStrings(warnings)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err, "The sync runs could not be read.")
	}
	return out, nil
}
