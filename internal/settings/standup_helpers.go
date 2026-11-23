package settings

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
)

// orderByRE matches an ORDER BY clause in an extra filter (case-insensitive, any whitespace).
var orderByRE = regexp.MustCompile(`(?i)\border\s+by\b`)

func bi(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullBool(b *bool) any {
	if b == nil {
		return nil
	}
	return bi(*b)
}

// readSettingsTx loads the standup_settings row.
func readSettingsTx(ctx context.Context, tx *sql.Tx, id int64) (Settings, error) {
	var (
		se                                               Settings
		reshuffle, commits, prs, comments, shadeWeekends int64
		seed                                             int64
		shuffleDate                                      sql.NullString
		pulse, backlog                                   int64
	)
	err := tx.QueryRowContext(ctx,
		`SELECT rows_mode, unassigned_position, reshuffle_daily, shuffle_seed, shuffle_date, pulse_days,
		        show_commits, show_prs, show_comments, shade_weekends, done_window, backlog_rows
		 FROM standup_settings WHERE standup_id = ?`, id).Scan(
		&se.RowsMode, &se.UnassignedPosition, &reshuffle, &seed, &shuffleDate, &pulse,
		&commits, &prs, &comments, &shadeWeekends, &se.DoneWindow, &backlog)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, apperr.New(apperr.NotFound, "That standup no longer exists.")
	}
	if err != nil {
		return Settings{}, dbErr(err)
	}
	se.ReshuffleDaily = reshuffle == 1
	se.ShuffleSeed = seed
	se.ShuffleDate = shuffleDate.String
	se.PulseDays = int(pulse)
	se.ShowCommits = commits == 1
	se.ShowPRs = prs == 1
	se.ShowComments = comments == 1
	se.ShadeWeekends = shadeWeekends == 1
	se.BacklogRows = int(backlog)
	return se, nil
}

// writeSettingsTx writes every standup_settings column of se.
func writeSettingsTx(ctx context.Context, tx *sql.Tx, id int64, se Settings) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE standup_settings SET rows_mode = ?, unassigned_position = ?, reshuffle_daily = ?, shuffle_seed = ?,
		        shuffle_date = ?, pulse_days = ?, show_commits = ?, show_prs = ?, show_comments = ?, shade_weekends = ?,
		        done_window = ?, backlog_rows = ?
		 WHERE standup_id = ?`,
		se.RowsMode, se.UnassignedPosition, bi(se.ReshuffleDaily), se.ShuffleSeed, nullStr(se.ShuffleDate),
		se.PulseDays, bi(se.ShowCommits), bi(se.ShowPRs), bi(se.ShowComments), bi(se.ShadeWeekends),
		se.DoneWindow, se.BacklogRows, id)
	return dbErr(err)
}

// readLanesTx returns the walk lanes of a standup by walk position.
func readLanesTx(ctx context.Context, tx *sql.Tx, id int64) ([]model.LaneConfig, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT lane, walk_position, limit_days FROM standup_lane WHERE standup_id = ? ORDER BY walk_position`, id)
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	var out []model.LaneConfig
	for rows.Next() {
		var l model.LaneConfig
		if err := rows.Scan(&l.Lane, &l.Position, &l.LimitDays); err != nil {
			return nil, dbErr(err)
		}
		out = append(out, l)
	}
	return out, dbErr(rows.Err())
}

// writeLanesTx replaces the standup's walk lanes.
func writeLanesTx(ctx context.Context, tx *sql.Tx, id int64, lanes []model.LaneConfig) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM standup_lane WHERE standup_id = ?`, id); err != nil {
		return dbErr(err)
	}
	for _, l := range lanes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days) VALUES (?, ?, ?, ?)`,
			id, l.Lane, l.Position, l.LimitDays); err != nil {
			return dbErr(err)
		}
	}
	return nil
}

