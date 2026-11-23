package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// ApplyIssueTx upserts one hydrated issue by (connection_id, source_id) and upserts its status, assignee and flag
// changes and its comment metadata by natural key. It never deletes a row: a re-fetch with fewer entries leaves the
// older ones in place. hydrated_at = at; last_seen_at is set to `at` on insert only (MarkSeenTx stamps scanned
// members); dev_fingerprint, dev_checked_at and removed_at are not touched. Returns the local issue id.
func ApplyIssueTx(ctx context.Context, tx *sql.Tx, connID int64, d model.IssueDetail, at time.Time) (int64, error) {
	meta := "{}"
	if len(d.Meta) > 0 {
		b, err := json.Marshal(d.Meta)
		if err != nil {
			return 0, dbErr(err, "The issues could not be saved.")
		}
		meta = string(b)
	}
	sprints := d.SprintIDs
	if sprints == nil {
		sprints = []string{}
	}
	var id int64
	err := tx.QueryRowContext(ctx,
		`INSERT INTO issue (connection_id, source_id, key, space_id, summary, issue_type_id, status_id,
		   assignee_account_id, flagged, rank, parent_key, sprint_ids, created_at, updated_at, resolved_at,
		   comment_total, hydrated_at, last_seen_at, source_meta)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(connection_id, source_id) DO UPDATE SET key = excluded.key, space_id = excluded.space_id,
		   summary = excluded.summary, issue_type_id = excluded.issue_type_id, status_id = excluded.status_id,
		   assignee_account_id = excluded.assignee_account_id, flagged = excluded.flagged, rank = excluded.rank,
		   parent_key = excluded.parent_key, sprint_ids = excluded.sprint_ids, created_at = excluded.created_at,
		   updated_at = excluded.updated_at, resolved_at = excluded.resolved_at,
		   comment_total = excluded.comment_total, hydrated_at = excluded.hydrated_at,
		   source_meta = excluded.source_meta
		 RETURNING id`,
		connID, d.SourceID, d.Key, d.SpaceID, d.Summary, d.TypeID, d.StatusID,
		nullStr(d.AssigneeID), boolInt(d.Flagged), nullStr(d.Rank), nullStr(d.ParentKey), encodeStrings(sprints),
		store.Ms(d.CreatedAt), store.Ms(d.UpdatedAt), nullMs(d.ResolvedAt),
		d.CommentTotal, store.Ms(at), store.Ms(at), meta).Scan(&id)
	if err != nil {
		return 0, dbErr(err, "The issues could not be saved.")
	}

	for _, c := range d.StatusChanges {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO status_change (issue_id, history_id, changed_at, from_status_id, to_status_id)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(issue_id, history_id) DO UPDATE SET changed_at = excluded.changed_at,
			 from_status_id = excluded.from_status_id, to_status_id = excluded.to_status_id`,
			id, c.ID, store.Ms(c.At), c.From, c.To); err != nil {
			return 0, dbErr(err, "The issue history could not be saved.")
		}
	}
	for _, c := range d.AssigneeChanges {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO assignee_change (issue_id, history_id, changed_at, from_account_id, to_account_id)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(issue_id, history_id) DO UPDATE SET changed_at = excluded.changed_at,
			 from_account_id = excluded.from_account_id, to_account_id = excluded.to_account_id`,
			id, c.ID, store.Ms(c.At), nullStr(c.From), nullStr(c.To)); err != nil {
			return 0, dbErr(err, "The issue history could not be saved.")
		}
	}
	for _, c := range d.FlagChanges {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO flag_change (issue_id, history_id, changed_at, flagged) VALUES (?, ?, ?, ?)
			 ON CONFLICT(issue_id, history_id) DO UPDATE SET changed_at = excluded.changed_at,
			 flagged = excluded.flagged`,
			id, c.ID, store.Ms(c.At), boolInt(c.Flagged)); err != nil {
			return 0, dbErr(err, "The issue history could not be saved.")
		}
	}
	for _, c := range d.Comments {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO comment (issue_id, comment_id, author_account_id, created_at) VALUES (?, ?, ?, ?)
			 ON CONFLICT(issue_id, comment_id) DO UPDATE SET author_account_id = excluded.author_account_id,
			 created_at = excluded.created_at`,
			id, c.ID, nullStr(c.AuthorID), store.Ms(c.At)); err != nil {
			return 0, dbErr(err, "The issue history could not be saved.")
		}
	}
	return id, nil
}

// ApplyDevTx upserts the commits and pull-request events of one issue (never deleting) and, when the activity carries
// a fingerprint, stores it as issue.dev_fingerprint. A PR status outside OPEN|MERGED|DECLINED is stored as UNKNOWN.
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
				`INSERT INTO dev_pr_event (issue_id, pr_key, status, at) VALUES (?, ?, ?, ?)
				 ON CONFLICT(issue_id, pr_key, status, at) DO NOTHING`,
				issueID, ev.ID, status, store.Ms(ev.At)); err != nil {
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
	for _, id := range issueIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE issue SET dev_checked_at = ? WHERE id = ?`, store.Ms(at), id); err != nil {
			return dbErr(err, "The development activity could not be saved.")
		}
	}
	return nil
}

// MarkSeenTx sets issue.last_seen_at = at for the given local issue ids (every member the scan returned).
func MarkSeenTx(ctx context.Context, tx *sql.Tx, issueIDs []int64, at time.Time) error {
	for _, id := range issueIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE issue SET last_seen_at = ? WHERE id = ?`, store.Ms(at), id); err != nil {
			return dbErr(err, "The issues could not be saved.")
		}
	}
	return nil
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
