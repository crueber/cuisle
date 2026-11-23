package export_test

// Tests for internal/export (WP-09): the cuisle.export.v1 document (architecture.md §3.5, ADR 0012, AGENTS.md
// invariant 12, testing.md "Sinks and export").
//
// SPEC-NOTE: Segment "to" dates are inclusive: a view seg {From:f, To:t} (To EXCLUSIVE day index) becomes
// from = Axis[f].Date, to = Axis[t-1].Date. Blocked spans (Span.To INCLUSIVE) become Axis[From].Date..Axis[To].Date.
// A one-day span (From == To) therefore exports from == to.
// SPEC-NOTE: Snapshot.date is view.Today (already the local date of AsOf); snapshot.timezone is
// meta.Timezone.String() (a time.FixedZone named "America/Chicago" yields "America/Chicago" here, so the tests
// need no tzdata). asOf, syncedAt, doneAt, generatedAt and sprint startAt/endAt are RFC 3339 UTC with no
// fractional seconds (fixtures use whole seconds, sub-second formatting is deliberately unspecified).
// Callers must therefore pass a NAMED location (time.Local.String() is "Local", not an IANA name); the tests cannot
// check that portably. The date is the LOCAL date (not the UTC date of asOf): see
// TestBuild_LocalDateDiffersFromUTCDate_DateIsLocal.
// SPEC-NOTE: window.from / window.to are the first / last Axis dates; for an empty Axis both are "" (no panic).
// SPEC-NOTE: view.BacklogTotal is exported verbatim, so -1 ("unknown") is exported as -1.
// SPEC-NOTE: Fields of the view that the §3.5 example does not list (warnings, previewKey, options, sync, pulse
// label, lane columns, speaker first/initials, rev, ids) are NOT exported; the golden file is the contract and the
// byte comparison enforces it. This also keeps e-mail-bearing warning text out of exports.
// SPEC-NOTE: Build emits one snapshot per view in input order and copies meta.Kind verbatim; people[] is
// view.Speakers in order with id/name/included/active/done only; tickets[] keeps view order; ticket assignee ""
// becomes null; doneAt 0 becomes null.
// SPEC-NOTE: standup{} (id, name, space, board, scope) and source{} come from Meta, never from the view.
// SPEC-NOTE: Encode output is not HTML-escaped-sensitive in these tests: fixtures avoid '<', '>' and '&' so either
// escaping policy passes. Encode need not normalise nil slices (Build guarantees non-nil lists).

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/export"
	"github.com/crueber/cuisle/internal/pulse"
	"github.com/crueber/cuisle/internal/view"
)

var update = flag.Bool("update", false, "rewrite testdata/export_current.golden.json from Build+Encode")

const goldenPath = "testdata/export_current.golden.json"

// ---- panic-safe wrappers (stubs panic until WP-09 lands; keep each test's failure separate) ----

func build(t *testing.T, views []view.StandupView, meta export.Meta) (d export.Document) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("export.Build panicked (unimplemented stub or bug): %v", r)
		}
	}()
	return export.Build(views, meta)
}

func encode(t *testing.T, d export.Document) (b []byte) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("export.Encode panicked (unimplemented stub or bug): %v", r)
		}
	}()
	b, err := export.Encode(d)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return b
}

// ---- fixtures ----

func ms(y int, mo time.Month, d, h, mi, s int) int64 {
	return time.Date(y, mo, d, h, mi, s, 0, time.UTC).UnixMilli()
}

var chicago = time.FixedZone("America/Chicago", -6*3600)

func fixtureMeta() export.Meta {
	return export.Meta{
		// 09:04:05 in Chicago (-06:00) == 15:04:05Z: Build must convert to UTC.
		GeneratedAt: time.Date(2026, 11, 23, 9, 4, 5, 0, chicago),
		Version:     "1.0.0",
		Kind:        "current",
		SourceKind:  "jira",
		BaseURL:     "https://acme.atlassian.net",
		StandupID:   1,
		Name:        "Saffron Mobile",
		SpaceKey:    "MOB",
		SpaceName:   "Saffron Mobile",
		BoardID:     "12",
		BoardName:   "MOB board",
		BoardKind:   "scrum",
		Scope:       "sprint",
		Timezone:    chicago,
	}
}

func axis7() []pulse.AxisDay {
	// 2026-11-17 is a Tuesday; 21 and 22 are the weekend; 23 is Monday.
	return []pulse.AxisDay{
		{Date: "2026-11-17", Label: "17"},
		{Date: "2026-11-18", Label: "18"},
		{Date: "2026-11-19", Label: "19"},
		{Date: "2026-11-20", Label: "20"},
		{Date: "2026-11-21", Label: "21", Weekend: true},
		{Date: "2026-11-22", Label: "22", Weekend: true},
		{Date: "2026-11-23", Label: "23", Monday: true},
	}
}

