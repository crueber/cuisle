package settings

import (
	"context"
	"database/sql"

	"github.com/crueber/cuisle/internal/model"
)

// columnRow is one standup_column_map row.
type columnRow struct {
	position int
	lane     string
	auto     int
}

// loadColumnMapTx reads a standup's column map keyed by column name.
func loadColumnMapTx(ctx context.Context, tx *sql.Tx, id int64) (map[string]columnRow, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT column_name, position, lane, auto FROM standup_column_map WHERE standup_id = ?`, id)
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	out := map[string]columnRow{}
	for rows.Next() {
		var name string
		var r columnRow
		if err := rows.Scan(&name, &r.position, &r.lane, &r.auto); err != nil {
			return nil, dbErr(err)
		}
		out[name] = r
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err)
	}
	return out, nil
}

// MergeColumnsTx merges cols into standup_column_map: new columns auto-mapped, removed ones deleted, auto=0 choices
// kept by column name, positions updated; then RebuildStatusLanesTx. Returns whether anything changed.
func MergeColumnsTx(ctx context.Context, tx *sql.Tx, id int64, cols []model.Column, statuses map[string]model.Status) (bool, error) {
	existing, err := loadColumnMapTx(ctx, tx, id)
	if err != nil {
		return false, err
	}
	changed := false
	desired := make(map[string]bool, len(cols))
	for i, col := range cols {
		desired[col.Name] = true
		lane := AutoLane(col, i, len(cols), statuses)
		auto := 1
		old, ok := existing[col.Name]
		if ok && old.auto == 0 {
			lane, auto = old.lane, 0
		}
		if !ok || old.position != i || old.lane != lane || old.auto != auto {
			changed = true
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(standup_id, column_name) DO UPDATE SET position = excluded.position,
			 lane_order = CASE WHEN standup_column_map.lane = excluded.lane THEN standup_column_map.lane_order END,
			 lane = excluded.lane, auto = excluded.auto`,
			id, col.Name, i, lane, auto); err != nil {
			return false, dbErr(err)
		}
	}
	for name := range existing {
		if desired[name] {
			continue
		}
		changed = true
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM standup_column_map WHERE standup_id = ? AND column_name = ?`, id, name); err != nil {
			return false, dbErr(err)
		}
	}
	if err := RebuildStatusLanesTx(ctx, tx, id); err != nil {
		return false, err
	}
	return changed, nil
}

// RebuildStatusLanesTx rewrites standup_status_lane from standup_column_map, board_column_status and status names.
func RebuildStatusLanesTx(ctx context.Context, tx *sql.Tx, id int64) error {
	connID, boardID, err := standupBoardTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM standup_status_lane WHERE standup_id = ?`, id); err != nil {
		return dbErr(err)
	}
	if !connID.Valid || !boardID.Valid {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO standup_status_lane (standup_id, status_id, status_name, column_name, lane)
		 SELECT ?, bcs.status_id, COALESCE(st.name, bcs.status_id), m.column_name, m.lane
		 FROM board_column_status bcs
		 JOIN board_column bc ON bc.connection_id = bcs.connection_id AND bc.board_id = bcs.board_id
		      AND bc.position = bcs.position
		 JOIN standup_column_map m ON m.standup_id = ? AND m.column_name = bc.name
		 LEFT JOIN status st ON st.connection_id = bcs.connection_id AND st.id = bcs.status_id
		 WHERE bcs.connection_id = ? AND bcs.board_id = ?`,
		id, id, connID.Int64, boardID.String); err != nil {
		return dbErr(err)
	}
	return nil
}
