package pulse

// WP-05 tests: Marks and Totals (jira-sync.md §5.3).
//
// This file is self-contained: it builds Window literals by hand (no NewWindow, which is WP-04) so the WP-05
// implementer is not blocked on WP-04. NOTE for implementers: if Marks calls Window.DayIndex it depends on WP-04; to
// stay independent, bucket with a local loop over w.Starts.
//
// SPEC-NOTE: a "PR update" is any DevPR event; distinct PRs are keyed by DevEvent.ID ("<repo>#<id>"), so the same
// number in two repos is two PRs.
//
// SPEC-NOTE: a comment whose author is missing from the people map counts (only Kind == "app" is excluded).
//
// SPEC-NOTE: "events before the ticket's creation day are dropped" is by local DAY, so an event earlier in the day on
// the creation day is kept and an event on the previous day is dropped, even when it is inside the window.
//
// SPEC-NOTE (not asserted): the same commit SHA appearing on two different days.

import (
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/crueber/cuisle/internal/model"
)

func m5Loc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func m5At(loc *time.Location, y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, loc)
}

// m5Window builds a Window literal (Starts[k] = local midnight of today−(n−1)+k).
func m5Window(loc *time.Location, asOf time.Time, n int) Window {
	y, mo, d := asOf.In(loc).Date()
	starts := make([]time.Time, n+1)
	for k := range starts {
		starts[k] = time.Date(y, mo, d-(n-1)+k, 0, 0, 0, 0, loc)
	}
	return Window{Days: n, Loc: loc, AsOf: asOf, Starts: starts}
}

func m5Commit(sha string, at time.Time, merge bool) model.DevEvent {
	return model.DevEvent{Kind: model.DevCommit, ID: sha, Repo: "acme/web", At: at, Merge: merge}
}

func m5PR(id string, at time.Time, status string) model.DevEvent {
	return model.DevEvent{Kind: model.DevPR, ID: id, Repo: id[:len(id)-2], At: at, Status: status}
}

func m5Comment(id string, at time.Time, author string) model.Comment {
	return model.Comment{ID: id, At: at, AuthorID: author}
}

var m5People = map[string]model.Person{
	"u1":  {AccountID: "u1", DisplayName: "Una", Active: true, Kind: "user"},
	"u2":  {AccountID: "u2", DisplayName: "Dara", Active: true, Kind: "user"},
	"bot": {AccountID: "bot", DisplayName: "Automation for Jira", Active: true, Kind: "app"},
}

func m5AssertMarks(t *testing.T, got, want []Mark) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("marks: got %d %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mark %d: got %+v, want %+v (all: %+v)", i, got[i], want[i], got)
		}
	}
}