func fixtureView() view.StandupView {
	const base = "https://acme.atlassian.net/browse/"
	return view.StandupView{
		StandupID:   1,
		Rev:         7,
		GeneratedAt: ms(2026, 11, 23, 15, 4, 5),
		Today:       "2026-11-23",
		AsOf:        ms(2026, 11, 23, 15, 4, 5),
		Live:        true,
		Name:        "Saffron Mobile",
		SpaceKey:    "MOB",
		BoardName:   "MOB board",
		BoardType:   "scrum",
		Scope:       "sprint",
		Sprint: &view.SprintInfo{
			ID: "42", Name: "Sprint 42",
			StartAt:   ms(2026, 11, 9, 15, 0, 0),
			EndAt:     ms(2026, 11, 25, 23, 0, 0),
			DaysLeft:  2,
			EndsLabel: "ends in 2 days",
		},
		PulseDays: 7,
		Axis:      axis7(),
		Lanes: []view.LaneInfo{
			{Lane: "todo", Label: "To do", LimitDays: 0, Columns: []string{"To Do"}},
			{Lane: "prog", Label: "In progress", LimitDays: 5, Columns: []string{"In Progress"}},
			{Lane: "review", Label: "Review", LimitDays: 2, Columns: []string{"Review"}},
			{Lane: "qa", Label: "QA", LimitDays: 3, Columns: []string{"QA"}},
			{Lane: "done", Label: "Done", LimitDays: 0, Columns: []string{"Done"}},
		},
		Speakers: []view.SpeakerView{
			{ID: "acc-iris", Name: "Iris Calloway", First: "Iris", Initials: "IC", Included: true, Active: 1, Done: 1},
			{ID: "unassigned", Name: "Unassigned", First: "Unassigned", Initials: "?", Unassigned: true, Included: true, Active: 1, Done: 0},
		},
		Tickets: []view.TicketView{
			{
				ID: 501, SourceID: "10401", Key: "MOB-1401", Summary: "Retry webhook delivery on 5xx",
				URL: base + "MOB-1401", Type: "story", Assignee: "acc-iris", Lane: "done", StatusName: "Done",
				DaysInLane: 2, DoneAt: ms(2026, 11, 22, 14, 0, 0),
				Pulse: view.Pulse{
					Segs: []view.PulseSeg{
						{Lane: "todo", From: 0, To: 2},
						{Lane: "prog", From: 2, To: 5}, // exclusive To 5 -> inclusive date axis[4] = 2026-11-21
						{Lane: "done", From: 5, To: 7}, // To == len(axis) -> last axis date
					},
					Marks: []view.PulseMark{
						{Day: 2, Commits: 2, PRs: 0, Comments: 1},
						{Day: 4, Commits: 0, PRs: 1, Comments: 0},
					},
					Blocked: []view.PulseSpan{},
					Label:   "Done 2 days ago",
				},
			},
			{
				ID: 502, SourceID: "10402", Key: "MOB-1402", Summary: "Document the export schema",
				URL: base + "MOB-1402", Type: "task", Assignee: "", Lane: "todo", StatusName: "To Do",
				DaysInLane: 7,
				Pulse: view.Pulse{
					Segs:  []view.PulseSeg{{Lane: "todo", From: 0, To: 7}},
					Marks: []view.PulseMark{}, Blocked: []view.PulseSpan{},
					Label: "In To do 7 days",
				},
			},
			{
				ID: 503, SourceID: "10403", Key: "MOB-1403", Summary: "Duplicate SMS on double reassign",
				URL: base + "MOB-1403", Type: "bug", Assignee: "acc-iris", Lane: "prog", StatusName: "In Progress",
				DaysInLane: 4, OverLimit: true, Blocked: true,
				Pulse: view.Pulse{
					Segs: []view.PulseSeg{
						{Lane: "todo", From: 0, To: 3},
						{Lane: "prog", From: 3, To: 7},
					},
					Marks: []view.PulseMark{
						{Day: 5, Commits: 1, PRs: 1, Comments: 3},
						{Day: 6, Commits: 0, PRs: 0, Comments: 1},
					},
					Blocked: []view.PulseSpan{{From: 3, To: 3}, {From: 5, To: 6}}, // inclusive; first is one day
					Label:   "Blocked 3 days",
				},
			},
		},
		Backlog: []view.BacklogItem{
			{Key: "MOB-1501", Summary: "Add CSV export", Type: "story", URL: base + "MOB-1501"},
			{Key: "MOB-1502", Summary: "Rate limit the sync endpoint", Type: "task", URL: base + "MOB-1502"},
		},
		BacklogTotal: 9,
		Counts:       view.Counts{Active: 2, Unassigned: 1, Done: 1, Backlog: 2},
		Options:      view.ViewOptions{RowsMode: "fit", ShowCommits: true, DoneWindow: "sprint", DoneLabel: "this sprint", DevActivity: "ok"},
		Sync:         view.SyncStatus{State: "idle", LastStatus: "ok"},
		Warnings:     []string{},
		PreviewKey:   "",
		LastSyncedAt: ms(2026, 11, 23, 15, 2, 10),
	}
}

