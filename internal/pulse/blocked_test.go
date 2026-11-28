package pulse

// WP-04 tests: BlockedNow, BlockedSpans, BlockedDays (jira-sync.md §5.5).
//
// SPEC-NOTE: day indexes are INCLUSIVE at both ends: a span [on, off) covers dayIndex(on) … dayIndex(off), and an open
// span (ends at asOf) ends at asOf's day (N-1). Instants exactly at local midnight are deliberately not used.
//
// SPEC-NOTE: BlockedSpans returns spans sorted ascending by From. Overlapping spans (sharing at least one day) are
// merged; merely adjacent spans (e.g. {5,5} and {6,6}) are not asserted either way.
//
// SPEC-NOTE: a flag span "from before the first change" (first change turns the flag OFF) starts at the ticket's
// existence; the tests only use tickets created before the window so the result is clipped to day 0.

import (
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/crueber/cuisle/internal/model"
)

func fc(id string, at time.Time, flagged bool) model.FlagChange {
	return model.FlagChange{ID: id, At: at, Flagged: flagged}
}

func assertSpans(t *testing.T, got, want []Span) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("spans: got %d %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("span %d: got %+v, want %+v (all: %+v)", i, got[i], want[i], got)
		}
	}
}

func TestBlockedNow(t *testing.T) {
	for _, tc := range []struct {
		flagged, hint, want bool
	}{
		{false, false, false},
		{true, false, true},
		{false, true, true},
		{true, true, true},
	} {
		if got := BlockedNow(tc.flagged, tc.hint); got != tc.want {
			t.Errorf("BlockedNow(flagged=%v, hint=%v) = %v, want %v", tc.flagged, tc.hint, got, tc.want)
		}
	}
}

func TestBlockedSpans_FlagSpans(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	w := NewWindow(asOf, 21) // idx = day-of-month − 3 for November
	created := ldt(chi, 2026, time.October, 1, 9, 0)
	nov := func(d, h int) time.Time { return ldt(chi, 2026, time.November, d, h, 0) }
	tl := []Interval{ivl("prog", created, asOf)}
	none := map[string]bool{"s-blocked": true}

	cases := []struct {
		name string
		tk   Ticket
		want []Span
	}{
		{
			"flag turned on before the window and off inside it",
			Ticket{CreatedAt: created, UpdatedAt: nov(8, 10), Flagged: false,
				FlagChanges: []model.FlagChange{fc("1", ldt(chi, 2026, time.October, 20, 9, 0), true), fc("2", nov(8, 10), false)}},
			[]Span{{0, 5}},
		},
		{
			"first change turns the flag off: it was on before (inverse of the result)",
			Ticket{CreatedAt: created, UpdatedAt: nov(8, 10), Flagged: false,
				FlagChanges: []model.FlagChange{fc("1", nov(8, 10), false)}},
			[]Span{{0, 5}},
		},
		{
			"flag span entirely before the window is dropped",
			Ticket{CreatedAt: created, UpdatedAt: nov(1, 10), Flagged: false,
				FlagChanges: []model.FlagChange{fc("1", ldt(chi, 2026, time.October, 10, 9, 0), true), fc("2", ldt(chi, 2026, time.October, 20, 9, 0), false)}},
			nil,
		},
		{
			"flag span open at today ends at asOf's day",
			Ticket{CreatedAt: created, UpdatedAt: nov(20, 9), Flagged: true,
				FlagChanges: []model.FlagChange{fc("1", nov(20, 9), true)}},
			[]Span{{17, 20}},
		},
		{
			"flag on before the window and still on: whole window",
			Ticket{CreatedAt: created, UpdatedAt: nov(22, 9), Flagged: true,
				FlagChanges: []model.FlagChange{fc("1", ldt(chi, 2026, time.October, 15, 9, 0), true)}},
			[]Span{{0, 20}},
		},
		{
			"toggled several times in one day gives one single-day span",
			Ticket{CreatedAt: created, UpdatedAt: nov(10, 12), Flagged: false,
				FlagChanges: []model.FlagChange{
					fc("1", nov(10, 9), true), fc("2", nov(10, 10), false),
					fc("3", nov(10, 11), true), fc("4", nov(10, 12), false),
				}},
			[]Span{{7, 7}},
		},
		{
			"toggled several times in one day and left on stays open",
			Ticket{CreatedAt: created, UpdatedAt: nov(10, 11), Flagged: true,
				FlagChanges: []model.FlagChange{
					fc("1", nov(10, 9), true), fc("2", nov(10, 10), false), fc("3", nov(10, 11), true),
				}},
			[]Span{{7, 20}},
		},
		{
			"two separate flag spans stay separate and ascending",
			Ticket{CreatedAt: created, UpdatedAt: nov(18, 10), Flagged: false,
				FlagChanges: []model.FlagChange{
					fc("1", nov(5, 10), true), fc("2", nov(6, 10), false), // idx 2..3
					fc("3", nov(15, 10), true), fc("4", nov(18, 10), false), // idx 12..15
				}},
			[]Span{{2, 3}, {12, 15}},
		},
		{
			"flagged with no history: [UpdatedAt, asOf)",
			Ticket{CreatedAt: created, UpdatedAt: nov(18, 10), Flagged: true},
			[]Span{{15, 20}},
		},
		{
			"not flagged and no history: no spans",
			Ticket{CreatedAt: created, UpdatedAt: nov(18, 10), Flagged: false},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertSpans(t, BlockedSpans(tc.tk, tl, none, w, asOf), tc.want)
		})
	}
}

