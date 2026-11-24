package view

// The content column of the ticket modal: attachments, subtasks, links, comments (ux-spec §14.4–14.5). WP-T6.

import (
	"path"
	"strconv"
	"strings"

	"github.com/crueber/cuisle/internal/adf"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
)

// person is the PersonRef of an account. An account the cache does not know shows as "Unknown user".
func (b *ticketBuilder) person(id string) PersonRef {
	name := b.people[id].DisplayName
	initials := Initials(name)
	if name == "" {
		name, initials = "Unknown user", "?"
	}
	return PersonRef{AccountID: id, Name: name, Initials: initials, Self: id != "" && id == b.opts.SelfAccountID}
}

// attachments are the attachment tiles and cards, in the order the cache read them (images first, newest first).
func (b *ticketBuilder) attachments() []AttachmentView {
	out := make([]AttachmentView, 0, len(b.t.Attachments))
	for _, a := range b.t.Attachments {
		who := PersonRef{}
		if a.AuthorID != "" {
			who = b.person(a.AuthorID)
		}
		id := strconv.FormatInt(a.LocalID, 10)
		out = append(out, AttachmentView{
			ID: a.LocalID, Name: a.Name, Ext: strings.ToUpper(strings.TrimPrefix(path.Ext(a.Name), ".")), Mime: a.Mime,
			Bytes: a.Bytes, AuthorInitials: who.Initials, AuthorName: who.Name, CreatedAt: millisOrZero(a.CreatedAt),
			Image: a.IsImage, Width: a.Width, Height: a.Height, Cached: cachedState(a.HasFile, a.HasThumb),
			URL: "/media/" + id, ThumbURL: "/media/" + id + "/thumb",
		})
	}
	return out
}

// issueRow is one subtask or linked-issue row. A synced issue shows its live summary and status; otherwise the snapshot
// stored with the link.
func (b *ticketBuilder) issueRow(l cache.TicketLink) IssueRow {
	summary, statusID := l.OtherSummary, l.OtherStatusID
	if l.OtherIssueID != 0 {
		if l.LiveSummary != "" {
			summary = l.LiveSummary
		}
		if l.LiveStatusID != "" {
			statusID = l.LiveStatusID
		}
	}
	lane := b.lane(statusID)
	kind := l.OtherTypeKind
	if kind == "" {
		kind = "other"
	}
	// The link payload carries no assignee, so a synced target shows its CURRENT assignee (none when unassigned); an
	// unsynced target shows the stored snapshot, which is empty for Jira.
	assignee := l.OtherAssigneeID
	if l.OtherIssueID != 0 {
		assignee = l.LiveAssigneeID
	}
	initials := ""
	if assignee != "" {
		if p, ok := b.people[assignee]; ok && p.DisplayName != "" {
			initials = Initials(p.DisplayName)
		}
	}
	return IssueRow{
		IssueID: l.OtherIssueID, Key: l.OtherKey, Summary: summary, TypeKind: kind, StatusName: b.statusName(statusID),
		StatusLozenge: lane, AssigneeInitials: initials, URL: b.snap.IssueURLPrefix + l.OtherKey, Done: lane == model.LaneDone,
	}
}

// subtasks are the subtask rows and how many of them are done.
func (b *ticketBuilder) subtasks() ([]IssueRow, int) {
	out := []IssueRow{}
	done := 0
	for _, l := range b.t.Links {
		if l.Relation != "subtask" {
			continue
		}
		r := b.issueRow(l)
		if r.Done {
			done++
		}
		out = append(out, r)
	}
	return out, done
}

// linkGroups groups the issue links by label (upper case), in the order the cache read them.
func (b *ticketBuilder) linkGroups() []LinkGroup {
	out := []LinkGroup{}
	index := map[string]int{}
	for _, l := range b.t.Links {
		if l.Relation != "link" {
			continue
		}
		label := strings.ToUpper(l.Label)
		i, ok := index[label]
		if !ok {
			i = len(out)
			index[label] = i
			out = append(out, LinkGroup{Label: label, Rows: []IssueRow{}})
		}
		out[i].Rows = append(out[i].Rows, b.issueRow(l))
	}
	return out
}

// comments are the stored comments, oldest first, with their bodies parsed. A restricted comment carries
// "<Group> only".
func (b *ticketBuilder) comments() []CommentView {
	out := make([]CommentView, 0, len(b.t.Comments))
	for _, c := range b.t.Comments {
		updated := c.UpdatedAt
		if updated.IsZero() {
			updated = c.At
		}
		vis := ""
		if c.Visibility != nil && c.Visibility.Value != "" {
			vis = c.Visibility.Value + " only"
		}
		body := adf.Doc{Nodes: []adf.Node{}}
		if !c.Body.IsEmpty() {
			body = b.parse(c.Body)
		}
		out = append(out, CommentView{
			ID: c.ID, Author: b.person(c.AuthorID), CreatedAt: millisOrZero(c.At), UpdatedAt: millisOrZero(updated),
			Edited: updated.After(c.At), Visibility: vis, Body: body,
		})
	}
	return out
}
