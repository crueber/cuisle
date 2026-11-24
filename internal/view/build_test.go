package view

// WP-31 tests: view.Build, BuildConfig, Initials, LaneLabel (architecture.md §3.2 and §5.2, ADR 0008, jira-sync.md §5.6
// and §5.7, ux-spec.md §5.7 and §6.1, data-model.md §2.3, testing.md §3 "Golden view").
//
// Build composes the pulse functions, which are separate work packages. These tests pin Build's WIRING (status->lane map
// from cfg.StatusLanes, blocked hints from snap.Statuses, People passed to Marks, per-lane limits, done window, speaker
// order inputs) with simple hand-computed cases that jira-sync §5 states outright. Every call into a stub goes through a
// recover helper so each test fails individually while the stubs panic.
//
// SPEC-NOTE: sprint labels. DaysLeft = (local date of EndAt) - (local date of asOf), in asOf's location, so it is
// negative for an ended sprint. Labels: 0 "ends today", 1 "ends tomorrow", n>=2 "ends in n days", -1 "ended yesterday"
// (mirrors "ends tomorrow"; the spec only gives "ended 2 days ago"), n<=-2 "ended n days ago". A sprint with a zero EndAt
// never wins "the sprint that ends first" over one with an EndAt; if it is the only active sprint, Sprint is non-nil
// with EndAt 0, DaysLeft 0 and EndsLabel "".
//
// SPEC-NOTE: unmapped statuses. A board-role issue whose status is in no cfg.StatusLanes row is not shown (data-model.md,
// the comment under standup_status_lane), exactly like hidden- and backlog-lane issues.
//
// SPEC-NOTE: Counts. Active = visible tickets in working lanes, Unassigned = of those with Assignee "", Done and Backlog
// are LIST LENGTHS (jira-sync §5.6): Counts.Backlog = len(Backlog) after the BacklogRows cap, while BacklogTotal is the
// separate "how many exist" number.
//
// SPEC-NOTE: speakers. SpeakerInput.WithTickets and HasUnassigned are derived from the VISIBLE tickets (active plus
// done-in-window), so an excluded speaker whose only tickets are hidden or left the done window is not listed. Names come
// from cfg.Speakers. The unassigned entry is {ID "unassigned", Name "Unassigned", First "Unassigned"} and its Initials
// are the two middle dots "··" (U+00B7 twice), taken from design/reference/prototype-logic.js (`ini: '··'`), because
// Initials("Unassigned") would be a misleading "U". Active/Done are the speaker's visible tickets in working lanes / the
// done lane.
//
// SPEC-NOTE: shuffle seed. With ReshuffleDaily, the seed passed to pulse.SpeakerOrder is cfg.ShuffleSeed when
// cfg.ShuffleDate == the local date of asOf (the Shuffle-button seed, or one the app layer already stored), else
// pulse.DailySeed(cfg.StandupID, today) (data-model §2.3: "fnv64(standup_id, today)"). Build itself never stores anything.
//
// SPEC-NOTE: PreviewKey = the ticket (among all visible tickets, rank order) with the highest total marks
// (commits+PRs+comments summed over its marks), ties broken in favour of Blocked, remaining ties by board rank order
// (first wins); "" when there are no tickets. Architecture says "most marks, blocked preferred"; the prototype picked
// blocked first, but the architecture wins. When every ticket has zero marks this degrades to "first blocked ticket, else
// first ticket".
//
// SPEC-NOTE: TicketView.Type is the Member's TypeKind unchanged (subtask/epic included; the TS layer picks the icon,
// ux-spec §5.4). BacklogItem.Type likewise.
//
// SPEC-NOTE: Sync is "filled by the app layer (zero from Build)": the test requires every field zero, with Warnings nil or
// empty, and exempts sync.warnings from the "lists are never null" JSON check.
//
// SPEC-NOTE: Pulse.Label. Build passes pulse.Label the lane label (LaneLabel), DaysInLane, window length, totals from
// pulse.Totals(marks) and blocked days from BlockedDays(spans); the tests pin the lane prefix, the activity clause, the
// blocked clause and whether a "previously <lane>" clause exists (it does iff an earlier lane lies inside the window).
// The number of days in the previous lane (LabelInput.PrevDays) is NOT specified and NOT asserted, so the golden will
// freeze whatever the implementation chooses.
//
// SPEC-NOTE: LastSyncedAt is UnixMilli of snap.LastSyncedAt, and 0 (not time.Time{}.UnixMilli()) when it is zero.
//
// SPEC-NOTE: Initials takes the first rune of the first word and of the LAST word (so three words give first+last:
// "Mary Jane Watson" -> "MW"), upper-cased rune-aware; blank input gives "". SpeakerView.First is the first
// whitespace-separated word as written. LaneLabel is tested for the five lane ids only.
//
// SPEC-NOTE: BuildConfig. Config Lanes are the four walk lanes only (no Done entry; Done is a StandupView lane, the
// Configure form has four steppers). Space is nil iff both SpaceID and SpaceKey are empty; Board is nil and Draft true
// iff BoardID is empty. Columns[].StatusNames are the StatusLane names of that column in cfg.StatusLanes order, [] when
// none. Speaker Note is exactly "no tickets right now" when Active is 0 (or the account is missing from ActiveByAccount),
// otherwise "". FilterCheck is copied; a nil Errors becomes [].

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/pulse"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden.json")

// ── header ──

func TestBuild_HeaderFields(t *testing.T) {
	snap := mainWorld().snap
	v := build(t, snap, mainAsOf)

	if v.StandupID != 7 || v.Rev != 3 {
		t.Errorf("StandupID/Rev = %d/%d, want 7/3", v.StandupID, v.Rev)
	}
	if v.Today != "2026-11-25" {
		t.Errorf("Today = %q, want 2026-11-25", v.Today)
	}
	if v.AsOf != mainAsOf.UnixMilli() || v.GeneratedAt != mainAsOf.UnixMilli() {
		t.Errorf("AsOf/GeneratedAt = %d/%d, want both %d", v.AsOf, v.GeneratedAt, mainAsOf.UnixMilli())
	}
	if !v.Live {
		t.Errorf("Live = false, want true")
	}
	if v.Name != "MOB standup" || v.SpaceKey != "MOB" || v.BoardName != "MOB board" || v.BoardType != "scrum" || v.Scope != "sprint" {
		t.Errorf("names = %q %q %q %q %q", v.Name, v.SpaceKey, v.BoardName, v.BoardType, v.Scope)
	}
	if v.PulseDays != 14 || len(v.Axis) != 14 {
		t.Errorf("PulseDays = %d, len(Axis) = %d, want 14/14", v.PulseDays, len(v.Axis))
	}
	if v.BacklogTotal != 28 {
		t.Errorf("BacklogTotal = %d, want 28", v.BacklogTotal)
	}
	if !reflect.DeepEqual(v.Warnings, []string{"unmapped_statuses"}) {
		t.Errorf("Warnings = %#v, want [unmapped_statuses]", v.Warnings)
	}
	if want := nv(25, 11, 30).UnixMilli(); v.LastSyncedAt != want {
		t.Errorf("LastSyncedAt = %d, want %d", v.LastSyncedAt, want)
	}
}

func TestBuild_HistoricalViewPassesLiveFalseThrough(t *testing.T) {
	snap := mainWorld().snap
	snap.Live = false
	v := build(t, snap, mainAsOf)
	if v.Live {
		t.Errorf("Live = true, want false for a historical snapshot")
	}
	if v.GeneratedAt != v.AsOf || v.AsOf != mainAsOf.UnixMilli() {
		t.Errorf("GeneratedAt/AsOf = %d/%d, want both %d", v.GeneratedAt, v.AsOf, mainAsOf.UnixMilli())
	}
}

func TestBuild_LastSyncedAtZeroWhenNeverSynced(t *testing.T) {
	snap := mainWorld().snap
	snap.LastSyncedAt = time.Time{}
	if v := build(t, snap, mainAsOf); v.LastSyncedAt != 0 {
		t.Errorf("LastSyncedAt = %d, want 0 for never synced", v.LastSyncedAt)
	}
}

func TestBuild_SyncIsLeftZeroForTheAppLayer(t *testing.T) {
	v := build(t, mainWorld().snap, mainAsOf)
	s := v.Sync
	if len(s.Warnings) == 0 {
		s.Warnings = nil
	}
	if !reflect.DeepEqual(s, SyncStatus{}) {
		t.Errorf("Sync = %+v, want zero value", v.Sync)
	}
}

