package pulse

import (
	"github.com/crueber/cuisle/internal/model"
)

// Marks buckets activity by local day (jira-sync.md §5.3): non-merge commits deduplicated by SHA, distinct PR keys per
// day, comments by non-app authors (people[authorID].Kind == "app" excluded). Events after w.AsOf, before the window or
// before the ticket's creation day are dropped. Only non-zero days, ascending.
func Marks(t Ticket, w Window, people map[string]model.Person) []Mark {
	n := w.Days
	if n <= 0 || len(w.Starts) < n+1 || (len(t.Dev) == 0 && len(t.Comments) == 0) {
		return nil
	}
	commits := make([]int, n)
	prs := make([]int, n)
	comments := make([]int, n)
	var prSeen map[mkPRKey]bool
	var shaSeen map[string]bool // commit SHAs already counted on this ticket
	createdDay := w.DayIndex(t.CreatedAt)

	for _, ev := range t.Dev {
		if ev.At.After(w.AsOf) {
			continue
		}
		k := w.DayIndex(ev.At)
		if k < 0 || k >= n || k < createdDay {
			continue
		}
		switch ev.Kind {
		case model.DevCommit:
			if ev.Merge {
				continue
			}
			if shaSeen == nil {
				shaSeen = map[string]bool{}
			} else if shaSeen[ev.ID] {
				continue
			}
			shaSeen[ev.ID] = true
			commits[k]++
		case model.DevPR:
			key := mkPRKey{day: k, id: ev.ID}
			if prSeen == nil {
				prSeen = map[mkPRKey]bool{}
			} else if prSeen[key] {
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
		k := w.DayIndex(c.At)
		if k < 0 || k >= n || k < createdDay {
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

// mkPRKey identifies one PR on one day.
type mkPRKey struct {
	day int
	id  string
}