func sp(s string) *string { return &s }

// expectedDoc is the hand-written Document that Build(fixtureView(), fixtureMeta()) must return. It mirrors
// testdata/export_current.golden.json.
func expectedDoc() export.Document {
	const base = "https://acme.atlassian.net/browse/"
	return export.Document{
		Schema:      export.Schema,
		GeneratedAt: "2026-11-23T15:04:05Z",
		Generator:   export.Generator{Name: "Cuisle", Version: "1.0.0"},
		Kind:        "current",
		Source:      export.SourceRef{Kind: "jira", BaseURL: "https://acme.atlassian.net"},
		Standup: export.StandupRef{
			ID: 1, Name: "Saffron Mobile",
			Space: export.SpaceRef{Key: "MOB", Name: "Saffron Mobile"},
			Board: export.BoardRef{ID: "12", Name: "MOB board", Kind: "scrum"},
			Scope: "sprint",
		},
		Snapshots: []export.Snapshot{{
			AsOf: "2026-11-23T15:04:05Z", Date: "2026-11-23", Timezone: "America/Chicago", Live: true,
			SyncedAt:  sp("2026-11-23T15:02:10Z"),
			Sprint:    &export.SprintRef{ID: "42", Name: "Sprint 42", StartAt: "2026-11-09T15:00:00Z", EndAt: "2026-11-25T23:00:00Z"},
			PulseDays: 7,
			Window:    export.DateRange{From: "2026-11-17", To: "2026-11-23"},
			Lanes: []export.LaneRef{
				{Lane: "todo", Label: "To do", LimitDays: 0},
				{Lane: "prog", Label: "In progress", LimitDays: 5},
				{Lane: "review", Label: "Review", LimitDays: 2},
				{Lane: "qa", Label: "QA", LimitDays: 3},
				{Lane: "done", Label: "Done", LimitDays: 0},
			},
			People: []export.Person{
				{ID: "acc-iris", Name: "Iris Calloway", Included: true, Active: 1, Done: 1},
				{ID: "unassigned", Name: "Unassigned", Included: true, Active: 1, Done: 0},
			},
			Tickets: []export.Ticket{
				{
					Key: "MOB-1401", SourceID: "10401", URL: base + "MOB-1401", Summary: "Retry webhook delivery on 5xx",
					Type: "story", Assignee: sp("acc-iris"), Lane: "done", Status: "Done", DaysInLane: 2,
					DoneAt: sp("2026-11-22T14:00:00Z"),
					Pulse: export.Pulse{
						Segments: []export.DatedSeg{
							{Lane: "todo", From: "2026-11-17", To: "2026-11-18"},
							{Lane: "prog", From: "2026-11-19", To: "2026-11-21"},
							{Lane: "done", From: "2026-11-22", To: "2026-11-23"},
						},
						Marks: []export.DatedMark{
							{Date: "2026-11-19", Commits: 2, PRs: 0, Comments: 1},
							{Date: "2026-11-21", Commits: 0, PRs: 1, Comments: 0},
						},
						Blocked: []export.DateRange{},
					},
				},
				{
					Key: "MOB-1402", SourceID: "10402", URL: base + "MOB-1402", Summary: "Document the export schema",
					Type: "task", Assignee: nil, Lane: "todo", Status: "To Do", DaysInLane: 7,
					Pulse: export.Pulse{
						Segments: []export.DatedSeg{{Lane: "todo", From: "2026-11-17", To: "2026-11-23"}},
						Marks:    []export.DatedMark{},
						Blocked:  []export.DateRange{},
					},
				},
				{
					Key: "MOB-1403", SourceID: "10403", URL: base + "MOB-1403", Summary: "Duplicate SMS on double reassign",
					Type: "bug", Assignee: sp("acc-iris"), Lane: "prog", Status: "In Progress", DaysInLane: 4,
					OverLimit: true, Blocked: true,
					Pulse: export.Pulse{
						Segments: []export.DatedSeg{
							{Lane: "todo", From: "2026-11-17", To: "2026-11-19"},
							{Lane: "prog", From: "2026-11-20", To: "2026-11-23"},
						},
						Marks: []export.DatedMark{
							{Date: "2026-11-22", Commits: 1, PRs: 1, Comments: 3},
							{Date: "2026-11-23", Commits: 0, PRs: 0, Comments: 1},
						},
						Blocked: []export.DateRange{
							{From: "2026-11-20", To: "2026-11-20"},
							{From: "2026-11-22", To: "2026-11-23"},
						},
					},
				},
			},
			Backlog: []export.Backlog{
				{Key: "MOB-1501", Summary: "Add CSV export", Type: "story", URL: base + "MOB-1501"},
				{Key: "MOB-1502", Summary: "Rate limit the sync endpoint", Type: "task", URL: base + "MOB-1502"},
			},
			Counts: export.Counts{Active: 2, Unassigned: 1, Done: 1, Backlog: 2, BacklogTotal: 9},
		}},
	}
}

