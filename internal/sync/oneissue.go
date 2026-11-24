package sync

import (
	"context"
	"database/sql"
	"time"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/settings"
)

// ApplyIssue is the one-issue apply after a light write or a ticket Refresh (jira-sync.md §4.8): one BEGIN IMMEDIATE
// with the same upsert code as a sync apply for that issue (issue row unless the stored row is newer, merged change
// tables, comments, content tables, attachment rows), drops its transition_cache row when the status changed, and
// bumps rev of every standup that has it as a current member. Membership, spans, config history and sync state are not
// touched. It emits nothing; the caller emits ticket:updated and data:changed.
//
// The references the refresh brought (status, type, people, sprints) are saved with it, so a new comment author or a
// new assignee resolves in the view. A skipped issue row (the stored one was newer) still bumps the standups: the
// merged change and comment rows may have changed what they show.
func (o *Orchestrator) ApplyIssue(ctx context.Context, connectionID int64, d model.IssueDetail) (IssueApplied, error) {
	var out IssueApplied
	err := o.opts.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		now := o.opts.Clock.Now()
		var statuses []model.Status
		if d.Status.ID != "" {
			statuses = append(statuses, d.Status)
		}
		var types []model.IssueType
		if d.Type.ID != "" {
			types = append(types, d.Type)
		}
		people := append([]model.Person(nil), d.People...)
		if d.Assignee != nil {
			people = append(people, *d.Assignee)
		}
		var sprints []model.Sprint
		for _, s := range d.Sprints {
			if s.BoardID != "" {
				sprints = append(sprints, s)
			}
		}
		if err := cache.ApplyStatusesTx(ctx, tx, connectionID, statuses); err != nil {
			return err
		}
		if err := cache.ApplyIssueTypesTx(ctx, tx, connectionID, types); err != nil {
			return err
		}
		if err := cache.ApplyPeopleTx(ctx, tx, connectionID, people, now); err != nil {
			return err
		}
		if err := cache.ApplySprintsTx(ctx, tx, connectionID, "", sprints, now); err != nil {
			return err
		}
		res, err := cache.ApplyIssueDetailTx(ctx, tx, connectionID, d, now)
		if err != nil {
			return err
		}
		if res.Status {
			if err := cache.DropTransitionCacheTx(ctx, tx, res.IssueID); err != nil {
				return err
			}
		}
		standups, err := bumpStandups(ctx, tx, res.IssueID, now)
		if err != nil {
			return err
		}
		out = IssueApplied{
			IssueID: res.IssueID, Key: d.Key, StandupIDs: standups, Changed: changedNames(res),
			StatusChanged: res.Status, Skipped: res.Skipped,
		}
		return nil
	})
	if err != nil {
		return IssueApplied{}, err
	}
	return out, nil
}

// ApplyIssueDelta applies the known effect of a successful write to the issue row only, when the follow-up refresh
// failed (jira-sync.md §4.8), and bumps rev of the standups containing it. The next sync merges the real changelog. No
// change-table row is invented and updated_at is left alone, so the next sync sees the issue as changed and hydrates it.
func (o *Orchestrator) ApplyIssueDelta(ctx context.Context, issueID int64, d IssueDelta) (IssueApplied, error) {
	var out IssueApplied
	err := o.opts.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		key, err := cache.IssueKeyTx(ctx, tx, issueID)
		if err != nil {
			return err
		}
		res, err := cache.ApplyIssueDeltaTx(ctx, tx, issueID, d.StatusID, d.AssigneeID, d.Flagged)
		if err != nil {
			return err
		}
		out = IssueApplied{IssueID: issueID, Key: key, Changed: changedNames(res), StatusChanged: res.Status, StandupIDs: []int64{}}
		if len(out.Changed) == 0 {
			return nil
		}
		if res.Status {
			if err := cache.DropTransitionCacheTx(ctx, tx, issueID); err != nil {
				return err
			}
		}
		out.StandupIDs, err = bumpStandups(ctx, tx, issueID, o.opts.Clock.Now())
		return err
	})
	if err != nil {
		return IssueApplied{}, err
	}
	return out, nil
}

// bumpStandups raises rev of every live standup that has the issue as a current member and returns their ids.
func bumpStandups(ctx context.Context, tx *sql.Tx, issueID int64, now time.Time) ([]int64, error) {
	ids, err := cache.LiveStandupIDsTx(ctx, tx, issueID)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := settings.BumpRevTx(ctx, tx, id, now); err != nil {
			return nil, err
		}
	}
	return ids, nil
}
