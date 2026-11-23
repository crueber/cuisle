package pulse

// WP-04 tests: Window / DayIndex / Axis / Timeline / Segments (jira-sync.md §5.1, §5.2; testing.md §2 internal/pulse).
//
// Shared helpers for the WP-04 test files (segments_test.go, lane_test.go, blocked_test.go) live here.
//
// SPEC-NOTE: Timeline's "adjacent equal lanes merged" wording conflicts with its consumers: BlockedSpans reads
// Interval.StatusID (a blocked status that shares a lane with its neighbour would be lost by a literal merge) and
// EnteredLane reads runs of RealLane (backlog→todo would be lost). So these tests never assert interval COUNTS or the
// StatusID of a merged interval; they compare after normalising runs of equal (Lane, RealLane) (see runsOf). Two
// dedicated tests pin the behaviour that must survive: TestTimeline_BlockedStatusSharingALane_StaysVisibleToBlockedSpans
// (blocked_test.go) and TestTimeline_BacklogThenTodo_KeepsRealLaneDistinct (below).
//
// SPEC-NOTE: Segments emits ONE segment per contiguous lane span (ux-spec §7): after zero-width segments are dropped,
// adjacent segments with the same Lane are merged (e.g. prog → review → prog all on one day gives a single prog segment).
//
// SPEC-NOTE: Interval.Lane (drawn lane) is what Segments draws; RealLane is only for EnteredLane. A status mapped to
// hidden/backlog or unmapped draws as "todo" with RealLane "hidden"/"backlog"/"" respectively.
//
// SPEC-NOTE (not asserted, spec does not decide): "yesterday" on Saturday/Sunday; spans/flags ending at exactly local
// midnight; whether merely adjacent (non-overlapping) blocked spans merge; flag state before the first change on a
// ticket created inside the window.

import (
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/crueber/cuisle/internal/model"
)

// ── helpers ─────────────────────────────────────────────────────────────────────────────────────────────────────

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

// ldt builds a local wall-clock time.
func ldt(loc *time.Location, y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, loc)
}

// ivl is a hand-made interval whose real lane equals its drawn lane and whose status id is "s-<lane>".
func ivl(lane string, from, to time.Time) Interval {
	return Interval{StatusID: "s-" + lane, Lane: lane, RealLane: lane, From: from, To: to}
}

// ivlS is a hand-made interval with an explicit status id.
func ivlS(status, lane string, from, to time.Time) Interval {
	return Interval{StatusID: status, Lane: lane, RealLane: lane, From: from, To: to}
}

var testStatusLanes = map[string]string{
	"s-backlog": model.LaneBacklog,
	"s-todo":    model.LaneTodo,
	"s-prog":    model.LaneProg,
	"s-dev":     model.LaneProg, // second status in the same lane
	"s-blocked": model.LaneProg, // a blocked status sharing the prog lane
	"s-review":  model.LaneReview,
	"s-qa":      model.LaneQA,
	"s-done":    model.LaneDone,
	"s-hidden":  model.LaneHidden,
	// "s-x" is deliberately unmapped
}

func sc(id, from, to string, at time.Time) model.StatusChange {
	return model.StatusChange{ID: id, At: at, From: from, To: to}
}

type run struct {
	Lane, Real string
	From, To   time.Time
}

// runsOf merges adjacent intervals with equal (Lane, RealLane) so tests don't depend on whether Timeline merges.
func runsOf(tl []Interval) []run {
	var out []run
	for _, iv := range tl {
		if n := len(out); n > 0 && out[n-1].Lane == iv.Lane && out[n-1].Real == iv.RealLane && out[n-1].To.Equal(iv.From) {
			out[n-1].To = iv.To
			continue
		}
		out = append(out, run{iv.Lane, iv.RealLane, iv.From, iv.To})
	}
	return out
}

func assertRuns(t *testing.T, tl []Interval, want []run) {
	t.Helper()
	got := runsOf(tl)
	if len(got) != len(want) {
		t.Fatalf("runs: got %d, want %d\n got: %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Lane != w.Lane || g.Real != w.Real || !g.From.Equal(w.From) || !g.To.Equal(w.To) {
			t.Errorf("run %d: got {%s/%q %s → %s}, want {%s/%q %s → %s}", i,
				g.Lane, g.Real, g.From, g.To, w.Lane, w.Real, w.From, w.To)
		}
	}
}

func assertContiguous(t *testing.T, tl []Interval, created, asOf time.Time) {
	t.Helper()
	if len(tl) == 0 {
		t.Fatalf("timeline is empty")
	}
	if !tl[0].From.Equal(created) {
		t.Errorf("first interval From = %s, want CreatedAt %s", tl[0].From, created)
	}
	for i := 1; i < len(tl); i++ {
		if !tl[i].From.Equal(tl[i-1].To) {
			t.Errorf("interval %d From %s != previous To %s (not contiguous)", i, tl[i].From, tl[i-1].To)
		}
	}
	if last := tl[len(tl)-1]; !last.To.Equal(asOf) {
		t.Errorf("last interval To = %s, want asOf %s", last.To, asOf)
	}
}

