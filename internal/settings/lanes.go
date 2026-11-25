package settings

import (
	"context"
	"database/sql"
	"strings"
	"unicode/utf8"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
)

// Lanes returns the working lanes (built-in and custom) by walk position.
func (s *Store) Lanes(ctx context.Context, id int64) ([]model.LaneConfig, error) {
	var out []model.LaneConfig
	err := s.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := standupBoardTx(ctx, tx, id); err != nil {
			return err
		}
		var err error
		out, err = readLanesTx(ctx, tx, id)
		return err
	})
	return out, err
}

// SetWalkOrder accepts a permutation of the standup's working lanes, custom lanes included (duplicates, missing lanes,
// done or an unknown lane → validation "lanes").
func (s *Store) SetWalkOrder(ctx context.Context, id int64, lanes []string) (Ack, error) {
	return s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		cur, err := readLanesTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if !validWalkOrder(cur, lanes) {
			return apperr.Validationf("lanes", "Put every working lane in order, each once.")
		}
		for pos, lane := range lanes {
			if _, err := tx.ExecContext(ctx,
				`UPDATE standup_lane SET walk_position = ? WHERE standup_id = ? AND lane = ?`, pos, id, lane); err != nil {
				return dbErr(err)
			}
		}
		return nil
	})
}

// SetLaneLimit sets limit_days 0–99 for a working lane of the standup, custom lanes included (0 = no limit).
func (s *Store) SetLaneLimit(ctx context.Context, id int64, lane string, days int) (Ack, error) {
	if !model.IsWorkingLane(lane) {
		return Ack{}, apperr.Validationf("lane", "Choose a working lane.")
	}
	if days < 0 || days > 99 {
		return Ack{}, apperr.Validationf("days", "Enter a limit from 0 to 99 days (0 means no limit).")
	}
	return s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE standup_lane SET limit_days = ? WHERE standup_id = ? AND lane = ?`, days, id, lane)
		if err != nil {
			return dbErr(err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return apperr.Validationf("lane", "That lane no longer exists.")
		}
		return nil
	})
}

// DefaultCustomLimit is the days-in-lane limit of a new custom lane.
const DefaultCustomLimit = 5

// CreateLane adds a custom working lane named label (trimmed, 1–40 characters, unique among the standup's lane labels
// ignoring case) at the end of the walk, with the next id ("c<N>", never reused) and the lowest palette colour no live custom lane uses (nextHue).
// When columnName is set, that column is mapped to the new lane in the same transaction (needs_sync). Creating a lane
// whose name matches an existing custom lane reuses that lane, so a retried call does not create a second one.
// Returns the lane id.
func (s *Store) CreateLane(ctx context.Context, id int64, label, columnName string) (string, Ack, error) {
	label = strings.TrimSpace(label)
	if err := checkLabelText(label); err != nil {
		return "", Ack{}, err
	}
	var laneID string
	ack, err := s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		lanes, err := readLanesTx(ctx, tx, id)
		if err != nil {
			return err
		}
		se, err := readSettingsTx(ctx, tx, id)
		if err != nil {
			return err
		}
		for _, l := range lanes {
			if model.IsCustomLane(l.Lane) && strings.EqualFold(l.Label, label) {
				laneID = l.Lane // idempotent: the lane already exists
			}
		}
		if laneID == "" {
			if clash := labelClash(lanes, se.DoneLabel, "", label); clash != "" {
				return apperr.Validationf("label", "There's already a lane called %s.", clash)
			}
			custom := 0
			for _, l := range lanes {
				if model.IsCustomLane(l.Lane) {
					custom++
				}
			}
			if custom >= model.MaxCustomLanes {
				return apperr.Validationf("label", "A standup can have up to %d lanes of its own.", model.MaxCustomLanes)
			}
			var seq int
			if err := tx.QueryRowContext(ctx,
				`UPDATE standup_settings SET lane_seq = lane_seq + 1 WHERE standup_id = ? RETURNING lane_seq`, id).Scan(&seq); err != nil {
				return dbErr(err)
			}
			laneID = customLaneID(seq)
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days, label, hue) VALUES (?, ?, ?, ?, ?, ?)`,
				id, laneID, len(lanes), DefaultCustomLimit, label, nextHue(lanes, seq)); err != nil {
				return dbErr(err)
			}
		}
		if columnName == "" {
			return nil
		}
		return setColumnLaneTx(ctx, tx, id, columnName, laneID)
	})
	if err != nil {
		return "", Ack{}, err
	}
	return laneID, ack, nil
}