func TestBuild_TodayIsTheLocalDateOfAsOfInItsOwnZone(t *testing.T) {
	auckland := mustLoc("Pacific/Auckland")
	cases := []struct {
		name  string
		asOf  time.Time
		today string
	}{
		{"Chicago 21:00 CST is already the next day in UTC", nv(25, 21, 0), "2026-11-25"},
		{"Chicago 00:30", nv(25, 0, 30), "2026-11-25"},
		{"Auckland 01:00 NZDT is still Nov 25 in UTC", time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC).In(auckland), "2026-11-26"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(tc.asOf, baseConfig())
			v := build(t, w.snap, tc.asOf)
			if v.Today != tc.today {
				t.Errorf("Today = %q, want %q", v.Today, tc.today)
			}
			if v.AsOf != tc.asOf.UnixMilli() {
				t.Errorf("AsOf = %d, want %d", v.AsOf, tc.asOf.UnixMilli())
			}
			if len(v.Axis) == 0 {
				t.Fatalf("Axis is empty")
			}
			if got := v.Axis[len(v.Axis)-1].Date; got != tc.today {
				t.Errorf("last axis date = %q, want today %q", got, tc.today)
			}
		})
	}
}

func TestBuild_AxisFlagsWeekendsAndMondays(t *testing.T) {
	v := build(t, mainWorld().snap, mainAsOf)
	if len(v.Axis) != 14 {
		t.Fatalf("len(Axis) = %d, want 14", len(v.Axis))
	}
	// Window = Nov 12 (k=0) .. Nov 25 (k=13).
	if v.Axis[0].Date != "2026-11-12" || v.Axis[0].Label != "12" {
		t.Errorf("Axis[0] = %+v, want 2026-11-12 / 12", v.Axis[0])
	}
	if v.Axis[13].Date != "2026-11-25" || v.Axis[13].Label != "25" {
		t.Errorf("Axis[13] = %+v, want 2026-11-25 / 25", v.Axis[13])
	}
	weekend := map[int]bool{2: true, 3: true, 9: true, 10: true} // Nov 14,15,21,22
	monday := map[int]bool{4: true, 11: true}                    // Nov 16, 23
	for k, d := range v.Axis {
		if d.Weekend != weekend[k] {
			t.Errorf("Axis[%d] (%s) Weekend = %v, want %v", k, d.Date, d.Weekend, weekend[k])
		}
		if d.Monday != monday[k] {
			t.Errorf("Axis[%d] (%s) Monday = %v, want %v", k, d.Date, d.Monday, monday[k])
		}
	}
}

func TestBuild_AxisHasOneEntryPerLocalDayAcrossFallBack(t *testing.T) {
	asOf := ct(time.November, 3, 12, 0) // DST ended Sunday Nov 1 2026
	w := newWorld(asOf, baseConfig())
	v := build(t, w.snap, asOf)
	if len(v.Axis) != 14 {
		t.Fatalf("len(Axis) = %d, want 14", len(v.Axis))
	}
	start := time.Date(2026, 10, 21, 0, 0, 0, 0, chi)
	for k, d := range v.Axis {
		day := start.AddDate(0, 0, k)
		want := pulse.AxisDay{
			Date: day.Format("2006-01-02"), Label: strconv.Itoa(day.Day()),
			Weekend: day.Weekday() == time.Saturday || day.Weekday() == time.Sunday, Monday: day.Weekday() == time.Monday,
		}
		if d != want {
			t.Errorf("Axis[%d] = %+v, want %+v", k, d, want)
		}
	}
}

// ── sprint ──

func TestBuild_Sprint_ScrumFieldsAndLabels(t *testing.T) {
	cases := []struct {
		name      string
		endAt     time.Time
		daysLeft  int
		endsLabel string
	}{
		{"ends later today (local)", nv(25, 17, 0), 0, "ends today"},
		{"ends 23:59 local today", nv(25, 23, 59), 0, "ends today"},
		{"evening local that is already tomorrow in UTC is still today", time.Date(2026, 11, 26, 2, 0, 0, 0, time.UTC), 0, "ends today"},
		{"00:30 local tomorrow", nv(26, 0, 30), 1, "ends tomorrow"},
		{"in 2 days", nv(27, 10, 0), 2, "ends in 2 days"},
		{"in 3 days", nv(28, 10, 0), 3, "ends in 3 days"},
		{"ended yesterday", nv(24, 17, 0), -1, "ended yesterday"},
		{"ended 2 days ago", nv(23, 17, 0), -2, "ended 2 days ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(mainAsOf, baseConfig())
			start := nv(18, 9, 0)
			w.snap.Sprints = []model.Sprint{{ID: "s-42", BoardID: "42", Name: "Sprint 42", State: "active", StartAt: start, EndAt: tc.endAt}}
			v := build(t, w.snap, mainAsOf)
			if v.Sprint == nil {
				t.Fatalf("Sprint = nil, want a SprintInfo")
			}
			want := SprintInfo{ID: "s-42", Name: "Sprint 42", StartAt: start.UnixMilli(), EndAt: tc.endAt.UnixMilli(), DaysLeft: tc.daysLeft, EndsLabel: tc.endsLabel}
			if *v.Sprint != want {
				t.Errorf("Sprint = %+v, want %+v", *v.Sprint, want)
			}
		})
	}
}

func TestBuild_Sprint_MultipleActiveSprintsPicksTheOneEndingFirst(t *testing.T) {
	a := model.Sprint{ID: "a", BoardID: "42", Name: "A", State: "active", StartAt: nv(10, 9, 0), EndAt: nv(30, 17, 0)}
	b := model.Sprint{ID: "b", BoardID: "42", Name: "B", State: "active", StartAt: nv(10, 9, 0), EndAt: nv(27, 17, 0)}
	c := model.Sprint{ID: "c", BoardID: "42", Name: "C", State: "active", StartAt: nv(10, 9, 0)} // EndAt unknown
	for _, order := range [][]model.Sprint{{a, b, c}, {c, b, a}, {b, a, c}} {
		cfg := baseConfig()
		cfg.ActiveSprintIDs = []string{"a", "b", "c"}
		w := newWorld(mainAsOf, cfg)
		w.snap.Sprints = order
		v := build(t, w.snap, mainAsOf)
		if v.Sprint == nil || v.Sprint.ID != "b" {
			t.Errorf("order %v: Sprint = %+v, want sprint b (ends first; the zero EndAt must not win)", []string{order[0].ID, order[1].ID, order[2].ID}, v.Sprint)
			continue
		}
		if v.Sprint.EndsLabel != "ends in 2 days" || v.Sprint.DaysLeft != 2 {
			t.Errorf("Sprint = %+v, want ends in 2 days / 2", *v.Sprint)
		}
	}
}

func TestBuild_Sprint_OnlyUnknownEndGivesEmptyLabel(t *testing.T) {
	w := newWorld(mainAsOf, baseConfig())
	w.snap.Sprints = []model.Sprint{{ID: "s-42", BoardID: "42", Name: "Sprint 42", State: "active", StartAt: nv(18, 9, 0)}}
	v := build(t, w.snap, mainAsOf)
	if v.Sprint == nil {
		t.Fatalf("Sprint = nil, want non-nil (one active sprint)")
	}
	if v.Sprint.EndAt != 0 || v.Sprint.DaysLeft != 0 || v.Sprint.EndsLabel != "" {
		t.Errorf("Sprint = %+v, want EndAt 0, DaysLeft 0, EndsLabel empty", *v.Sprint)
	}
}

func TestBuild_Sprint_NilForScrumWithoutActiveSprint(t *testing.T) {
	cfg := baseConfig()
	cfg.ActiveSprintIDs = nil
	w := newWorld(mainAsOf, cfg)
	w.snap.Sprints = nil
	if v := build(t, w.snap, mainAsOf); v.Sprint != nil {
		t.Errorf("Sprint = %+v, want nil", *v.Sprint)
	}
}

func kanbanWorld() *world {
	cfg := baseConfig()
	cfg.BoardType = "kanban"
	cfg.Scope = "board"
	cfg.DoneWindow = "14d"
	cfg.ActiveSprintIDs = nil
	return newWorld(mainAsOf, cfg)
}

func TestBuild_Kanban_HasNoSprintAndDoneLabelIsFourteenDays(t *testing.T) {
	w := kanbanWorld()
	w.add(newTkt(1, "K-1", "task", "a1", sTodo, nv(24, 9, 0), chg{nv(24, 10, 0), sProg}))
	v := build(t, w.snap, mainAsOf)
	if v.Sprint != nil {
		t.Errorf("Sprint = %+v, want nil for kanban", *v.Sprint)
	}
	if v.BoardType != "kanban" || v.Scope != "board" {
		t.Errorf("BoardType/Scope = %q/%q, want kanban/board", v.BoardType, v.Scope)
	}
	if v.Options.DoneWindow != "14d" || v.Options.DoneLabel != "14 days" {
		t.Errorf("DoneWindow/DoneLabel = %q/%q, want 14d / 14 days", v.Options.DoneWindow, v.Options.DoneLabel)
	}
	if got := keys(v); !reflect.DeepEqual(got, []string{"K-1"}) {
		t.Errorf("tickets = %v, want [K-1]", got)
	}
}