func TestMarks_Buckets(t *testing.T) {
	chi := m5Loc(t, "America/Chicago")
	asOf := m5At(chi, 2026, time.November, 23, 15, 0)
	w := m5Window(chi, asOf, 21) // idx = day-of-month − 3 for November
	created := m5At(chi, 2026, time.November, 5, 9, 0)
	nov := func(d, h, mi int) time.Time { return m5At(chi, 2026, time.November, d, h, mi) }

	cases := []struct {
		name string
		tk   Ticket
		want []Mark
	}{
		{
			"commits counted per day, merge commits excluded, same SHA deduplicated",
			Ticket{CreatedAt: created, Dev: []model.DevEvent{
				m5Commit("a1", nov(10, 9, 0), false),
				m5Commit("a2", nov(10, 10, 0), false),
				m5Commit("a3", nov(10, 11, 0), false),
				m5Commit("m1", nov(10, 12, 0), true), // merge: excluded
				m5Commit("a1", nov(10, 9, 0), false), // duplicate SHA
				m5Commit("mm", nov(14, 12, 0), true), // merge-only day: no mark at all
			}},
			[]Mark{{Day: 7, Commits: 3}},
		},
		{
			"distinct PR keys per day (several events for one PR count once)",
			Ticket{CreatedAt: created, Dev: []model.DevEvent{
				m5PR("acme/web#1", nov(12, 9, 0), "OPEN"),
				m5PR("acme/web#1", nov(12, 13, 0), "MERGED"),
				m5PR("acme/web#2", nov(12, 11, 0), "OPEN"),
				m5PR("acme/web#1", nov(13, 9, 0), "MERGED"), // same PR on another day counts again there
			}},
			[]Mark{{Day: 9, PRs: 2}, {Day: 10, PRs: 1}},
		},
		{
			"the same PR number in two repos is two PRs",
			Ticket{CreatedAt: created, Dev: []model.DevEvent{
				m5PR("acme/web#1", nov(12, 9, 0), "OPEN"),
				m5PR("acme/api#1", nov(12, 10, 0), "OPEN"),
			}},
			[]Mark{{Day: 9, PRs: 2}},
		},
		{
			"comments by people count; app authors are excluded; unknown authors count",
			Ticket{CreatedAt: created, Comments: []model.Comment{
				m5Comment("c1", nov(15, 9, 0), "u1"),
				m5Comment("c2", nov(15, 10, 0), "u2"),
				m5Comment("c3", nov(15, 11, 0), "bot"),
				m5Comment("c4", nov(15, 12, 0), "ghost"),
				m5Comment("c5", nov(16, 9, 0), "bot"), // only an app comment: no mark for that day
			}},
			[]Mark{{Day: 12, Comments: 3}},
		},
		{
			"a day with all three kinds, and unordered input comes out ascending with only non-zero days",
			Ticket{
				CreatedAt: created,
				Dev: []model.DevEvent{
					m5Commit("z9", nov(20, 9, 0), false),
					m5PR("acme/web#7", nov(12, 9, 0), "OPEN"),
					m5Commit("z1", nov(12, 10, 0), false),
					m5Commit("z2", nov(12, 11, 0), false),
				},
				Comments: []model.Comment{
					m5Comment("c1", nov(12, 12, 0), "u1"),
					m5Comment("c2", nov(6, 12, 0), "u2"),
				},
			},
			[]Mark{
				{Day: 3, Comments: 1},
				{Day: 9, Commits: 2, PRs: 1, Comments: 1},
				{Day: 17, Commits: 1},
			},
		},
		{
			"events before the creation day are dropped; earlier on the creation day is kept",
			Ticket{
				CreatedAt: m5At(chi, 2026, time.November, 10, 15, 0), // idx 7
				Dev: []model.DevEvent{
					m5Commit("b0", nov(9, 23, 59), false), // previous day: dropped
					m5Commit("b1", nov(10, 8, 0), false),  // creation day, before creation time: kept
					m5Commit("b2", nov(11, 8, 0), false),
				},
				Comments: []model.Comment{m5Comment("c0", nov(9, 12, 0), "u1")},
			},
			[]Mark{{Day: 7, Commits: 1}, {Day: 8, Commits: 1}},
		},
		{
			"events before the window are dropped; the first instant of the window is day 0",
			Ticket{
				CreatedAt: m5At(chi, 2026, time.October, 20, 9, 0),
				Dev: []model.DevEvent{
					m5Commit("w0", nov(2, 23, 59), false), // before window
					m5Commit("w1", nov(3, 0, 0), false),   // idx 0
				},
				Comments: []model.Comment{m5Comment("c0", m5At(chi, 2026, time.October, 25, 12, 0), "u1")},
			},
			[]Mark{{Day: 0, Commits: 1}},
		},
		{
			"events after asOf are dropped; earlier today is day N-1",
			Ticket{
				CreatedAt: created,
				Dev: []model.DevEvent{
					m5Commit("f0", nov(23, 14, 59), false), // kept: idx 20
					m5Commit("f1", m5At(chi, 2026, time.November, 23, 15, 0).Add(time.Second), false),
					m5Commit("f2", nov(24, 9, 0), false),
					m5PR("acme/web#9", nov(24, 9, 0), "OPEN"),
				},
				Comments: []model.Comment{m5Comment("c0", nov(23, 16, 0), "u1")},
			},
			[]Mark{{Day: 20, Commits: 1}},
		},
		{
			"buckets are local days: 23:30 and 00:30 local (same UTC date) are different days",
			Ticket{CreatedAt: created, Dev: []model.DevEvent{
				m5Commit("l1", nov(10, 23, 30), false), // 05:30Z Nov 11
				m5Commit("l2", nov(11, 0, 30), false),  // 06:30Z Nov 11
			}},
			[]Mark{{Day: 7, Commits: 1}, {Day: 8, Commits: 1}},
		},
		{
			"no events at all gives no marks",
			Ticket{CreatedAt: created},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m5AssertMarks(t, Marks(tc.tk, w, m5People), tc.want)
		})
	}
}

func TestMarks_NilPeopleMapIsSafe(t *testing.T) {
	chi := m5Loc(t, "America/Chicago")
	asOf := m5At(chi, 2026, time.November, 23, 15, 0)
	w := m5Window(chi, asOf, 21)
	tk := Ticket{
		CreatedAt: m5At(chi, 2026, time.November, 5, 9, 0),
		Comments:  []model.Comment{m5Comment("c1", m5At(chi, 2026, time.November, 10, 9, 0), "u1")},
	}
	m5AssertMarks(t, Marks(tk, w, nil), []Mark{{Day: 7, Comments: 1}})
}

