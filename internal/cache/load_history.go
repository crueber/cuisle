package cache

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// loadHistory returns the per-issue history of the standup's members, keyed by source id.
// Status and flag changes are never window-bounded (the prior state is needed). Comments, commits and PR events are
// bounded below by windowStart. Merge commits are kept (Merge=true); the pulse drops them. Events of issues that are
// not in sourceByLocal are ignored.
func loadHistory(ctx context.Context, q querier, standupID int64, windowStart time.Time, sourceByLocal map[int64]string) (map[string]model.IssueHistory, error) {
	byLocal := map[int64]*model.IssueHistory{}
	get := func(id int64) *model.IssueHistory {
		h, ok := byLocal[id]
		if !ok {
			h = &model.IssueHistory{}
			byLocal[id] = h
		}
		return h
	}
	ws := store.Ms(windowStart)

	if err := loadStatusChanges(ctx, q, standupID, sourceByLocal, get); err != nil {
		return nil, err
	}
	if err := loadFlagChanges(ctx, q, standupID, sourceByLocal, get); err != nil {
		return nil, err
	}
	if err := loadComments(ctx, q, standupID, ws, sourceByLocal, get); err != nil {
		return nil, err
	}
	devs, err := loadDevEvents(ctx, q, standupID, ws, sourceByLocal)
	if err != nil {
		return nil, err
	}
	for id, evs := range devs {
		get(id).Dev = evs
	}

	out := map[string]model.IssueHistory{}
	for id, h := range byLocal {
		out[sourceByLocal[id]] = *h
	}
	return out, nil
}

func loadStatusChanges(ctx context.Context, q querier, standupID int64, sourceByLocal map[int64]string, get func(int64) *model.IssueHistory) error {
	rows, err := q.QueryContext(ctx, `
		SELECT c.issue_id, c.history_id, c.changed_at, c.from_status_id, c.to_status_id
		  FROM status_change c JOIN standup_issue si ON si.issue_id = c.issue_id
		 WHERE si.standup_id = ?
		 ORDER BY c.issue_id, c.changed_at, c.history_id`, standupID)
	if err != nil {
		return dbErr(err, "The status history could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var issue, changed int64
		var c model.StatusChange
		if err := rows.Scan(&issue, &c.ID, &changed, &c.From, &c.To); err != nil {
			return dbErr(err, "The status history could not be read.")
		}
		if _, ok := sourceByLocal[issue]; !ok {
			continue
		}
		c.At = store.FromMs(changed)
		h := get(issue)
		h.StatusChanges = append(h.StatusChanges, c)
	}
	return dbErr(rows.Err(), "The status history could not be read.")
}

func loadFlagChanges(ctx context.Context, q querier, standupID int64, sourceByLocal map[int64]string, get func(int64) *model.IssueHistory) error {
	rows, err := q.QueryContext(ctx, `
		SELECT f.issue_id, f.history_id, f.changed_at, f.flagged
		  FROM flag_change f JOIN standup_issue si ON si.issue_id = f.issue_id
		 WHERE si.standup_id = ?
		 ORDER BY f.issue_id, f.changed_at, f.history_id`, standupID)
	if err != nil {
		return dbErr(err, "The flag history could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var issue, changed int64
		var flagged int
		var c model.FlagChange
		if err := rows.Scan(&issue, &c.ID, &changed, &flagged); err != nil {
			return dbErr(err, "The flag history could not be read.")
		}
		if _, ok := sourceByLocal[issue]; !ok {
			continue
		}
		c.At = store.FromMs(changed)
		c.Flagged = flagged != 0
		h := get(issue)
		h.FlagChanges = append(h.FlagChanges, c)
	}
	return dbErr(rows.Err(), "The flag history could not be read.")
}

func loadComments(ctx context.Context, q querier, standupID, windowStart int64, sourceByLocal map[int64]string, get func(int64) *model.IssueHistory) error {
	rows, err := q.QueryContext(ctx, `
		SELECT c.issue_id, c.comment_id, COALESCE(c.author_account_id, ''), c.created_at
		  FROM comment c JOIN standup_issue si ON si.issue_id = c.issue_id
		 WHERE si.standup_id = ? AND c.created_at >= ?
		 ORDER BY c.issue_id, c.created_at, c.comment_id`, standupID, windowStart)
	if err != nil {
		return dbErr(err, "The comments could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var issue, created int64
		var cm model.Comment
		if err := rows.Scan(&issue, &cm.ID, &cm.AuthorID, &created); err != nil {
			return dbErr(err, "The comments could not be read.")
		}
		if _, ok := sourceByLocal[issue]; !ok {
			continue
		}
		cm.At = store.FromMs(created)
		h := get(issue)
		h.Comments = append(h.Comments, cm)
	}
	return dbErr(rows.Err(), "The comments could not be read.")
}

// loadDevEvents returns commits and PR events per issue, oldest first across both kinds.
func loadDevEvents(ctx context.Context, q querier, standupID, windowStart int64, sourceByLocal map[int64]string) (map[int64][]model.DevEvent, error) {
	out := map[int64][]model.DevEvent{}

	rows, err := q.QueryContext(ctx, `
		SELECT d.issue_id, d.commit_id, d.repo, d.authored_at, d.is_merge
		  FROM dev_commit d JOIN standup_issue si ON si.issue_id = d.issue_id
		 WHERE si.standup_id = ? AND d.authored_at >= ?
		 ORDER BY d.issue_id, d.authored_at, d.commit_id`, standupID, windowStart)
	if err != nil {
		return nil, dbErr(err, "The commits could not be read.")
	}
	for rows.Next() {
		var issue, at int64
		var merge int
		var e model.DevEvent
		if err := rows.Scan(&issue, &e.ID, &e.Repo, &at, &merge); err != nil {
			_ = rows.Close()
			return nil, dbErr(err, "The commits could not be read.")
		}
		if _, ok := sourceByLocal[issue]; !ok {
			continue
		}
		e.Kind = model.DevCommit
		e.At = store.FromMs(at)
		e.Merge = merge != 0
		out[issue] = append(out[issue], e)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, dbErr(err, "The commits could not be read.")
	}
	_ = rows.Close()

	rows, err = q.QueryContext(ctx, `
		SELECT e.issue_id, e.pr_key, e.status, e.at
		  FROM dev_pr_event e JOIN standup_issue si ON si.issue_id = e.issue_id
		 WHERE si.standup_id = ? AND e.at >= ?
		 ORDER BY e.issue_id, e.at, e.pr_key, e.status`, standupID, windowStart)
	if err != nil {
		return nil, dbErr(err, "The pull requests could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var issue, at int64
		var e model.DevEvent
		if err := rows.Scan(&issue, &e.ID, &e.Status, &at); err != nil {
			return nil, dbErr(err, "The pull requests could not be read.")
		}
		if _, ok := sourceByLocal[issue]; !ok {
			continue
		}
		e.Kind = model.DevPR
		e.At = store.FromMs(at)
		if i := strings.LastIndex(e.ID, "#"); i >= 0 {
			e.Repo = e.ID[:i]
		}
		out[issue] = append(out[issue], e)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err, "The pull requests could not be read.")
	}

	for id := range out {
		evs := out[id]
		sort.SliceStable(evs, func(i, j int) bool { return evs[i].At.Before(evs[j].At) })
	}
	return out, nil
}