func kanbanView() view.StandupView {
	return view.StandupView{
		StandupID: 2,
		AsOf:      ms(2026, 11, 23, 10, 0, 0),
		Today:     "2026-11-23",
		Live:      false,
		Name:      "Support Flow",
		SpaceKey:  "SUP",
		BoardName: "Support board",
		BoardType: "kanban",
		Scope:     "board",
		Sprint:    nil,
		PulseDays: 3,
		Axis: []pulse.AxisDay{
			{Date: "2026-11-21", Label: "21", Weekend: true},
			{Date: "2026-11-22", Label: "22", Weekend: true},
			{Date: "2026-11-23", Label: "23", Monday: true},
		},
		Lanes: []view.LaneInfo{{Lane: "todo", Label: "To do"}, {Lane: "done", Label: "Done"}},
		Speakers: []view.SpeakerView{
			{ID: "acc-sam", Name: "Sam Ortiz", Included: true, Active: 1},
		},
		Tickets: []view.TicketView{{
			SourceID: "20001", Key: "SUP-9", Summary: "Printer offline", URL: "https://acme.atlassian.net/browse/SUP-9",
			Type: "task", Assignee: "acc-sam", Lane: "todo", StatusName: "To Do", DaysInLane: 3,
			Pulse: view.Pulse{Segs: []view.PulseSeg{{Lane: "todo", From: 0, To: 3}}}, // nil Marks and Blocked
		}},
		BacklogTotal: -1,
		Counts:       view.Counts{Active: 1},
		LastSyncedAt: 0,
	}
}

func kanbanMeta() export.Meta {
	return export.Meta{
		GeneratedAt: time.Date(2026, 11, 23, 10, 0, 1, 0, time.UTC),
		Version:     "1.0.0", Kind: "current", SourceKind: "jira", BaseURL: "https://acme.atlassian.net",
		StandupID: 2, Name: "Support Flow", SpaceKey: "SUP", SpaceName: "Support",
		BoardID: "7", BoardName: "Support board", BoardKind: "kanban", Scope: "board", Timezone: time.UTC,
	}
}

// ---- generic JSON helpers ----

func decodeAny(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, b)
	}
	return v
}

// walk calls fn for every object key (with its dotted path) in the decoded JSON tree.
func walk(v any, path string, fn func(path, key string, val any)) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			fn(path, k, val)
			walk(val, path+"."+k, fn)
		}
	case []any:
		for _, e := range x {
			walk(e, path+"[]", fn)
		}
	}
}

var nullable = map[string]bool{"syncedAt": true, "sprint": true, "assignee": true, "doneAt": true}

func assertNoNullLists(t *testing.T, b []byte) {
	t.Helper()
	walk(decodeAny(t, b), "$", func(path, key string, val any) {
		if val == nil && !nullable[key] {
			t.Errorf("%s.%s is null; lists must be [] and only syncedAt/sprint/assignee/doneAt may be null", path, key)
		}
	})
}

// ---- Build ----

func TestBuild_CurrentView_ProducesExpectedDocument(t *testing.T) {
	got := build(t, []view.StandupView{fixtureView()}, fixtureMeta())
	want := expectedDoc()
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.MarshalIndent(got, "", "  ")
		wj, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("Document mismatch\n--- got ---\n%s\n--- want ---\n%s", gj, wj)
	}
}

func TestBuild_SegmentToIsExclusiveInViewAndInclusiveDate(t *testing.T) {
	v := fixtureView()
	v.Tickets = v.Tickets[:1]
	v.Tickets[0].Pulse = view.Pulse{
		Segs:    []view.PulseSeg{{Lane: "prog", From: 2, To: 5}, {Lane: "qa", From: 5, To: 6}, {Lane: "done", From: 6, To: 7}},
		Marks:   []view.PulseMark{{Day: 0, Commits: 1}, {Day: 6, PRs: 2}},
		Blocked: []view.PulseSpan{{From: 0, To: 0}, {From: 4, To: 6}},
	}
	d := build(t, []view.StandupView{v}, fixtureMeta())
	p := d.Snapshots[0].Tickets[0].Pulse
	wantSegs := []export.DatedSeg{
		{Lane: "prog", From: "2026-11-19", To: "2026-11-21"}, // {2,5} -> axis[2]..axis[4]
		{Lane: "qa", From: "2026-11-22", To: "2026-11-22"},   // {5,6} one day
		{Lane: "done", From: "2026-11-23", To: "2026-11-23"}, // {6,7} last day
	}
	if !reflect.DeepEqual(p.Segments, wantSegs) {
		t.Errorf("segments = %+v, want %+v", p.Segments, wantSegs)
	}
	wantMarks := []export.DatedMark{
		{Date: "2026-11-17", Commits: 1},
		{Date: "2026-11-23", PRs: 2},
	}
	if !reflect.DeepEqual(p.Marks, wantMarks) {
		t.Errorf("marks = %+v, want %+v", p.Marks, wantMarks)
	}
	wantBlocked := []export.DateRange{{From: "2026-11-17", To: "2026-11-17"}, {From: "2026-11-21", To: "2026-11-23"}}
	if !reflect.DeepEqual(p.Blocked, wantBlocked) {
		t.Errorf("blocked = %+v, want %+v", p.Blocked, wantBlocked)
	}
}

