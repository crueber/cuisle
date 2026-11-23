package pulse

import (
	"sort"
	"strconv"
	"time"

	"github.com/crueber/cuisle/internal/model"
)

// NewWindow builds the window of days ending on the local date of asOf (in asOf.Location()).
func NewWindow(asOf time.Time, days int) Window {
	loc := asOf.Location()
	y, m, d := asOf.Date()
	starts := make([]time.Time, days+1)
	for k := range starts {
		starts[k] = time.Date(y, m, d-(days-1)+k, 0, 0, 0, 0, loc)
	}
	return Window{Days: days, Loc: loc, AsOf: asOf, Starts: starts}
}

// DayIndex returns k with Starts[k] ≤ t < Starts[k+1]; -1 before the window; Days at or after Starts[Days].
func (w Window) DayIndex(t time.Time) int {
	if len(w.Starts) == 0 || t.Before(w.Starts[0]) {
		return -1
	}
	if !t.Before(w.Starts[w.Days]) {
		return w.Days
	}
	i := sort.Search(len(w.Starts), func(i int) bool { return t.Before(w.Starts[i]) })
	return i - 1
}

// Axis returns the Days axis entries, oldest first.
func (w Window) Axis() []AxisDay {
	out := make([]AxisDay, 0, w.Days)
	for k := 0; k < w.Days; k++ {
		s := w.Starts[k]
		wd := s.Weekday()
		out = append(out, AxisDay{
			Date:    s.Format("2006-01-02"),
			Label:   strconv.Itoa(s.Day()),
			Weekend: wd == time.Saturday || wd == time.Sunday,
			Monday:  wd == time.Monday,
		})
	}
	return out
}

// Timeline builds the status timeline as of asOf (jira-sync.md §5.2 step 1–2): initial status = From of the first
// change ≤ asOf (or the current status when none), effective from CreatedAt; a synthetic change at UpdatedAt when the
// last change's To differs from the current status; mapped through statusLanes. Adjacent intervals merge only when
// Lane, RealLane AND StatusID all match (BlockedSpans needs StatusID, EnteredLane needs RealLane); Segments merges
// by drawn Lane.
func Timeline(t Ticket, statusLanes map[string]string, asOf time.Time) []Interval {
	type step struct {
		at     time.Time
		status string
	}
	var changes []model.StatusChange
	for _, c := range t.StatusChanges {
		if !c.At.After(asOf) {
			changes = append(changes, c)
		}
	}
	initial := t.StatusID
	if len(changes) > 0 {
		initial = changes[0].From
	}
	steps := []step{{t.CreatedAt, initial}}
	for _, c := range changes {
		steps = append(steps, step{c.At, c.To})
	}
	if n := len(changes); n > 0 && changes[n-1].To != t.StatusID {
		at := t.UpdatedAt
		if last := changes[n-1].At; at.Before(last) {
			at = last
		}
		if at.After(asOf) {
			at = asOf
		}
		steps = append(steps, step{at, t.StatusID})
	}

	var out []Interval
	for i, s := range steps {
		from := clampTime(s.at, t.CreatedAt, asOf)
		to := asOf
		if i+1 < len(steps) {
			to = clampTime(steps[i+1].at, t.CreatedAt, asOf)
		}
		drawn, real := laneOf(s.status, statusLanes)
		iv := Interval{StatusID: s.status, Lane: drawn, RealLane: real, From: from, To: to}
		if n := len(out); n > 0 && out[n-1].Lane == iv.Lane && out[n-1].RealLane == iv.RealLane && out[n-1].StatusID == iv.StatusID {
			out[n-1].To = iv.To
			continue
		}
		out = append(out, iv)
	}
	return out
}

// Segments snaps a timeline to the window (§5.2 steps 3–5): the day of a transition belongs to the new lane, the last
// lane of a day wins, zero-width segments dropped, pre-window state starts at 0, a ticket created in the window starts
// at its creation day, the last segment ends at Days. Contiguous, oldest first.
func Segments(tl []Interval, w Window) []Seg {
	var segs []Seg
	for k := 0; k < w.Days; k++ {
		lane, ok := dayLane(tl, w.Starts[k], w.Starts[k+1])
		if !ok {
			continue
		}
		if n := len(segs); n > 0 && segs[n-1].Lane == lane && segs[n-1].To == k {
			segs[n-1].To = k + 1
			continue
		}
		segs = append(segs, Seg{Lane: lane, From: k, To: k + 1})
	}
	return segs
}

// dayLane returns the drawn lane active at the end of the day [start, end): the last non-zero-width interval that
// overlaps the day.
func dayLane(tl []Interval, start, end time.Time) (string, bool) {
	lane, ok := "", false
	for _, iv := range tl {
		if iv.From.Before(end) && iv.To.After(start) && iv.From.Before(iv.To) {
			lane, ok = iv.Lane, true
		}
	}
	return lane, ok
}

// laneOf maps a status to its drawn lane and its real lane (jira-sync.md §5.2). Unmapped statuses draw as todo with
// no real lane; backlog and hidden draw as todo but keep their real lane.
func laneOf(statusID string, statusLanes map[string]string) (drawn, real string) {
	lane, ok := statusLanes[statusID]
	if !ok || lane == "" {
		return model.LaneTodo, ""
	}
	if lane == model.LaneBacklog || lane == model.LaneHidden {
		return model.LaneTodo, lane
	}
	return lane, lane
}

func clampTime(t, lo, hi time.Time) time.Time {
	if t.Before(lo) {
		return lo
	}
	if t.After(hi) {
		return hi
	}
	return t
}