func assertSegs(t *testing.T, got, want []Seg) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("segs: got %d %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("seg %d: got %+v, want %+v (all: %+v)", i, got[i], want[i], got)
		}
	}
}

// assertSegInvariants checks the Seg contract: 0 ≤ From < To ≤ Days, contiguous, last ends at Days.
func assertSegInvariants(t *testing.T, segs []Seg, days int) {
	t.Helper()
	for i, s := range segs {
		if s.From < 0 || s.From >= s.To || s.To > days {
			t.Errorf("seg %d %+v violates 0 ≤ From < To ≤ %d", i, s, days)
		}
		if i > 0 && s.From != segs[i-1].To {
			t.Errorf("seg %d %+v is not contiguous with %+v", i, s, segs[i-1])
		}
	}
	if len(segs) > 0 && segs[len(segs)-1].To != days {
		t.Errorf("last segment ends at %d, want %d", segs[len(segs)-1].To, days)
	}
}

// ── Window ──────────────────────────────────────────────────────────────────────────────────────────────────────

func TestNewWindow_StartsAreLocalMidnights(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0) // Monday
	for _, n := range []int{7, 21, 28} {
		w := NewWindow(asOf, n)
		if w.Days != n {
			t.Errorf("N=%d: Days = %d", n, w.Days)
		}
		if w.Loc.String() != chi.String() {
			t.Errorf("N=%d: Loc = %v, want %v", n, w.Loc, chi)
		}
		if !w.AsOf.Equal(asOf) {
			t.Errorf("N=%d: AsOf = %v, want %v", n, w.AsOf, asOf)
		}
		if len(w.Starts) != n+1 {
			t.Fatalf("N=%d: len(Starts) = %d, want %d", n, len(w.Starts), n+1)
		}
		for k := 0; k <= n; k++ {
			want := time.Date(2026, time.November, 23-(n-1)+k, 0, 0, 0, 0, chi)
			if !w.Starts[k].Equal(want) {
				t.Errorf("N=%d: Starts[%d] = %v, want %v", n, k, w.Starts[k], want)
			}
		}
		// today = index N-1; Starts[N] is tomorrow's midnight
		if want := time.Date(2026, time.November, 24, 0, 0, 0, 0, chi); !w.Starts[n].Equal(want) {
			t.Errorf("N=%d: Starts[N] = %v, want tomorrow midnight %v", n, w.Starts[n], want)
		}
	}
}

func TestNewWindow_SameTodayForAnyTimeOfDay(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	early := NewWindow(time.Date(2026, time.November, 23, 0, 0, 0, 0, chi), 21)
	late := NewWindow(time.Date(2026, time.November, 23, 23, 59, 59, 0, chi), 21)
	for k := range early.Starts {
		if !early.Starts[k].Equal(late.Starts[k]) {
			t.Errorf("Starts[%d] differs between 00:00 and 23:59:59 of the same day: %v vs %v", k, early.Starts[k], late.Starts[k])
		}
	}
}

func TestNewWindow_AcrossMonthAndYearBoundary(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	w := NewWindow(ldt(chi, 2026, time.January, 3, 10, 0), 7) // Dec 28 … Jan 3
	if want := time.Date(2025, time.December, 28, 0, 0, 0, 0, chi); !w.Starts[0].Equal(want) {
		t.Errorf("Starts[0] = %v, want %v", w.Starts[0], want)
	}
	if want := time.Date(2026, time.January, 4, 0, 0, 0, 0, chi); !w.Starts[7].Equal(want) {
		t.Errorf("Starts[7] = %v, want %v", w.Starts[7], want)
	}
}

func TestWindow_DayIndex_BoundariesAndOutOfRange(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	w := NewWindow(asOf, 21) // idx 0 = Nov 3, idx 20 = Nov 23
	probes := []struct {
		name string
		at   time.Time
		want int
	}{
		{"first instant of window", w.Starts[0], 0},
		{"one ns before window", w.Starts[0].Add(-time.Nanosecond), -1},
		{"long before window", ldt(chi, 2026, time.January, 1, 0, 0), -1},
		{"last ns of day 0", w.Starts[1].Add(-time.Nanosecond), 0},
		{"first instant of day 1", w.Starts[1], 1},
		{"midday day 7", ldt(chi, 2026, time.November, 10, 12, 0), 7},
		{"asOf is today", asOf, 20},
		{"last ns of today", w.Starts[21].Add(-time.Nanosecond), 20},
		{"tomorrow midnight = Days", w.Starts[21], 21},
		{"far future = Days", ldt(chi, 2027, time.June, 1, 0, 0), 21},
	}
	for _, p := range probes {
		if got := w.DayIndex(p.at); got != p.want {
			t.Errorf("%s: DayIndex(%v) = %d, want %d", p.name, p.at, got, p.want)
		}
	}
}

