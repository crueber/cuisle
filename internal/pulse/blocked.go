package pulse

import (
	"sort"
	"time"

	"github.com/crueber/cuisle/internal/model"
)

// BlockedNow = flagged OR the current status has the blocked hint.
func BlockedNow(flagged, statusBlockedHint bool) bool { return flagged || statusBlockedHint }

// BlockedSpans is the union of flag spans (fold FlagChanges ≤ asOf into [on, off); state before the first change is the
// inverse of its result; open spans end at asOf; flagged with no history → [UpdatedAt, asOf)) and status spans
// (timeline intervals whose status is in blockedStatuses), snapped to inclusive day indexes, clipped and merged
// (jira-sync.md §5.5).
func BlockedSpans(t Ticket, tl []Interval, blockedStatuses map[string]bool, w Window, asOf time.Time) []Span {
	var spans []Span
	add := func(from, to time.Time) {
		f := w.DayIndex(from)
		if f < 0 {
			f = 0
		}
		last := w.DayIndex(to)
		if last >= w.Days {
			last = w.Days - 1
		}
		if last < f {
			return
		}
		spans = append(spans, Span{From: f, To: last})
	}

	var fcs []model.FlagChange
	for _, c := range t.FlagChanges {
		if !c.At.After(asOf) {
			fcs = append(fcs, c)
		}
	}
	if len(fcs) == 0 {
		if t.Flagged {
			add(t.UpdatedAt, asOf)
		}
	} else {
		on := false
		var onAt time.Time
		if !fcs[0].Flagged {
			on, onAt = true, t.CreatedAt
		}
		for _, c := range fcs {
			switch {
			case c.Flagged && !on:
				on, onAt = true, c.At
			case !c.Flagged && on:
				add(onAt, c.At)
				on = false
			}
		}
		if on {
			add(onAt, asOf)
		} else if t.Flagged {
			at := t.UpdatedAt
			if last := fcs[len(fcs)-1].At; at.Before(last) {
				at = last
			}
			if at.After(asOf) {
				at = asOf
			}
			add(at, asOf)
		}
	}

	for _, iv := range tl {
		if blockedStatuses[iv.StatusID] && iv.From.Before(iv.To) {
			add(iv.From, iv.To)
		}
	}

	sort.Slice(spans, func(i, j int) bool {
		if spans[i].From != spans[j].From {
			return spans[i].From < spans[j].From
		}
		return spans[i].To < spans[j].To
	})
	var out []Span
	for _, s := range spans {
		if n := len(out); n > 0 && s.From <= out[n-1].To {
			if s.To > out[n-1].To {
				out[n-1].To = s.To
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// BlockedDays = number of distinct days covered by spans.
func BlockedDays(spans []Span) int {
	switch len(spans) {
	case 0:
		return 0
	case 1:
		if spans[0].To < spans[0].From {
			return 0
		}
		return spans[0].To - spans[0].From + 1
	}
	seen := map[int]bool{}
	for _, s := range spans {
		for d := s.From; d <= s.To; d++ {
			seen[d] = true
		}
	}
	return len(seen)
}
