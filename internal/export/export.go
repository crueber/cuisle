package export

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/crueber/cuisle/internal/view"
)

// Build makes a document with one snapshot per view (kind current: exactly one). Day indexes become inclusive local
// dates via view.Axis (segment To is exclusive in the view → inclusive date To-1 here). Lists never nil.
func Build(views []view.StandupView, meta Meta) Document {
	d := Document{
		Schema:      Schema,
		GeneratedAt: formatUTC(meta.GeneratedAt),
		Generator:   Generator{Name: "Cuisle", Version: meta.Version},
		Kind:        meta.Kind,
		Source:      SourceRef{Kind: meta.SourceKind, BaseURL: meta.BaseURL},
		Standup: StandupRef{
			ID:    meta.StandupID,
			Name:  meta.Name,
			Space: SpaceRef{Key: meta.SpaceKey, Name: meta.SpaceName},
			Board: BoardRef{ID: meta.BoardID, Name: meta.BoardName, Kind: meta.BoardKind},
			Scope: meta.Scope,
		},
		Snapshots: make([]Snapshot, 0, len(views)),
	}
	for _, v := range views {
		d.Snapshots = append(d.Snapshots, buildSnapshot(v, meta))
	}
	return d
}

func buildSnapshot(v view.StandupView, meta Meta) Snapshot {
	axis := v.Axis
	s := Snapshot{
		AsOf:      unixMilliUTC(v.AsOf),
		Date:      v.Today,
		Timezone:  zoneName(meta.Timezone),
		Live:      v.Live,
		SyncedAt:  optionalTime(v.LastSyncedAt),
		PulseDays: v.PulseDays,
		Lanes:     make([]LaneRef, 0, len(v.Lanes)),
		People:    make([]Person, 0, len(v.Speakers)),
		Tickets:   make([]Ticket, 0, len(v.Tickets)),
		Backlog:   make([]Backlog, 0, len(v.Backlog)),
		Counts: Counts{
			Active:       v.Counts.Active,
			Unassigned:   v.Counts.Unassigned,
			Done:         v.Counts.Done,
			Backlog:      v.Counts.Backlog,
			BacklogTotal: v.BacklogTotal,
		},
	}
	if v.Sprint != nil {
		s.Sprint = &SprintRef{
			ID:      v.Sprint.ID,
			Name:    v.Sprint.Name,
			StartAt: unixMilliUTC(v.Sprint.StartAt),
			EndAt:   unixMilliUTC(v.Sprint.EndAt),
		}
	}
	if n := len(axis); n > 0 {
		s.Window = DateRange{From: axis[0].Date, To: axis[n-1].Date}
	}
	labels := make(map[string]string, len(v.Lanes))
	for _, l := range v.Lanes {
		s.Lanes = append(s.Lanes, LaneRef{Lane: l.Lane, Label: l.Label, LimitDays: l.LimitDays, Kind: l.Kind, Custom: l.Custom})
		labels[l.Lane] = l.Label
	}
	for _, sp := range v.Speakers {
		s.People = append(s.People, Person{ID: sp.ID, Name: sp.Name, Included: sp.Included, Active: sp.Active, Done: sp.Done})
	}
	for _, t := range v.Tickets {
		tk := buildTicket(t, axis)
		tk.LaneLabel = labels[t.Lane]
		s.Tickets = append(s.Tickets, tk)
	}
	for _, b := range v.Backlog {
		s.Backlog = append(s.Backlog, Backlog{Key: b.Key, Summary: b.Summary, Type: b.Type, URL: b.URL})
	}
	return s
}

func buildTicket(t view.TicketView, axis []view.AxisDay) Ticket {
	tk := Ticket{
		Key:        t.Key,
		SourceID:   t.SourceID,
		URL:        t.URL,
		Summary:    t.Summary,
		Type:       t.Type,
		Lane:       t.Lane,
		Status:     t.StatusName,
		DaysInLane: t.DaysInLane,
		OverLimit:  t.OverLimit,
		Blocked:    t.Blocked,
		DoneAt:     optionalTime(t.DoneAt),
		Pulse: Pulse{
			Segments: make([]DatedSeg, 0, len(t.Pulse.Segs)),
			Marks:    make([]DatedMark, 0, len(t.Pulse.Marks)),
			Blocked:  make([]DateRange, 0, len(t.Pulse.Blocked)),
		},
	}
	if t.Assignee != "" {
		a := t.Assignee
		tk.Assignee = &a
	}
	for _, sg := range t.Pulse.Segs {
		tk.Pulse.Segments = append(tk.Pulse.Segments, DatedSeg{
			Lane: sg.Lane,
			From: axisDate(axis, sg.From),
			To:   axisDate(axis, sg.To-1), // view To is exclusive
		})
	}
	for _, m := range t.Pulse.Marks {
		tk.Pulse.Marks = append(tk.Pulse.Marks, DatedMark{
			Date:     axisDate(axis, m.Day),
			Commits:  m.Commits,
			PRs:      m.PRs,
			Comments: m.Comments,
		})
	}
	for _, sp := range t.Pulse.Blocked {
		tk.Pulse.Blocked = append(tk.Pulse.Blocked, DateRange{
			From: axisDate(axis, sp.From),
			To:   axisDate(axis, sp.To), // view span To is inclusive
		})
	}
	return tk
}

// axisDate returns the local date of day index i, clamped to the axis. An empty axis yields "".
func axisDate(axis []view.AxisDay, i int) string {
	n := len(axis)
	if n == 0 {
		return ""
	}
	if i < 0 {
		i = 0
	}
	if i >= n {
		i = n - 1
	}
	return axis[i].Date
}

// unixMilliUTC formats epoch milliseconds as RFC 3339 UTC.
func unixMilliUTC(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// optionalTime returns nil for 0 ("never"), otherwise the RFC 3339 UTC time.
func optionalTime(ms int64) *string {
	if ms == 0 {
		return nil
	}
	s := unixMilliUTC(ms)
	return &s
}

func formatUTC(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func zoneName(loc *time.Location) string {
	if loc == nil {
		return time.UTC.String()
	}
	return loc.String()
}

// Encode returns the canonical JSON (2-space indent, trailing newline).
func Encode(d Document) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
