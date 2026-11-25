package settings

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// AutoLaneNamed maps a board column to a lane: first a lane whose label equals the column name (trimmed, ignoring
// case; custom lanes and renamed built-in lanes), then the AutoLane rules. lanes are the standup's working lanes. Both
// settings.MergeColumnsTx and sync's lane preview use it, so they always agree.
func AutoLaneNamed(col model.Column, index, count int, statuses map[string]model.Status, lanes []model.LaneConfig) string {
	name := strings.TrimSpace(col.Name)
	for _, l := range lanes {
		if l.Label != "" && strings.EqualFold(strings.TrimSpace(l.Label), name) {
			return l.Lane
		}
	}
	return AutoLane(col, index, count, statuses)
}

// AutoLane maps a board column to a lane by the jira-sync.md §2.5 rules (first match; case-insensitive on the name;
// index/count give "last column"; statuses supply categories by id).
func AutoLane(col model.Column, index, count int, statuses map[string]model.Status) string {
	name := strings.ToLower(strings.TrimSpace(col.Name))
	switch {
	case strings.Contains(name, "backlog"):
		return model.LaneBacklog
	case strings.Contains(name, "review") || name == "pr":
		return model.LaneReview
	case containsAny(name, "qa", "test", "verif", "uat", "ready for release"):
		return model.LaneQA
	}

	allDone, allNew := len(col.StatusIDs) > 0, len(col.StatusIDs) > 0
	for _, id := range col.StatusIDs {
		st, ok := statuses[id]
		if !ok {
			allDone, allNew = false, false
			continue
		}
		allDone = allDone && st.Category == "done"
		allNew = allNew && st.Category == "new"
	}
	if index == count-1 || allDone {
		return model.LaneDone
	}

	if strings.Contains(name, "progress") || strings.Contains(name, "doing") ||
		(strings.Contains(name, "dev") && !strings.Contains(name, "for dev")) {
		return model.LaneProg
	}
	if allNew {
		return model.LaneTodo
	}
	if len(col.StatusIDs) > 0 {
		if st, ok := statuses[col.StatusIDs[0]]; ok && st.Category == "indeterminate" {
			return model.LaneProg
		}
	}
	return model.LaneTodo
}

// metaJSON encodes a provider snapshot map; nil becomes "{}".
func metaJSON(m map[string]any) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", apperr.Wrap(apperr.Internal, "Could not save settings.", err)
	}
	return string(b), nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SetBoard records the board snapshot (name, kind, meta), applies kanban defaults (scope=board, done_window=14d),
// replaces the board_column* cache rows for the board, merges the column map and rebuilds status lanes — one tx;
// needs_sync.
func (s *Store) SetBoard(ctx context.Context, id int64, cfg model.BoardConfig, statuses []model.Status) (Ack, error) {
	kind := cfg.Board.Kind
	if kind != "scrum" && kind != "kanban" {
		return Ack{}, apperr.Validationf("boardType", "That board type is not supported.")
	}
	meta, err := metaJSON(cfg.Board.Meta)
	if err != nil {
		return Ack{}, err
	}
	byID := make(map[string]model.Status, len(statuses))
	for _, st := range statuses {
		byID[st.ID] = st
	}
	return s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		connID, _, err := standupBoardTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if !connID.Valid {
			return apperr.Validationf("boardId", "Connect to a site before choosing a board.")
		}
		// Speakers are the people on the board: another board (or a first board after a space change) starts a fresh
		// list, which the next sync fills. Re-picking the same board keeps it.
		var prev sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT board_id FROM standup WHERE id = ?`, id).Scan(&prev); err != nil {
			return dbErr(err)
		}
		if !prev.Valid || prev.String != cfg.Board.ID {
			if err := clearSpeakersTx(ctx, tx, id); err != nil {
				return err
			}
		}
		if kind == "kanban" {
			if _, err := tx.ExecContext(ctx, `UPDATE standup SET scope = 'board' WHERE id = ?`, id); err != nil {
				return dbErr(err)
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE standup_settings SET done_window = '14d' WHERE standup_id = ?`, id); err != nil {
				return dbErr(err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE standup SET board_id = ?, board_name = ?, board_type = ?, board_meta = ? WHERE id = ?`,
			cfg.Board.ID, cfg.Board.Name, kind, meta, id); err != nil {
			return dbErr(err)
		}
		if cfg.Board.SpaceID != "" {
			if _, err := tx.ExecContext(ctx,
				`UPDATE standup SET space_id = ? WHERE id = ?`, cfg.Board.SpaceID, id); err != nil {
				return dbErr(err)
			}
		}
		for _, st := range statuses {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO status (connection_id, id, name, category, blocked_hint) VALUES (?, ?, ?, ?, ?)
				 ON CONFLICT(connection_id, id) DO UPDATE SET name = excluded.name, category = excluded.category,
				 blocked_hint = excluded.blocked_hint`,
				connID.Int64, st.ID, st.Name, st.Category, boolInt(st.BlockedHint)); err != nil {
				return dbErr(err)
			}
		}
		if err := ReplaceBoardColumnsTx(ctx, tx, connID.Int64, cfg); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE board SET config_fetched_at = ?, fetched_at = ? WHERE connection_id = ? AND id = ?`,
			store.Ms(s.Now()), store.Ms(s.Now()), connID.Int64, cfg.Board.ID); err != nil {
			return dbErr(err)
		}
		if _, err := MergeColumnsTx(ctx, tx, id, cfg.Columns, byID); err != nil {
			return err
		}
		return SetNeedsSyncTx(ctx, tx, id)
	})
}

// ReplaceBoardColumnsTx replaces board/board_column/board_column_status cache rows for cfg.Board under connID.
func ReplaceBoardColumnsTx(ctx context.Context, tx *sql.Tx, connID int64, cfg model.BoardConfig) error {
	b := cfg.Board
	meta, err := metaJSON(b.Meta)
	if err != nil {
		return err
	}
	raw := b.RawType
	if raw == "" {
		raw = b.Kind
	}
	var space any
	if b.SpaceID != "" {
		space = b.SpaceID
	}
	now := store.Ms(time.Now())
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO board (connection_id, id, name, type, space_id, source_meta, config_fetched_at, fetched_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(connection_id, id) DO UPDATE SET name = excluded.name, type = excluded.type,
		 space_id = excluded.space_id, source_meta = excluded.source_meta,
		 config_fetched_at = excluded.config_fetched_at, fetched_at = excluded.fetched_at`,
		connID, b.ID, b.Name, raw, space, meta, now, now); err != nil {
		return dbErr(err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM board_column_status WHERE connection_id = ? AND board_id = ?`, connID, b.ID); err != nil {
		return dbErr(err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM board_column WHERE connection_id = ? AND board_id = ?`, connID, b.ID); err != nil {
		return dbErr(err)
	}
	for i, col := range cfg.Columns {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO board_column (connection_id, board_id, position, name) VALUES (?, ?, ?, ?)`,
			connID, b.ID, i, col.Name); err != nil {
			return dbErr(err)
		}
		for _, sid := range col.StatusIDs {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO board_column_status (connection_id, board_id, position, status_id) VALUES (?, ?, ?, ?)`,
				connID, b.ID, i, sid); err != nil {
				return dbErr(err)
			}
		}
	}
	return nil
}

