package cache

import (
	"context"
	"database/sql"
	"time"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// MemberRef is one scanned member of a standup, resolved to its local issue id.
type MemberRef struct {
	IssueID   int64
	Role      string // board|backlog
	RankIndex int
}

type openSpan struct {
	role string
	from int64
}

// ApplyMembersTx rewrites standup_issue for the standup from the scanned members and maintains standup_issue_span
// (data-model.md §2.6), with T the run's started_at:
//   - a member without an open span opens one at T;
//   - a member whose open span has another role closes it (valid_to = T) and opens a new one at T;
//   - an open span whose issue is not scanned any more closes at T;
//   - issue.removed_at = T when the issue's last open span (any standup) closed, NULL when a span opened.
//
// Spans and issues are never deleted; only the derived standup_issue rows are rewritten. If two refs name the same
// issue the board role wins.
func ApplyMembersTx(ctx context.Context, tx *sql.Tx, standupID int64, members []MemberRef, t time.Time) error {
	desired := make(map[int64]MemberRef, len(members))
	var order []int64
	for _, m := range members {
		cur, dup := desired[m.IssueID]
		if !dup {
			order = append(order, m.IssueID)
		}
		if !dup || (m.Role == model.RoleBoard && cur.Role != model.RoleBoard) {
			desired[m.IssueID] = m
		}
	}

	open := map[int64]openSpan{}
	var openOrder []int64
	rows, err := tx.QueryContext(ctx,
		`SELECT issue_id, role, valid_from FROM standup_issue_span WHERE standup_id = ? AND valid_to IS NULL`, standupID)
	if err != nil {
		return dbErr(err, "The membership could not be read.")
	}
	for rows.Next() {
		var id int64
		var sp openSpan
		if err := rows.Scan(&id, &sp.role, &sp.from); err != nil {
			_ = rows.Close()
			return dbErr(err, "The membership could not be read.")
		}
		open[id] = sp
		openOrder = append(openOrder, id)
	}
	if err := rows.Close(); err != nil {
		return dbErr(err, "The membership could not be read.")
	}
	if err := rows.Err(); err != nil {
		return dbErr(err, "The membership could not be read.")
	}

	T := store.Ms(t)
	closeSpan := func(issueID int64, sp openSpan) (int64, error) {
		to := T
		if to <= sp.from { // a repeat apply at the same instant: keep valid_to > valid_from
			to = sp.from + 1
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE standup_issue_span SET valid_to = ?
			 WHERE standup_id = ? AND issue_id = ? AND role = ? AND valid_from = ? AND valid_to IS NULL`,
			to, standupID, issueID, sp.role, sp.from)
		return to, dbErr(err, "The membership could not be saved.")
	}
	openNew := func(issueID int64, role string, from int64) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO standup_issue_span (standup_id, issue_id, role, valid_from, valid_to) VALUES (?, ?, ?, ?, NULL)`,
			standupID, issueID, role, from)
		return dbErr(err, "The membership could not be saved.")
	}

	var left, opened []int64
	// Close first (the open-span unique index allows one open span per issue), then open.
	for _, id := range openOrder {
		sp := open[id]
		want, ok := desired[id]
		switch {
		case !ok:
			if _, err := closeSpan(id, sp); err != nil {
				return err
			}
			left = append(left, id)
		case want.Role != sp.role:
			to, err := closeSpan(id, sp)
			if err != nil {
				return err
			}
			if err := openNew(id, want.Role, to); err != nil {
				return err
			}
		}
	}
	for _, id := range order {
		if _, ok := open[id]; ok {
			continue
		}
		if err := openNew(id, desired[id].Role, T); err != nil {
			return err
		}
		opened = append(opened, id)
	}

	for _, id := range left {
		if _, err := tx.ExecContext(ctx,
			`UPDATE issue SET removed_at = ? WHERE id = ?
			 AND NOT EXISTS (SELECT 1 FROM standup_issue_span WHERE issue_id = ? AND valid_to IS NULL)`,
			T, id, id); err != nil {
			return dbErr(err, "The membership could not be saved.")
		}
	}
	for _, id := range opened {
		if _, err := tx.ExecContext(ctx, `UPDATE issue SET removed_at = NULL WHERE id = ?`, id); err != nil {
			return dbErr(err, "The membership could not be saved.")
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM standup_issue WHERE standup_id = ?`, standupID); err != nil {
		return dbErr(err, "The membership could not be saved.")
	}
	for _, id := range order {
		m := desired[id]
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO standup_issue (standup_id, issue_id, role, rank_index) VALUES (?, ?, ?, ?)`,
			standupID, id, m.Role, m.RankIndex); err != nil {
			return dbErr(err, "The membership could not be saved.")
		}
	}
	return nil
}

// BoardAssigneesTx lists the assignees of the standup's current board-role members in first-appearance (rank) order,
// with the display name from the person cache ("" when unknown). Unassigned issues are not listed.
func BoardAssigneesTx(ctx context.Context, tx *sql.Tx, connID, standupID int64) ([]model.Person, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT i.assignee_account_id, COALESCE(p.display_name, ''), COALESCE(p.active, 1), COALESCE(p.account_type, '')
		 FROM standup_issue si
		 JOIN issue i ON i.id = si.issue_id
		 LEFT JOIN person p ON p.connection_id = ? AND p.account_id = i.assignee_account_id
		 WHERE si.standup_id = ? AND si.role = 'board' AND i.assignee_account_id IS NOT NULL
		 GROUP BY i.assignee_account_id
		 ORDER BY MIN(si.rank_index), i.assignee_account_id`, connID, standupID)
	if err != nil {
		return nil, dbErr(err, "The speakers could not be read.")
	}
	defer rows.Close()
	var out []model.Person
	for rows.Next() {
		var p model.Person
		var active int
		if err := rows.Scan(&p.AccountID, &p.DisplayName, &active, &p.Kind); err != nil {
			return nil, dbErr(err, "The speakers could not be read.")
		}
		p.Active = active == 1
		out = append(out, p)
	}
	return out, dbErr(rows.Err(), "The speakers could not be read.")
}