// ── lanes ──

func TestBuild_Lanes_WalkOrderThenDoneWithLabelsLimitsAndColumns(t *testing.T) {
	v := build(t, mainWorld().snap, mainAsOf)
	want := []LaneInfo{
		{Lane: "qa", Label: "QA", LimitDays: 3, Columns: []string{"Testing"}},
		{Lane: "review", Label: "In Review", LimitDays: 2, Columns: []string{"Review"}},
		{Lane: "prog", Label: "In Progress", LimitDays: 5, Columns: []string{"In Progress", "Blocked"}},
		{Lane: "todo", Label: "To Do", LimitDays: 10, Columns: []string{"To Do"}},
		{Lane: "done", Label: "Done", LimitDays: 0, Columns: []string{"Done"}},
	}
	if !reflect.DeepEqual(v.Lanes, want) {
		t.Errorf("Lanes =\n %+v\nwant\n %+v", v.Lanes, want)
	}
}

func TestBuild_Lanes_LaneWithNoMappedColumnHasEmptyNonNilColumns(t *testing.T) {
	cfg := baseConfig()
	var cols []model.ColumnLane
	for _, c := range cfg.Columns {
		if c.ColumnName != "Testing" {
			cols = append(cols, c)
		}
	}
	cfg.Columns = cols
	var sl []model.StatusLane
	for _, r := range cfg.StatusLanes {
		if r.ColumnName != "Testing" {
			sl = append(sl, r)
		}
	}
	cfg.StatusLanes = sl
	v := build(t, newWorld(mainAsOf, cfg).snap, mainAsOf)
	if len(v.Lanes) == 0 {
		t.Fatalf("Lanes is empty")
	}
	if v.Lanes[0].Lane != "qa" || v.Lanes[0].Columns == nil || len(v.Lanes[0].Columns) != 0 {
		t.Errorf("qa lane = %+v, want Columns empty and non-nil", v.Lanes[0])
	}
}

func TestBuild_Lanes_FollowTheConfiguredWalkOrder(t *testing.T) {
	cfg := baseConfig()
	cfg.Lanes = []model.LaneConfig{
		{Lane: model.LaneProg, Position: 0, LimitDays: 0},
		{Lane: model.LaneTodo, Position: 1, LimitDays: 4},
		{Lane: model.LaneQA, Position: 2, LimitDays: 1},
		{Lane: model.LaneReview, Position: 3, LimitDays: 99},
	}
	v := build(t, newWorld(mainAsOf, cfg).snap, mainAsOf)
	var got []string
	limits := map[string]int{}
	for _, l := range v.Lanes {
		got = append(got, l.Lane)
		limits[l.Lane] = l.LimitDays
	}
	if want := []string{"prog", "todo", "qa", "review", "done"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lane order = %v, want %v", got, want)
	}
	if limits["prog"] != 0 || limits["todo"] != 4 || limits["qa"] != 1 || limits["review"] != 99 {
		t.Errorf("limits = %v", limits)
	}
}

// ── tickets ──

type wantTicket struct {
	key, summary, url, typ, assignee, lane, statusName, sourceID string
	id                                                           int64
	days                                                         int
	over, blocked                                                bool
	doneAt                                                       int64
}

func mainWantTickets() []wantTicket {
	return []wantTicket{
		{key: "MOB-2", id: 2, sourceID: "102", typ: "bug", assignee: "a2", lane: "review", statusName: "In Review", days: 1},
		{key: "MOB-5", id: 5, sourceID: "105", typ: "story", assignee: "a2", lane: "done", statusName: "Done", days: 2, doneAt: nv(23, 14, 0).UnixMilli()},
		{key: "MOB-1", id: 1, sourceID: "101", typ: "story", assignee: "a1", lane: "prog", statusName: "In Progress", days: 7, over: true},
		{key: "MOB-10", id: 10, sourceID: "1010", typ: "subtask", assignee: "", lane: "todo", statusName: "To Do", days: 1},
		{key: "MOB-3", id: 3, sourceID: "103", typ: "task", assignee: "", lane: "qa", statusName: "QA", days: 5, over: true, blocked: true},
		{key: "MOB-9", id: 9, sourceID: "109", typ: "task", assignee: "a3", lane: "todo", statusName: "To Do", days: 16, over: true},
		{key: "MOB-4", id: 4, sourceID: "104", typ: "spike", assignee: "a1", lane: "prog", statusName: "Blocked", days: 1, blocked: true},
		{key: "MOB-11", id: 11, sourceID: "1011", typ: "epic", assignee: "a3", lane: "done", statusName: "Done", days: 5, doneAt: nv(20, 10, 0).UnixMilli()},
	}
}

func TestBuild_Tickets_VisibleOnesInBoardRankOrder(t *testing.T) {
	v := build(t, mainWorld().snap, mainAsOf)
	want := []string{"MOB-2", "MOB-5", "MOB-1", "MOB-10", "MOB-3", "MOB-9", "MOB-4", "MOB-11"}
	if got := keys(v); !reflect.DeepEqual(got, want) {
		t.Errorf("ticket keys = %v\n want %v (rank order kept, not lane order; hidden, backlog-lane, unmapped and backlog-role excluded)", got, want)
	}
}

func TestBuild_Tickets_FieldsPerTicket(t *testing.T) {
	v := build(t, mainWorld().snap, mainAsOf)
	for _, w := range mainWantTickets() {
		t.Run(w.key, func(t *testing.T) {
			got := ticketByKey(t, v, w.key)
			got.Pulse = Pulse{}
			want := TicketView{
				ID: w.id, SourceID: w.sourceID, Key: w.key, Summary: "Summary of " + w.key, URL: urlPrefix + w.key,
				Type: w.typ, Assignee: w.assignee, Lane: w.lane, StatusName: w.statusName,
				DaysInLane: w.days, OverLimit: w.over, Blocked: w.blocked, DoneAt: w.doneAt,
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ticket =\n %+v\nwant\n %+v", got, want)
			}
		})
	}
}

func TestBuild_Tickets_PulseSegmentsMarksBlockedAndLabel(t *testing.T) {
	v := build(t, mainWorld().snap, mainAsOf)
	seg := func(lane string, from, to int) PulseSeg { return PulseSeg{Lane: lane, From: from, To: to} }
	cm := func(day int) PulseMark { return PulseMark{Day: day, Comments: 1} }
	cases := []struct {
		key        string
		segs       []PulseSeg
		marks      []PulseMark
		blocked    []PulseSpan
		labelStart string
		labelHas   []string
		labelNot   []string
	}{
		{"MOB-1", []PulseSeg{seg("todo", 0, 6), seg("prog", 6, 14)}, nil, nil, "In Progress 7 days", []string{"previously To Do", "no activity in 14 days"}, nil},
		{
			"MOB-2", []PulseSeg{seg("prog", 0, 12), seg("review", 12, 14)},
			[]PulseMark{{Day: 12, Commits: 2, Comments: 1}, {Day: 13, PRs: 1}}, nil,
			"In Review 1 day", []string{"previously In Progress", "2 commits, 1 PR update, 1 comment in 14 days"}, nil,
		},
		{
			"MOB-3", []PulseSeg{seg("prog", 0, 2), seg("review", 2, 8), seg("qa", 8, 14)},
			[]PulseMark{cm(8), cm(9), cm(10), cm(11), cm(12)}, []PulseSpan{{From: 12, To: 13}},
			"QA 5 days", []string{"previously In Review", "5 comments in 14 days", "; blocked 2 days"}, nil,
		},
		{"MOB-4", []PulseSeg{seg("prog", 12, 14)}, nil, []PulseSpan{{From: 12, To: 13}}, "In Progress 1 day", []string{"; blocked 2 days"}, []string{"previously"}},
		{"MOB-5", []PulseSeg{seg("prog", 0, 11), seg("done", 11, 14)}, nil, nil, "Done 2 days ago", []string{"previously In Progress"}, nil},
		{"MOB-9", []PulseSeg{seg("todo", 0, 14)}, nil, nil, "To Do 16 days", nil, []string{"previously"}},
		{"MOB-10", []PulseSeg{seg("todo", 12, 14)}, nil, nil, "To Do 1 day", nil, []string{"previously"}},
		{"MOB-11", []PulseSeg{seg("todo", 0, 8), seg("done", 8, 14)}, nil, nil, "Done 5 days ago", []string{"previously To Do"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			p := ticketByKey(t, v, tc.key).Pulse
			if !reflect.DeepEqual(p.Segs, tc.segs) {
				t.Errorf("Segs = %+v, want %+v", p.Segs, tc.segs)
			}
			if p.Marks == nil || (len(p.Marks) == 0) != (len(tc.marks) == 0) || (len(tc.marks) > 0 && !reflect.DeepEqual(p.Marks, tc.marks)) {
				t.Errorf("Marks = %#v, want %+v (empty must be non-nil)", p.Marks, tc.marks)
			}
			if p.Blocked == nil || (len(p.Blocked) == 0) != (len(tc.blocked) == 0) || (len(tc.blocked) > 0 && !reflect.DeepEqual(p.Blocked, tc.blocked)) {
				t.Errorf("Blocked = %#v, want %+v (empty must be non-nil)", p.Blocked, tc.blocked)
			}
			if !strings.HasPrefix(p.Label, tc.labelStart) {
				t.Errorf("Label = %q, want prefix %q", p.Label, tc.labelStart)
			}
			for _, s := range tc.labelHas {
				if !strings.Contains(p.Label, s) {
					t.Errorf("Label = %q, want it to contain %q", p.Label, s)
				}
			}
			for _, s := range tc.labelNot {
				if strings.Contains(p.Label, s) {
					t.Errorf("Label = %q, want it NOT to contain %q (no earlier lane inside the window)", p.Label, s)
				}
			}
		})
	}
}