func TestBuild_UnassignedAndOpenTickets_HaveNullAssigneeAndDoneAt(t *testing.T) {
	d := build(t, []view.StandupView{fixtureView()}, fixtureMeta())
	tk := d.Snapshots[0].Tickets
	if tk[1].Assignee != nil {
		t.Errorf("unassigned ticket assignee = %q, want nil", *tk[1].Assignee)
	}
	if tk[1].DoneAt != nil || tk[2].DoneAt != nil {
		t.Errorf("open tickets must have nil doneAt")
	}
	if tk[0].DoneAt == nil || *tk[0].DoneAt != "2026-11-22T14:00:00Z" {
		t.Errorf("done ticket doneAt = %v, want 2026-11-22T14:00:00Z", tk[0].DoneAt)
	}
	if tk[0].Assignee == nil || *tk[0].Assignee != "acc-iris" {
		t.Errorf("assigned ticket assignee = %v, want acc-iris", tk[0].Assignee)
	}
}

func TestBuild_GeneratedAtConvertedToUTC(t *testing.T) {
	d := build(t, []view.StandupView{fixtureView()}, fixtureMeta())
	if d.GeneratedAt != "2026-11-23T15:04:05Z" {
		t.Errorf("generatedAt = %q, want 2026-11-23T15:04:05Z", d.GeneratedAt)
	}
	if d.Schema != "cuisle.export.v1" {
		t.Errorf("schema = %q", d.Schema)
	}
}

func TestBuild_KanbanNeverSynced_SprintAndSyncedAtNull(t *testing.T) {
	d := build(t, []view.StandupView{kanbanView()}, kanbanMeta())
	s := d.Snapshots[0]
	if s.Sprint != nil {
		t.Errorf("sprint = %+v, want nil for kanban", s.Sprint)
	}
	if s.SyncedAt != nil {
		t.Errorf("syncedAt = %q, want nil when LastSyncedAt == 0", *s.SyncedAt)
	}
	if s.Live {
		t.Errorf("live = true, want false")
	}
	if s.Timezone != "UTC" || s.Date != "2026-11-23" || s.AsOf != "2026-11-23T10:00:00Z" {
		t.Errorf("snapshot header = %q %q %q", s.Timezone, s.Date, s.AsOf)
	}
	if s.Window != (export.DateRange{From: "2026-11-21", To: "2026-11-23"}) {
		t.Errorf("window = %+v", s.Window)
	}
	if s.Counts.BacklogTotal != -1 || s.Counts.Active != 1 {
		t.Errorf("counts = %+v, want backlogTotal -1 (unknown, verbatim) and active 1", s.Counts)
	}
	if d.Standup.Board.Kind != "kanban" || d.Standup.Scope != "board" || d.Standup.ID != 2 {
		t.Errorf("standup ref = %+v", d.Standup)
	}
	pl := s.Tickets[0].Pulse
	if pl.Marks == nil || pl.Blocked == nil || len(pl.Marks) != 0 || len(pl.Blocked) != 0 {
		t.Errorf("nil view marks/blocked must become empty non-nil lists, got marks=%#v blocked=%#v", pl.Marks, pl.Blocked)
	}
	if want := []export.DatedSeg{{Lane: "todo", From: "2026-11-21", To: "2026-11-23"}}; !reflect.DeepEqual(pl.Segments, want) {
		t.Errorf("segments = %+v, want %+v", pl.Segments, want)
	}
}

func TestEncode_KanbanNeverSynced_SprintAndSyncedAtAreJSONNull(t *testing.T) {
	d := build(t, []view.StandupView{kanbanView()}, kanbanMeta())
	b := encode(t, d)
	m := decodeAny(t, b).(map[string]any)
	snap := m["snapshots"].([]any)[0].(map[string]any)
	for _, k := range []string{"sprint", "syncedAt"} {
		v, ok := snap[k]
		if !ok {
			t.Errorf("key %q missing (must be present as null)", k)
		} else if v != nil {
			t.Errorf("%q = %v, want null", k, v)
		}
	}
	if !bytes.Contains(b, []byte(`"sprint": null`)) || !bytes.Contains(b, []byte(`"syncedAt": null`)) {
		t.Errorf("expected literal \"sprint\": null and \"syncedAt\": null in output:\n%s", b)
	}
}

