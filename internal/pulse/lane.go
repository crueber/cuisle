package pulse

import (
	"time"

	"github.com/crueber/cuisle/internal/model"
)

// EnteredLane returns the start of the latest contiguous run of intervals whose RealLane equals the last interval's
// RealLane (zero time for an empty timeline).
func EnteredLane(tl []Interval) time.Time {
	if len(tl) == 0 {
		return time.Time{}
	}
	last := tl[len(tl)-1].RealLane
	i := len(tl) - 1
	for i > 0 && tl[i-1].RealLane == last {
		i--
	}
	return tl[i].From
}

// DaysInLane = max(1, calendar days between the local dates of entered and asOf) (jira-sync.md §5.4).
func DaysInLane(entered, asOf time.Time) int {
	loc := asOf.Location()
	ey, em, ed := entered.In(loc).Date()
	ay, am, ad := asOf.Date()
	e := time.Date(ey, em, ed, 0, 0, 0, 0, time.UTC)
	a := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	days := int(a.Sub(e) / (24 * time.Hour))
	if days < 1 {
		return 1
	}
	return days
}

// OverLimit = lane is a working lane (built-in or custom) AND limitDays > 0 AND days > limitDays.
func OverLimit(lane string, days, limitDays int) bool {
	return model.IsWorkingLane(lane) && limitDays > 0 && days > limitDays
}

// DoneSince returns the lower bound of the done window for "14d" (local midnight 14 days ago) and "yesterday" (local
// midnight of the previous working day: Monday → Friday). "sprint" returns the zero time (caller filters by sprint).
func DoneSince(window string, asOf time.Time) time.Time {
	loc := asOf.Location()
	y, m, d := asOf.Date()
	switch window {
	case "14d":
		return time.Date(y, m, d-14, 0, 0, 0, 0, loc)
	case "yesterday":
		for k := 1; ; k++ {
			t := time.Date(y, m, d-k, 0, 0, 0, 0, loc)
			if wd := t.Weekday(); wd != time.Saturday && wd != time.Sunday {
				return t
			}
		}
	}
	return time.Time{}
}

// DoneLabel returns "this sprint" | "14 days" | "since <Weekday>" for the window as of asOf.
func DoneLabel(window string, asOf time.Time) string {
	switch window {
	case "sprint":
		return "this sprint"
	case "14d":
		return "14 days"
	case "yesterday":
		return "since " + DoneSince(window, asOf).Weekday().String()
	}
	return ""
}
