package pulse

import (
	"fmt"
	"strings"
)

// Label composes the track aria-label (jira-sync.md §5.8), e.g.
// "In progress 6 days, previously To do 5 days; 3 commits, 1 PR update, 2 comments in 21 days; blocked 3 days"
// "Done 2 days ago, previously QA 3 days" · "To do 1 day; no activity in 14 days".
func Label(in LabelInput) string {
	prev := ""
	if in.PrevLaneLabel != "" {
		prev = fmt.Sprintf(", previously %s", plural(in.PrevDays, "day", "days", in.PrevLaneLabel))
	}

	if in.Done {
		return fmt.Sprintf("%s ago%s", plural(in.DaysInLane, "day", "days", in.LaneLabel), prev)
	}

	var parts []string
	if in.Commits > 0 {
		parts = append(parts, plural(in.Commits, "commit", "commits", ""))
	}
	if in.PRs > 0 {
		parts = append(parts, plural(in.PRs, "PR update", "PR updates", ""))
	}
	if in.Comments > 0 {
		parts = append(parts, plural(in.Comments, "comment", "comments", ""))
	}

	activity := fmt.Sprintf("no activity in %d days", in.Days)
	if len(parts) > 0 {
		activity = fmt.Sprintf("%s in %d days", strings.Join(parts, ", "), in.Days)
	}

	var b strings.Builder
	b.WriteString(plural(in.DaysInLane, "day", "days", in.LaneLabel))
	b.WriteString(prev)
	b.WriteString("; ")
	b.WriteString(activity)
	if in.BlockedDays > 0 {
		b.WriteString("; ")
		b.WriteString(plural(in.BlockedDays, "day", "days", "blocked"))
	}
	return b.String()
}

// plural renders "<prefix> <n> <unit>" with the singular unit for 1 and the plural otherwise. An empty prefix
// renders "<n> <unit>". The prefix comes first so lane labels read "In progress 6 days".
func plural(n int, singular, pluralForm, prefix string) string {
	unit := pluralForm
	if n == 1 {
		unit = singular
	}
	if prefix == "" {
		return fmt.Sprintf("%d %s", n, unit)
	}
	return fmt.Sprintf("%s %d %s", prefix, n, unit)
}
