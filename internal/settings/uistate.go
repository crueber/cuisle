package settings

import (
	"context"
	"database/sql"
	"errors"

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
		rows, err := tx.QueryContext(ctx, `SELECT standup_id, COALESCE(last_person, '') FROM standup_ui_state`)
		if err != nil {
			return dbErr(err)
		}
		defer rows.Close()
		st.LastPerson = map[int64]string{}
		for rows.Next() {
			var id int64
			var who string
			if err := rows.Scan(&id, &who); err != nil {
				return dbErr(err)
			}
			st.LastPerson[id] = who
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
		r, err := loadUIRowTx(ctx, tx)
		if err != nil {
			return err
		}
		if p.LastView != nil && *p.LastView != "standup" && *p.LastView != "configure" {
			return apperr.Validationf("lastView", "The last view must be the standup or Configure.")
		}
		if p.LastStandupID != nil && *p.LastStandupID != 0 {
			if err := requireStandupTx(ctx, tx, *p.LastStandupID, "lastStandupId"); err != nil {
				return err
			}
		}
		if p.StandupID != nil {
			if err := requireStandupTx(ctx, tx, *p.StandupID, "standupId"); err != nil {
				return err
			}
		}
		if p.LastPerson != nil && p.StandupID == nil {
			return apperr.Validationf("standupId", "A last person needs the standup it belongs to.")
		}

		now := store.Ms(s.Now())
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
		if p.StandupID != nil && p.LastPerson != nil {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO standup_ui_state (standup_id, last_person, updated_at) VALUES (?, ?, ?)
				 ON CONFLICT(standup_id) DO UPDATE SET last_person = excluded.last_person, updated_at = excluded.updated_at`,
				*p.StandupID, nullStr(*p.LastPerson), now); err != nil {
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
