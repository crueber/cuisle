package sync

import (
	"context"
	"sort"
	"strconv"
	gosync "sync"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source"
)

// configPhase is phase 1: board configuration, active sprints and (full) reference data, in parallel; then the in-run
// promotions (column set or sprint ids changed) and the in-memory lane map the queries are built from.
func (rn *runner) configPhase(ctx context.Context, f *Fetched) (map[string]string, error) {
	rn.prog.report(PhaseConfig, 0, 1)

	var (
		board    model.BoardConfig
		sprints  []model.Sprint
		statuses []model.Status
		types    []model.IssueType
	)
	fns := []func(context.Context) error{
		func(ctx context.Context) error {
			var err error
			board, err = rn.src.BoardConfig(ctx, rn.cfg.BoardID)
			return err
		},
	}
	if rn.hasSprints() {
		fns = append(fns, func(ctx context.Context) error {
			var err error
			sprints, err = rn.src.ActiveSprints(ctx, rn.cfg.BoardID)
			return err
		})
	}
	if rn.mode == ModeFull {
		fns = append(fns, func(ctx context.Context) error {
			var err error
			statuses, types, err = rn.src.Reference(ctx)
			return err
		})
	}
	if err := parallel(ctx, rn.logPanic, fns...); err != nil {
		return nil, err
	}

	f.Board = board
	f.Sprints = sprints
	f.BoardChanged = boardSetChanged(rn.cfg, board)

	// Promotion found during the run: everything is hydrated and reference data is refreshed (statuses and types via
	// Reference below). The adapter's field-id discovery is tied to Full at open time and is not repeated here: a
	// board or sprint change does not move the flagged/sprint field ids, and the next full sync re-discovers them.
	if rn.mode != ModeFull && (f.BoardChanged || sprintIDsChanged(rn.cfg.ActiveSprintIDs, sprints)) {
		rn.promote(ctx, ModeFull)
		var err error
		statuses, types, err = rn.src.Reference(ctx)
		if err != nil {
			return nil, err
		}
	}
	f.Statuses, f.Types = statuses, types

	lanes := cachedLanes(rn.cfg)
	if f.BoardChanged {
		lanes = newLanes(rn.cfg, board, statuses)
	}
	rn.prog.report(PhaseConfig, 1, 1)
	rn.logf(ctx, "info", PhaseConfig, "board configuration read")
	return lanes, nil
}

func (rn *runner) hasSprints() bool {
	return rn.src.Capabilities().HasSprints && rn.cfg.BoardType != "kanban"
}

// boardSetChanged reports whether the board's column→status set differs from the cached one (names, order, statuses).
func boardSetChanged(cfg model.EffectiveConfig, board model.BoardConfig) bool {
	cached := make([]model.ColumnLane, len(cfg.Columns))
	copy(cached, cfg.Columns)
	sort.SliceStable(cached, func(i, j int) bool { return cached[i].Position < cached[j].Position })
	if len(cached) != len(board.Columns) {
		return true
	}
	cols := make([]model.Column, len(board.Columns))
	copy(cols, board.Columns)
	sort.SliceStable(cols, func(i, j int) bool { return cols[i].Position < cols[j].Position })
	have := map[string]bool{}
	for _, sl := range cfg.StatusLanes {
		have[sl.ColumnName+"\x00"+sl.StatusID] = true
	}
	n := 0
	for i, c := range cols {
		if cached[i].ColumnName != c.Name {
			return true
		}
		for _, id := range c.StatusIDs {
			if !have[c.Name+"\x00"+id] {
				return true
			}
			n++
		}
	}
	return n != len(have)
}

func sprintIDsChanged(cached []string, sprints []model.Sprint) bool {
	var now []string
	for _, s := range sprints {
		if s.State == "active" {
			now = append(now, s.ID)
		}
	}
	old := append([]string(nil), cached...)
	sort.Strings(now)
	sort.Strings(old)
	if len(now) != len(old) {
		return true
	}
	for i := range now {
		if now[i] != old[i] {
			return true
		}
	}
	return false
}

// cachedLanes is the status id → lane map of the effective config.
func cachedLanes(cfg model.EffectiveConfig) map[string]string {
	out := make(map[string]string, len(cfg.StatusLanes))
	for _, sl := range cfg.StatusLanes {
		out[sl.StatusID] = sl.Lane
	}
	return out
}

