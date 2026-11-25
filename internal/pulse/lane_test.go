package pulse

// WP-04 tests: EnteredLane, DaysInLane, OverLimit, DoneSince, DoneLabel (jira-sync.md §5.4, §5.6).
//
// SPEC-NOTE: DaysInLane compares the LOCAL dates of entered and asOf in asOf's location (entered is converted with
// asOf.Location()), so an `entered` expressed in UTC still counts local calendar days. It returns max(1, difference),
// which also means an `entered` after asOf yields 1.
//
// SPEC-NOTE: DoneSince("yesterday") is only specified for Monday→Friday and weekday→previous day; Saturday/Sunday are
// not asserted. Unknown window names are not asserted.

import (
	"testing"
	"time"
	_ "time/tzdata"
)

func TestEnteredLane(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	d := func(day, h int) time.Time { return ldt(chi, 2026, time.November, day, h, 0) }
	asOf := d(23, 15)
	cases := []struct {
		name string
		tl   []Interval
		want time.Time
	}{
		{"empty timeline is the zero time", nil, time.Time{}},
		{"single interval", []Interval{ivl("prog", d(5, 9), asOf)}, d(5, 9)},
		{
			"latest contiguous run only (prog, review, prog)",
			[]Interval{ivl("prog", d(5, 9), d(8, 9)), ivl("review", d(8, 9), d(12, 9)), ivl("prog", d(12, 9), asOf)},
			d(12, 9),
		},
		{
			"a run of several intervals with the same real lane starts at the first of them",
			[]Interval{
				ivl("review", d(5, 9), d(8, 9)),
				ivlS("s-prog", "prog", d(8, 9), d(12, 9)),
				ivlS("s-dev", "prog", d(12, 9), asOf),
			},
			d(8, 9),
		},
		{
			"real lane drives the run, not the drawn lane (backlog drawn as todo, then todo)",
			[]Interval{
				{StatusID: "s-backlog", Lane: "todo", RealLane: "backlog", From: d(5, 9), To: d(8, 9)},
				{StatusID: "s-todo", Lane: "todo", RealLane: "todo", From: d(8, 9), To: asOf},
			},
			d(8, 9),
		},
		{
			"reopened Done: only the latest Done run counts",
			[]Interval{
				ivl("prog", d(5, 9), d(8, 9)), ivl("done", d(8, 9), d(12, 9)),
				ivl("prog", d(12, 9), d(18, 9)), ivl("done", d(18, 9), asOf),
			},
			d(18, 9),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EnteredLane(tc.tl)
			if tc.want.IsZero() {
				if !got.IsZero() {
					t.Errorf("EnteredLane = %v, want zero time", got)
				}
				return
			}
			if !got.Equal(tc.want) {
				t.Errorf("EnteredLane = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDaysInLane(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0) // Monday
	cases := []struct {
		name    string
		entered time.Time
		asOf    time.Time
		want    int
	}{
		{"entered today = 1", ldt(chi, 2026, time.November, 23, 9, 0), asOf, 1},
		{"entered a minute ago = 1", ldt(chi, 2026, time.November, 23, 14, 59), asOf, 1},
		{"entered yesterday = 1", ldt(chi, 2026, time.November, 22, 9, 0), asOf, 1},
		{"entered 23:59 yesterday vs 00:01 today = 1", ldt(chi, 2026, time.November, 22, 23, 59), ldt(chi, 2026, time.November, 23, 0, 1), 1},
		{"two calendar days ago = 2", ldt(chi, 2026, time.November, 21, 9, 0), asOf, 2},
		{"calendar days, not 24h blocks: 00:01 two days ago to 23:59 today = 2", ldt(chi, 2026, time.November, 21, 0, 1), ldt(chi, 2026, time.November, 23, 23, 59), 2},
		{"two weeks = 14", ldt(chi, 2026, time.November, 9, 8, 0), asOf, 14},
		{"across a month boundary", ldt(chi, 2026, time.October, 30, 12, 0), asOf, 24},
		{"across the spring-forward change (71h elapsed) = 3 calendar days", ldt(chi, 2026, time.March, 7, 12, 0), ldt(chi, 2026, time.March, 10, 11, 0), 3},
		{"across the fall-back change (73h elapsed) = 3 calendar days", ldt(chi, 2026, time.October, 31, 12, 0), ldt(chi, 2026, time.November, 3, 13, 0), 3},
		{"entered in UTC counts local dates (Nov 21 03:00Z = Nov 20 21:00 CST)", time.Date(2026, time.November, 21, 3, 0, 0, 0, time.UTC), asOf, 3},
		{"entered after asOf is still at least 1", ldt(chi, 2026, time.November, 24, 9, 0), asOf, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DaysInLane(tc.entered, tc.asOf); got != tc.want {
				t.Errorf("DaysInLane(%v, %v) = %d, want %d", tc.entered, tc.asOf, got, tc.want)
			}
		})
	}
}

func TestDaysInLane_PastAsOfUsesAsOfNotNow(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	entered := ldt(chi, 2026, time.November, 10, 9, 0)
	asOf := ldt(chi, 2026, time.November, 15, 12, 0)
	if got := DaysInLane(entered, asOf); got != 5 {
		t.Errorf("DaysInLane = %d, want 5", got)
	}
}

func TestOverLimit(t *testing.T) {
	cases := []struct {
		name  string
		lane  string
		days  int
		limit int
		want  bool
	}{
		{"strictly greater is over", "prog", 6, 5, true},
		{"equal to the limit is NOT over (> not ≥)", "prog", 5, 5, false},
		{"below the limit", "prog", 2, 5, false},
		{"limit 0 means never over", "prog", 99, 0, false},
		{"negative limit never over", "prog", 99, -1, false},
		{"todo works", "todo", 11, 10, true},
		{"review works", "review", 3, 2, true},
		{"qa works", "qa", 4, 3, true},
		{"done is never over", "done", 99, 1, false},
		{"backlog is not a working lane", "backlog", 99, 1, false},
		{"hidden is not a working lane", "hidden", 99, 1, false},
		{"a custom lane is a working lane", "c3", 6, 5, true},
		{"a custom lane at its limit is not over", "c12", 5, 5, false},
		{"a malformed custom id is not a lane", "c0", 99, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OverLimit(tc.lane, tc.days, tc.limit); got != tc.want {
				t.Errorf("OverLimit(%q, %d, %d) = %v, want %v", tc.lane, tc.days, tc.limit, got, tc.want)
			}
		})
	}
}

func TestDoneSince(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	mid := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, chi) }
	cases := []struct {
		name   string
		window string
		asOf   time.Time
		want   time.Time
	}{
		{"14d on a Monday afternoon", "14d", ldt(chi, 2026, time.November, 23, 15, 0), mid(2026, time.November, 9)},
		{"14d at 00:00 same day", "14d", ldt(chi, 2026, time.November, 23, 0, 0), mid(2026, time.November, 9)},
		{"14d at 23:59 same day", "14d", ldt(chi, 2026, time.November, 23, 23, 59), mid(2026, time.November, 9)},
		{"14d across fall-back is local midnight CDT", "14d", ldt(chi, 2026, time.November, 10, 12, 0), mid(2026, time.October, 27)},
		{"14d just after fall-back", "14d", ldt(chi, 2026, time.November, 2, 9, 0), mid(2026, time.October, 19)},
		{"14d across spring-forward is local midnight CST", "14d", ldt(chi, 2026, time.March, 10, 12, 0), mid(2026, time.February, 24)},
		{"yesterday on a Monday looks back to Friday", "yesterday", ldt(chi, 2026, time.November, 23, 15, 0), mid(2026, time.November, 20)},
		{"yesterday on a Monday at 00:30", "yesterday", ldt(chi, 2026, time.November, 23, 0, 30), mid(2026, time.November, 20)},
		{"yesterday on a Tuesday is Monday", "yesterday", ldt(chi, 2026, time.November, 24, 9, 0), mid(2026, time.November, 23)},
		{"yesterday on a Wednesday is Tuesday", "yesterday", ldt(chi, 2026, time.November, 25, 15, 0), mid(2026, time.November, 24)},
		{"yesterday on a Friday is Thursday", "yesterday", ldt(chi, 2026, time.November, 20, 15, 0), mid(2026, time.November, 19)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DoneSince(tc.window, tc.asOf); !got.Equal(tc.want) {
				t.Errorf("DoneSince(%q, %v) = %v, want %v", tc.window, tc.asOf, got, tc.want)
			}
		})
	}
}

