package view

import (
	"slices"
	"sort"
	"time"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/pulse"
)

// Build turns a snapshot into the StandupView as of asOf (architecture.md §3.2, jira-sync.md §5). Pure: no I/O, no
// time.Now(). Sync is left zero (the app fills it). Lists are never nil.
func Build(snap model.BoardSnapshot, asOf time.Time) StandupView {
	cfg := snap.Config
	today := asOf.Format("2006-01-02")
	win := pulse.NewWindow(asOf, cfg.PulseDays)

	statusLanes := drawableLanes(cfg)
	blockedStatuses := map[string]bool{}
	for id, st := range snap.Statuses {
		if st.BlockedHint {
			blockedStatuses[id] = true
		}
	}
	limits := map[string]int{}
	for _, l := range cfg.Lanes {
		limits[l.Lane] = l.LimitDays
	}

	views, preview := buildTickets(snap, asOf, win, statusLanes, blockedStatuses, limits)
	sortByColumnOrder(views, snap)

	var counts Counts
	activeBy := map[string]int{}
	doneBy := map[string]int{}
	withTickets := map[string]bool{}
	hasUnassigned := false
	for i := range views {
		tv := &views[i]
		if tv.Lane == model.LaneDone {
			counts.Done++
			doneBy[tv.Assignee]++
		} else {
			counts.Active++
			activeBy[tv.Assignee]++
			if tv.Assignee == "" {
				counts.Unassigned++
			}
		}
		if tv.Assignee == "" {
			hasUnassigned = true
		} else {
			withTickets[tv.Assignee] = true
		}
	}

	seed := pulse.DailySeed(cfg.StandupID, today)
	if cfg.ShuffleDate == today {
		seed = cfg.ShuffleSeed
	}
	slots := pulse.SpeakerOrder(pulse.SpeakerInput{
		Speakers:           cfg.Speakers,
		ReshuffleDaily:     cfg.ReshuffleDaily,
		Seed:               seed,
		UnassignedPosition: cfg.UnassignedPosition,
		HasUnassigned:      hasUnassigned,
		WithTickets:        withTickets,
	})

	backlog := buildBacklog(snap)
	counts.Backlog = len(backlog)

	warnings := append([]string{}, snap.Warnings...)

	return StandupView{
		StandupID:    snap.StandupID,
		Rev:          cfg.Rev,
		GeneratedAt:  asOf.UnixMilli(),
		Today:        today,
		AsOf:         asOf.UnixMilli(),
		Live:         snap.Live,
		Name:         cfg.Name,
		SpaceKey:     cfg.SpaceKey,
		BoardName:    cfg.BoardName,
		BoardType:    cfg.BoardType,
		Scope:        cfg.Scope,
		Sprint:       activeSprint(cfg, snap.Sprints, asOf),
		PulseDays:    cfg.PulseDays,
		Axis:         win.Axis(),
		Lanes:        laneInfos(cfg, true),
		Speakers:     speakerViews(cfg, slots, activeBy, doneBy),
		Tickets:      views,
		Backlog:      backlog,
		BacklogTotal: snap.BacklogTotal,
		Counts:       counts,
		Options: ViewOptions{
			RowsMode:      cfg.RowsMode,
			ShowCommits:   cfg.ShowCommits,
			ShowPRs:       cfg.ShowPRs,
			ShowComments:  cfg.ShowComments,
			ShadeWeekends: cfg.ShadeWeekends,
			DoneWindow:    cfg.DoneWindow,
			DoneLabel:     pulse.DoneLabel(cfg.DoneWindow, asOf),
			DevActivity:   snap.DevActivity,
		},
		Warnings:     warnings,
		PreviewKey:   preview,
		LastSyncedAt: millisOrZero(snap.LastSyncedAt),
	}
}