func TestMarks_PastAsOf_IgnoresEventsAfterAsOf(t *testing.T) {
	chi := m5Loc(t, "America/Chicago")
	asOf := m5At(chi, 2026, time.November, 15, 12, 0) // N=7 → Nov 9 (0) … Nov 15 (6)
	w := m5Window(chi, asOf, 7)
	at := func(d, h, mi int) time.Time { return m5At(chi, 2026, time.November, d, h, mi) }
	tk := Ticket{
		CreatedAt: m5At(chi, 2026, time.October, 1, 9, 0),
		Dev: []model.DevEvent{
			m5Commit("p0", at(8, 23, 59), false), // before the window
			m5Commit("p1", at(9, 9, 0), false),   // idx 0
			m5Commit("p2", at(15, 11, 0), false), // idx 6, before asOf
			m5Commit("p3", at(15, 13, 0), false), // after asOf
			m5Commit("p4", at(18, 9, 0), false),  // after asOf
			m5PR("acme/web#3", at(16, 9, 0), "MERGED"),
		},
		Comments: []model.Comment{m5Comment("c1", at(17, 9, 0), "u1")},
	}
	m5AssertMarks(t, Marks(tk, w, m5People), []Mark{{Day: 0, Commits: 1}, {Day: 6, Commits: 1}})
}

// Day buckets in the OS timezone, including both DST transition days (America/Chicago and Europe/Dublin).
func TestMarks_DSTTransitionDaysBucketByLocalDay(t *testing.T) {
	cases := []struct {
		name   string
		zone   string
		asOf   [5]int
		probes [][6]int // y, m, d, h, min, wantIdx (one commit each; N=7)
	}{
		{
			"Chicago spring forward 2026-03-08", "America/Chicago", [5]int{2026, 3, 10, 15, 0},
			[][6]int{{2026, 3, 7, 23, 30, 3}, {2026, 3, 8, 0, 30, 4}, {2026, 3, 8, 23, 30, 4}, {2026, 3, 9, 0, 30, 5}, {2026, 3, 10, 9, 0, 6}},
		},
		{
			"Chicago fall back 2026-11-01", "America/Chicago", [5]int{2026, 11, 3, 15, 0},
			[][6]int{{2026, 10, 31, 23, 30, 3}, {2026, 11, 1, 0, 30, 4}, {2026, 11, 1, 23, 30, 4}, {2026, 11, 2, 0, 30, 5}, {2026, 11, 3, 9, 0, 6}},
		},
		{
			"Dublin spring forward 2026-03-29", "Europe/Dublin", [5]int{2026, 3, 31, 12, 0},
			[][6]int{{2026, 3, 28, 23, 30, 3}, {2026, 3, 29, 0, 30, 4}, {2026, 3, 29, 23, 30, 4}, {2026, 3, 30, 0, 30, 5}, {2026, 3, 31, 9, 0, 6}},
		},
		{
			"Dublin fall back 2026-10-25", "Europe/Dublin", [5]int{2026, 10, 27, 12, 0},
			[][6]int{{2026, 10, 24, 23, 30, 3}, {2026, 10, 25, 0, 30, 4}, {2026, 10, 25, 23, 30, 4}, {2026, 10, 26, 0, 30, 5}, {2026, 10, 27, 9, 0, 6}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc := m5Loc(t, tc.zone)
			a := tc.asOf
			asOf := m5At(loc, a[0], time.Month(a[1]), a[2], a[3], a[4])
			w := m5Window(loc, asOf, 7)
			var devs []model.DevEvent
			counts := map[int]int{}
			for i, p := range tc.probes {
				devs = append(devs, m5Commit("sha"+string(rune('a'+i)), m5At(loc, p[0], time.Month(p[1]), p[2], p[3], p[4]), false))
				counts[p[5]]++
			}
			var want []Mark
			for idx := 0; idx < 7; idx++ {
				if counts[idx] > 0 {
					want = append(want, Mark{Day: idx, Commits: counts[idx]})
				}
			}
			tk := Ticket{CreatedAt: m5At(loc, 2026, time.January, 5, 9, 0), Dev: devs}
			m5AssertMarks(t, Marks(tk, w, m5People), want)
		})
	}
}

func TestTotals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		marks   []Mark
		c, p, m int
	}{
		{"nil", nil, 0, 0, 0},
		{"empty", []Mark{}, 0, 0, 0},
		{"single", []Mark{{Day: 3, Commits: 2, PRs: 1, Comments: 4}}, 2, 1, 4},
		{"several days", []Mark{{Day: 0, Commits: 1}, {Day: 5, PRs: 2, Comments: 1}, {Day: 9, Commits: 4, PRs: 1, Comments: 3}}, 5, 3, 4},
	} {
		c, p, m := Totals(tc.marks)
		if c != tc.c || p != tc.p || m != tc.m {
			t.Errorf("%s: Totals = (%d, %d, %d), want (%d, %d, %d)", tc.name, c, p, m, tc.c, tc.p, tc.m)
		}
	}
}