func TestBlockedSpans_StatusSpansViaBlockedStatuses(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	w := NewWindow(asOf, 21)
	created := ldt(chi, 2026, time.October, 1, 9, 0)
	nov := func(d, h int) time.Time { return ldt(chi, 2026, time.November, d, h, 0) }
	tk := Ticket{CreatedAt: created, UpdatedAt: asOf, StatusID: "s-prog"}
	blocked := map[string]bool{"s-blocked": true}

	cases := []struct {
		name string
		tl   []Interval
		want []Span
	}{
		{
			"blocked in the middle of the window",
			[]Interval{
				ivlS("s-prog", "prog", created, nov(12, 10)),
				ivlS("s-blocked", "prog", nov(12, 10), nov(16, 9)),
				ivlS("s-prog", "prog", nov(16, 9), asOf),
			},
			[]Span{{9, 13}},
		},
		{
			"blocked now (open interval ends at asOf's day)",
			[]Interval{ivlS("s-prog", "prog", created, nov(20, 9)), ivlS("s-blocked", "prog", nov(20, 9), asOf)},
			[]Span{{17, 20}},
		},
		{
			"blocked before the window, unblocked inside it: clipped at day 0",
			[]Interval{ivlS("s-blocked", "prog", created, nov(5, 9)), ivlS("s-prog", "prog", nov(5, 9), asOf)},
			[]Span{{0, 2}},
		},
		{
			"blocked entirely before the window: dropped",
			[]Interval{
				ivlS("s-blocked", "prog", created, ldt(chi, 2026, time.October, 20, 9, 0)),
				ivlS("s-prog", "prog", ldt(chi, 2026, time.October, 20, 9, 0), asOf),
			},
			nil,
		},
		{
			"two blocked stretches",
			[]Interval{
				ivlS("s-prog", "prog", created, nov(5, 9)),
				ivlS("s-blocked", "prog", nov(5, 9), nov(6, 17)),
				ivlS("s-prog", "prog", nov(6, 17), nov(15, 9)),
				ivlS("s-blocked", "prog", nov(15, 9), nov(16, 17)),
				ivlS("s-prog", "prog", nov(16, 17), asOf),
			},
			[]Span{{2, 3}, {12, 13}},
		},
		{
			"no blocked status in the timeline",
			[]Interval{ivlS("s-prog", "prog", created, asOf)},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertSpans(t, BlockedSpans(tk, tc.tl, blocked, w, asOf), tc.want)
		})
	}
}

func TestBlockedSpans_NilBlockedStatusesMapIsSafe(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	w := NewWindow(asOf, 21)
	created := ldt(chi, 2026, time.October, 1, 9, 0)
	tk := Ticket{CreatedAt: created, UpdatedAt: created}
	got := BlockedSpans(tk, []Interval{ivl("prog", created, asOf)}, nil, w, asOf)
	assertSpans(t, got, nil)
}