// rowContext holds snapshot-level lookups shared by board rows (build.go) and modal header rows (ticket_row.go).
type rowContext struct {
	cfg         model.EffectiveConfig
	urlPrefix   string
	asOf        time.Time
	win         pulse.Window
	statusLanes map[string]string
	blocked     map[string]bool
	limits      map[string]int
	people      map[string]model.Person
}

type rowItem struct {
	localID    int64
	issue      model.Issue
	typeKind   string
	statusName string
	lane       string
	history    model.IssueHistory
}

// buildRow computes one TicketView plus its total mark count and blocked days over rc.win. When filterDone is true,
// done tickets outside the standup's done window return ok=false before computing marks/spans/labels.
func (rc *rowContext) buildRow(it rowItem, filterDone bool) (tv TicketView, markTotal int, blockedDays int, ok bool) {
	is, h := it.issue, it.history
	pt := pulse.Ticket{
		CreatedAt:     is.CreatedAt,
		UpdatedAt:     is.UpdatedAt,
		StatusID:      is.StatusID,
		Flagged:       is.Flagged,
		StatusChanges: h.StatusChanges,
		FlagChanges:   h.FlagChanges,
		Comments:      h.Comments,
		Dev:           h.Dev,
	}
	tl := pulse.Timeline(pt, rc.statusLanes, rc.asOf)
	entered := pulse.EnteredLane(tl)
	if filterDone && it.lane == model.LaneDone && !doneInWindow(rc.cfg, rc.asOf, entered, is.SprintIDs) {
		return TicketView{}, 0, 0, false
	}

	days := pulse.DaysInLane(entered, rc.asOf)
	marks := pulse.Marks(pt, rc.win, rc.people)
	spans := pulse.BlockedSpans(pt, tl, rc.blocked, rc.win, rc.asOf)
	segs := pulse.Segments(tl, rc.win)
	if segs == nil {
		segs = []pulse.Seg{}
	}
	if marks == nil {
		marks = []pulse.Mark{}
	}
	if spans == nil {
		spans = []pulse.Span{}
	}

	commits, prs, comments := pulse.Totals(marks)
	prevLabel, prevDays := "", 0
	if n := len(segs); n >= 2 {
		prevLabel = rc.cfg.LaneLabel(segs[n-2].Lane)
		prevDays = segs[n-2].To - segs[n-2].From
	}
	blockedDays = pulse.BlockedDays(spans)
	label := pulse.Label(pulse.LabelInput{
		LaneLabel:     rc.cfg.LaneLabel(it.lane),
		DaysInLane:    days,
		PrevLaneLabel: prevLabel,
		PrevDays:      prevDays,
		Commits:       commits,
		PRs:           prs,
		Comments:      comments,
		Days:          rc.win.Days,
		BlockedDays:   blockedDays,
		Done:          it.lane == model.LaneDone,
	})

	var doneAt int64
	if it.lane == model.LaneDone {
		doneAt = entered.UnixMilli()
	}
	tv = TicketView{
		ID:         it.localID,
		SourceID:   is.SourceID,
		Key:        is.Key,
		Summary:    is.Summary,
		URL:        rc.urlPrefix + is.Key,
		Type:       it.typeKind,
		Assignee:   is.AssigneeID,
		Lane:       it.lane,
		StatusName: it.statusName,
		DaysInLane: days,
		OverLimit:  pulse.OverLimit(it.lane, days, rc.limits[it.lane]),
		Blocked:    pulse.BlockedNow(is.Flagged, rc.blocked[is.StatusID]),
		DoneAt:     doneAt,
		Pulse: Pulse{
			Segs:    segs,
			Marks:   marks,
			Blocked: spans,
			Label:   label,
		},
	}
	return tv, commits + prs + comments, blockedDays, true
}

