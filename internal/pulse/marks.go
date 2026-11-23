package pulse

import (
	"time"

	"github.com/crueber/cuisle/internal/model"
)

// Marks buckets activity by local day (jira-sync.md §5.3): non-merge commits deduplicated by SHA, distinct PR keys per
// day, comments by non-app authors (people[authorID].Kind == "app" excluded). Events after w.AsOf, before the window or
// before the ticket's creation day are dropped. Only non-zero days, ascending.
func Marks(t Ticket, w Window, people map[string]model.Person) []Mark {
	n := w.Days
	if n <= 0 || len(w.Starts) < n+1 {
		return nil
	}
	commits := make([]int, n)
	prs := make([]int, n)
	comments := make([]int, n)
	prSeen := map[mkPRKey]bool{}
	shaSeen := map[string]bool{} // commit SHAs already counted on this ticket
	created := mkDateKey(t.CreatedAt, w.Loc)

	for _, ev := range t.Dev {
		if ev.At.After(w.AsOf) {
			continue
		}
		k := mkDayIndex(w, ev.At)
		if k < 0 || mkDateKey(ev.At, w.Loc) < created {
			continue
		}
		switch ev.Kind {
		case model.DevCommit:
			if ev.Merge || shaSeen[ev.ID] {
				continue
			}
			shaSeen[ev.ID] = true
			commits[k]++
		case model.DevPR:
			key := mkPRKey{day: k, id: ev.ID}
			if prSeen[key] {
				continue
			}
			prSeen[key] = true
			prs[k]++
		}
	}

	for _, c := range t.Comments {
		if c.At.After(w.AsOf) {
			continue
		}
		k := mkDayIndex(w, c.At)
		if k < 0 || mkDateKey(c.At, w.Loc) < created {
			continue
		}
		if p, ok := people[c.AuthorID]; ok && p.Kind == "app" {
			continue
		}
		comments[k]++
	}

	var out []Mark
	for k := 0; k < n; k++ {
		if commits[k] == 0 && prs[k] == 0 && comments[k] == 0 {
			continue
		}
		out = append(out, Mark{Day: k, Commits: commits[k], PRs: prs[k], Comments: comments[k]})
	}
	return out
}

// Totals sums marks.
func Totals(marks []Mark) (commits, prs, comments int) {
	for _, m := range marks {
		commits += m.Commits
		prs += m.PRs
		comments += m.Comments
	}
	return commits, prs, comments
}

// mkDayIndex returns k such that Starts[k] ≤ at < Starts[k+1], or -1 when at is outside the window.
func mkDayIndex(w Window, at time.Time) int {
	n := w.Days
	for k := 0; k < n; k++ {
		if !at.Before(w.Starts[k]) && at.Before(w.Starts[k+1]) {
			return k
		}
	}
	return -1
}

// mkPRKey identifies one PR on one day.
type mkPRKey struct {
	day int
	id  string
}

// mkDateKey encodes the local calendar date of at as YYYYMMDD so dates compare with <.
func mkDateKey(at time.Time, loc *time.Location) int {
	y, mo, d := at.In(loc).Date()
	return y*10000 + int(mo)*100 + d
}
