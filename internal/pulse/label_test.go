package pulse

// WP-05 tests: Label (jira-sync.md §5.8, ux-spec.md §7 accessibility).
//
// Form: "<lane label> <D> day(s)[, previously <prev label> <n> day(s)]; <c> commit(s), <p> PR update(s), <m> comment(s)
// in <N> days[; blocked <b> day(s)]".
//
// SPEC-NOTE: the ux-spec §7 example ("In Review 3 days, previously In Progress 6 days; 9 commits, 2 PR updates,
// 4 comments in 21 days; blocked 3 days") matches §5.8 exactly, so it is used as a case verbatim. Lane labels are
// whatever the caller passes.
//
// SPEC-NOTE: Done rows are "<LaneLabel> <D> days ago[, previously <prev> <n> days]" ONLY: the examples carry no
// activity or blocked clause (marks are not drawn on Done rows, ux-spec §7), so activity and blocked days are ignored
// when Done is true. For Done the day count uses the same singular rule ("Done 1 day ago").
//
// SPEC-NOTE: zero-count activity kinds are omitted and the remaining kinds are joined with ", " (no "and"); when all
// are zero the clause is "no activity in <N> days". The activity clause names the window with the plural "days"
// (pulse windows are 7/14/21/28).

import "testing"

func TestLabel(t *testing.T) {
	cases := []struct {
		name string
		in   LabelInput
		want string
	}{
		{
			"jira-sync 5.8 example: previous lane, all activity kinds, blocked",
			LabelInput{LaneLabel: "In progress", DaysInLane: 6, PrevLaneLabel: "To do", PrevDays: 5, Commits: 3, PRs: 1, Comments: 2, Days: 21, BlockedDays: 3},
			"In progress 6 days, previously To do 5 days; 3 commits, 1 PR update, 2 comments in 21 days; blocked 3 days",
		},
		{
			"ux-spec 7 example",
			LabelInput{LaneLabel: "In Review", DaysInLane: 3, PrevLaneLabel: "In Progress", PrevDays: 6, Commits: 9, PRs: 2, Comments: 4, Days: 21, BlockedDays: 3},
			"In Review 3 days, previously In Progress 6 days; 9 commits, 2 PR updates, 4 comments in 21 days; blocked 3 days",
		},
		{
			"no activity: 1 day singular, no previous lane, no blocked clause",
			LabelInput{LaneLabel: "To do", DaysInLane: 1, Days: 14},
			"To do 1 day; no activity in 14 days",
		},
		{
			"no activity in 21 days",
			LabelInput{LaneLabel: "QA", DaysInLane: 4, Days: 21},
			"QA 4 days; no activity in 21 days",
		},
		{
			"no activity but blocked",
			LabelInput{LaneLabel: "QA", DaysInLane: 4, Days: 21, BlockedDays: 2},
			"QA 4 days; no activity in 21 days; blocked 2 days",
		},
		{
			"all singulars",
			LabelInput{LaneLabel: "Code review", DaysInLane: 1, PrevLaneLabel: "In progress", PrevDays: 1, Commits: 1, PRs: 1, Comments: 1, Days: 7, BlockedDays: 1},
			"Code review 1 day, previously In progress 1 day; 1 commit, 1 PR update, 1 comment in 7 days; blocked 1 day",
		},
		{
			"zero-count kinds are omitted (PRs only)",
			LabelInput{LaneLabel: "In progress", DaysInLane: 2, PRs: 2, Days: 21},
			"In progress 2 days; 2 PR updates in 21 days",
		},
		{
			"zero-count kinds are omitted (commits and comments)",
			LabelInput{LaneLabel: "In progress", DaysInLane: 2, Commits: 4, Comments: 1, Days: 28},
			"In progress 2 days; 4 commits, 1 comment in 28 days",
		},
		{
			"zero-count kinds are omitted (commits and PRs)",
			LabelInput{LaneLabel: "In progress", DaysInLane: 2, Commits: 1, PRs: 3, Days: 21},
			"In progress 2 days; 1 commit, 3 PR updates in 21 days",
		},
		{
			"comments only",
			LabelInput{LaneLabel: "Review", DaysInLane: 9, Comments: 7, Days: 21},
			"Review 9 days; 7 comments in 21 days",
		},
		{
			"previous clause only when a previous lane exists (empty label omits it even with days set)",
			LabelInput{LaneLabel: "In progress", DaysInLane: 3, PrevLaneLabel: "", PrevDays: 5, Commits: 1, Days: 21},
			"In progress 3 days; 1 commit in 21 days",
		},
		{
			"previous clause without activity",
			LabelInput{LaneLabel: "Review", DaysInLane: 2, PrevLaneLabel: "In progress", PrevDays: 8, Days: 21},
			"Review 2 days, previously In progress 8 days; no activity in 21 days",
		},
		{
			"blocked clause omitted when zero blocked days",
			LabelInput{LaneLabel: "Review", DaysInLane: 2, Commits: 2, Days: 21, BlockedDays: 0},
			"Review 2 days; 2 commits in 21 days",
		},
		{
			"Done with previous lane (stub doc example)",
			LabelInput{LaneLabel: "Done", DaysInLane: 2, PrevLaneLabel: "QA", PrevDays: 3, Days: 21, Done: true},
			"Done 2 days ago, previously QA 3 days",
		},
		{
			"Done, singular day",
			LabelInput{LaneLabel: "Done", DaysInLane: 1, PrevLaneLabel: "QA", PrevDays: 1, Days: 21, Done: true},
			"Done 1 day ago, previously QA 1 day",
		},
		{
			"Done without a previous lane in the window",
			LabelInput{LaneLabel: "Done", DaysInLane: 5, Days: 21, Done: true},
			"Done 5 days ago",
		},
		{
			"Done ignores activity and blocked days",
			LabelInput{LaneLabel: "Done", DaysInLane: 2, PrevLaneLabel: "QA", PrevDays: 3, Commits: 5, PRs: 2, Comments: 3, Days: 21, BlockedDays: 4, Done: true},
			"Done 2 days ago, previously QA 3 days",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Label(tc.in); got != tc.want {
				t.Errorf("Label(%+v)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}