func TestWindow_DayIndex_UsesWindowLocationForNonLocalInstants(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	w := NewWindow(ldt(chi, 2026, time.November, 23, 15, 0), 7) // Nov 17 … Nov 23
	// 2026-11-21 05:30 UTC = Nov 20 23:30 CST (idx 3), although the UTC date is Nov 21 (idx 4).
	if got := w.DayIndex(time.Date(2026, time.November, 21, 5, 30, 0, 0, time.UTC)); got != 3 {
		t.Errorf("DayIndex(05:30Z Nov 21) = %d, want 3 (local Nov 20)", got)
	}
	// 2026-11-21 06:30 UTC = Nov 21 00:30 CST (idx 4).
	if got := w.DayIndex(time.Date(2026, time.November, 21, 6, 30, 0, 0, time.UTC)); got != 4 {
		t.Errorf("DayIndex(06:30Z Nov 21) = %d, want 4 (local Nov 21)", got)
	}
}

// DST transition days. A window spanning the change still has N+1 local midnights, each at 00:00 local, and events at
// 00:30 / 23:30 local bucket to the right day (a naive "+24h" implementation gets these wrong).
func TestWindow_DST_TransitionDays(t *testing.T) {
	cases := []struct {
		name      string
		zone      string
		asOf      [5]int // y, m, d, h, min
		days      int
		transIdx  int           // index of the transition day
		wantDayLn time.Duration // length of the transition day
		probes    [][6]int      // y, m, d, h, min, wantIdx
	}{
		{
			name: "Chicago spring forward 2026-03-08 (23h day)", zone: "America/Chicago",
			asOf: [5]int{2026, 3, 10, 15, 0}, days: 7, transIdx: 4, wantDayLn: 23 * time.Hour,
			probes: [][6]int{
				{2026, 3, 7, 23, 30, 3},
				{2026, 3, 8, 0, 30, 4},
				{2026, 3, 8, 23, 30, 4},
				{2026, 3, 9, 0, 0, 5},
				{2026, 3, 9, 0, 30, 5},
				{2026, 3, 10, 14, 0, 6},
			},
		},
		{
			name: "Chicago fall back 2026-11-01 (25h day)", zone: "America/Chicago",
			asOf: [5]int{2026, 11, 3, 15, 0}, days: 7, transIdx: 4, wantDayLn: 25 * time.Hour,
			probes: [][6]int{
				{2026, 10, 31, 23, 30, 3},
				{2026, 11, 1, 0, 30, 4},
				{2026, 11, 1, 23, 30, 4},
				{2026, 11, 2, 0, 30, 5},
				{2026, 11, 3, 23, 59, 6},
			},
		},
		{
			name: "Dublin spring forward 2026-03-29 (23h day)", zone: "Europe/Dublin",
			asOf: [5]int{2026, 3, 31, 12, 0}, days: 7, transIdx: 4, wantDayLn: 23 * time.Hour,
			probes: [][6]int{
				{2026, 3, 28, 23, 30, 3},
				{2026, 3, 29, 0, 30, 4},
				{2026, 3, 29, 23, 30, 4},
				{2026, 3, 30, 0, 30, 5},
				{2026, 3, 31, 11, 0, 6},
			},
		},
		{
			name: "Dublin fall back 2026-10-25 (25h day)", zone: "Europe/Dublin",
			asOf: [5]int{2026, 10, 27, 12, 0}, days: 7, transIdx: 4, wantDayLn: 25 * time.Hour,
			probes: [][6]int{
				{2026, 10, 24, 23, 30, 3},
				{2026, 10, 25, 0, 30, 4},
				{2026, 10, 25, 23, 30, 4},
				{2026, 10, 26, 0, 30, 5},
				{2026, 10, 27, 11, 0, 6},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc := mustLoc(t, tc.zone)
			a := tc.asOf
			asOf := ldt(loc, a[0], time.Month(a[1]), a[2], a[3], a[4])
			w := NewWindow(asOf, tc.days)
			if len(w.Starts) != tc.days+1 {
				t.Fatalf("len(Starts) = %d, want %d local midnights", len(w.Starts), tc.days+1)
			}
			for k, s := range w.Starts {
				want := time.Date(a[0], time.Month(a[1]), a[2]-(tc.days-1)+k, 0, 0, 0, 0, loc)
				if !s.Equal(want) {
					t.Errorf("Starts[%d] = %v, want %v", k, s, want)
				}
				if l := s.In(loc); l.Hour() != 0 || l.Minute() != 0 {
					t.Errorf("Starts[%d] = %v is not local midnight", k, l)
				}
			}
			if got := w.Starts[tc.transIdx+1].Sub(w.Starts[tc.transIdx]); got != tc.wantDayLn {
				t.Errorf("transition day length = %v, want %v", got, tc.wantDayLn)
			}
			for _, p := range tc.probes {
				at := ldt(loc, p[0], time.Month(p[1]), p[2], p[3], p[4])
				if got := w.DayIndex(at); got != p[5] {
					t.Errorf("DayIndex(%v) = %d, want %d", at, got, p[5])
				}
			}
		})
	}
}

