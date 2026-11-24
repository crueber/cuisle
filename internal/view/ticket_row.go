package view

// The ticket builder's shared state, the lane and status lookups, and the header row (the board row, built from the
// ticket's own history over TicketPulseDays). WP-T6. The row logic mirrors buildTickets in build.go (which is not part of
// this work package); TestBuildTicket_RowEqualsTheBoardRowAtTwentyOneDays keeps the two from drifting.

import (
	"time"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/pulse"
)

type ticketBuilder struct {
	snap model.BoardSnapshot
	t    cache.Ticket
	asOf time.Time
	opts TicketOptions

	win         pulse.Window
	statusLanes map[string]string // cfg status id → lane (as the board maps it)
	statusNames map[string]string // status id → name (the standup's name first)
	limits      map[string]int
	blocked     map[string]bool
	people      map[string]model.Person

	unresolvedMedia bool
}

func newTicketBuilder(snap model.BoardSnapshot, t cache.Ticket, asOf time.Time, opts TicketOptions) *ticketBuilder {
	cfg := snap.Config
	b := &ticketBuilder{
		snap: snap, t: t, asOf: asOf, opts: opts,
		win:         pulse.NewWindow(asOf, TicketPulseDays),
		statusLanes: make(map[string]string, len(cfg.StatusLanes)),
		statusNames: map[string]string{},
		limits:      map[string]int{},
		blocked:     map[string]bool{},
		people:      make(map[string]model.Person, len(snap.People)+len(t.People)),
	}
	for id, st := range snap.Statuses {
		b.statusNames[id] = st.Name
		if st.BlockedHint {
			b.blocked[id] = true
		}
	}
	for _, r := range cfg.StatusLanes {
		b.statusLanes[r.StatusID] = r.Lane
		if r.StatusName != "" {
			b.statusNames[r.StatusID] = r.StatusName
		}
	}
	for _, l := range cfg.Lanes {
		b.limits[l.Lane] = l.LimitDays
	}
	for id, p := range snap.People {
		b.people[id] = p
	}
	for id, p := range t.People {
		b.people[id] = p
	}
	return b
}

// lane is the lane a status shows as in the modal: the standup's lane when it is todo|prog|review|qa|done, else the
// status category (new → todo, indeterminate → prog, done → done; anything else → todo).
func (b *ticketBuilder) lane(statusID string) string {
	if l, ok := b.statusLanes[statusID]; ok && isShownLane(l) {
		return l
	}
	switch b.snap.Statuses[statusID].Category {
	case "done":
		return model.LaneDone
	case "indeterminate":
		return model.LaneProg
	}
	return model.LaneTodo
}

func (b *ticketBuilder) statusName(statusID string) string { return b.statusNames[statusID] }

// row builds the header row and returns the blocked days over the window.
func (b *ticketBuilder) row() (TicketView, int) {
	is := b.t.Issue
	h := b.t.History
	lane := b.lane(is.StatusID)

	pt := pulse.Ticket{
		CreatedAt: is.CreatedAt, UpdatedAt: is.UpdatedAt, StatusID: is.StatusID, Flagged: is.Flagged,
		StatusChanges: h.StatusChanges, FlagChanges: h.FlagChanges, Comments: h.Comments, Dev: h.Dev,
	}
	tl := pulse.Timeline(pt, b.statusLanes, b.asOf)
	entered := pulse.EnteredLane(tl)
	days := pulse.DaysInLane(entered, b.asOf)
	marks := pulse.Marks(pt, b.win, b.people)
	spans := pulse.BlockedSpans(pt, tl, b.blocked, b.win, b.asOf)
	segs := pulse.Segments(tl, b.win)
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
		prevLabel = LaneLabel(segs[n-2].Lane)
		prevDays = segs[n-2].To - segs[n-2].From
	}
	label := pulse.Label(pulse.LabelInput{
		LaneLabel: LaneLabel(lane), DaysInLane: days, PrevLaneLabel: prevLabel, PrevDays: prevDays,
		Commits: commits, PRs: prs, Comments: comments, Days: b.win.Days, BlockedDays: pulse.BlockedDays(spans),
		Done: lane == model.LaneDone,
	})

	var doneAt int64
	if lane == model.LaneDone {
		doneAt = entered.UnixMilli()
	}
	tv := TicketView{
		ID: b.t.LocalID, SourceID: is.SourceID, Key: is.Key, Summary: is.Summary, URL: b.snap.IssueURLPrefix + is.Key,
		Type: b.t.TypeKind, Assignee: is.AssigneeID, Lane: lane, StatusName: b.statusName(is.StatusID),
		DaysInLane: days, OverLimit: pulse.OverLimit(lane, days, b.limits[lane]),
		Blocked: pulse.BlockedNow(is.Flagged, b.blocked[is.StatusID]), DoneAt: doneAt,
		Pulse: Pulse{Segs: segs, Marks: marks, Blocked: spans, Label: label},
	}
	return tv, pulse.BlockedDays(spans)
}