func TestBuild_Tickets_PulseInvariantsHoldForEveryTicket(t *testing.T) {
	v := build(t, mainWorld().snap, mainAsOf)
	assertPulseInvariants(t, v)
}

func assertPulseInvariants(t *testing.T, v StandupView) {
	t.Helper()
	for _, tk := range v.Tickets {
		p := tk.Pulse
		if len(p.Segs) == 0 || p.Marks == nil || p.Blocked == nil || p.Label == "" {
			t.Errorf("%s: Pulse not fully populated: segs=%d marks-nil=%v blocked-nil=%v label=%q", tk.Key, len(p.Segs), p.Marks == nil, p.Blocked == nil, p.Label)
			continue
		}
		if p.Segs[len(p.Segs)-1].To != v.PulseDays {
			t.Errorf("%s: last seg ends at %d, want %d", tk.Key, p.Segs[len(p.Segs)-1].To, v.PulseDays)
		}
		for i, s := range p.Segs {
			if s.From < 0 || s.From >= s.To || s.To > v.PulseDays {
				t.Errorf("%s: seg %d = %+v out of range", tk.Key, i, s)
			}
			if i > 0 && p.Segs[i-1].To != s.From {
				t.Errorf("%s: segs not contiguous at %d: %+v then %+v", tk.Key, i, p.Segs[i-1], s)
			}
		}
	}
}

func TestBuild_Tickets_DaysInLaneIsMeasuredToAsOfNotNow(t *testing.T) {
	// The same snapshot viewed two days later: MOB-1 has been in progress 9 days.
	asOf := nv(27, 12, 0)
	w := mainWorld()
	w.snap.AsOf = asOf
	v := build(t, w.snap, asOf)
	if got := ticketByKey(t, v, "MOB-1").DaysInLane; got != 9 {
		t.Errorf("MOB-1 DaysInLane at Nov 27 = %d, want 9", got)
	}
	if v.Today != "2026-11-27" {
		t.Errorf("Today = %q, want 2026-11-27", v.Today)
	}
}

// ── done window ──

func TestBuild_DoneWindow_Table(t *testing.T) {
	mon := nv(23, 10, 0)
	cases := []struct {
		name   string
		asOf   time.Time
		scope  string
		window string
		doneAt []time.Time
		spr    [][]string
		want   []string
		label  string
	}{
		{
			"14d: local midnight 14 days ago is inclusive", mainAsOf, "board", "14d",
			[]time.Time{nv(10, 23, 30), nv(11, 0, 0), nv(25, 9, 0), ct(time.October, 1, 9, 0)}, nil,
			[]string{"D-2", "D-3"}, "14 days",
		},
		{
			"yesterday on a Wednesday: since Tuesday 00:00", mainAsOf, "board", "yesterday",
			[]time.Time{nv(23, 23, 59), nv(24, 0, 0), nv(25, 8, 0)}, nil,
			[]string{"D-2", "D-3"}, "since Tuesday",
		},
		{
			"yesterday on a Monday: since Friday 00:00", mon, "board", "yesterday",
			[]time.Time{nv(19, 16, 0), nv(20, 9, 0), nv(21, 3, 0)}, nil,
			[]string{"D-2", "D-3"}, "since Friday",
		},
		{
			"sprint window, sprint scope: every done ticket", mainAsOf, "sprint", "sprint",
			[]time.Time{ct(time.October, 1, 9, 0), nv(10, 9, 0), nv(25, 9, 0)}, nil,
			[]string{"D-1", "D-2", "D-3"}, "this sprint",
		},
		{
			"sprint window, board scope: only tickets in an active sprint", mainAsOf, "board", "sprint",
			[]time.Time{nv(20, 9, 0), nv(21, 9, 0), nv(22, 9, 0), nv(23, 9, 0)},
			[][]string{{"s-41"}, {"s-42"}, {"s-41", "s-42"}, nil},
			[]string{"D-2", "D-3"}, "this sprint",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doneWorld(tc.asOf, tc.scope, tc.window, tc.doneAt, tc.spr)
			v := build(t, w.snap, tc.asOf)
			if got := keys(v); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("done tickets = %v, want %v", got, tc.want)
			}
			if v.Counts.Done != len(tc.want) || v.Counts.Active != 0 {
				t.Errorf("Counts = %+v, want Done %d Active 0", v.Counts, len(tc.want))
			}
			if v.Options.DoneLabel != tc.label || v.Options.DoneWindow != tc.window {
				t.Errorf("DoneWindow/DoneLabel = %q/%q, want %q/%q", v.Options.DoneWindow, v.Options.DoneLabel, tc.window, tc.label)
			}
			for _, tk := range v.Tickets {
				if tk.Lane != "done" || tk.DoneAt == 0 {
					t.Errorf("%s: Lane/DoneAt = %q/%d, want done with a DoneAt", tk.Key, tk.Lane, tk.DoneAt)
				}
			}
		})
	}
}

func TestBuild_DoneWindow_DoneAtIsWhenTheTicketEnteredDone(t *testing.T) {
	done := nv(25, 9, 15)
	w := doneWorld(mainAsOf, "sprint", "sprint", []time.Time{done}, nil)
	v := build(t, w.snap, mainAsOf)
	if len(v.Tickets) != 1 || v.Tickets[0].DoneAt != done.UnixMilli() {
		t.Fatalf("tickets = %+v, want one with DoneAt %d", v.Tickets, done.UnixMilli())
	}
	if v.Tickets[0].DaysInLane != 1 || v.Tickets[0].OverLimit || v.Tickets[0].Blocked {
		t.Errorf("done ticket = %+v, want DaysInLane 1, not over limit, not blocked", v.Tickets[0])
	}
}

func TestBuild_DoneWindow_SpeakerDoneCountsFollowTheWindow(t *testing.T) {
	// Window "yesterday" (since Tuesday): MOB-5 (Nov 23) and MOB-11 (Nov 20) leave the list.
	cfg := baseConfig()
	cfg.DoneWindow = "yesterday"
	w := mainWorld()
	w.snap.Config = cfg
	v := build(t, w.snap, mainAsOf)
	if v.Counts.Done != 0 {
		t.Errorf("Counts.Done = %d, want 0", v.Counts.Done)
	}
	for _, s := range v.Speakers {
		if s.Done != 0 {
			t.Errorf("speaker %s Done = %d, want 0", s.ID, s.Done)
		}
	}
}

// ── counts, backlog ──

func TestBuild_Counts_ActiveUnassignedDoneBacklog(t *testing.T) {
	v := build(t, mainWorld().snap, mainAsOf)
	if want := (Counts{Active: 6, Unassigned: 2, Done: 2, Backlog: 2}); v.Counts != want {
		t.Errorf("Counts = %+v, want %+v", v.Counts, want)
	}
}