func TestWindow_Axis_N7_WeekendMondayLabels(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	w := NewWindow(ldt(chi, 2026, time.November, 23, 15, 0), 7) // Tue 17 … Mon 23
	want := []AxisDay{
		{Date: "2026-11-17", Label: "17", Weekend: false, Monday: false}, // Tue
		{Date: "2026-11-18", Label: "18"},                                // Wed
		{Date: "2026-11-19", Label: "19"},                                // Thu
		{Date: "2026-11-20", Label: "20"},                                // Fri
		{Date: "2026-11-21", Label: "21", Weekend: true},                 // Sat
		{Date: "2026-11-22", Label: "22", Weekend: true},                 // Sun
		{Date: "2026-11-23", Label: "23", Monday: true},                  // Mon
	}
	got := w.Axis()
	if len(got) != len(want) {
		t.Fatalf("len(Axis) = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Axis[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestWindow_Axis_LengthsAndMonthBoundaryLabels(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	for _, n := range []int{7, 21, 28} {
		axis := NewWindow(asOf, n).Axis()
		if len(axis) != n {
			t.Fatalf("N=%d: len(Axis) = %d", n, len(axis))
		}
		for k, a := range axis {
			d := time.Date(2026, time.November, 23-(n-1)+k, 12, 0, 0, 0, chi)
			if a.Date != d.Format("2006-01-02") {
				t.Errorf("N=%d Axis[%d].Date = %q, want %q", n, k, a.Date, d.Format("2006-01-02"))
			}
			if want := d.Format("2"); a.Label != want {
				t.Errorf("N=%d Axis[%d].Label = %q, want %q (day of month, no padding)", n, k, a.Label, want)
			}
			if wantW := d.Weekday() == time.Saturday || d.Weekday() == time.Sunday; a.Weekend != wantW {
				t.Errorf("N=%d Axis[%d] (%s) Weekend = %v", n, k, a.Date, a.Weekend)
			}
			if wantM := d.Weekday() == time.Monday; a.Monday != wantM {
				t.Errorf("N=%d Axis[%d] (%s) Monday = %v", n, k, a.Date, a.Monday)
			}
		}
	}
	// N=28 starts Oct 27: labels 27 28 29 30 31 1 2 …
	axis := NewWindow(asOf, 28).Axis()
	for i, want := range []string{"27", "28", "29", "30", "31", "1", "2"} {
		if axis[i].Label != want {
			t.Errorf("N=28 Axis[%d].Label = %q, want %q", i, axis[i].Label, want)
		}
	}
	if axis[0].Date != "2026-10-27" || axis[27].Date != "2026-11-23" {
		t.Errorf("N=28 axis runs %s … %s, want 2026-10-27 … 2026-11-23", axis[0].Date, axis[27].Date)
	}
}

func TestWindow_Axis_DSTWindowStillHasNDays(t *testing.T) {
	for _, tc := range []struct {
		zone       string
		y, m, d    int
		days       int
		firstDate  string
		lastDate   string
		weekendIdx []int
	}{
		{"America/Chicago", 2026, 3, 10, 7, "2026-03-04", "2026-03-10", []int{3, 4}}, // Sat Mar 7, Sun Mar 8
		{"Europe/Dublin", 2026, 3, 31, 7, "2026-03-25", "2026-03-31", []int{3, 4}},   // Sat Mar 28, Sun Mar 29
	} {
		loc := mustLoc(t, tc.zone)
		axis := NewWindow(ldt(loc, tc.y, time.Month(tc.m), tc.d, 12, 0), tc.days).Axis()
		if len(axis) != tc.days || axis[0].Date != tc.firstDate || axis[tc.days-1].Date != tc.lastDate {
			t.Errorf("%s: axis %v … %v (len %d), want %s … %s", tc.zone, axis[0].Date, axis[len(axis)-1].Date, len(axis), tc.firstDate, tc.lastDate)
			continue
		}
		wk := map[int]bool{}
		for _, i := range tc.weekendIdx {
			wk[i] = true
		}
		for i, a := range axis {
			if a.Weekend != wk[i] {
				t.Errorf("%s: Axis[%d] (%s) Weekend = %v, want %v", tc.zone, i, a.Date, a.Weekend, wk[i])
			}
		}
	}
}

// ── Timeline ────────────────────────────────────────────────────────────────────────────────────────────────────

func TestTimeline_NoChanges_SingleIntervalOfCurrentStatus(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	created := ldt(chi, 2026, time.November, 5, 9, 0)
	tk := Ticket{CreatedAt: created, UpdatedAt: created, StatusID: "s-prog"}
	tl := Timeline(tk, testStatusLanes, asOf)
	assertContiguous(t, tl, created, asOf)
	assertRuns(t, tl, []run{{"prog", "prog", created, asOf}})
	if tl[0].StatusID != "s-prog" {
		t.Errorf("StatusID = %q, want s-prog", tl[0].StatusID)
	}
}

func TestTimeline_TwoChanges_InitialStatusIsFromOfFirstChange(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	created := ldt(chi, 2026, time.November, 5, 9, 0)
	t1 := ldt(chi, 2026, time.November, 10, 10, 0)
	t2 := ldt(chi, 2026, time.November, 15, 11, 0)
	tk := Ticket{
		CreatedAt: created, UpdatedAt: t2, StatusID: "s-review",
		StatusChanges: []model.StatusChange{sc("1", "s-todo", "s-prog", t1), sc("2", "s-prog", "s-review", t2)},
	}
	tl := Timeline(tk, testStatusLanes, asOf)
	assertContiguous(t, tl, created, asOf)
	assertRuns(t, tl, []run{
		{"todo", "todo", created, t1},
		{"prog", "prog", t1, t2},
		{"review", "review", t2, asOf},
	})
}

func TestTimeline_LastChangeToDiffersFromCurrent_AddsSyntheticChangeAtUpdatedAt(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	created := ldt(chi, 2026, time.November, 5, 9, 0)
	t1 := ldt(chi, 2026, time.November, 10, 10, 0)
	upd := ldt(chi, 2026, time.November, 18, 9, 0)
	tk := Ticket{
		CreatedAt: created, UpdatedAt: upd, StatusID: "s-qa", // changelog lag: last To is s-prog
		StatusChanges: []model.StatusChange{sc("1", "s-todo", "s-prog", t1)},
	}
	tl := Timeline(tk, testStatusLanes, asOf)
	assertContiguous(t, tl, created, asOf)
	assertRuns(t, tl, []run{
		{"todo", "todo", created, t1},
		{"prog", "prog", t1, upd},
		{"qa", "qa", upd, asOf},
	})
}

func TestTimeline_UnmappedHiddenAndBacklogDrawAsTodoButKeepRealLane(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	created := ldt(chi, 2026, time.November, 1, 9, 0)
	d6 := ldt(chi, 2026, time.November, 6, 10, 0)
	d8 := ldt(chi, 2026, time.November, 8, 10, 0)
	d9 := ldt(chi, 2026, time.November, 9, 10, 0)
	d10 := ldt(chi, 2026, time.November, 10, 10, 0)
	tk := Ticket{
		CreatedAt: created, UpdatedAt: d10, StatusID: "s-prog",
		StatusChanges: []model.StatusChange{
			sc("1", "s-backlog", "s-todo", d6),
			sc("2", "s-todo", "s-hidden", d8),
			sc("3", "s-hidden", "s-x", d9), // unmapped status
			sc("4", "s-x", "s-prog", d10),
		},
	}
	tl := Timeline(tk, testStatusLanes, asOf)
	assertContiguous(t, tl, created, asOf)
	assertRuns(t, tl, []run{
		{"todo", "backlog", created, d6},
		{"todo", "todo", d6, d8},
		{"todo", "hidden", d8, d9},
		{"todo", "", d9, d10},
		{"prog", "prog", d10, asOf},
	})
}

func TestTimeline_BacklogThenTodo_KeepsRealLaneDistinct(t *testing.T) {
	// A merge on the drawn Lane alone would lose the backlog→todo entry that EnteredLane needs.
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	created := ldt(chi, 2026, time.November, 1, 9, 0)
	d6 := ldt(chi, 2026, time.November, 6, 10, 0)
	tk := Ticket{
		CreatedAt: created, UpdatedAt: d6, StatusID: "s-todo",
		StatusChanges: []model.StatusChange{sc("1", "s-backlog", "s-todo", d6)},
	}
	tl := Timeline(tk, testStatusLanes, asOf)
	if got := EnteredLane(tl); !got.Equal(d6) {
		t.Errorf("EnteredLane after backlog→todo = %v, want %v", got, d6)
	}
}

func TestTimeline_ReopenDoneToProgToDone(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	created := ldt(chi, 2026, time.November, 1, 9, 0)
	a := ldt(chi, 2026, time.November, 3, 10, 0)
	b := ldt(chi, 2026, time.November, 5, 10, 0)
	c := ldt(chi, 2026, time.November, 8, 10, 0)
	d := ldt(chi, 2026, time.November, 12, 10, 0)
	tk := Ticket{
		CreatedAt: created, UpdatedAt: d, StatusID: "s-done",
		StatusChanges: []model.StatusChange{
			sc("1", "s-todo", "s-prog", a),
			sc("2", "s-prog", "s-done", b),
			sc("3", "s-done", "s-prog", c), // reopened
			sc("4", "s-prog", "s-done", d),
		},
	}
	tl := Timeline(tk, testStatusLanes, asOf)
	assertContiguous(t, tl, created, asOf)
	assertRuns(t, tl, []run{
		{"todo", "todo", created, a},
		{"prog", "prog", a, b},
		{"done", "done", b, c},
		{"prog", "prog", c, d},
		{"done", "done", d, asOf},
	})
	if got := EnteredLane(tl); !got.Equal(d) {
		t.Errorf("EnteredLane = %v, want the latest Done entry %v", got, d)
	}
}

func TestTimeline_TwoStatusesInOneLane_ActLikeOneLaneRun(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	created := ldt(chi, 2026, time.November, 1, 9, 0)
	a := ldt(chi, 2026, time.November, 8, 10, 0)
	b := ldt(chi, 2026, time.November, 12, 10, 0)
	tk := Ticket{
		CreatedAt: created, UpdatedAt: b, StatusID: "s-review",
		StatusChanges: []model.StatusChange{sc("1", "s-prog", "s-dev", a), sc("2", "s-dev", "s-review", b)},
	}
	tl := Timeline(tk, testStatusLanes, asOf)
	assertContiguous(t, tl, created, asOf)
	assertRuns(t, tl, []run{
		{"prog", "prog", created, b},
		{"review", "review", b, asOf},
	})
}

func TestTimeline_PastAsOf_IgnoresChangesAfterAsOf(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	created := ldt(chi, 2026, time.November, 1, 9, 0)
	t1 := ldt(chi, 2026, time.November, 10, 10, 0)
	t2 := ldt(chi, 2026, time.November, 20, 10, 0)
	asOf := ldt(chi, 2026, time.November, 15, 12, 0) // before t2
	tk := Ticket{
		CreatedAt: created, UpdatedAt: t1, StatusID: "s-prog", // state as of asOf
		StatusChanges: []model.StatusChange{sc("1", "s-todo", "s-prog", t1), sc("2", "s-prog", "s-review", t2)},
	}
	tl := Timeline(tk, testStatusLanes, asOf)
	assertContiguous(t, tl, created, asOf)
	assertRuns(t, tl, []run{
		{"todo", "todo", created, t1},
		{"prog", "prog", t1, asOf},
	})
}

func TestTimeline_PastAsOf_AllChangesAfterAsOf_UsesCurrentStatus(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	created := ldt(chi, 2026, time.November, 1, 9, 0)
	t1 := ldt(chi, 2026, time.November, 20, 10, 0)
	asOf := ldt(chi, 2026, time.November, 15, 12, 0)
	tk := Ticket{
		CreatedAt: created, UpdatedAt: created, StatusID: "s-todo",
		StatusChanges: []model.StatusChange{sc("1", "s-todo", "s-prog", t1)},
	}
	tl := Timeline(tk, testStatusLanes, asOf)
	assertContiguous(t, tl, created, asOf)
	assertRuns(t, tl, []run{{"todo", "todo", created, asOf}})
}

// ── Segments ────────────────────────────────────────────────────────────────────────────────────────────────────

func TestSegments_Golden_Todo5Prog6Review3_N21(t *testing.T) {
	// docs/testing.md §4: history [todo 5, prog 6, review 3], N=21 → segments start at day 7, widths 5/6/3, last ends at 21.
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)     // window Nov 3 (0) … Nov 23 (20)
	created := ldt(chi, 2026, time.November, 10, 9, 0)   // idx 7
	toProg := ldt(chi, 2026, time.November, 15, 11, 0)   // idx 12
	toReview := ldt(chi, 2026, time.November, 21, 10, 0) // idx 18
	tk := Ticket{
		CreatedAt: created, UpdatedAt: toReview, StatusID: "s-review",
		StatusChanges: []model.StatusChange{sc("1", "s-todo", "s-prog", toProg), sc("2", "s-prog", "s-review", toReview)},
	}
	w := NewWindow(asOf, 21)
	segs := Segments(Timeline(tk, testStatusLanes, asOf), w)
	assertSegs(t, segs, []Seg{{"todo", 7, 12}, {"prog", 12, 18}, {"review", 18, 21}})
	assertSegInvariants(t, segs, 21)
}