func TestBuild_LocalDateDiffersFromUTCDate_DateIsLocal(t *testing.T) {
	v := fixtureView()
	v.AsOf = ms(2026, 11, 24, 3, 0, 0) // 21:00 on 2026-11-23 in Chicago
	v.Today = "2026-11-23"
	d := build(t, []view.StandupView{v}, fixtureMeta())
	s := d.Snapshots[0]
	if s.AsOf != "2026-11-24T03:00:00Z" {
		t.Errorf("asOf = %q, want 2026-11-24T03:00:00Z (UTC)", s.AsOf)
	}
	if s.Date != "2026-11-23" {
		t.Errorf("date = %q, want the local date 2026-11-23, not the UTC date", s.Date)
	}
	if s.Window.To != "2026-11-23" {
		t.Errorf("window.to = %q, want 2026-11-23 (last axis date)", s.Window.To)
	}
	if s.Timezone != "America/Chicago" {
		t.Errorf("timezone = %q", s.Timezone)
	}
}

func TestBuild_StandupRefComesFromMeta(t *testing.T) {
	v := fixtureView()
	v.StandupID, v.Name, v.SpaceKey, v.BoardName, v.BoardType, v.Scope = 99, "view-name", "VW", "view-board", "kanban", "board"
	m := fixtureMeta()
	d := build(t, []view.StandupView{v}, m)
	if d.Standup != expectedDoc().Standup {
		t.Errorf("standup = %+v, want the Meta-derived %+v", d.Standup, expectedDoc().Standup)
	}
	if d.Source != (export.SourceRef{Kind: "jira", BaseURL: "https://acme.atlassian.net"}) {
		t.Errorf("source = %+v", d.Source)
	}
}

func TestBuild_MultipleViews_OneSnapshotEachInOrder(t *testing.T) {
	a := kanbanView()
	b := kanbanView()
	b.AsOf = ms(2026, 11, 22, 10, 0, 0)
	b.Today = "2026-11-22"
	b.Axis = []pulse.AxisDay{{Date: "2026-11-20"}, {Date: "2026-11-21"}, {Date: "2026-11-22"}}
	m := kanbanMeta()
	m.Kind = "range"
	d := build(t, []view.StandupView{b, a}, m)
	if d.Kind != "range" {
		t.Errorf("kind = %q, want range (copied from meta)", d.Kind)
	}
	if len(d.Snapshots) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(d.Snapshots))
	}
	if d.Snapshots[0].Date != "2026-11-22" || d.Snapshots[1].Date != "2026-11-23" {
		t.Errorf("snapshot order = %q, %q; want input order 2026-11-22, 2026-11-23", d.Snapshots[0].Date, d.Snapshots[1].Date)
	}
	if got := d.Snapshots[0].Window; got != (export.DateRange{From: "2026-11-20", To: "2026-11-22"}) {
		t.Errorf("first snapshot window = %+v", got)
	}
}

// ---- lists never null ----

func TestBuildEncode_NoViews_SnapshotsIsEmptyArray(t *testing.T) {
	d := build(t, nil, fixtureMeta())
	if d.Snapshots == nil || len(d.Snapshots) != 0 {
		t.Fatalf("Snapshots = %#v, want empty non-nil", d.Snapshots)
	}
	b := encode(t, d)
	if !bytes.Contains(b, []byte(`"snapshots": []`)) {
		t.Errorf("expected \"snapshots\": [] in:\n%s", b)
	}
	assertNoNullLists(t, b)
}

func TestBuildEncode_EmptyView_AllListsAreEmptyArrays(t *testing.T) {
	d := build(t, []view.StandupView{{}}, kanbanMeta()) // all-zero view: nil slices, empty Axis, nil Sprint
	if len(d.Snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(d.Snapshots))
	}
	s := d.Snapshots[0]
	if s.Lanes == nil || s.People == nil || s.Tickets == nil || s.Backlog == nil {
		t.Errorf("nil list in snapshot: lanes=%v people=%v tickets=%v backlog=%v", s.Lanes == nil, s.People == nil, s.Tickets == nil, s.Backlog == nil)
	}
	if s.Window != (export.DateRange{}) {
		t.Errorf("window = %+v, want empty dates for an empty axis", s.Window)
	}
	b := encode(t, d)
	for _, k := range []string{`"lanes": []`, `"people": []`, `"tickets": []`, `"backlog": []`} {
		if !bytes.Contains(b, []byte(k)) {
			t.Errorf("expected %s in:\n%s", k, b)
		}
	}
	assertNoNullLists(t, b)
}

func TestBuildEncode_TicketWithNilPulseLists_EncodesEmptyArrays(t *testing.T) {
	v := kanbanView()
	v.Tickets[0].Pulse = view.Pulse{} // Segs, Marks, Blocked all nil
	b := encode(t, build(t, []view.StandupView{v}, kanbanMeta()))
	for _, k := range []string{`"segments": []`, `"marks": []`, `"blocked": []`} {
		if !bytes.Contains(b, []byte(k)) {
			t.Errorf("expected %s in:\n%s", k, b)
		}
	}
	assertNoNullLists(t, b)
}

func TestBuildEncode_FixtureHasNoUnexpectedNulls(t *testing.T) {
	assertNoNullLists(t, encode(t, build(t, []view.StandupView{fixtureView()}, fixtureMeta())))
}

// ---- Encode ----

