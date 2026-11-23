package settings

import (
	"context"
	"database/sql"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
)

// Lanes returns the 4 walk lanes by walk position.
func (s *Store) Lanes(ctx context.Context, id int64) ([]model.LaneConfig, error) {
	var out []model.LaneConfig
	err := s.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := standupBoardTx(ctx, tx, id); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT lane, walk_position, limit_days FROM standup_lane WHERE standup_id = ? ORDER BY walk_position`, id)
		if err != nil {
			return dbErr(err)
		}
		defer rows.Close()
		for rows.Next() {
			var lc model.LaneConfig
			if err := rows.Scan(&lc.Lane, &lc.Position, &lc.LimitDays); err != nil {
				return dbErr(err)
			}
			out = append(out, lc)
		}
		if err := rows.Err(); err != nil {
			return dbErr(err)
		}
		return nil
	})
	return out, err
}

// SetWalkOrder accepts a permutation of todo, prog, review, qa (duplicates, missing lanes, done → validation "lanes").
func (s *Store) SetWalkOrder(ctx context.Context, id int64, lanes []string) (Ack, error) {
	if !validWalkOrder(lanes) {
		return Ack{}, apperr.Validationf("lanes", "Put the four working lanes in order, each once.")
	}
	return s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		for pos, lane := range lanes {
			if _, err := tx.ExecContext(ctx,
				`UPDATE standup_lane SET walk_position = ? WHERE standup_id = ? AND lane = ?`, pos, id, lane); err != nil {
				return dbErr(err)
			}
		}
		return nil
	})
}

// SetLaneLimit sets limit_days 0–99 for a working lane (0 = no limit).
func (s *Store) SetLaneLimit(ctx context.Context, id int64, lane string, days int) (Ack, error) {
	if !isWorkingLane(lane) {
		return Ack{}, apperr.Validationf("lane", "Choose a working lane: todo, prog, review or qa.")
	}
	if days < 0 || days > 99 {
		return Ack{}, apperr.Validationf("days", "Enter a limit from 0 to 99 days (0 means no limit).")
	}
	return s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE standup_lane SET limit_days = ? WHERE standup_id = ? AND lane = ?`, days, id, lane); err != nil {
			return dbErr(err)
		}
		return nil
	})
}