func TestSegments_HandIntervals(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	w := NewWindow(asOf, 21) // idx = day-of-month − 3 for November
	oct1 := ldt(chi, 2026, time.October, 1, 9, 0)
	cases := []struct {
		name string
		tl   []Interval
		want []Seg
	}{
		{
			"status before the window is clipped at day 0",
			[]Interval{ivl("prog", oct1, asOf)},
			[]Seg{{"prog", 0, 21}},
		},
		{
			"pre-window state then a transition inside the window",
			[]Interval{ivl("prog", oct1, ldt(chi, 2026, time.November, 10, 9, 0)), ivl("review", ldt(chi, 2026, time.November, 10, 9, 0), asOf)},
			[]Seg{{"prog", 0, 7}, {"review", 7, 21}},
		},
		{
			"created inside the window starts at the creation day",
			[]Interval{ivl("todo", ldt(chi, 2026, time.November, 10, 15, 0), asOf)},
			[]Seg{{"todo", 7, 21}},
		},
		{
			"created today",
			[]Interval{ivl("todo", ldt(chi, 2026, time.November, 23, 9, 0), asOf)},
			[]Seg{{"todo", 20, 21}},
		},
		{
			"created and moved on the same day: creation lane is zero-width and dropped",
			[]Interval{
				ivl("todo", ldt(chi, 2026, time.November, 10, 9, 0), ldt(chi, 2026, time.November, 10, 10, 0)),
				ivl("prog", ldt(chi, 2026, time.November, 10, 10, 0), asOf),
			},
			[]Seg{{"prog", 7, 21}},
		},
		{
			"several transitions in one day: the last lane of that day wins and the list stays contiguous",
			[]Interval{
				ivl("todo", oct1, ldt(chi, 2026, time.November, 10, 9, 0)),
				ivl("prog", ldt(chi, 2026, time.November, 10, 9, 0), ldt(chi, 2026, time.November, 10, 14, 0)),
				ivl("review", ldt(chi, 2026, time.November, 10, 14, 0), asOf),
			},
			[]Seg{{"todo", 0, 7}, {"review", 7, 21}},
		},
		{
			"the day of a transition belongs to the new lane (even at 23:59)",
			[]Interval{
				ivl("prog", oct1, ldt(chi, 2026, time.November, 10, 23, 59)),
				ivl("review", ldt(chi, 2026, time.November, 10, 23, 59), asOf),
			},
			[]Seg{{"prog", 0, 7}, {"review", 7, 21}},
		},
		{
			"bouncing back to the same lane within a day gives one segment",
			[]Interval{
				ivl("prog", oct1, ldt(chi, 2026, time.November, 10, 9, 0)),
				ivl("review", ldt(chi, 2026, time.November, 10, 9, 0), ldt(chi, 2026, time.November, 10, 10, 0)),
				ivl("prog", ldt(chi, 2026, time.November, 10, 10, 0), asOf),
			},
			[]Seg{{"prog", 0, 21}},
		},
		{
			"reopen: Done, In Progress, Done again",
			[]Interval{
				ivl("prog", ldt(chi, 2026, time.October, 20, 9, 0), ldt(chi, 2026, time.November, 5, 10, 0)),
				ivl("done", ldt(chi, 2026, time.November, 5, 10, 0), ldt(chi, 2026, time.November, 12, 10, 0)),
				ivl("prog", ldt(chi, 2026, time.November, 12, 10, 0), ldt(chi, 2026, time.November, 18, 10, 0)),
				ivl("done", ldt(chi, 2026, time.November, 18, 10, 0), asOf),
			},
			[]Seg{{"prog", 0, 2}, {"done", 2, 9}, {"prog", 9, 15}, {"done", 15, 21}},
		},
		{
			"transition on the first day of the window",
			[]Interval{
				ivl("todo", oct1, ldt(chi, 2026, time.November, 3, 8, 0)),
				ivl("prog", ldt(chi, 2026, time.November, 3, 8, 0), asOf),
			},
			[]Seg{{"prog", 0, 21}},
		},
		{
			"transition today",
			[]Interval{
				ivl("prog", oct1, ldt(chi, 2026, time.November, 23, 9, 0)),
				ivl("review", ldt(chi, 2026, time.November, 23, 9, 0), asOf),
			},
			[]Seg{{"prog", 0, 20}, {"review", 20, 21}},
		},
		{
			"empty timeline gives no segments",
			nil,
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			segs := Segments(tc.tl, w)
			assertSegs(t, segs, tc.want)
			assertSegInvariants(t, segs, 21)
		})
	}
}

