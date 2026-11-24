package view

import (
	"time"

	"github.com/crueber/cuisle/internal/adf"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
)

// TicketPulseDays is the header track length of the ticket modal (ux-spec §14.3).
const TicketPulseDays = 21

// BuildTicket builds the ticket modal payload (architecture.md §3.2 "Rules for view.BuildTicket"): rail folding,
// ADF parsing through an adf.Resolver over t, the header pulse over TicketPulseDays, comments oldest first. An issue that
// is not a member of snap's standup is built with that standup's lane mapping and an empty pulse.
//
// The frozen signature carries neither the connected account, nor the source's capabilities and the connection state,
// nor "the refresh failed": BuildTicket builds with the zero TicketOptions (nobody is Self; Can is read_only). The app
// calls BuildTicketWith.
func BuildTicket(snap model.BoardSnapshot, t cache.Ticket, prefs TicketFieldPrefs, asOf time.Time) TicketDetail {
	return BuildTicketWith(snap, t, prefs, asOf, TicketOptions{})
}

// BuildTicketWith is BuildTicket with the caller's facts about the connection (ticket_options.go). Pure: no I/O, no
// time.Now(); every list in the result is non-nil.
func BuildTicketWith(snap model.BoardSnapshot, t cache.Ticket, prefs TicketFieldPrefs, asOf time.Time, opts TicketOptions) TicketDetail {
	b := newTicketBuilder(snap, t, asOf, opts)
	row, blockedDays := b.row()

	d := TicketDetail{
		IssueID:   t.LocalID,
		StandupID: snap.StandupID,
		AsOf:      asOf.UnixMilli(),
		SyncedAt:  millisOrZero(t.HydratedAt),
		UpdatedAt: millisOrZero(t.Issue.UpdatedAt),
		Row:       row,
		URL:       row.URL,
		PulseDays: TicketPulseDays,
		Axis:      b.win.Axis(),
		LimitDays: b.limits[row.Lane],
		Can:       CanFor(opts.Writes, opts.AuthState),
		NewSince:  previousWorkingDayStart(asOf),
		Draft:     b.draft(),
		Parent:    b.parent(),
	}
	d.BlockedDays = blockedDays

	if t.Description != nil && !t.Description.IsEmpty() {
		doc := b.parse(*t.Description)
		d.Description = &doc
	}
	d.Attachments = b.attachments()
	d.Subtasks, d.SubtasksDone = b.subtasks()
	d.LinkGroups = b.linkGroups()
	d.Comments = b.comments()
	d.Rail, d.Dev = b.rail(prefs, blockedDays)
	d.Warnings = b.warnings()
	return d
}

// previousWorkingDayStart is local midnight, in asOf's location, of the previous Monday-to-Friday day (D3: "since
// yesterday"; a Monday looks back to Friday, a weekend to Friday). Calendar arithmetic, so DST-safe.
func previousWorkingDayStart(asOf time.Time) int64 {
	y, m, day := asOf.Date()
	for {
		day--
		d := time.Date(y, m, day, 0, 0, 0, 0, asOf.Location())
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			return d.UnixMilli()
		}
	}
}

// warnings lists the ticket's warnings (Warn*), in a fixed order, without duplicates.
func (b *ticketBuilder) warnings() []string {
	out := []string{}
	if b.unresolvedMedia {
		out = append(out, WarnMediaUnresolved)
	}
	if !b.t.RemovedAt.IsZero() {
		out = append(out, WarnNotOnBoard)
	}
	if b.opts.RefreshFailed {
		out = append(out, WarnRefreshFailed)
	}
	return out
}

// draft is the stored comment draft with its mentions (never nil).
func (b *ticketBuilder) draft() CommentInput {
	in := CommentInput{Text: b.t.Draft.Text, Mentions: []Mention{}}
	for _, m := range b.t.Draft.Mentions {
		in.Mentions = append(in.Mentions, Mention{Start: m.Start, End: m.End, AccountID: m.AccountID, Name: m.Name})
	}
	return in
}

// parent is the compact parent reference (nil without a parent). A synced parent shows its live summary.
func (b *ticketBuilder) parent() *IssueRefView {
	p := b.t.Parent
	if p == nil {
		return nil
	}
	summary := p.OtherSummary
	if p.OtherIssueID != 0 && p.LiveSummary != "" {
		summary = p.LiveSummary
	}
	return &IssueRefView{IssueID: p.OtherIssueID, Key: p.OtherKey, Summary: summary, TypeKind: p.OtherTypeKind}
}

// parse turns a stored document into the node tree through the ticket's resolver.
func (b *ticketBuilder) parse(raw model.RichText) adf.Doc {
	if raw.IsEmpty() {
		return adf.Doc{Nodes: []adf.Node{}}
	}
	return adf.Parse(raw, newTicketResolver(b, raw))
}