func TestBuild_Backlog_TopRowsByRankWithURLs(t *testing.T) {
	snap := mainWorld().snap
	v := build(t, snap, mainAsOf)
	localID := map[string]int64{}
	for _, m := range snap.Members {
		localID[m.Issue.Key] = m.LocalID
	}
	want := []BacklogItem{ // IssueID is the member's local id, so a backlog row can open the ticket modal (F12)
		{IssueID: localID["MOB-101"], Key: "MOB-101", Summary: "Summary of MOB-101", Type: "story", URL: urlPrefix + "MOB-101"},
		{IssueID: localID["MOB-102"], Key: "MOB-102", Summary: "Summary of MOB-102", Type: "bug", URL: urlPrefix + "MOB-102"},
	}
	if !reflect.DeepEqual(v.Backlog, want) {
		t.Errorf("Backlog = %+v, want %+v", v.Backlog, want)
	}
	if v.BacklogTotal != 28 {
		t.Errorf("BacklogTotal = %d, want 28 (not capped by BacklogRows)", v.BacklogTotal)
	}
}

func TestBuild_Backlog_RowsSettingCapsAndZeroHidesIt(t *testing.T) {
	cases := []struct {
		rows      int
		wantKeys  []string
		wantCount int
	}{
		{0, []string{}, 0},
		{1, []string{"MOB-101"}, 1},
		{50, []string{"MOB-101", "MOB-102", "MOB-103"}, 3},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("rows=%d", tc.rows), func(t *testing.T) {
			w := mainWorld()
			w.snap.Config.BacklogRows = tc.rows
			v := build(t, w.snap, mainAsOf)
			got := []string{}
			for _, b := range v.Backlog {
				got = append(got, b.Key)
			}
			if !reflect.DeepEqual(got, tc.wantKeys) {
				t.Errorf("backlog keys = %v, want %v", got, tc.wantKeys)
			}
			if v.Backlog == nil {
				t.Errorf("Backlog is nil, want an empty list")
			}
			if v.Counts.Backlog != tc.wantCount {
				t.Errorf("Counts.Backlog = %d, want %d", v.Counts.Backlog, tc.wantCount)
			}
			if v.BacklogTotal != 28 {
				t.Errorf("BacklogTotal = %d, want 28", v.BacklogTotal)
			}
		})
	}
}

func TestBuild_Backlog_UnknownTotalIsKeptAsMinusOne(t *testing.T) {
	w := mainWorld()
	w.snap.BacklogTotal = -1
	if v := build(t, w.snap, mainAsOf); v.BacklogTotal != -1 {
		t.Errorf("BacklogTotal = %d, want -1", v.BacklogTotal)
	}
}

// ── options, warnings, preview ──

func TestBuild_Options_FromConfigAndSnapshot(t *testing.T) {
	v := build(t, mainWorld().snap, mainAsOf)
	want := ViewOptions{
		RowsMode: "roomy", ShowCommits: true, ShowPRs: false, ShowComments: true, ShadeWeekends: true,
		DoneWindow: "sprint", DoneLabel: "this sprint", DevActivity: "ok",
	}
	if v.Options != want {
		t.Errorf("Options = %+v, want %+v", v.Options, want)
	}
}

func TestBuild_Options_DevActivityAndFlagsPassThrough(t *testing.T) {
	w := mainWorld()
	w.snap.DevActivity = "unavailable"
	w.snap.Config.ShowCommits = false
	w.snap.Config.ShowPRs = true
	w.snap.Config.ShowComments = false
	w.snap.Config.ShadeWeekends = false
	w.snap.Config.RowsMode = "fit"
	v := build(t, w.snap, mainAsOf)
	want := ViewOptions{
		RowsMode: "fit", ShowCommits: false, ShowPRs: true, ShowComments: false, ShadeWeekends: false,
		DoneWindow: "sprint", DoneLabel: "this sprint", DevActivity: "unavailable",
	}
	if v.Options != want {
		t.Errorf("Options = %+v, want %+v", v.Options, want)
	}
}

func TestBuild_Warnings_CopiedFromTheSnapshot(t *testing.T) {
	w := mainWorld()
	w.snap.Warnings = []string{"dev_activity_unavailable", "unmapped_statuses"}
	v := build(t, w.snap, mainAsOf)
	if want := []string{"dev_activity_unavailable", "unmapped_statuses"}; !reflect.DeepEqual(v.Warnings, want) {
		t.Errorf("Warnings = %#v, want %#v", v.Warnings, want)
	}
	w.snap.Warnings = nil
	v = build(t, w.snap, mainAsOf)
	if v.Warnings == nil || len(v.Warnings) != 0 {
		t.Errorf("Warnings = %#v, want empty non-nil", v.Warnings)
	}
}

func TestBuild_PreviewKey_Rule(t *testing.T) {
	commentsOnDays := func(prefix, author string, days ...int) []model.Comment {
		var out []model.Comment
		for i, d := range days {
			out = append(out, comment(fmt.Sprintf("%s%d", prefix, i), nv(d, 13, 0), author))
		}
		return out
	}
	mk := func(k *tkt, cs []model.Comment) *tkt { return k.withComments(cs...) }
	newW := func() *world { return newWorld(mainAsOf, baseConfig()) }
	cases := []struct {
		name string
		w    func() *world
		want string
	}{
		{"no tickets", newW, ""},
		{
			"mainWorld: the blocked ticket also has the most marks", func() *world { return mainWorld() }, "MOB-3",
		},
		{
			"the single ticket with marks wins over earlier ones without", func() *world {
				w := newW()
				w.add(newTkt(1, "P-1", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}))
				w.add(mk(newTkt(2, "P-2", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}), commentsOnDays("x", "a1", 20)))
				return w
			}, "P-2",
		},
		{
			"tie on marks: blocked preferred even when ranked later", func() *world {
				w := newW()
				w.add(mk(newTkt(1, "T-1", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}), commentsOnDays("a", "a1", 18, 19, 20)))
				w.add(mk(newTkt(2, "T-2", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sBlocked}), commentsOnDays("b", "a1", 18, 19, 20)))
				w.add(mk(newTkt(3, "T-3", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}), commentsOnDays("c", "a1", 20)))
				return w
			}, "T-2",
		},
		{
			"more marks beat blocked", func() *world {
				w := newW()
				w.add(mk(newTkt(1, "M-1", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}), commentsOnDays("a", "a1", 17, 18, 19, 20)))
				w.add(mk(newTkt(2, "M-2", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sBlocked}), commentsOnDays("b", "a1", 18, 19, 20)))
				return w
			}, "M-1",
		},
		{
			"full tie, none blocked: first in rank order", func() *world {
				w := newW()
				w.add(mk(newTkt(1, "F-1", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}), commentsOnDays("a", "a1", 20)))
				w.add(mk(newTkt(2, "F-2", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}), commentsOnDays("b", "a1", 21)))
				return w
			}, "F-1",
		},
		{
			"no marks anywhere: first blocked ticket", func() *world {
				w := newW()
				w.add(newTkt(1, "Z-1", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}))
				w.add(newTkt(2, "Z-2", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sBlocked}))
				return w
			}, "Z-2",
		},
		{
			"no marks, nothing blocked: first ticket", func() *world {
				w := newW()
				w.add(newTkt(1, "Y-1", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}))
				w.add(newTkt(2, "Y-2", "task", "a1", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}))
				return w
			}, "Y-1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.w()
			if got := build(t, w.snap, mainAsOf).PreviewKey; got != tc.want {
				t.Errorf("PreviewKey = %q, want %q", got, tc.want)
			}
		})
	}
}

// ── speakers ──

func TestBuild_Speakers_EffectiveOrderAndFields(t *testing.T) {
	v := build(t, mainWorld().snap, mainAsOf)
	want := []SpeakerView{
		{ID: "a1", Name: "Iris Calloway", First: "Iris", Initials: "IC", Included: true, Active: 2, Done: 0},
		{ID: "a2", Name: "Silas Webb", First: "Silas", Initials: "SW", Included: true, Active: 1, Done: 1},
		{ID: "a4", Name: "Cher", First: "Cher", Initials: "C", Included: true},
		{ID: "unassigned", Name: "Unassigned", First: "Unassigned", Initials: "··", Unassigned: true, Included: true, Active: 2, Done: 0},
		{ID: "a3", Name: "Amara Osei", First: "Amara", Initials: "AO", Included: false, Active: 1, Done: 1},
	}
	if !reflect.DeepEqual(v.Speakers, want) {
		t.Errorf("Speakers =\n %+v\nwant\n %+v\n(a5 is excluded and only has a hidden-lane ticket, so it is not listed)", v.Speakers, want)
	}
}