func TestSegments_UnmappedAndHiddenDrawAsTodoAndStayContiguous(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	created := ldt(chi, 2026, time.October, 20, 9, 0)
	tk := Ticket{
		CreatedAt: created, UpdatedAt: ldt(chi, 2026, time.November, 18, 10, 0), StatusID: "s-review",
		StatusChanges: []model.StatusChange{
			sc("1", "s-backlog", "s-prog", ldt(chi, 2026, time.November, 8, 10, 0)), // idx 5
			sc("2", "s-prog", "s-hidden", ldt(chi, 2026, time.November, 12, 10, 0)), // idx 9
			sc("3", "s-hidden", "s-x", ldt(chi, 2026, time.November, 15, 10, 0)),    // idx 12 (unmapped)
			sc("4", "s-x", "s-review", ldt(chi, 2026, time.November, 18, 10, 0)),    // idx 15
		},
	}
	segs := Segments(Timeline(tk, testStatusLanes, asOf), NewWindow(asOf, 21))
	assertSegs(t, segs, []Seg{{"todo", 0, 5}, {"prog", 5, 9}, {"todo", 9, 15}, {"review", 15, 21}})
	assertSegInvariants(t, segs, 21)
}

func TestSegments_SyntheticChangeAtUpdatedAt(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	tk := Ticket{
		CreatedAt: ldt(chi, 2026, time.November, 5, 9, 0),  // idx 2
		UpdatedAt: ldt(chi, 2026, time.November, 18, 9, 0), // idx 15
		StatusID:  "s-review",
		StatusChanges: []model.StatusChange{
			sc("1", "s-todo", "s-prog", ldt(chi, 2026, time.November, 10, 10, 0)), // idx 7
		},
	}
	segs := Segments(Timeline(tk, testStatusLanes, asOf), NewWindow(asOf, 21))
	assertSegs(t, segs, []Seg{{"todo", 2, 7}, {"prog", 7, 15}, {"review", 15, 21}})
}