// SetColumnLane sets a column's lane (auto=0) and rebuilds status lanes; needs_sync. lane is a built-in lane id or one
// of the standup's custom lanes.
func (s *Store) SetColumnLane(ctx context.Context, id int64, columnName, lane string) (Ack, error) {
	if lane == "" {
		return Ack{}, apperr.Validationf("lane", "Choose a lane for this column.")
	}
	if columnName == "" {
		return Ack{}, apperr.Validationf("columnName", "Choose a column.")
	}
	return s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		return setColumnLaneTx(ctx, tx, id, columnName, lane)
	})
}

// setColumnLaneTx maps one column to lane (auto=0), rebuilds status lanes and marks the standup as needing a sync.
func setColumnLaneTx(ctx context.Context, tx *sql.Tx, id int64, columnName, lane string) error {
	ok, err := laneExistsTx(ctx, tx, id, lane)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.Validationf("lane", "Choose a lane for this column.")
	}
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM standup_column_map WHERE standup_id = ? AND column_name = ?`,
		id, columnName).Scan(&n); err != nil {
		return dbErr(err)
	}
	if n == 0 {
		return apperr.Validationf("columnName", "That column is not on this board.")
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE standup_column_map SET lane_order = CASE WHEN lane = ? THEN lane_order END, lane = ?, auto = 0
		 WHERE standup_id = ? AND column_name = ?`,
		lane, lane, id, columnName); err != nil {
		return dbErr(err)
	}
	if err := RebuildStatusLanesTx(ctx, tx, id); err != nil {
		return err
	}
	return SetNeedsSyncTx(ctx, tx, id)
}

// MoveColumn moves a column to toIndex (clamped) among the columns mapped to the same lane and stores the whole lane's
// order densely. Tickets in that lane are then grouped in this order (view.Build). No sync is needed.
func (s *Store) MoveColumn(ctx context.Context, id int64, columnName string, toIndex int) (Ack, error) {
	return s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		cols, err := effectiveColumns(ctx, tx, id)
		if err != nil {
			return err
		}
		lane := ""
		for _, c := range cols {
			if c.ColumnName == columnName {
				lane = c.Lane
			}
		}
		if lane == "" {
			return apperr.Validationf("columnName", "That column is not on this board.")
		}
		var group []model.ColumnLane
		for _, c := range cols {
			if c.Lane == lane {
				group = append(group, c)
			}
		}
		slices.SortFunc(group, func(a, b model.ColumnLane) int { return cmp.Compare(a.LaneOrder, b.LaneOrder) })
		names := make([]string, 0, len(group))
		for _, c := range group {
			if c.ColumnName != columnName {
				names = append(names, c.ColumnName)
			}
		}
		toIndex = max(0, min(toIndex, len(names)))
		names = slices.Insert(names, toIndex, columnName)
		for i, n := range names {
			if _, err := tx.ExecContext(ctx,
				`UPDATE standup_column_map SET lane_order = ? WHERE standup_id = ? AND column_name = ?`, i, id, n); err != nil {
				return dbErr(err)
			}
		}
		return nil
	})
}

// UnmappedStatuses returns names of statuses in the board's columns that map to no status lane (sorted).
func (s *Store) UnmappedStatuses(ctx context.Context, id int64) ([]string, error) {
	var out []string
	err := s.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		connID, boardID, err := standupBoardTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if !connID.Valid || !boardID.Valid {
			return nil
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT DISTINCT st.name FROM board_column_status bcs
			 JOIN status st ON st.connection_id = bcs.connection_id AND st.id = bcs.status_id
			 WHERE bcs.connection_id = ? AND bcs.board_id = ?
			   AND NOT EXISTS (SELECT 1 FROM standup_status_lane l WHERE l.standup_id = ? AND l.status_id = bcs.status_id)
			 ORDER BY st.name`, connID.Int64, boardID.String, id)
		if err != nil {
			return dbErr(err)
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return dbErr(err)
			}
			out = append(out, name)
		}
		if err := rows.Err(); err != nil {
			return dbErr(err)
		}
		return nil
	})
	return out, err
}