func TestDoneSince_SprintIsZeroTime(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	if got := DoneSince("sprint", ldt(chi, 2026, time.November, 23, 15, 0)); !got.IsZero() {
		t.Errorf("DoneSince(sprint) = %v, want zero time (caller filters by sprint)", got)
	}
}

func TestDoneLabel(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	cases := []struct {
		name   string
		window string
		asOf   time.Time
		want   string
	}{
		{"sprint", "sprint", ldt(chi, 2026, time.November, 23, 15, 0), "this sprint"},
		{"14d", "14d", ldt(chi, 2026, time.November, 23, 15, 0), "14 days"},
		{"yesterday on a Monday", "yesterday", ldt(chi, 2026, time.November, 23, 15, 0), "since Friday"},
		{"yesterday on a Tuesday", "yesterday", ldt(chi, 2026, time.November, 24, 15, 0), "since Monday"},
		{"yesterday on a Wednesday", "yesterday", ldt(chi, 2026, time.November, 25, 15, 0), "since Tuesday"},
		{"yesterday on a Friday", "yesterday", ldt(chi, 2026, time.November, 20, 15, 0), "since Thursday"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DoneLabel(tc.window, tc.asOf); got != tc.want {
				t.Errorf("DoneLabel(%q, %v) = %q, want %q", tc.window, tc.asOf, got, tc.want)
			}
		})
	}
}