func TestSegments_PastAsOf_IgnoresEventsAfterAsOf(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 15, 12, 0) // Sunday; N=7 → Nov 9 (0) … Nov 15 (6)
	tk := Ticket{
		CreatedAt: ldt(chi, 2026, time.November, 1, 9, 0),
		UpdatedAt: ldt(chi, 2026, time.November, 10, 10, 0),
		StatusID:  "s-prog", // as of asOf
		StatusChanges: []model.StatusChange{
			sc("1", "s-todo", "s-prog", ldt(chi, 2026, time.November, 10, 10, 0)),   // idx 1
			sc("2", "s-prog", "s-review", ldt(chi, 2026, time.November, 20, 10, 0)), // after asOf
			sc("3", "s-review", "s-done", ldt(chi, 2026, time.November, 22, 10, 0)), // after asOf
		},
	}
	segs := Segments(Timeline(tk, testStatusLanes, asOf), NewWindow(asOf, 7))
	assertSegs(t, segs, []Seg{{"todo", 0, 1}, {"prog", 1, 7}})
}

func TestSegments_DSTDayBoundaryUsesLocalDays(t *testing.T) {
	for _, tc := range []struct {
		zone string
		asOf [5]int
		to   [5]int // transition instant
		want []Seg
	}{
		// Chicago N=7 Mar 4..10: 00:30 on Mar 9 (day after spring-forward) is idx 5; 23:30 on Mar 8 is idx 4.
		{"America/Chicago", [5]int{2026, 3, 10, 15, 0}, [5]int{2026, 3, 9, 0, 30}, []Seg{{"prog", 0, 5}, {"review", 5, 7}}},
		{"America/Chicago", [5]int{2026, 3, 10, 15, 0}, [5]int{2026, 3, 8, 23, 30}, []Seg{{"prog", 0, 4}, {"review", 4, 7}}},
		// Dublin N=7 Mar 25..31: 00:30 IST on Mar 30 is idx 5.
		{"Europe/Dublin", [5]int{2026, 3, 31, 12, 0}, [5]int{2026, 3, 30, 0, 30}, []Seg{{"prog", 0, 5}, {"review", 5, 7}}},
		// Chicago fall back, Nov 1 is idx 4 of Oct 28..Nov 3: 23:30 CST Nov 1 is still idx 4.
		{"America/Chicago", [5]int{2026, 11, 3, 15, 0}, [5]int{2026, 11, 1, 23, 30}, []Seg{{"prog", 0, 4}, {"review", 4, 7}}},
		// Dublin fall back, Oct 25 is idx 4 of Oct 21..27: 23:30 GMT Oct 25 is still idx 4.
		{"Europe/Dublin", [5]int{2026, 10, 27, 12, 0}, [5]int{2026, 10, 25, 23, 30}, []Seg{{"prog", 0, 4}, {"review", 4, 7}}},
	} {
		loc := mustLoc(t, tc.zone)
		a, b := tc.asOf, tc.to
		asOf := ldt(loc, a[0], time.Month(a[1]), a[2], a[3], a[4])
		at := ldt(loc, b[0], time.Month(b[1]), b[2], b[3], b[4])
		tl := []Interval{
			ivl("prog", ldt(loc, 2026, time.January, 5, 9, 0), at),
			ivl("review", at, asOf),
		}
		segs := Segments(tl, NewWindow(asOf, 7))
		if len(segs) != len(tc.want) {
			t.Errorf("%s transition %v: got %+v, want %+v", tc.zone, at, segs, tc.want)
			continue
		}
		for i := range segs {
			if segs[i] != tc.want[i] {
				t.Errorf("%s transition %v: seg %d = %+v, want %+v", tc.zone, at, i, segs[i], tc.want[i])
			}
		}
	}
}