func TestEncode_HandBuiltDocument_MatchesGolden(t *testing.T) {
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	got := encode(t, expectedDoc())
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode(expectedDoc) differs from %s\n--- got ---\n%s\n--- want ---\n%s", goldenPath, got, want)
	}
}

func TestBuildEncode_CurrentView_MatchesGoldenByteForByte(t *testing.T) {
	got := encode(t, build(t, []view.StandupView{fixtureView()}, fixtureMeta()))
	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s (review the diff!)", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("output differs from %s (run with -update after reviewing)\n--- got ---\n%s\n--- want ---\n%s", goldenPath, got, want)
	}
}

func TestEncode_IsTwoSpaceIndentedWithSingleTrailingNewline(t *testing.T) {
	b := encode(t, build(t, []view.StandupView{fixtureView()}, fixtureMeta()))
	if !bytes.HasSuffix(b, []byte("}\n")) || bytes.HasSuffix(b, []byte("\n\n")) {
		t.Errorf("output must end with exactly one newline after the closing brace; tail = %q", b[max(0, len(b)-8):])
	}
	lines := strings.Split(string(b), "\n")
	if lines[0] != "{" || lines[1] != `  "schema": "cuisle.export.v1",` {
		t.Errorf("first lines = %q, %q; want \"{\" then 2-space indented schema", lines[0], lines[1])
	}
	for i, l := range lines {
		if strings.HasPrefix(l, "\t") {
			t.Errorf("line %d is tab-indented: %q", i+1, l)
		}
	}
	var compact, indented bytes.Buffer
	if err := json.Compact(&compact, b); err != nil {
		t.Fatal(err)
	}
	if err := json.Indent(&indented, compact.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	indented.WriteByte('\n')
	if !bytes.Equal(b, indented.Bytes()) {
		t.Errorf("output is not canonical 2-space-indented JSON")
	}
}

func TestEncode_IsDeterministic(t *testing.T) {
	d := build(t, []view.StandupView{fixtureView()}, fixtureMeta())
	a, b := encode(t, d), encode(t, d)
	if !bytes.Equal(a, b) {
		t.Errorf("two Encode calls differ")
	}
}

// objectKeys returns the keys of the JSON object in raw, in document order.
func objectKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not an object: %v %v", tok, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func TestEncode_KeyOrderFollowsStructOrder(t *testing.T) {
	b := encode(t, build(t, []view.StandupView{fixtureView()}, fixtureMeta()))
	var top struct {
		Snapshots []json.RawMessage `json:"snapshots"`
		Standup   json.RawMessage   `json:"standup"`
	}
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		raw  []byte
		want []string
	}{
		{"document", b, []string{"schema", "generatedAt", "generator", "kind", "source", "standup", "snapshots"}},
		{"standup", top.Standup, []string{"id", "name", "space", "board", "scope"}},
		{"snapshot", top.Snapshots[0], []string{"asOf", "date", "timezone", "live", "syncedAt", "sprint", "pulseDays", "window", "lanes", "people", "tickets", "backlog", "counts"}},
	}
	var snap struct {
		Tickets []json.RawMessage `json:"tickets"`
	}
	if err := json.Unmarshal(top.Snapshots[0], &snap); err != nil {
		t.Fatal(err)
	}
	var tk struct {
		Pulse json.RawMessage `json:"pulse"`
	}
	if err := json.Unmarshal(snap.Tickets[0], &tk); err != nil {
		t.Fatal(err)
	}
	cases = append(cases,
		struct {
			name string
			raw  []byte
			want []string
		}{"ticket", snap.Tickets[0], []string{"key", "sourceId", "url", "summary", "type", "assignee", "lane", "status", "daysInLane", "overLimit", "blocked", "doneAt", "pulse"}},
		struct {
			name string
			raw  []byte
			want []string
		}{"pulse", tk.Pulse, []string{"segments", "marks", "blocked"}},
	)
	for _, c := range cases {
		if got := objectKeys(t, c.raw); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s key order = %v, want %v", c.name, got, c.want)
		}
	}
}

// ---- additive-only guard (architecture §3.5) ----

func requireKeys(t *testing.T, where string, v any, keys ...string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Errorf("%s: want object, got %T", where, v)
		return nil
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("%s: v1 field %q is missing (v1 is additive-only: removing or renaming needs cuisle.export.v2)", where, k)
		}
	}
	return m
}

func firstOf(t *testing.T, where string, v any) any {
	t.Helper()
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		t.Fatalf("%s: want non-empty array, got %T", where, v)
	}
	return a[0]
}

