package sync

import (
	"context"
	"database/sql"
	"log/slog"
	"sort"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/settings"
)

// apply performs jira-sync.md §4.6 in ONE store.WriteTx: reference upserts and connection meta, column-map merge and
// status-lane rebuild (settings.MergeColumnsTx), issue/change/comment/dev upserts (never delete), membership + spans +
// removed_at, speaker merge (settings.MergeSpeakersTx), config history by fingerprint, sync state, rev bump. WP-30.
// Any error rolls back everything, so a reader sees the old state or the new one in full. T is f.StartedAt.
func (o *Orchestrator) apply(ctx context.Context, f *Fetched) (ApplyResult, error) {
	res, _, err := o.applyWith(ctx, f, nil)
	return res, err
}

// applyExtras carries what the frozen Fetched has no slot for (WP-T6): the field registry in, and the per-issue changes
// out (ticket:updated, emitted by the runner after the commit).
type applyExtras struct {
	FieldDefs []model.FieldDef // stored with the references (field_def)
}

// applyWith is apply plus the extras. The returned updates name every hydrated issue that changed, with the standups
// that have it as a current member (read after the membership step, inside the transaction).
func (o *Orchestrator) applyWith(ctx context.Context, f *Fetched, ex *applyExtras) (ApplyResult, []TicketUpdated, error) {
	var res ApplyResult
	var updates []TicketUpdated
	err := o.opts.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		res, err = o.applyTx(ctx, tx, f, ex, &updates)
		return err
	})
	if err != nil {
		return ApplyResult{}, nil, err
	}
	return res, updates, nil
}