func TestBuild_Speakers_UnassignedPosition(t *testing.T) {
	cases := []struct {
		pos          string
		wantIDs      []string
		wantUnIncl   bool
		dropUnassign bool
	}{
		{"first", []string{"unassigned", "a1", "a2", "a4", "a3"}, true, false},
		{"last", []string{"a1", "a2", "a4", "unassigned", "a3"}, true, false},
		{"skip", []string{"a1", "a2", "a4", "unassigned", "a3"}, false, false},
		{"first", []string{"a1", "a2", "a4", "a3"}, false, true},
		{"last", []string{"a1", "a2", "a4", "a3"}, false, true},
	}
	for _, tc := range cases {
		name := tc.pos
		if tc.dropUnassign {
			name += "/no unassigned tickets: no entry"
		}
		t.Run(name, func(t *testing.T) {
			w := mainWorld()
			w.snap.Config.UnassignedPosition = tc.pos
			if tc.dropUnassign {
				removeMembers(&w.snap, "MOB-3", "MOB-10")
			}
			v := build(t, w.snap, mainAsOf)
			if got := speakerIDs(v); !reflect.DeepEqual(got, tc.wantIDs) {
				t.Fatalf("speaker ids = %v, want %v", got, tc.wantIDs)
			}
			for _, s := range v.Speakers {
				if s.Unassigned {
					if s.Included != tc.wantUnIncl {
						t.Errorf("unassigned Included = %v, want %v", s.Included, tc.wantUnIncl)
					}
				}
			}
		})
	}
}

func TestBuild_Speakers_ExcludedAreListedOnlyWhileTheyHaveVisibleTickets(t *testing.T) {
	// a3 (excluded) has MOB-9 (active) and MOB-11 (done). Drop MOB-9 and move the done window to "yesterday":
	// MOB-11 (Nov 20) leaves the done list, so a3 has no visible ticket and disappears.
	w := mainWorld()
	removeMembers(&w.snap, "MOB-9")
	w.snap.Config.DoneWindow = "yesterday"
	v := build(t, w.snap, mainAsOf)
	for _, s := range v.Speakers {
		if s.ID == "a3" || s.ID == "a5" {
			t.Errorf("speaker %s listed, want absent (excluded without visible tickets)", s.ID)
		}
	}
	// Same, but keep the done window wide: the done ticket alone keeps a3 listed, Included=false, Active 0, Done 1.
	w = mainWorld()
	removeMembers(&w.snap, "MOB-9")
	v = build(t, w.snap, mainAsOf)
	var got *SpeakerView
	for i := range v.Speakers {
		if v.Speakers[i].ID == "a3" {
			got = &v.Speakers[i]
		}
	}
	if got == nil || got.Included || got.Active != 0 || got.Done != 1 {
		t.Errorf("a3 = %+v, want listed with Included false, Active 0, Done 1", got)
	}
}

func TestBuild_Speakers_IncludedOrderFollowsConfigPosition(t *testing.T) {
	w := mainWorld()
	w.snap.Config.Speakers = []model.SpeakerConfig{
		{AccountID: "a4", Name: "Cher", Position: 0, Included: true},
		{AccountID: "a2", Name: "Silas Webb", Position: 1, Included: true},
		{AccountID: "a1", Name: "Iris Calloway", Position: 2, Included: true},
		{AccountID: "a3", Name: "Amara Osei", Position: 3, Included: false},
	}
	v := build(t, w.snap, mainAsOf)
	if want := []string{"a4", "a2", "a1", "unassigned", "a3"}; !reflect.DeepEqual(speakerIDs(v), want) {
		t.Errorf("speaker ids = %v, want %v", speakerIDs(v), want)
	}
}

func TestBuild_Speakers_ReshuffleDailyUsesTheStoredSeedForTodayElseDailySeed(t *testing.T) {
	// Ten included speakers (10! permutations) so a missing or wrong-seed shuffle cannot pass by luck.
	cfg := baseConfig()
	cfg.Speakers = nil
	for i := 0; i < 10; i++ {
		cfg.Speakers = append(cfg.Speakers, model.SpeakerConfig{AccountID: fmt.Sprintf("p%d", i), Name: fmt.Sprintf("Person %d", i), Position: i, Included: true})
	}
	cfg.ReshuffleDaily = true
	cfg.ShuffleSeed = 987654321
	mk := func(shuffleDate string) *world {
		c := cfg
		c.ShuffleDate = shuffleDate
		w := newWorld(mainAsOf, c)
		w.add(newTkt(1, "R-1", "task", "p0", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}))
		w.add(newTkt(2, "R-2", "task", "p3", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}))
		w.add(newTkt(3, "R-3", "task", "", sTodo, nv(10, 9, 0), chg{nv(11, 9, 0), sProg}))
		return w
	}
	orderFor := func(seed int64, reshuffle bool) []string {
		slots := orderFromPulse(t, pulse.SpeakerInput{
			Speakers: cfg.Speakers, ReshuffleDaily: reshuffle, Seed: seed, UnassignedPosition: "last", HasUnassigned: true,
			WithTickets: map[string]bool{"p0": true, "p3": true},
		})
		var ids []string
		for _, sl := range slots {
			ids = append(ids, sl.ID)
		}
		return ids
	}
	stored := orderFor(987654321, true)
	daily := orderFor(dailySeed(t, 7, "2026-11-25"), true)
	plain := orderFor(0, false)
	if reflect.DeepEqual(stored, daily) || reflect.DeepEqual(stored, plain) || reflect.DeepEqual(daily, plain) {
		t.Fatalf("fixture not discriminating: stored=%v daily=%v plain=%v must be pairwise different", stored, daily, plain)
	}
	cases := []struct {
		name        string
		shuffleDate string
		want        []string
	}{
		{"seed already stored for today", "2026-11-25", stored},
		{"stale seed date falls back to the daily seed", "2026-11-24", daily},
		{"never shuffled falls back to the daily seed", "", daily},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := build(t, mk(tc.shuffleDate).snap, mainAsOf)
			if got := speakerIDs(v); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("speaker ids = %v\nwant %v", got, tc.want)
			}
		})
	}
}

func TestBuild_Speakers_ReshuffleOffIgnoresTheSeed(t *testing.T) {
	w := mainWorld()
	w.snap.Config.ShuffleSeed = 42
	w.snap.Config.ShuffleDate = "2026-11-25"
	v := build(t, w.snap, mainAsOf)
	if want := []string{"a1", "a2", "a4", "unassigned", "a3"}; !reflect.DeepEqual(speakerIDs(v), want) {
		t.Errorf("speaker ids = %v, want %v", speakerIDs(v), want)
	}
}

func dailySeed(t *testing.T, id int64, date string) (s int64) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("pulse.DailySeed panicked: %v", r)
		}
	}()
	return pulse.DailySeed(id, date)
}

func orderFromPulse(t *testing.T, in pulse.SpeakerInput) (out []pulse.SpeakerSlot) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("pulse.SpeakerOrder panicked: %v", r)
		}
	}()
	return pulse.SpeakerOrder(in)
}

// removeMembers drops board/backlog members by key (their history entries stay; harmless).
func removeMembers(snap *model.BoardSnapshot, drop ...string) {
	skip := map[string]bool{}
	for _, k := range drop {
		skip[k] = true
	}
	var kept []model.Member
	for _, m := range snap.Members {
		if !skip[m.Issue.Key] {
			m.RankIndex = len(kept)
			kept = append(kept, m)
		}
	}
	snap.Members = kept
}

// ── lists are never nil, purity, golden ──

// Paths (after the root) that may legitimately be JSON null.
var nullableView = map[string]bool{".sprint": true, ".sync.warnings": true}

func assertNoNullLists(t *testing.T, doc any, nullable map[string]bool) {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var walk func(path string, x any)
	walk = func(path string, x any) {
		switch tv := x.(type) {
		case map[string]any:
			for k, val := range tv {
				p := path + "." + k
				if val == nil && !nullable[p] {
					t.Errorf("JSON %s is null; lists must be [] (and only %v may be null)", p, keysOf(nullable))
				}
				walk(p, val)
			}
		case []any:
			for _, e := range tv {
				walk(path+"[]", e)
			}
		}
	}
	walk("", generic)
}

