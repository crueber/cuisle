package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// ApplyIssueTx upserts one hydrated issue by (connection_id, source_id) and returns its local issue id. It is
// ApplyIssueDetailTx (content_apply.go) without the change report: the issue row, the merged status, assignee and flag
// changes, the comments (with bodies and visibility) and, when the source returned content, the ticket content tables.
// It never deletes a row, and an incoming issue older than the stored row leaves the issue row untouched.
func ApplyIssueTx(ctx context.Context, tx *sql.Tx, connID int64, d model.IssueDetail, at time.Time) (int64, error) {
	res, err := ApplyIssueDetailTx(ctx, tx, connID, d, at)
	if err != nil {
		return 0, err
	}
	return res.IssueID, nil
}

// ApplyDevTx upserts the commits and pull-request events of one issue (never deleting) and, when the activity carries
// a fingerprint, stores it as issue.dev_fingerprint. A PR title or branch fills the row's NULL (never blanks a known
// value). A PR status outside OPEN|MERGED|DECLINED is stored as UNKNOWN.
func ApplyDevTx(ctx context.Context, tx *sql.Tx, issueID int64, da model.DevActivity) error {
	for _, ev := range da.Events {
		switch ev.Kind {
		case model.DevCommit:
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO dev_commit (issue_id, commit_id, repo, authored_at, is_merge) VALUES (?, ?, ?, ?, ?)
				 ON CONFLICT(issue_id, commit_id) DO UPDATE SET repo = excluded.repo,
				 authored_at = excluded.authored_at, is_merge = excluded.is_merge`,
				issueID, ev.ID, ev.Repo, store.Ms(ev.At), boolInt(ev.Merge)); err != nil {
				return dbErr(err, "The development activity could not be saved.")
			}
		case model.DevPR:
			status := ev.Status
			switch status {
			case "OPEN", "MERGED", "DECLINED":
			default:
				status = "UNKNOWN"
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO dev_pr_event (issue_id, pr_key, status, at, title, branch) VALUES (?, ?, ?, ?, ?, ?)
				 ON CONFLICT(issue_id, pr_key, status, at) DO UPDATE SET
				   title  = COALESCE(excluded.title, title),
				   branch = COALESCE(excluded.branch, branch)`,
				issueID, ev.ID, status, store.Ms(ev.At), nullStr(ev.Title), nullStr(ev.Branch)); err != nil {
				return dbErr(err, "The development activity could not be saved.")
			}
		}
	}
	if da.Fingerprint != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE issue SET dev_fingerprint = ? WHERE id = ?`, da.Fingerprint, issueID); err != nil {
			return dbErr(err, "The development activity could not be saved.")
		}
	}
	return nil
}

// MarkDevCheckedTx sets issue.dev_checked_at = at for the given local issue ids.
func MarkDevCheckedTx(ctx context.Context, tx *sql.Tx, issueIDs []int64, at time.Time) error {
	if len(issueIDs) == 0 {
		return nil
	}
	arr, err := json.Marshal(issueIDs)
	if err != nil {
		return dbErr(err, "The development activity could not be saved.")
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE issue SET dev_checked_at = ? WHERE id IN (SELECT value FROM json_each(?))`,
		store.Ms(at), string(arr)); err != nil {
		return dbErr(err, "The development activity could not be saved.")
	}
	return nil
}

// MarkSeenTx sets issue.last_seen_at = at for the given local issue ids (every member the scan returned).
func MarkSeenTx(ctx context.Context, tx *sql.Tx, issueIDs []int64, at time.Time) error {
	if len(issueIDs) == 0 {
		return nil
	}
	arr, err := json.Marshal(issueIDs)
	if err != nil {
		return dbErr(err, "The issues could not be saved.")
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE issue SET last_seen_at = ? WHERE id IN (SELECT value FROM json_each(?))`,
		store.Ms(at), string(arr)); err != nil {
		return dbErr(err, "The issues could not be saved.")
	}
	return nil
}

// IssueIDsForTx maps provider issue id → local issue id for the given provider issue ids of the connection.
func IssueIDsForTx(ctx context.Context, tx *sql.Tx, connID int64, sourceIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(sourceIDs))
	if len(sourceIDs) == 0 {
		return out, nil
	}
	arr, err := json.Marshal(sourceIDs)
	if err != nil {
		return nil, dbErr(err, "The issues could not be read.")
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT source_id, id FROM issue WHERE connection_id = ? AND source_id IN (SELECT value FROM json_each(?))`,
		connID, string(arr))
	if err != nil {
		return nil, dbErr(err, "The issues could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var src string
		var id int64
		if err := rows.Scan(&src, &id); err != nil {
			return nil, dbErr(err, "The issues could not be read.")
		}
		out[src] = id
	}
	return out, dbErr(rows.Err(), "The issues could not be read.")
}

// IssueIDsTx maps provider issue id → local issue id for every cached issue of the connection.
func IssueIDsTx(ctx context.Context, tx *sql.Tx, connID int64) (map[string]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT source_id, id FROM issue WHERE connection_id = ?`, connID)
	if err != nil {
		return nil, dbErr(err, "The issues could not be read.")
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var src string
		var id int64
		if err := rows.Scan(&src, &id); err != nil {
			return nil, dbErr(err, "The issues could not be read.")
		}
		out[src] = id
	}
	return out, dbErr(rows.Err(), "The issues could not be read.")
}