func (o *Orchestrator) applyTx(ctx context.Context, tx *sql.Tx, f *Fetched, ex *applyExtras, updates *[]TicketUpdated) (ApplyResult, error) {
	T, connID, sid := f.StartedAt, f.ConnectionID, f.StandupID
	var res ApplyResult

	if err := cache.RequireLiveStandupTx(ctx, tx, sid); err != nil {
		return res, err
	}

	// 1. Reference rows and connection meta. Per-issue references first so the run-level lists win.
	statuses, types, people, sprints := collectReferences(f)
	if err := cache.ApplyStatusesTx(ctx, tx, connID, statuses); err != nil {
		return res, err
	}
	if err := cache.ApplyIssueTypesTx(ctx, tx, connID, types); err != nil {
		return res, err
	}
	if err := cache.ApplyPeopleTx(ctx, tx, connID, people, T); err != nil {
		return res, err
	}
	if err := cache.ApplySprintsTx(ctx, tx, connID, f.Board.Board.ID, sprints, T); err != nil {
		return res, err
	}
	if err := cache.ApplyConnectionTx(ctx, tx, connID, f.SourceMeta, f.DevState, T); err != nil {
		return res, err
	}
	if ex != nil && len(ex.FieldDefs) > 0 {
		if err := cache.ApplyFieldDefsTx(ctx, tx, connID, ex.FieldDefs, T); err != nil {
			return res, err
		}
	}

	// 2. Board snapshot, column map merge, status-lane rebuild.
	columnsChanged := false
	if f.Board.Board.ID != "" {
		if err := cache.ApplyBoardSnapshotTx(ctx, tx, sid, f.Board.Board); err != nil {
			return res, err
		}
		if len(f.Board.Columns) > 0 {
			if err := settings.ReplaceBoardColumnsTx(ctx, tx, connID, f.Board); err != nil {
				return res, err
			}
			byID := make(map[string]model.Status, len(statuses))
			for _, s := range statuses {
				byID[s.ID] = s
			}
			changed, err := settings.MergeColumnsTx(ctx, tx, sid, f.Board.Columns, byID)
			if err != nil {
				return res, err
			}
			columnsChanged = changed
		}
	}

	// 3. Issues and their change/comment rows.
	var changed []issueChange
	for _, d := range f.Details {
		r, err := cache.ApplyIssueDetailTx(ctx, tx, connID, d, T)
		if err != nil {
			return res, err
		}
		if names := announcedNames(r); len(names) > 0 {
			changed = append(changed, issueChange{issueID: r.IssueID, key: d.Key, changed: names})
		}
	}
	res.Hydrated = len(f.Details)
	needSet := make(map[string]struct{}, len(f.Members)+len(f.Dev)+len(f.DevChecked))
	for _, m := range f.Members {
		needSet[m.SourceID] = struct{}{}
	}
	for _, src := range f.DevChecked {
		needSet[src] = struct{}{}
	}
	for src := range f.Dev {
		needSet[src] = struct{}{}
	}
	needed := make([]string, 0, len(needSet))
	for src := range needSet {
		needed = append(needed, src)
	}
	ids, err := cache.IssueIDsForTx(ctx, tx, connID, needed)
	if err != nil {
		return res, err
	}
	refs := make([]cache.MemberRef, 0, len(f.Members))
	seen := make([]int64, 0, len(f.Members))
	for _, m := range f.Members {
		id, ok := ids[m.SourceID]
		if !ok {
			o.logger().Warn("sync apply: scanned member has no cached issue row, skipped", "key", m.Key, "standup", sid)
			continue
		}
		refs = append(refs, cache.MemberRef{IssueID: id, Role: m.Role, RankIndex: m.RankIndex})
		seen = append(seen, id)
	}
	if err := cache.MarkSeenTx(ctx, tx, seen, T); err != nil {
		return res, err
	}

	// 4. Dev activity.
	checked := map[string]bool{}
	for _, src := range f.DevChecked {
		checked[src] = true
	}
	devIDs := make([]string, 0, len(f.Dev))
	for src := range f.Dev {
		checked[src] = true
		devIDs = append(devIDs, src)
	}
	sort.Strings(devIDs)
	for _, src := range devIDs {
		id, ok := ids[src]
		if !ok {
			continue
		}
		if err := cache.ApplyDevTx(ctx, tx, id, f.Dev[src]); err != nil {
			return res, err
		}
	}
	var checkedIDs []int64
	for src := range checked {
		if id, ok := ids[src]; ok {
			checkedIDs = append(checkedIDs, id)
		}
	}
	if err := cache.MarkDevCheckedTx(ctx, tx, checkedIDs, T); err != nil {
		return res, err
	}

	// 5. Membership, spans, removed_at.
	if err := cache.ApplyMembersTx(ctx, tx, sid, refs, T); err != nil {
		return res, err
	}

	// ticket:updated {cause: sync} for every hydrated issue that changed, with the standups that now contain it.
	if updates != nil {
		for _, c := range changed {
			standups, err := cache.LiveStandupIDsTx(ctx, tx, c.issueID)
			if err != nil {
				return res, err
			}
			*updates = append(*updates, TicketUpdated{IssueID: c.issueID, Key: c.key, StandupIDs: standups, Cause: CauseSync, Changed: c.changed})
		}
	}

	// 6. Speakers: assignees of board-role members only (from the cache, so unhydrated members count too).
	assignees, err := cache.BoardAssigneesTx(ctx, tx, connID, sid)
	if err != nil {
		return res, err
	}
	speakersChanged, err := settings.MergeSpeakersTx(ctx, tx, sid, assignees, T)
	if err != nil {
		return res, err
	}
	res.ConfigChanged = columnsChanged || speakersChanged

	// 8. Sync state.
	active := activeSprintIDs(f.Sprints)
	if err := cache.WriteSyncStateTx(ctx, tx, sid, T, f.Mode == ModeFull, f.BacklogTotal, active); err != nil {
		return res, err
	}

	// 7. Config history, by fingerprint of the effective config as of this apply.
	cfg, err := settings.EffectiveTx(ctx, tx, sid)
	if err != nil {
		return res, err
	}
	cfg.ActiveSprintIDs = active
	doc, fp := cache.ConfigJSON(cfg)
	if _, err := cache.RecordConfigTx(ctx, tx, sid, doc, fp, T); err != nil {
		return res, err
	}

	// 9. Rev bump.
	now := T
	if o.opts.Clock != nil {
		now = o.opts.Clock.Now()
	}
	rev, err := settings.BumpRevTx(ctx, tx, sid, now)
	if err != nil {
		return res, err
	}
	res.Rev = rev
	return res, nil
}

func (o *Orchestrator) logger() *slog.Logger {
	if o.opts.Logger != nil {
		return o.opts.Logger
	}
	return slog.Default()
}

// collectReferences gathers the reference rows to upsert: the ones carried by each hydrated issue first, then the
// run-level lists, so on a clash the run-level value wins (later upserts overwrite earlier ones).
func collectReferences(f *Fetched) (statuses []model.Status, types []model.IssueType, people []model.Person, sprints []model.Sprint) {
	for _, d := range f.Details {
		if d.Status.ID != "" {
			statuses = append(statuses, d.Status)
		}
		if d.Type.ID != "" {
			types = append(types, d.Type)
		}
		if d.Assignee != nil {
			people = append(people, *d.Assignee)
		}
		people = append(people, d.People...)
		sprints = append(sprints, d.Sprints...)
	}
	statuses = append(statuses, f.Statuses...)
	types = append(types, f.Types...)
	people = append(people, f.People...)
	sprints = append(sprints, f.Sprints...)
	return
}

// activeSprintIDs is the sorted list of ids of the active sprints among sprints (never nil).
func activeSprintIDs(sprints []model.Sprint) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range sprints {
		if s.State == "active" && !seen[s.ID] {
			seen[s.ID] = true
			out = append(out, s.ID)
		}
	}
	sort.Strings(out)
	return out
}
