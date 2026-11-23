package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// ApplySprintsTx upserts sprints (kept after they close). A sprint without a board id gets boardID.
func ApplySprintsTx(ctx context.Context, tx *sql.Tx, connID int64, boardID string, sprints []model.Sprint, at time.Time) error {
	for _, s := range sprints {
		board := s.BoardID
		if board == "" {
			board = boardID
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sprint (connection_id, id, board_id, name, state, start_at, end_at, complete_at, fetched_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(connection_id, id) DO UPDATE SET board_id = excluded.board_id, name = excluded.name,
			 state = excluded.state, start_at = excluded.start_at, end_at = excluded.end_at,
			 complete_at = excluded.complete_at, fetched_at = excluded.fetched_at`,
			connID, s.ID, board, s.Name, s.State, nullMs(s.StartAt), nullMs(s.EndAt), nullMs(s.CompleteAt),
			store.Ms(at)); err != nil {
			return dbErr(err, "The sprints could not be saved.")
		}
	}
	return nil
}

// ApplyStatusesTx upserts statuses. An empty category is stored as "undefined".
func ApplyStatusesTx(ctx context.Context, tx *sql.Tx, connID int64, statuses []model.Status) error {
	for _, s := range statuses {
		cat := s.Category
		if cat == "" {
			cat = "undefined"
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO status (connection_id, id, name, category, blocked_hint) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(connection_id, id) DO UPDATE SET name = excluded.name, category = excluded.category,
			 blocked_hint = excluded.blocked_hint`,
			connID, s.ID, s.Name, cat, boolInt(s.BlockedHint)); err != nil {
			return dbErr(err, "The statuses could not be saved.")
		}
	}
	return nil
}

// ApplyIssueTypesTx upserts issue types. An empty kind is stored as "other".
func ApplyIssueTypesTx(ctx context.Context, tx *sql.Tx, connID int64, types []model.IssueType) error {
	for _, t := range types {
		kind := t.Kind
		if kind == "" {
			kind = "other"
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO issue_type (connection_id, id, name, kind) VALUES (?, ?, ?, ?)
			 ON CONFLICT(connection_id, id) DO UPDATE SET name = excluded.name, kind = excluded.kind`,
			connID, t.ID, t.Name, kind); err != nil {
			return dbErr(err, "The issue types could not be saved.")
		}
	}
	return nil
}

// ApplyPeopleTx upserts people (no email, no avatar). The account type is the person's Kind.
func ApplyPeopleTx(ctx context.Context, tx *sql.Tx, connID int64, people []model.Person, at time.Time) error {
	for _, p := range people {
		if p.AccountID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO person (connection_id, account_id, display_name, active, account_type, fetched_at)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(connection_id, account_id) DO UPDATE SET
			   display_name = CASE WHEN excluded.display_name <> '' THEN excluded.display_name ELSE person.display_name END,
			   active = CASE WHEN excluded.display_name <> '' THEN excluded.active ELSE person.active END,
			   account_type = COALESCE(excluded.account_type, person.account_type), fetched_at = excluded.fetched_at`,
			connID, p.AccountID, p.DisplayName, boolInt(p.Active), nullStr(p.Kind), store.Ms(at)); err != nil {
			return dbErr(err, "The people could not be saved.")
		}
	}
	return nil
}

// ApplyConnectionTx merges meta over the connection's source_meta (a nil or empty meta leaves it alone) and, when
// devState is "ok" or "unavailable", records it with dev_activity_checked_at = at. Any other devState ("unknown",
// "": the dev API was not probed this run) leaves the stored state and its timestamp alone.
func ApplyConnectionTx(ctx context.Context, tx *sql.Tx, connID int64, meta map[string]any, devState string, at time.Time) error {
	if len(meta) > 0 {
		var cur string
		if err := tx.QueryRowContext(ctx, `SELECT source_meta FROM connection WHERE id = ?`, connID).Scan(&cur); err != nil {
			return dbErr(err, "The connection could not be saved.")
		}
		merged := decodeMap(cur)
		for k, v := range meta {
			merged[k] = v
		}
		b, err := json.Marshal(merged)
		if err != nil {
			return dbErr(err, "The connection could not be saved.")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE connection SET source_meta = ? WHERE id = ?`, string(b), connID); err != nil {
			return dbErr(err, "The connection could not be saved.")
		}
	}
	if devState == "ok" || devState == "unavailable" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE connection SET dev_activity_state = ?, dev_activity_checked_at = ? WHERE id = ?`,
			devState, store.Ms(at), connID); err != nil {
			return dbErr(err, "The connection could not be saved.")
		}
	}
	return nil
}
