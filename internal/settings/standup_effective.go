package settings

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"slices"

	"github.com/crueber/cuisle/internal/model"
)

// EffectiveTx loads the effective config of a standup inside tx (Lanes by walk position, Columns by position,
// StatusLanes, Speakers by position with display names, settings, ActiveSprintIDs from standup_sync_state).
func EffectiveTx(ctx context.Context, tx *sql.Tx, id int64) (model.EffectiveConfig, error) {
	st, err := loadStandupTx(ctx, tx, id)
	if err != nil {
		return model.EffectiveConfig{}, err
	}
	se, err := readSettingsTx(ctx, tx, id)
	if err != nil {
		return model.EffectiveConfig{}, err
	}
	lanes, err := readLanesTx(ctx, tx, id)
	if err != nil {
		return model.EffectiveConfig{}, err
	}
	out := model.EffectiveConfig{
		StandupID: id, Rev: st.Rev, Name: st.Name,
		SpaceID: st.SpaceID, SpaceKey: st.SpaceKey, SpaceName: st.SpaceName,
		BoardID: st.BoardID, BoardName: st.BoardName, BoardType: st.BoardType,
		Scope: st.Scope, ExtraFilter: st.ExtraFilter, Lanes: lanes,
		UnassignedPosition: se.UnassignedPosition, ReshuffleDaily: se.ReshuffleDaily,
		ShuffleSeed: se.ShuffleSeed, ShuffleDate: se.ShuffleDate, PulseDays: se.PulseDays,
		ShowCommits: se.ShowCommits, ShowPRs: se.ShowPRs, ShowComments: se.ShowComments,
		ShadeWeekends: se.ShadeWeekends, RowsMode: se.RowsMode, DoneWindow: se.DoneWindow,
		BacklogRows: se.BacklogRows, DoneLabel: se.DoneLabel,
	}

	if out.Columns, err = effectiveColumns(ctx, tx, id); err != nil {
		return model.EffectiveConfig{}, err
	}
	if out.StatusLanes, err = effectiveStatusLanes(ctx, tx, id); err != nil {
		return model.EffectiveConfig{}, err
	}
	if out.Speakers, err = effectiveSpeakers(ctx, tx, id); err != nil {
		return model.EffectiveConfig{}, err
	}

	var sprints string
	err = tx.QueryRowContext(ctx,
		`SELECT active_sprint_ids FROM standup_sync_state WHERE standup_id = ?`, id).Scan(&sprints)
	if err != nil && err != sql.ErrNoRows {
		return model.EffectiveConfig{}, dbErr(err)
	}
	if sprints != "" {
		if err := json.Unmarshal([]byte(sprints), &out.ActiveSprintIDs); err != nil {
			return model.EffectiveConfig{}, dbErr(err)
		}
	}
	return out, nil
}

func effectiveColumns(ctx context.Context, tx *sql.Tx, id int64) ([]model.ColumnLane, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT column_name, position, lane, auto, lane_order FROM standup_column_map WHERE standup_id = ?
		 ORDER BY position, column_name`, id)
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	var out []model.ColumnLane
	var stored []sql.NullInt64
	for rows.Next() {
		var c model.ColumnLane
		var auto int64
		var order sql.NullInt64
		if err := rows.Scan(&c.ColumnName, &c.Position, &c.Lane, &auto, &order); err != nil {
			return nil, dbErr(err)
		}
		c.Auto = auto == 1
		out = append(out, c)
		stored = append(stored, order)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err)
	}
	for _, idx := range laneGroups(out, stored) {
		for i, j := range idx {
			out[j].LaneOrder = i
		}
	}
	return out, nil
}

// laneGroups returns, per lane, the indexes of cols in their within-lane order: columns with a stored lane_order
// first (by that order), then the rest in board order. cols must be in board order.
func laneGroups(cols []model.ColumnLane, stored []sql.NullInt64) map[string][]int {
	out := map[string][]int{}
	for i, c := range cols {
		out[c.Lane] = append(out[c.Lane], i)
	}
	for _, idx := range out {
		slices.SortStableFunc(idx, func(a, b int) int {
			sa, sb := stored[a], stored[b]
			switch {
			case sa.Valid && sb.Valid:
				return cmp.Compare(sa.Int64, sb.Int64)
			case sa.Valid:
				return -1
			case sb.Valid:
				return 1
			}
			return 0
		})
	}
	return out
}

func effectiveStatusLanes(ctx context.Context, tx *sql.Tx, id int64) ([]model.StatusLane, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT status_id, status_name, column_name, lane FROM standup_status_lane WHERE standup_id = ? ORDER BY status_id`, id)
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	var out []model.StatusLane
	for rows.Next() {
		var l model.StatusLane
		if err := rows.Scan(&l.StatusID, &l.StatusName, &l.ColumnName, &l.Lane); err != nil {
			return nil, dbErr(err)
		}
		out = append(out, l)
	}
	return out, dbErr(rows.Err())
}

func effectiveSpeakers(ctx context.Context, tx *sql.Tx, id int64) ([]model.SpeakerConfig, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT account_id, display_name, position, included, added_manually
		 FROM standup_speaker WHERE standup_id = ? ORDER BY position, account_id`, id)
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	var out []model.SpeakerConfig
	for rows.Next() {
		var sp model.SpeakerConfig
		var included, manual int64
		if err := rows.Scan(&sp.AccountID, &sp.Name, &sp.Position, &included, &manual); err != nil {
			return nil, dbErr(err)
		}
		sp.Included = included == 1
		sp.Manual = manual == 1
		out = append(out, sp)
	}
	return out, dbErr(rows.Err())
}

// Effective is EffectiveTx in its own read transaction.
func (s *Store) Effective(ctx context.Context, id int64) (model.EffectiveConfig, error) {
	var out model.EffectiveConfig
	err := s.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = EffectiveTx(ctx, tx, id)
		return err
	})
	if err != nil {
		return model.EffectiveConfig{}, err
	}
	return out, nil
}