func TestBlockedSpans_FlagAndStatusSpansAreMergedWhenTheyOverlap(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	w := NewWindow(asOf, 21)
	created := ldt(chi, 2026, time.October, 1, 9, 0)
	nov := func(d, h int) time.Time { return ldt(chi, 2026, time.November, d, h, 0) }
	blocked := map[string]bool{"s-blocked": true}

	t.Run("overlapping flag and status spans merge into one", func(t *testing.T) {
		tk := Ticket{CreatedAt: created, UpdatedAt: nov(12, 10), Flagged: false,
			FlagChanges: []model.FlagChange{fc("1", nov(5, 10), true), fc("2", nov(12, 10), false)}} // idx 2..9
		tl := []Interval{
			ivlS("s-prog", "prog", created, nov(10, 10)),
			ivlS("s-blocked", "prog", nov(10, 10), nov(15, 9)), // idx 7..12
			ivlS("s-prog", "prog", nov(15, 9), asOf),
		}
		assertSpans(t, BlockedSpans(tk, tl, blocked, w, asOf), []Span{{2, 12}})
	})

	t.Run("a span contained in another disappears into it", func(t *testing.T) {
		tk := Ticket{CreatedAt: created, UpdatedAt: nov(14, 10), Flagged: false,
			FlagChanges: []model.FlagChange{fc("1", nov(8, 10), true), fc("2", nov(10, 10), false)}} // idx 5..7
		tl := []Interval{
			ivlS("s-prog", "prog", created, nov(5, 10)),
			ivlS("s-blocked", "prog", nov(5, 10), nov(14, 10)), // idx 2..11
			ivlS("s-prog", "prog", nov(14, 10), asOf),
		}
		assertSpans(t, BlockedSpans(tk, tl, blocked, w, asOf), []Span{{2, 11}})
	})

	t.Run("disjoint flag and status spans stay separate and sorted", func(t *testing.T) {
		tk := Ticket{CreatedAt: created, UpdatedAt: nov(19, 10), Flagged: false,
			FlagChanges: []model.FlagChange{fc("1", nov(17, 10), true), fc("2", nov(19, 10), false)}} // idx 14..16
		tl := []Interval{
			ivlS("s-prog", "prog", created, nov(5, 10)),
			ivlS("s-blocked", "prog", nov(5, 10), nov(6, 17)), // idx 2..3
			ivlS("s-prog", "prog", nov(6, 17), asOf),
		}
		assertSpans(t, BlockedSpans(tk, tl, blocked, w, asOf), []Span{{2, 3}, {14, 16}})
	})
}

func TestBlockedSpans_PastAsOf_IgnoresFlagChangesAfterAsOf(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 15, 12, 0) // Sunday; N=7 → Nov 9 (0) … Nov 15 (6)
	w := NewWindow(asOf, 7)
	created := ldt(chi, 2026, time.October, 1, 9, 0)
	tk := Ticket{
		CreatedAt: created, UpdatedAt: ldt(chi, 2026, time.November, 14, 10, 0),
		Flagged: true, // as of asOf
		FlagChanges: []model.FlagChange{
			fc("1", ldt(chi, 2026, time.November, 14, 10, 0), true),  // idx 5
			fc("2", ldt(chi, 2026, time.November, 18, 10, 0), false), // after asOf: must be ignored
		},
	}
	got := BlockedSpans(tk, []Interval{ivl("prog", created, asOf)}, map[string]bool{"s-blocked": true}, w, asOf)
	assertSpans(t, got, []Span{{5, 6}})
}

func TestBlockedSpans_DaysUseLocalTimezone(t *testing.T) {
	// 23:30 local Nov 8 and 00:30 local Nov 9 share a UTC date; they must land on different local days.
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 15, 12, 0)
	w := NewWindow(asOf, 7) // Nov 9 (0) … Nov 15 (6)
	created := ldt(chi, 2026, time.October, 1, 9, 0)
	tk := Ticket{
		CreatedAt: created, UpdatedAt: ldt(chi, 2026, time.November, 11, 0, 30), Flagged: false,
		FlagChanges: []model.FlagChange{
			fc("1", ldt(chi, 2026, time.November, 9, 0, 30), true),
			fc("2", ldt(chi, 2026, time.November, 11, 23, 30), false),
		},
	}
	got := BlockedSpans(tk, []Interval{ivl("prog", created, asOf)}, nil, w, asOf)
	assertSpans(t, got, []Span{{0, 2}})
}

// Pins the Timeline/BlockedSpans interplay described in the file-level SPEC-NOTE of segments_test.go: a blocked status
// mapped to the SAME lane as its neighbours (In Progress ↔ Blocked, both "prog") must still produce a blocked span,
// so Timeline may not merge such intervals in a way that drops the blocked StatusID.
func TestTimeline_BlockedStatusSharingALane_StaysVisibleToBlockedSpans(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	w := NewWindow(asOf, 21)
	created := ldt(chi, 2026, time.October, 1, 9, 0)
	a := ldt(chi, 2026, time.November, 12, 10, 0) // idx 9
	b := ldt(chi, 2026, time.November, 16, 9, 0)  // idx 13
	tk := Ticket{
		CreatedAt: created, UpdatedAt: b, StatusID: "s-prog",
		StatusChanges: []model.StatusChange{sc("1", "s-prog", "s-blocked", a), sc("2", "s-blocked", "s-prog", b)},
	}
	tl := Timeline(tk, testStatusLanes, asOf)
	assertSpans(t, BlockedSpans(tk, tl, map[string]bool{"s-blocked": true}, w, asOf), []Span{{9, 13}})
	// and the lane view is unaffected: one continuous prog lane
	assertSegs(t, Segments(tl, w), []Seg{{"prog", 0, 21}})
}

