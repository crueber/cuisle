package view

// Lanes as the UI sees them: the configured labels, kinds and colours of a standup's lanes (custom lanes included).
// TypeScript never invents lane data; everything here comes from the effective config.

import (
	"sort"
	"strconv"

	"github.com/crueber/cuisle/internal/model"
)

// columnsByLane groups board column names by lane in board column order.
func columnsByLane(cols []model.ColumnLane) map[string][]string {
	sorted := sortedColumns(cols)
	byLane := make(map[string][]string)
	for _, c := range sorted {
		byLane[c.Lane] = append(byLane[c.Lane], c.ColumnName)
	}
	return byLane
}

// laneInfos returns the working lanes (custom lanes included) in walk order, then done when withDone is set.
func laneInfos(cfg model.EffectiveConfig, withDone bool) []LaneInfo {
	walk := append([]model.LaneConfig(nil), cfg.Lanes...)
	sort.SliceStable(walk, func(i, j int) bool { return walk[i].Position < walk[j].Position })
	cols := columnsByLane(cfg.Columns)
	out := make([]LaneInfo, 0, len(walk)+1)
	for _, l := range walk {
		out = append(out, laneInfo(cfg, l.Lane, l.LimitDays, l.Hue, cols))
	}
	if withDone {
		out = append(out, laneInfo(cfg, model.LaneDone, 0, 0, cols))
	}
	return out
}

// laneInfo describes one lane of cfg.
func laneInfo(cfg model.EffectiveConfig, lane string, limit, hue int, colsByLane map[string][]string) LaneInfo {
	kind := LaneKindWorking
	switch lane {
	case model.LaneDone:
		kind = LaneKindDone
	case model.LaneBacklog:
		kind = LaneKindBacklog
	case model.LaneHidden:
		kind = LaneKindHidden
	}
	cols := colsByLane[lane]
	if cols == nil {
		cols = []string{}
	}
	return LaneInfo{
		Lane: lane, Label: cfg.LaneLabel(lane), DefaultLabel: model.DefaultLaneLabel(lane), Kind: kind,
		Custom: model.IsCustomLane(lane), Hue: hue, LimitDays: limit, Columns: cols,
	}
}

// laneOptions lists every lane a column can map to: backlog, the four built-in working lanes, the custom lanes in
// creation order, done, hidden.
func laneOptions(cfg model.EffectiveConfig) []LaneInfo {
	byLane := make(map[string]model.LaneConfig, len(cfg.Lanes))
	var custom []model.LaneConfig
	for _, l := range cfg.Lanes {
		byLane[l.Lane] = l
		if model.IsCustomLane(l.Lane) {
			custom = append(custom, l)
		}
	}
	sort.SliceStable(custom, func(i, j int) bool { return customSeq(custom[i].Lane) < customSeq(custom[j].Lane) })
	cols := columnsByLane(cfg.Columns)
	out := make([]LaneInfo, 0, len(cfg.Lanes)+3)
	out = append(out, laneInfo(cfg, model.LaneBacklog, 0, 0, cols))
	for _, id := range model.WorkingLanes {
		out = append(out, laneInfo(cfg, id, byLane[id].LimitDays, 0, cols))
	}
	for _, l := range custom {
		out = append(out, laneInfo(cfg, l.Lane, l.LimitDays, l.Hue, cols))
	}
	return append(out, laneInfo(cfg, model.LaneDone, 0, 0, cols), laneInfo(cfg, model.LaneHidden, 0, 0, cols))
}

// customSeq is the number of a custom lane id ("c12" → 12; 0 for anything else).
func customSeq(lane string) int {
	if !model.IsCustomLane(lane) {
		return 0
	}
	n, _ := strconv.Atoi(lane[len(model.CustomLanePrefix):])
	return n
}

// walkLeftToRight orders the working lanes by their first board column. Lanes with no column come after the mapped
// ones in built-in order (To Do, In Progress, In Review, QA, then custom lanes by creation).
func walkLeftToRight(cfg model.EffectiveConfig) []string {
	first := map[string]int{}
	for _, c := range cfg.Columns {
		if p, ok := first[c.Lane]; !ok || c.Position < p {
			first[c.Lane] = c.Position
		}
	}
	have := make(map[string]bool, len(cfg.Lanes))
	for _, l := range cfg.Lanes {
		have[l.Lane] = true
	}
	out := make([]string, 0, len(cfg.Lanes))
	for _, o := range laneOptions(cfg) {
		if o.Kind == LaneKindWorking && have[o.Lane] {
			out = append(out, o.Lane)
		}
	}
	key := func(l string) int {
		if p, ok := first[l]; ok {
			return p
		}
		return 1 << 30
	}
	sort.SliceStable(out, func(i, j int) bool { return key(out[i]) < key(out[j]) })
	return out
}

// drawableLanes maps each status to its lane. A lane the config no longer has (a deleted custom lane in an older
// mapping) is drawn as To Do, so its tickets stay on screen (never hide tickets).
func drawableLanes(cfg model.EffectiveConfig) map[string]string {
	out := make(map[string]string, len(cfg.StatusLanes))
	for _, r := range cfg.StatusLanes {
		lane := r.Lane
		if !cfg.HasLane(lane) {
			lane = model.LaneTodo
		}
		out[r.StatusID] = lane
	}
	return out
}