func checkV1Fields(t *testing.T, label string, b []byte) {
	t.Helper()
	root := requireKeys(t, label, decodeAny(t, b), "schema", "generatedAt", "generator", "kind", "source", "standup", "snapshots")
	if root == nil {
		return
	}
	requireKeys(t, label+".generator", root["generator"], "name", "version")
	requireKeys(t, label+".source", root["source"], "kind", "baseUrl")
	st := requireKeys(t, label+".standup", root["standup"], "id", "name", "space", "board", "scope")
	if st != nil {
		requireKeys(t, label+".standup.space", st["space"], "key", "name")
		requireKeys(t, label+".standup.board", st["board"], "id", "name", "kind")
	}
	snap := requireKeys(t, label+".snapshots[0]", firstOf(t, label+".snapshots", root["snapshots"]),
		"asOf", "date", "timezone", "live", "syncedAt", "sprint", "pulseDays", "window", "lanes", "people", "tickets", "backlog", "counts")
	if snap == nil {
		return
	}
	requireKeys(t, label+".sprint", snap["sprint"], "id", "name", "startAt", "endAt")
	requireKeys(t, label+".window", snap["window"], "from", "to")
	requireKeys(t, label+".lanes[0]", firstOf(t, "lanes", snap["lanes"]), "lane", "label", "limitDays")
	requireKeys(t, label+".people[0]", firstOf(t, "people", snap["people"]), "id", "name", "included", "active", "done")
	tk := requireKeys(t, label+".tickets[0]", firstOf(t, "tickets", snap["tickets"]),
		"key", "sourceId", "url", "summary", "type", "assignee", "lane", "status", "daysInLane", "overLimit", "blocked", "doneAt", "pulse")
	if tk != nil {
		pl := requireKeys(t, label+".pulse", tk["pulse"], "segments", "marks", "blocked")
		if pl != nil {
			requireKeys(t, label+".pulse.segments[0]", firstOf(t, "segments", pl["segments"]), "lane", "from", "to")
			requireKeys(t, label+".pulse.marks[0]", firstOf(t, "marks", pl["marks"]), "date", "commits", "prs", "comments")
		}
	}
	// The blocked ticket (third) carries the blocked spans.
	tickets := snap["tickets"].([]any)
	if len(tickets) >= 3 {
		if pl, ok := tickets[2].(map[string]any)["pulse"].(map[string]any); ok {
			requireKeys(t, label+".pulse.blocked[0]", firstOf(t, "blocked", pl["blocked"]), "from", "to")
		}
	}
	requireKeys(t, label+".backlog[0]", firstOf(t, "backlog", snap["backlog"]), "key", "summary", "type", "url")
	requireKeys(t, label+".counts", snap["counts"], "active", "unassigned", "done", "backlog", "backlogTotal")
}

func TestAdditiveOnlyGuard_EveryV1FieldIsPresent(t *testing.T) {
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	checkV1Fields(t, "golden", golden)
	checkV1Fields(t, "encoded", encode(t, build(t, []view.StandupView{fixtureView()}, fixtureMeta())))
}

// ---- privacy (AGENTS invariant 12, testing.md "Sinks and export") ----

var emailRE = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

func assertNoSensitive(t *testing.T, label string, b []byte) {
	t.Helper()
	if m := emailRE.Find(b); m != nil {
		t.Errorf("%s: output contains an e-mail-like string %q", label, m)
	}
	walk(decodeAny(t, b), "$", func(path, key string, val any) {
		lk := strings.ToLower(key)
		for _, bad := range []string{"token", "secret", "password", "email", "authorization", "apikey", "body"} {
			if strings.Contains(lk, bad) {
				t.Errorf("%s: key %s.%s looks like a secret / e-mail / comment body", label, path, key)
			}
		}
		if strings.Contains(lk, "comment") {
			if _, isNum := val.(float64); !isNum {
				t.Errorf("%s: key %s.%s is a non-numeric comment field (only the marks[].comments count is allowed)", label, path, key)
			}
		}
	})
}

func TestEncode_Golden_ContainsNoCommentBodiesEmailsOrSecrets(t *testing.T) {
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSensitive(t, "golden file", golden)
	assertNoSensitive(t, "encoded fixture", encode(t, build(t, []view.StandupView{fixtureView()}, fixtureMeta())))
}

func TestBuild_ViewFieldsOutsideTheContract_AreNotExported(t *testing.T) {
	const (
		mail1 = "leak.one@example.com"
		mail2 = "leak.two@example.org"
		sec   = "s3cr3t-sentinel-value"
	)
	v := fixtureView()
	v.Warnings = []string{"comments failed for " + mail1}
	v.PreviewKey = "preview-" + sec
	v.Sync = view.SyncStatus{
		State: "failed", Warnings: []string{"warn " + mail2}, ErrorCode: "auth",
		ErrorMessage: "token " + sec + " rejected for " + mail1,
	}
	v.Tickets[0].Pulse.Label = "label " + mail2
	v.Speakers[0].First = "first-" + sec
	v.Speakers[0].Initials = "init-" + sec
	v.Lanes[0].Columns = []string{"col-" + sec}
	v.Options.DoneLabel = "done-label-" + sec
	b := encode(t, build(t, []view.StandupView{v}, fixtureMeta()))
	for _, s := range []string{mail1, mail2, sec} {
		if bytes.Contains(b, []byte(s)) {
			t.Errorf("sentinel %q from a non-exported view field leaked into the export", s)
		}
	}
	assertNoSensitive(t, "leak probe", b)
}