func TestBlockedDays(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spans []Span
		want  int
	}{
		{"nil", nil, 0},
		{"empty", []Span{}, 0},
		{"single day", []Span{{5, 5}}, 1},
		{"inclusive range", []Span{{0, 5}}, 6},
		{"two disjoint spans", []Span{{0, 2}, {5, 5}}, 4},
		{"overlapping input counts distinct days", []Span{{0, 3}, {2, 5}}, 6},
		{"whole window", []Span{{0, 20}}, 21},
	} {
		if got := BlockedDays(tc.spans); got != tc.want {
			t.Errorf("%s: BlockedDays(%+v) = %d, want %d", tc.name, tc.spans, got, tc.want)
		}
	}
}

func TestBlockedSpans_OptimisticFlagFallbackAndZeroWidthInterval(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	asOf := ldt(chi, 2026, time.November, 23, 15, 0)
	w := NewWindow(asOf, 21) // idx = day-of-month - 3 for November
	created := ldt(chi, 2026, time.October, 1, 9, 0)
	nov := func(d, h int) time.Time { return ldt(chi, 2026, time.November, d, h, 0) }
	tl := []Interval{ivlS("s-prog", "prog", created, asOf)}
	blocked := map[string]bool{"s-blocked": true}

	t.Run("optimistic flag with empty FlagChanges synthesizes [UpdatedAt, asOf]", func(t *testing.T) {
		tk := Ticket{CreatedAt: created, UpdatedAt: nov(20, 9), Flagged: true}
		got := BlockedSpans(tk, tl, blocked, w, asOf)
		assertSpans(t, got, []Span{{17, 20}})
		if days := BlockedDays(got); days != 4 {
			t.Errorf("BlockedDays(%+v) = %d, want 4", got, days)
		}
	})

	t.Run("optimistic flag when FlagChanges ends with Flagged=false", func(t *testing.T) {
		tk := Ticket{
			CreatedAt: created,
			UpdatedAt: nov(20, 9),
			Flagged:   true,
			FlagChanges: []model.FlagChange{
				fc("1", nov(5, 10), true),  // idx 2
				fc("2", nov(7, 10), false), // idx 4
			},
		}
		got := BlockedSpans(tk, tl, blocked, w, asOf)
		assertSpans(t, got, []Span{{2, 4}, {17, 20}})
		if days := BlockedDays(got); days != 7 {
			t.Errorf("BlockedDays(%+v) = %d, want 7", got, days)
		}
	})

	t.Run("optimistic flag with UpdatedAt before last FlagChange clamps to last change", func(t *testing.T) {
		tk := Ticket{
			CreatedAt: created,
			UpdatedAt: nov(10, 9),
			Flagged:   true,
			FlagChanges: []model.FlagChange{
				fc("1", nov(15, 10), true),  // idx 12
				fc("2", nov(18, 10), false), // idx 15
			},
		}
		got := BlockedSpans(tk, tl, blocked, w, asOf)
		assertSpans(t, got, []Span{{12, 20}})
		if days := BlockedDays(got); days != 9 {
			t.Errorf("BlockedDays(%+v) = %d, want 9", got, days)
		}
	})

	t.Run("zero-width blocked status interval is ignored", func(t *testing.T) {
		tk := Ticket{CreatedAt: created, UpdatedAt: asOf, StatusID: "s-prog"}
		zeroTL := []Interval{
			ivlS("s-prog", "prog", created, nov(12, 10)),
			ivlS("s-blocked", "prog", nov(12, 10), nov(12, 10)),
			ivlS("s-prog", "prog", nov(12, 10), asOf),
		}
		got := BlockedSpans(tk, zeroTL, blocked, w, asOf)
		assertSpans(t, got, nil)
		if days := BlockedDays(got); days != 0 {
			t.Errorf("BlockedDays(%+v) = %d, want 0", got, days)
		}
	})
}