func keysOf(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestBuild_ListsAreNeverNullInJSON(t *testing.T) {
	cases := []struct {
		name string
		snap model.BoardSnapshot
	}{
		{"rich scrum world", mainWorld().snap},
		{"kanban with no members", kanbanWorld().snap},
		{"nil maps, no sprints, no members, no speakers", model.BoardSnapshot{
			StandupID: 1, AsOf: mainAsOf, Live: true, BacklogTotal: -1,
			Config: model.EffectiveConfig{StandupID: 1, Name: "Empty", BoardType: "scrum", Scope: "sprint", PulseDays: 7, DoneWindow: "sprint", RowsMode: "fit"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := build(t, tc.snap, mainAsOf)
			assertNoNullLists(t, v, nullableView)
		})
	}
}

func TestBuild_EmptyBoardGivesEmptyListsAndZeroCounts(t *testing.T) {
	v := build(t, kanbanWorld().snap, mainAsOf)
	if len(v.Tickets) != 0 || len(v.Backlog) != 0 || len(v.Warnings) != 0 {
		t.Errorf("want empty tickets/backlog/warnings, got %d/%d/%d", len(v.Tickets), len(v.Backlog), len(v.Warnings))
	}
	// Included speakers are listed even without tickets; no unassigned entry because nothing is unassigned.
	if want := []string{"a1", "a2", "a4"}; !reflect.DeepEqual(speakerIDs(v), want) {
		t.Errorf("speaker ids = %v, want %v", speakerIDs(v), want)
	}
	for _, sp := range v.Speakers {
		if sp.Active != 0 || sp.Done != 0 || !sp.Included {
			t.Errorf("speaker %+v, want included with 0 active / 0 done", sp)
		}
	}
	if v.Counts != (Counts{}) {
		t.Errorf("Counts = %+v, want zero", v.Counts)
	}
	if v.PreviewKey != "" {
		t.Errorf("PreviewKey = %q, want empty", v.PreviewKey)
	}
}

func TestBuild_IsPureAndDeterministic(t *testing.T) {
	snap := mainWorld().snap
	a := build(t, snap, mainAsOf)
	b := build(t, snap, mainAsOf)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("two builds of the same snapshot differ")
	}
	if !reflect.DeepEqual(snap, mainWorld().snap) {
		t.Errorf("Build mutated its input snapshot")
	}
}

func TestBuild_GoldenSmall(t *testing.T) {
	path := filepath.Join("testdata", "view_small.golden.json")
	if !*update {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("golden missing: %s does not exist; the implementer must run `go test ./internal/view -run TestBuild_GoldenSmall -update`, review the diff, and commit it", path)
		}
	}
	v := build(t, mainWorld().snap, mainAsOf)
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("StandupView JSON differs from %s; run with -update and review the diff.\n--- got ---\n%s", path, got)
	}
}

// ── Initials, LaneLabel ──