// liveOrderTx returns the ids of live standups in position order.
func liveOrderTx(ctx context.Context, tx *sql.Tx) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM standup WHERE deleted_at IS NULL ORDER BY position, id`)
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, dbErr(err)
		}
		ids = append(ids, id)
	}
	return ids, dbErr(rows.Err())
}

// rewriteOrderTx gives the ids dense positions 0..n-1 in the given order.
func rewriteOrderTx(ctx context.Context, tx *sql.Tx, ids []int64) error {
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE standup SET position = ? WHERE id = ?`, i, id); err != nil {
			return dbErr(err)
		}
	}
	return nil
}

// withoutID returns ids minus id (a new slice).
func withoutID(ids []int64, id int64) []int64 {
	out := make([]int64, 0, len(ids))
	for _, v := range ids {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}

// validateStandupFields checks a patch against the standup it applies to (kanban rules need its board type).
func validateStandupFields(cur Standup, p Patch) error {
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if c := utf8.RuneCountInString(n); c < 1 || c > 80 {
			return apperr.Validationf("name", "The name must be 1 to 80 characters.")
		}
	}
	kanban := cur.BoardType == "kanban"
	if p.Scope != nil {
		switch *p.Scope {
		case "board":
		case "sprint":
			if kanban {
				return apperr.Validationf("scope", "A kanban board has no sprints; use the whole board.")
			}
		default:
			return apperr.Validationf("scope", "Scope must be a sprint or the whole board.")
		}
	}
	if p.ExtraFilter != nil && orderByRE.MatchString(*p.ExtraFilter) {
		return apperr.Validationf("extraFilter", "The extra filter cannot contain ORDER BY.")
	}
	if p.RowsMode != nil && *p.RowsMode != "fit" && *p.RowsMode != "roomy" {
		return apperr.Validationf("rowsMode", "Rows must be fit or roomy.")
	}
	if p.UnassignedPosition != nil {
		switch *p.UnassignedPosition {
		case "first", "last", "skip":
		default:
			return apperr.Validationf("unassignedPosition", "Unassigned must be first, last or skipped.")
		}
	}
	if p.PulseDays != nil {
		switch *p.PulseDays {
		case 7, 14, 21, 28:
		default:
			return apperr.Validationf("pulseDays", "The pulse must cover 7, 14, 21 or 28 days.")
		}
	}
	if p.DoneWindow != nil {
		switch *p.DoneWindow {
		case "14d", "yesterday":
		case "sprint":
			if kanban {
				return apperr.Validationf("doneWindow", "A kanban board has no sprint to finish in; use 14 days or yesterday.")
			}
		default:
			return apperr.Validationf("doneWindow", "Done must be sprint, 14 days or yesterday.")
		}
	}
	if p.BacklogRows != nil && (*p.BacklogRows < 0 || *p.BacklogRows > 50) {
		return apperr.Validationf("backlogRows", "The backlog must be 0 to 50 rows.")
	}
	return nil
}

// applyStandupPatch returns the standup and settings with p applied (p must already be validated).
func applyStandupPatch(st Standup, se Settings, p Patch) (Standup, Settings) {
	if p.Name != nil {
		st.Name = strings.TrimSpace(*p.Name)
	}
	if p.Scope != nil {
		st.Scope = *p.Scope
	}
	if p.ExtraFilter != nil {
		st.ExtraFilter = *p.ExtraFilter
	}
	if p.RowsMode != nil {
		se.RowsMode = *p.RowsMode
	}
	if p.UnassignedPosition != nil {
		se.UnassignedPosition = *p.UnassignedPosition
	}
	if p.ReshuffleDaily != nil {
		se.ReshuffleDaily = *p.ReshuffleDaily
	}
	if p.PulseDays != nil {
		se.PulseDays = *p.PulseDays
	}
	if p.ShowCommits != nil {
		se.ShowCommits = *p.ShowCommits
	}
	if p.ShowPRs != nil {
		se.ShowPRs = *p.ShowPRs
	}
	if p.ShowComments != nil {
		se.ShowComments = *p.ShowComments
	}
	if p.ShadeWeekends != nil {
		se.ShadeWeekends = *p.ShadeWeekends
	}
	if p.DoneWindow != nil {
		se.DoneWindow = *p.DoneWindow
	}
	if p.BacklogRows != nil {
		se.BacklogRows = *p.BacklogRows
	}
	return st, se
}
