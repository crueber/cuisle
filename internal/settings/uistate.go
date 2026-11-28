package settings

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/store"
)

// UIState reads ui_state + standup_ui_state.
func (s *Store) UIState(ctx context.Context) (UIState, error) {
	var st UIState
	err := s.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		st, err = readUIStateTx(ctx, tx)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT standup_id, COALESCE(last_person, ''), board_group, zoom FROM standup_ui_state`)
		if err != nil {
			return dbErr(err)
		}
		defer rows.Close()
		st.LastPerson = map[int64]string{}
		st.BoardGroup = map[int64]string{}
		st.Zoom = map[int64]int{}
		for rows.Next() {
			var id int64
			var who, group string
			var zoom int
			if err := rows.Scan(&id, &who, &group, &zoom); err != nil {
				return dbErr(err)
			}
			st.LastPerson[id] = who
			st.BoardGroup[id] = group
			st.Zoom[id] = zoom
		}
		return dbErr(rows.Err())
	})
	if err != nil {
		return UIState{}, err
	}
	return st, nil
}

// uiRow is the raw ui_state row (single row, id = 1).
type uiRow struct {
	lastStandup sql.NullInt64
	lastView    string
	x, y, w, h  sql.NullInt64
	maximised   int64
}

func readUIStateTx(ctx context.Context, tx *sql.Tx) (UIState, error) {
	r, err := loadUIRowTx(ctx, tx)
	if err != nil {
		return UIState{}, err
	}
	st := UIState{LastStandupID: r.lastStandup.Int64, LastView: r.lastView}
	if r.x.Valid && r.y.Valid && r.w.Valid && r.h.Valid {
		st.Window = &WindowState{
			X: int(r.x.Int64), Y: int(r.y.Int64), W: int(r.w.Int64), H: int(r.h.Int64),
			Maximised: r.maximised == 1,
		}
	}
	return st, nil
}

func loadUIRowTx(ctx context.Context, tx *sql.Tx) (uiRow, error) {
	var r uiRow
	err := tx.QueryRowContext(ctx,
		`SELECT last_standup_id, last_view, win_x, win_y, win_w, win_h, win_maximised FROM ui_state WHERE id = 1`).Scan(
		&r.lastStandup, &r.lastView, &r.x, &r.y, &r.w, &r.h, &r.maximised)
	if err != nil {
		return uiRow{}, dbErr(err)
	}
	return r, nil
}

// SetUIState applies a patch (validation: lastView standup|configure; standup ids must exist).
// The patch is all-or-nothing: every field is validated before anything is written.
func (s *Store) SetUIState(ctx context.Context, p UIStatePatch) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if p.LastView != nil && *p.LastView != "standup" && *p.LastView != "configure" {
			return apperr.Validationf("lastView", "The last view must be the standup or Configure.")
		}
		if p.BoardGroup != nil && *p.BoardGroup != BoardGroupAssignee && *p.BoardGroup != BoardGroupStatus {
			return apperr.Validationf("boardGroup", "The board grouping must be assignee or status.")
		}
		if p.Zoom != nil && !slices.Contains(ZoomLevels, *p.Zoom) {
			return apperr.Validationf("zoom", "The zoom must be one of 70, 80, 90, 100, 110, 125, 150, 175 or 200 percent.")
		}
		if p.LastStandupID != nil && *p.LastStandupID != 0 {
			if err := requireStandupTx(ctx, tx, *p.LastStandupID, "lastStandupId"); err != nil {
				return err
			}
		}
		if p.StandupID != nil && (p.LastStandupID == nil || *p.LastStandupID != *p.StandupID || *p.LastStandupID == 0) {
			if err := requireStandupTx(ctx, tx, *p.StandupID, "standupId"); err != nil {
				return err
			}
		}
		if p.LastPerson != nil && p.StandupID == nil {
			return apperr.Validationf("standupId", "A last person needs the standup it belongs to.")
		}
		if p.BoardGroup != nil && p.StandupID == nil {
			return apperr.Validationf("standupId", "A board grouping needs the standup it belongs to.")
		}
		if p.Zoom != nil && p.StandupID == nil {
			return apperr.Validationf("standupId", "A zoom needs the standup it belongs to.")
		}

		now := store.Ms(s.Now())
		if p.LastStandupID != nil || p.LastView != nil || p.Window != nil {
			r, err := loadUIRowTx(ctx, tx)
			if err != nil {
				return err
			}
			if p.LastStandupID != nil {
				r.lastStandup = sql.NullInt64{Int64: *p.LastStandupID, Valid: *p.LastStandupID != 0}
			}
			if p.LastView != nil {
				r.lastView = *p.LastView
			}
			if p.Window != nil {
				w := p.Window
				r.x = sql.NullInt64{Int64: int64(w.X), Valid: true}
				r.y = sql.NullInt64{Int64: int64(w.Y), Valid: true}
				r.w = sql.NullInt64{Int64: int64(w.W), Valid: true}
				r.h = sql.NullInt64{Int64: int64(w.H), Valid: true}
				r.maximised = int64(bi(w.Maximised))
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE ui_state SET last_standup_id = ?, last_view = ?, win_x = ?, win_y = ?, win_w = ?, win_h = ?,
				        win_maximised = ?, updated_at = ? WHERE id = 1`,
				r.lastStandup, r.lastView, r.x, r.y, r.w, r.h, r.maximised, now); err != nil {
				return dbErr(err)
			}
		}
		if p.StandupID != nil && (p.LastPerson != nil || p.BoardGroup != nil || p.Zoom != nil) {
			cols := []string{"standup_id"}
			vals := []string{"?"}
			args := []any{*p.StandupID}
			updates := []string{}
			if p.LastPerson != nil {
				cols = append(cols, "last_person")
				vals = append(vals, "?")
				args = append(args, nullStr(*p.LastPerson))
				updates = append(updates, "last_person = excluded.last_person")
			}
			if p.BoardGroup != nil {
				cols = append(cols, "board_group")
				vals = append(vals, "?")
				args = append(args, *p.BoardGroup)
				updates = append(updates, "board_group = excluded.board_group")
			}
			if p.Zoom != nil {
				cols = append(cols, "zoom")
				vals = append(vals, "?")
				args = append(args, *p.Zoom)
				updates = append(updates, "zoom = excluded.zoom")
			}
			cols = append(cols, "updated_at")
			vals = append(vals, "?")
			args = append(args, now)
			updates = append(updates, "updated_at = excluded.updated_at")
			q := "INSERT INTO standup_ui_state (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(vals, ", ") +
				") ON CONFLICT(standup_id) DO UPDATE SET " + strings.Join(updates, ", ")
			if _, err := tx.ExecContext(ctx, q, args...); err != nil {
				return dbErr(err)
			}
		}
		return nil
	})
}

// requireStandupTx returns a validation error on field when no standup has the id.
func requireStandupTx(ctx context.Context, tx *sql.Tx, id int64, field string) error {
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM standup WHERE id = ?`, id).Scan(&n)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dbErr(err)
	}
	if n == 0 {
		return apperr.Validationf(field, "That standup does not exist.")
	}
	return nil
}