func TestInitials(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Iris Calloway", "IC"},
		{"Cher", "C"},
		{"  iris   calloway  ", "IC"},
		{"iris calloway", "IC"},
		{"Silas Webb", "SW"},
		{"Mary Jane Watson", "MW"},
		{"Anne-Marie Smith", "AS"},
		{"Zoë Álvarez", "ZÁ"},
		{"élodie", "É"},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.in), func(t *testing.T) {
			if got := initials(t, tc.in); got != tc.want {
				t.Errorf("Initials(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLaneLabel(t *testing.T) {
	cases := []struct{ lane, want string }{
		{model.LaneTodo, "To Do"},
		{model.LaneProg, "In Progress"},
		{model.LaneReview, "In Review"},
		{model.LaneQA, "QA"},
		{model.LaneDone, "Done"},
	}
	for _, tc := range cases {
		t.Run(tc.lane, func(t *testing.T) {
			if got := laneLabel(t, tc.lane); got != tc.want {
				t.Errorf("LaneLabel(%q) = %q, want %q", tc.lane, got, tc.want)
			}
		})
	}
}

// ── BuildConfig ──

func mainConfigInput() ConfigInput {
	return ConfigInput{
		Position:         3,
		FilterCheck:      &FilterCheck{OK: false, Errors: []string{"Field 'foo' does not exist"}, CheckedAt: 1700000000000},
		NeedsSync:        true,
		UnmappedStatuses: []string{"Mystery"},
		ActiveByAccount:  map[string]int{"a1": 2, "a2": 1, "a4": 0},
	}
}

func TestBuildConfig_MapsEveryField(t *testing.T) {
	got := buildConfig(t, baseConfig(), mainConfigInput())
	const note = "no tickets right now"
	want := StandupConfig{
		ID: 7, Rev: 3, Position: 3, Name: "MOB standup",
		Space:       &SpaceOption{ID: "10001", Key: "MOB", Name: "Service Desk"},
		Board:       &BoardOption{ID: "42", Name: "MOB board", Type: "scrum"},
		Scope:       "sprint",
		ExtraFilter: "labels = standup",
		FilterCheck: &FilterCheck{OK: false, Errors: []string{"Field 'foo' does not exist"}, CheckedAt: 1700000000000},
		Columns: []ColumnMapping{
			{ColumnName: "Backlog", StatusNames: []string{"Backlog"}, Lane: "backlog", Auto: true},
			{ColumnName: "To Do", StatusNames: []string{"To Do"}, Lane: "todo", Auto: true},
			{ColumnName: "In Progress", StatusNames: []string{"In Progress", "Doing"}, Lane: "prog", Auto: true},
			{ColumnName: "Blocked", StatusNames: []string{"Blocked"}, Lane: "prog", Auto: false},
			{ColumnName: "Review", StatusNames: []string{"In Review"}, Lane: "review", Auto: true},
			{ColumnName: "Testing", StatusNames: []string{"QA"}, Lane: "qa", Auto: false},
			{ColumnName: "Done", StatusNames: []string{"Done"}, Lane: "done", Auto: true},
			{ColumnName: "Parked", StatusNames: []string{"Parked"}, Lane: "hidden", Auto: true},
			{ColumnName: "Icebox", StatusNames: []string{}, Lane: "hidden", Auto: false},
		},
		Lanes: []LaneInfo{
			{Lane: "qa", Label: "QA", LimitDays: 3, Columns: []string{"Testing"}},
			{Lane: "review", Label: "In Review", LimitDays: 2, Columns: []string{"Review"}},
			{Lane: "prog", Label: "In Progress", LimitDays: 5, Columns: []string{"In Progress", "Blocked"}},
			{Lane: "todo", Label: "To Do", LimitDays: 10, Columns: []string{"To Do"}},
		},
		UnmappedStatuses: []string{"Mystery"},
		RowsMode:         "roomy",
		Speakers: []SpeakerConfig{
			{AccountID: "a1", Name: "Iris Calloway", Initials: "IC", Position: 0, Included: true, Active: 2},
			{AccountID: "a2", Name: "Silas Webb", Initials: "SW", Position: 1, Included: true, Manual: true, Active: 1},
			{AccountID: "a4", Name: "Cher", Initials: "C", Position: 2, Included: true, Active: 0, Note: note},
			{AccountID: "a3", Name: "Amara Osei", Initials: "AO", Position: 3, Included: false, Note: note},
			{AccountID: "a5", Name: "Lee Park", Initials: "LP", Position: 4, Included: false, Note: note},
		},
		UnassignedPosition: "last", ReshuffleDaily: false, PulseDays: 14,
		ShowCommits: true, ShowPRs: false, ShowComments: true, ShadeWeekends: true,
		DoneWindow: "sprint", BacklogRows: 2, NeedsSync: true, Draft: false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StandupConfig =\n %+v\nwant\n %+v", got, want)
		if !reflect.DeepEqual(got.Columns, want.Columns) {
			t.Errorf("  Columns differ:\n   got  %+v\n   want %+v", got.Columns, want.Columns)
		}
		if !reflect.DeepEqual(got.Speakers, want.Speakers) {
			t.Errorf("  Speakers differ:\n   got  %+v\n   want %+v", got.Speakers, want.Speakers)
		}
		if !reflect.DeepEqual(got.Lanes, want.Lanes) {
			t.Errorf("  Lanes differ:\n   got  %+v\n   want %+v", got.Lanes, want.Lanes)
		}
	}
}

func TestBuildConfig_NoteAppearsOnlyForZeroActive(t *testing.T) {
	in := ConfigInput{ActiveByAccount: map[string]int{"a1": 0, "a2": 3}}
	got := buildConfig(t, baseConfig(), in)
	notes := map[string]string{}
	active := map[string]int{}
	for _, s := range got.Speakers {
		notes[s.AccountID] = s.Note
		active[s.AccountID] = s.Active
	}
	if notes["a1"] != "no tickets right now" || active["a1"] != 0 {
		t.Errorf("a1 (explicit 0) = %q/%d, want note and 0", notes["a1"], active["a1"])
	}
	if notes["a2"] != "" || active["a2"] != 3 {
		t.Errorf("a2 = %q/%d, want no note and 3", notes["a2"], active["a2"])
	}
	if notes["a4"] != "no tickets right now" {
		t.Errorf("a4 (missing from map) note = %q, want the no-tickets note", notes["a4"])
	}
}

func TestBuildConfig_NilActiveMapMeansNoTicketsEverywhere(t *testing.T) {
	got := buildConfig(t, baseConfig(), ConfigInput{})
	for _, s := range got.Speakers {
		if s.Active != 0 || s.Note != "no tickets right now" {
			t.Errorf("speaker %s = active %d note %q, want 0 and the no-tickets note", s.AccountID, s.Active, s.Note)
		}
	}
}

func TestBuildConfig_DraftHasNoBoardAndEmptyLists(t *testing.T) {
	cfg := model.EffectiveConfig{
		StandupID: 9, Rev: 1, Name: "New standup", SpaceID: "10001", SpaceKey: "MOB", SpaceName: "Service Desk",
		Scope: "sprint", PulseDays: 21, RowsMode: "fit", DoneWindow: "sprint", BacklogRows: 12, UnassignedPosition: "last",
	}
	got := buildConfig(t, cfg, ConfigInput{Position: 1})
	if !got.Draft || got.Board != nil {
		t.Errorf("Draft/Board = %v/%v, want true/nil", got.Draft, got.Board)
	}
	if got.Space == nil || *got.Space != (SpaceOption{ID: "10001", Key: "MOB", Name: "Service Desk"}) {
		t.Errorf("Space = %+v, want the chosen space", got.Space)
	}
	if got.Columns == nil || got.Lanes == nil || got.Speakers == nil || got.UnmappedStatuses == nil {
		t.Errorf("lists must be non-nil: columns=%v lanes=%v speakers=%v unmapped=%v", got.Columns == nil, got.Lanes == nil, got.Speakers == nil, got.UnmappedStatuses == nil)
	}
	assertNoNullLists(t, got, map[string]bool{".board": true, ".filterCheck": true})

	empty := buildConfig(t, model.EffectiveConfig{StandupID: 10, Name: "Blank"}, ConfigInput{})
	if empty.Space != nil || empty.Board != nil || !empty.Draft {
		t.Errorf("blank config: Space/Board/Draft = %v/%v/%v, want nil/nil/true", empty.Space, empty.Board, empty.Draft)
	}
	assertNoNullLists(t, empty, map[string]bool{".space": true, ".board": true, ".filterCheck": true})
}

func TestBuildConfig_ConfiguredBoardIsNotDraft(t *testing.T) {
	got := buildConfig(t, baseConfig(), ConfigInput{})
	if got.Draft || got.Board == nil || got.Board.ID != "42" {
		t.Errorf("Draft/Board = %v/%+v, want false and board 42", got.Draft, got.Board)
	}
	if got.FilterCheck != nil {
		t.Errorf("FilterCheck = %+v, want nil when the input has none", got.FilterCheck)
	}
	assertNoNullLists(t, got, map[string]bool{".filterCheck": true})
}

func TestBuildConfig_FilterCheckNilErrorsBecomeEmptyList(t *testing.T) {
	got := buildConfig(t, baseConfig(), ConfigInput{FilterCheck: &FilterCheck{OK: true, CheckedAt: 5}})
	if got.FilterCheck == nil || !got.FilterCheck.OK || got.FilterCheck.CheckedAt != 5 {
		t.Fatalf("FilterCheck = %+v", got.FilterCheck)
	}
	assertNoNullLists(t, got, nil)
}

// ── larger snapshot: shape test and benchmark ──

type bigStats struct{ active, unassigned, done, board int }

// bigWorld builds a ~400-ticket synthetic board with a 28-day window: 340 board issues walking
// todo -> prog -> review -> qa -> done (68 of them done), then 60 backlog-role issues (board role first, as a loader
// produces them), 12 speakers, comments and commits spread over the window. Every third board issue also carries about
// three years of history (a dozen ancient status bounces and 20 ancient comments/commits), per testing.md.
func bigWorld() (*world, bigStats) {
	cfg := baseConfig()
	cfg.PulseDays = 28
	cfg.BacklogRows = 12
	cfg.Speakers = nil
	for i := 0; i < 12; i++ {
		cfg.Speakers = append(cfg.Speakers, model.SpeakerConfig{AccountID: fmt.Sprintf("p%d", i), Name: fmt.Sprintf("Person %d Tester", i), Position: i, Included: true})
	}
	w := newWorld(mainAsOf, cfg)
	var st bigStats
	path := []string{sTodo, sProg, sReview, sQA, sDone}
	assigneeOf := func(i int) string {
		if i%13 == 0 {
			return ""
		}
		return fmt.Sprintf("p%d", i%12)
	}
	for i := 60; i < 400; i++ {
		assignee := assigneeOf(i)
		key := fmt.Sprintf("BIG-%d", i)
		n := 1 + i%5
		base := mainAsOf.AddDate(0, 0, -(30 + i%20)) // when the recent walk starts
		created := base
		var changes []chg
		ancient := i%3 == 0
		if ancient {
			created = mainAsOf.AddDate(0, 0, -(1090 - i%30))
			for m := 0; m < 12; m++ { // todo -> prog -> todo ... ends in todo, long before the window
				to := sProg
				if m%2 == 1 {
					to = sTodo
				}
				changes = append(changes, chg{created.AddDate(0, 0, 60*(m+1)), to})
			}
		}
		for m := 1; m < n; m++ {
			changes = append(changes, chg{base.AddDate(0, 0, 5*m), path[m]})
		}
		k := newTkt(int64(1000+i), key, "task", assignee, sTodo, created, changes...)
		author := assignee
		if author == "" {
			author = "p0"
		}
		for c := 0; c < 3; c++ {
			k.withComments(comment(fmt.Sprintf("c-%d-%d", i, c), mainAsOf.AddDate(0, 0, -(1+(i+c)%10)), author))
		}
		for c := 0; c < 2; c++ {
			k.withDev(model.DevEvent{Kind: model.DevCommit, ID: fmt.Sprintf("sha-%d-%d", i, c), Repo: "big", At: mainAsOf.AddDate(0, 0, -(1 + (i+2*c)%12))})
		}
		if ancient {
			for c := 0; c < 10; c++ {
				at := created.AddDate(0, 0, 30*(c+1))
				k.withComments(comment(fmt.Sprintf("old-c-%d-%d", i, c), at, author))
				k.withDev(model.DevEvent{Kind: model.DevCommit, ID: fmt.Sprintf("old-sha-%d-%d", i, c), Repo: "big", At: at})
			}
		}
		w.add(k)
		st.board++
		if n == 5 {
			st.done++
		} else {
			st.active++
			if assignee == "" {
				st.unassigned++
			}
		}
	}
	for i := 0; i < 60; i++ {
		w.add(newTkt(int64(1000+i), fmt.Sprintf("BIG-%d", i), "story", assigneeOf(i), sBacklog, mainAsOf.AddDate(0, 0, -60)).backlog())
	}
	w.snap.BacklogTotal = 60
	return w, st
}

func TestBuild_LargeSnapshotShape(t *testing.T) {
	w, st := bigWorld()
	v := build(t, w.snap, mainAsOf)
	if len(v.Tickets) != st.board {
		t.Errorf("len(Tickets) = %d, want %d", len(v.Tickets), st.board)
	}
	if want := (Counts{Active: st.active, Unassigned: st.unassigned, Done: st.done, Backlog: 12}); v.Counts != want {
		t.Errorf("Counts = %+v, want %+v", v.Counts, want)
	}
	if v.BacklogTotal != 60 || len(v.Backlog) != 12 {
		t.Errorf("BacklogTotal/len(Backlog) = %d/%d, want 60/12", v.BacklogTotal, len(v.Backlog))
	}
	if len(v.Axis) != 28 || v.PulseDays != 28 {
		t.Errorf("PulseDays/len(Axis) = %d/%d, want 28/28", v.PulseDays, len(v.Axis))
	}
	if len(v.Speakers) != 13 {
		t.Errorf("len(Speakers) = %d, want 13 (12 people + unassigned)", len(v.Speakers))
	}
	seen := map[string]bool{}
	for _, tk := range v.Tickets {
		if seen[tk.Key] {
			t.Errorf("duplicate ticket %s", tk.Key)
		}
		seen[tk.Key] = true
	}
	assertPulseInvariants(t, v)
}

var sinkView StandupView

// BenchmarkStandupView builds a ~400-ticket board with a 28-day pulse and about three years of history on a third of it. Budget (architecture §6.1): <= 15 ms, <= 2 MB
// allocated per build; the allocation budget is checked later, this reports it.
func BenchmarkStandupView(b *testing.B) {
	w, _ := bigWorld()
	snap := w.snap
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkView = Build(snap, mainAsOf)
	}
}