// newLanes recomputes the lanes for a changed board the way settings.MergeColumnsTx will: columns the user mapped
// by hand keep their lane, the others are auto-mapped.
func newLanes(cfg model.EffectiveConfig, board model.BoardConfig, statuses []model.Status) map[string]string {
	byID := make(map[string]model.Status, len(statuses))
	for _, s := range statuses {
		byID[s.ID] = s
	}
	manual := map[string]string{}
	for _, c := range cfg.Columns {
		if !c.Auto {
			manual[c.ColumnName] = c.Lane
		}
	}
	cols := make([]model.Column, len(board.Columns))
	copy(cols, board.Columns)
	sort.SliceStable(cols, func(i, j int) bool { return cols[i].Position < cols[j].Position })
	out := map[string]string{}
	for i, c := range cols {
		lane, ok := manual[c.Name]
		if !ok {
			lane = settings.AutoLane(c, i, len(cols), byID)
		}
		for _, id := range c.StatusIDs {
			out[id] = lane
		}
	}
	return out
}

// scanPhase is phase 2: build the queries, scan membership, and fill f.Members (board role wins over backlog).
func (rn *runner) scanPhase(ctx context.Context, f *Fetched, lanes map[string]string) ([]Member, error) {
	queries, err := rn.src.Plan(ctx, source.PlanInput{
		Board: f.Board, Scope: rn.cfg.Scope, ExtraFilter: rn.cfg.ExtraFilter, ActiveSprints: f.Sprints,
		StatusLanes: lanes, BacklogLimit: rn.cfg.BacklogRows,
	})
	if err != nil {
		return nil, err
	}

	rn.prog.report(PhaseScan, 0, len(queries))
	results := make([]source.ScanResult, len(queries))
	var mu gosync.Mutex
	done := 0
	fns := make([]func(context.Context) error, len(queries))
	for i, q := range queries {
		fns[i] = func(ctx context.Context) error {
			res, err := rn.src.Scan(ctx, q)
			if err != nil {
				return err
			}
			results[i] = res
			mu.Lock()
			done++
			d := done
			mu.Unlock()
			rn.prog.report(PhaseScan, d, len(queries))
			return nil
		}
	}
	if err := parallel(ctx, rn.logPanic, fns...); err != nil {
		return nil, err
	}

	members := make([]Member, 0)
	seen := map[string]bool{}
	rn.devFP = map[string]string{}
	f.BacklogTotal = -1
	for _, role := range []string{model.RoleBoard, model.RoleBacklog} {
		rank := 0
		for i, q := range queries {
			if q.Role != role {
				continue
			}
			if role == model.RoleBacklog && q.Count && results[i].ApproxTotal >= 0 && f.BacklogTotal < 0 {
				f.BacklogTotal = results[i].ApproxTotal
			}
			for _, it := range results[i].Items {
				if seen[it.SourceID] {
					continue
				}
				seen[it.SourceID] = true
				if it.DevFingerprint != "" && role == model.RoleBoard {
					rn.devFP[it.SourceID] = it.DevFingerprint
				}
				members = append(members, Member{
					SourceID: it.SourceID, Key: it.Key, Role: role, RankIndex: rank, UpdatedAt: it.UpdatedAt,
				})
				rank++
			}
		}
	}
	f.Members = members
	rn.scanned = len(members)
	rn.logf(ctx, "info", PhaseScan, "scanned "+strconv.Itoa(len(members))+" issues")
	return members, nil
}

// diffMembers is the scan diff: the ids to hydrate. Full runs hydrate everything; incremental runs hydrate new ids and
// ids whose updated stamp differs from the cache.
func (rn *runner) diffMembers(ctx context.Context, members []Member) ([]string, map[string]cache.IssueStamp, error) {
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.SourceID
	}
	stamps, err := cache.IssueStamps(ctx, rn.o.opts.DB.R, rn.conn.ID, ids)
	if err != nil {
		return nil, nil, err
	}
	if rn.mode == ModeFull {
		return ids, stamps, nil
	}
	var out []string
	for _, m := range members {
		st, ok := stamps[m.SourceID]
		if !ok || st.UpdatedAt.UnixMilli() != m.UpdatedAt.UnixMilli() {
			out = append(out, m.SourceID)
		}
	}
	return out, stamps, nil
}