// RenameLane sets the display label of a lane: a custom lane (label required), a built-in working lane or Done (""
// restores the built-in label). Labels are trimmed, 1–40 characters and unique among the standup's lanes ignoring case.
// Ids never change, so history and mappings are untouched; no sync is needed.
func (s *Store) RenameLane(ctx context.Context, id int64, lane, label string) (Ack, error) {
	label = strings.TrimSpace(label)
	switch {
	case model.IsCustomLane(lane):
		if err := checkLabelText(label); err != nil {
			return Ack{}, err
		}
	case model.IsWorkingLane(lane) || lane == model.LaneDone:
		if label != "" {
			if err := checkLabelText(label); err != nil {
				return Ack{}, err
			}
		}
	default:
		return Ack{}, apperr.Validationf("lane", "That lane can't be renamed.")
	}
	return s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		lanes, err := readLanesTx(ctx, tx, id)
		if err != nil {
			return err
		}
		se, err := readSettingsTx(ctx, tx, id)
		if err != nil {
			return err
		}
		effective := label
		if effective == "" {
			effective = model.DefaultLaneLabel(lane)
		}
		if clash := labelClash(lanes, se.DoneLabel, lane, effective); clash != "" {
			return apperr.Validationf("label", "There's already a lane called %s.", clash)
		}
		if lane == model.LaneDone {
			_, err = tx.ExecContext(ctx, `UPDATE standup_settings SET done_label = ? WHERE standup_id = ?`, nullStr(label), id)
			return dbErr(err)
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE standup_lane SET label = ? WHERE standup_id = ? AND lane = ?`, nullStr(label), id, lane)
		if err != nil {
			return dbErr(err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return apperr.Validationf("lane", "That lane no longer exists.")
		}
		return nil
	})
}

// DeleteLane removes a custom lane. Its columns move to moveTo (any other lane of the standup: built-in, done, backlog,
// hidden or another custom lane), as the user's choice; moveTo may be "" only when no column is mapped to the lane.
// The walk closes up. Synced history is never touched: the lane id is simply no longer mapped (needs_sync when columns
// moved).
func (s *Store) DeleteLane(ctx context.Context, id int64, lane, moveTo string) (Ack, error) {
	if !model.IsCustomLane(lane) {
		return Ack{}, apperr.Validationf("lane", "Only lanes you added can be deleted.")
	}
	if moveTo == lane {
		return Ack{}, apperr.Validationf("moveTo", "Choose another lane for its columns.")
	}
	return s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		lanes, err := readLanesTx(ctx, tx, id)
		if err != nil {
			return err
		}
		found := false
		for _, l := range lanes {
			found = found || l.Lane == lane
		}
		if !found {
			return nil // already deleted (a retried call)
		}
		var cols int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM standup_column_map WHERE standup_id = ? AND lane = ?`, id, lane).Scan(&cols); err != nil {
			return dbErr(err)
		}
		if cols > 0 {
			if !laneExists(lanes, moveTo) {
				return apperr.Validationf("moveTo", "Choose a lane for its columns.")
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE standup_column_map SET lane = ?, auto = 0, lane_order = NULL WHERE standup_id = ? AND lane = ?`,
				moveTo, id, lane); err != nil {
				return dbErr(err)
			}
			if err := RebuildStatusLanesTx(ctx, tx, id); err != nil {
				return err
			}
			if err := SetNeedsSyncTx(ctx, tx, id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM standup_lane WHERE standup_id = ? AND lane = ?`, id, lane); err != nil {
			return dbErr(err)
		}
		pos := 0
		for _, l := range lanes {
			if l.Lane == lane {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE standup_lane SET walk_position = ? WHERE standup_id = ? AND lane = ?`, pos, id, l.Lane); err != nil {
				return dbErr(err)
			}
			pos++
		}
		return nil
	})
}

// nextHue is the colour of a new custom lane: the lowest palette index (1..LaneHues) no live custom lane of the standup
// uses, so colours stay distinct; when every colour is taken it rotates by the lane's sequence number.
func nextHue(lanes []model.LaneConfig, seq int) int {
	used := make(map[int]bool, len(lanes))
	for _, l := range lanes {
		if model.IsCustomLane(l.Lane) {
			used[l.Hue] = true
		}
	}
	for h := 1; h <= model.LaneHues; h++ {
		if !used[h] {
			return h
		}
	}
	return (seq-1)%model.LaneHues + 1
}

// checkLabelText validates a trimmed, non-empty lane label.
func checkLabelText(label string) error {
	if label == "" {
		return apperr.Validationf("label", "Give the lane a name.")
	}
	if utf8.RuneCountInString(label) > model.MaxLaneLabel {
		return apperr.Validationf("label", "Keep lane names to %d characters.", model.MaxLaneLabel)
	}
	return nil
}

// labelClash returns the existing label that equals label ignoring case, among every lane of the standup except self
// (built-in lanes by their effective label, Backlog and Hidden included), or "".
func labelClash(lanes []model.LaneConfig, doneLabel, self, label string) string {
	cfg := model.EffectiveConfig{Lanes: lanes, DoneLabel: doneLabel}
	for _, id := range allLaneIDs(lanes) {
		if id == self {
			continue
		}
		if other := cfg.LaneLabel(id); strings.EqualFold(other, label) {
			return other
		}
	}
	return ""
}

// allLaneIDs lists the lane ids a column can map to: backlog, the four built-in working lanes, the custom lanes in
// creation order, done, hidden.
func allLaneIDs(lanes []model.LaneConfig) []string {
	out := []string{model.LaneBacklog, model.LaneTodo, model.LaneProg, model.LaneReview, model.LaneQA}
	out = append(out, customLaneIDs(lanes)...)
	return append(out, model.LaneDone, model.LaneHidden)
}
