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

	win          pulse.Window
	statusLanes  map[string]string // cfg status id → lane (as the board maps it)
	statusNames  map[string]string // status id → name (the standup's name first)
	limits       map[string]int
	blocked      map[string]bool
	people       map[string]model.Person
	attByMediaID map[string]int // MediaFileID → index in t.Attachments
	attByName    map[string]int // Name → unique index in t.Attachments (-1 = duplicate)

	unresolvedMedia bool
}

func newTicketBuilder(snap model.BoardSnapshot, t cache.Ticket, asOf time.Time, opts TicketOptions) *ticketBuilder {
	cfg := snap.Config
	b := &ticketBuilder{
		snap: snap, t: t, asOf: asOf, opts: opts,
		win:          pulse.NewWindow(asOf, TicketPulseDays),
		statusLanes:  drawableLanes(cfg),
		statusNames:  map[string]string{},
		limits:       map[string]int{},
		blocked:      map[string]bool{},
		people:       make(map[string]model.Person, len(snap.People)+len(t.People)),
		attByMediaID: make(map[string]int, len(t.Attachments)),
		attByName:    make(map[string]int, len(t.Attachments)),
	}
	for i, a := range t.Attachments {
		if a.MediaFileID != "" {
			if _, dup := b.attByMediaID[a.MediaFileID]; !dup {
				b.attByMediaID[a.MediaFileID] = i
			}
		}
		if a.Name != "" {
			if _, seen := b.attByName[a.Name]; seen {
				b.attByName[a.Name] = -1
			} else {
				b.attByName[a.Name] = i
			}
		}
	}
	for id, st := range snap.Statuses {
		b.statusNames[id] = st.Name
		if st.BlockedHint {
			b.blocked[id] = true
		}
	}
	for _, r := range cfg.StatusLanes {
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
	rc := rowContext{
		cfg: b.snap.Config, urlPrefix: b.snap.IssueURLPrefix, asOf: b.asOf, win: b.win,
		statusLanes: b.statusLanes, blocked: b.blocked, limits: b.limits, people: b.people,
	}
	tv, _, blockedDays, _ := rc.buildRow(rowItem{
		localID: b.t.LocalID, issue: is, typeKind: b.t.TypeKind,
		statusName: b.statusName(is.StatusID), lane: b.lane(is.StatusID), history: b.t.History,
	}, false)
	return tv, blockedDays
}