// buildTickets returns the visible board tickets in rank order: working lanes, plus done tickets inside the done window.
// Members whose status maps to no lane, or to a hidden or backlog lane, are not shown. It also returns the
// PreviewKey: the ticket with the most marks (commits + PRs + comments); ties go to a blocked ticket, then to the
// earlier rank. The slice is sized once for every member and returned as is (never nil).
func buildTickets(snap model.BoardSnapshot, asOf time.Time, win pulse.Window, statusLanes map[string]string, blockedStatuses map[string]bool, limits map[string]int) ([]TicketView, string) {
	rc := rowContext{
		cfg: snap.Config, urlPrefix: snap.IssueURLPrefix, asOf: asOf, win: win,
		statusLanes: statusLanes, blocked: blockedStatuses, limits: limits, people: snap.People,
	}
	out := make([]TicketView, 0, len(snap.Members))
	best, bestMarks := -1, 0
	for _, m := range snap.Members {
		if m.Role != model.RoleBoard {
			continue
		}
		lane, ok := statusLanes[m.Issue.StatusID]
		if !ok || !isShownLane(lane) {
			continue
		}
		tv, total, _, kept := rc.buildRow(rowItem{
			localID: m.LocalID, issue: m.Issue, typeKind: m.TypeKind,
			statusName: m.StatusName, lane: lane, history: snap.History[m.Issue.SourceID],
		}, true)
		if !kept {
			continue
		}
		if best < 0 || total > bestMarks || (total == bestMarks && tv.Blocked && !out[best].Blocked) {
			best, bestMarks = len(out), total
		}
		out = append(out, tv)
	}
	if best < 0 {
		return out, ""
	}
	return out, out[best].Key
}

// doneInWindow reports whether a done ticket belongs in the done list (jira-sync.md §5.6).
func doneInWindow(cfg model.EffectiveConfig, asOf, doneAt time.Time, sprintIDs []string) bool {
	switch cfg.DoneWindow {
	case "14d", "yesterday":
		return !doneAt.Before(pulse.DoneSince(cfg.DoneWindow, asOf))
	}
	if cfg.Scope != "board" {
		return true
	}
	for _, id := range sprintIDs {
		if slices.Contains(cfg.ActiveSprintIDs, id) {
			return true
		}
	}
	return false
}

// buildBacklog returns the first BacklogRows backlog-role members in rank order.
func buildBacklog(snap model.BoardSnapshot) []BacklogItem {
	out := []BacklogItem{}
	for _, m := range snap.Members {
		if m.Role != model.RoleBacklog {
			continue
		}
		if len(out) >= snap.Config.BacklogRows {
			break
		}
		out = append(out, BacklogItem{
			IssueID: m.LocalID,
			Key:     m.Issue.Key,
			Summary: m.Issue.Summary,
			Type:    m.TypeKind,
			URL:     snap.IssueURLPrefix + m.Issue.Key,
		})
	}
	return out
}

// isShownLane is true for the working lanes (custom lanes included) and done; hidden, backlog and unknown lanes are
// not shown.
func isShownLane(lane string) bool { return model.IsShownLane(lane) }

func millisOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// sortByColumnOrder groups each lane's tickets by the order of their board columns within that lane (ColumnLane.
// LaneOrder, set in Configure), keeping rank order inside a column. Ticket order across lanes does not matter: the
// frontend groups by lane.
func sortByColumnOrder(views []TicketView, snap model.BoardSnapshot) {
	cfg := snap.Config
	colOrder := make(map[string]int, len(cfg.Columns))
	for _, c := range cfg.Columns {
		colOrder[c.ColumnName] = c.LaneOrder
	}
	statusOrder := make(map[string]int, len(cfg.StatusLanes))
	for _, r := range cfg.StatusLanes {
		statusOrder[r.StatusID] = colOrder[r.ColumnName]
	}
	issueStatus := make(map[string]string, len(snap.Members))
	for _, m := range snap.Members {
		issueStatus[m.Issue.SourceID] = m.Issue.StatusID
	}
	sort.SliceStable(views, func(i, j int) bool {
		return statusOrder[issueStatus[views[i].SourceID]] < statusOrder[issueStatus[views[j].SourceID]]
	})
}
