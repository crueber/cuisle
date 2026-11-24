package jiratest

// Tests for WP-07 fixture loading and materialisation (ParseFixture, Materialize; jira-sync.md §6).
//
// SPEC-NOTE (history days): FxHistory.Days counts calendar days the issue stayed in the status, the last entry "up to
// and including today". So an entry list [To Do 3, In Progress 2] means To Do on days -4,-3,-2 and In Progress on days
// -1,0: the transition happens on day -(days of the last entry − 1) = -1, at 10:00 local; the issue is created on day
// -(sum of days − 1) = -4. This matches jira-sync §5.2 ("the day of a transition belongs to the new lane").
//
// SPEC-NOTE (created): Created falls on the first history entry's first day (local date asserted, time of day free)
// and is not after the first change. The first history entry is the initial status, so n entries give n−1 status
// changes; a single-entry history has none.
//
// SPEC-NOTE (assignee changes): an assigned issue gets at least one change with FieldID "assignee" / Field "assignee"
// whose To is the assignee and whose first From is "" (assigned from unassigned); unassigned issues get none. The
// time of the assignee change is not pinned.
//
// SPEC-NOTE (flag changes): FlaggedDays [from,to] give one change on local day `from` (FieldID FlaggedFieldID, Field
// "Flagged", From "" To "Impediment") and, when to < 0, one clearing change (From "Impediment" To "") on local day
// `to` or `to+1` (the range is inclusive; the instant is not pinned). to == 0 stays flagged (no clearing change).
// DSIssue.Flagged is the state at the end. The time of day of flag changes is not pinned.
//
// SPEC-NOTE (Updated/Resolved): Updated is not before Created, any changelog entry or any comment, and not after Now.
// Resolved is the time of the change into the last (Done) column, zero for unresolved issues.
//
// SPEC-NOTE (backlog issues): backlog issues get the status of the board's FIRST column's first status ("To Do" here,
// which is what the §2.8 backlog query `status in (BL ∪ T)` selects), no sprint, no assignee, no changelog, and
// rank after every board issue of that board (slice order: board issues, then backlog).
//
// SPEC-NOTE (duplicate keys): the real sample has keys that are both a ticket and a backlog entry (14 of them).
// Materialize yields ONE issue per key, the board entry winning (mirrors jira-sync §2.8 "role board wins").
//
// SPEC-NOTE (ordering of activity): Comments and Commits of an issue keep the fixture's slice order (the DST test
// indexes them that way); Changelog is oldest first. Backlog issues have non-zero Created/Updated.
//
// SPEC-NOTE (statuses): one DSStatus per distinct status name used by any board column; category by the column's
// position: first column new, last column done, others indeterminate.

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func matTiny(t *testing.T) (*Fixture, *Dataset) {
	t.Helper()
	fx := tinyFixture()
	return fx, Materialize(fx, testNow, nyc)
}

func mustIssue(t *testing.T, ds *Dataset, key string) *DSIssue {
	t.Helper()
	is := issueByKey(ds, key)
	if is == nil {
		t.Fatalf("issue %s missing from dataset", key)
	}
	return is
}

func changesOf(is *DSIssue, fieldID string) []DSChange {
	var out []DSChange
	for _, c := range is.Changelog {
		if c.FieldID == fieldID {
			out = append(out, c)
		}
	}
	return out
}

// ── ParseFixture ────────────────────────────────────────────────────────────────────────────────

func TestParseFixture_RoundTripsAllFields(t *testing.T) {
	defer guard(t)
	want, err := json.Marshal(tinyFixture())
	if err != nil {
		t.Fatal(err)
	}
	fx, err := ParseFixture(want)
	if err != nil {
		t.Fatalf("ParseFixture: %v", err)
	}
	got, _ := json.Marshal(fx)
	if string(got) != string(want) {
		t.Fatalf("round trip differs\n got:  %s\n want: %s", got, want)
	}
	if len(fx.Issues) != 9 || len(fx.Backlog) != 2 || fx.Boards[0].Sprint == nil || fx.Boards[0].Sprint.ID != 42 {
		t.Errorf("parsed fixture lost data: %d issues, %d backlog", len(fx.Issues), len(fx.Backlog))
	}
	if fx.Issues[0].PRs[0] != (FxPR{ID: 12, Day: -1, Status: "MERGED", Title: "Add the login page", Branch: "feature/MOB-1-login-page"}) {
		t.Errorf("PR = %+v", fx.Issues[0].PRs[0])
	}
}

func TestParseFixture_AcceptsEmptyDocumentAndIgnoresUnknownFields(t *testing.T) {
	defer guard(t)
	if fx, err := ParseFixture([]byte(`{"projects":[],"boards":[],"users":[],"issues":[],"backlog":[]}`)); err != nil || fx == nil {
		t.Fatalf("empty fixture: %v %v", fx, err)
	}
	fx, err := ParseFixture([]byte(`{"projects":[{"id":"1","key":"A","name":"Alpha","extra":true}],"somethingNew":1}`))
	if err != nil || fx == nil || len(fx.Projects) != 1 || fx.Projects[0].Key != "A" {
		t.Fatalf("unknown fields must be ignored: %+v %v", fx, err)
	}
}

func TestParseFixture_InvalidInputIsAnError(t *testing.T) {
	defer guard(t)
	for name, in := range map[string]string{
		"empty":       ``,
		"not json":    `not json`,
		"wrong shape": `[]`,
		"wrong type":  `{"projects":"nope"}`,
		"truncated":   `{"projects":[`,
	} {
		sub(t, name, func(t *testing.T) {
			if fx, err := ParseFixture([]byte(in)); err == nil {
				t.Fatalf("ParseFixture(%q) = %+v, want an error", in, fx)
			}
		})
	}
}

// ── Materialize: structure ──────────────────────────────────────────────────────────────────────

func TestMaterialize_Deterministic(t *testing.T) {
	defer guard(t)
	a := Materialize(tinyFixture(), testNow, nyc)
	b := Materialize(tinyFixture(), testNow, nyc)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("two Materialize calls with identical input differ")
	}
	if !a.Now.Equal(testNow) {
		t.Errorf("Dataset.Now = %v, want %v", a.Now, testNow)
	}
}

func TestMaterialize_DoesNotMutateTheFixture(t *testing.T) {
	defer guard(t)
	fx := tinyFixture()
	before, _ := json.Marshal(fx)
	Materialize(fx, testNow, nyc)
	after, _ := json.Marshal(fx)
	if string(before) != string(after) {
		t.Fatalf("Materialize modified its input fixture")
	}
}

func TestMaterialize_EmptyFixtureYieldsEmptyDataset(t *testing.T) {
	defer guard(t)
	ds := Materialize(&Fixture{}, testNow, nyc)
	if ds == nil || len(ds.Issues) != 0 || len(ds.Boards) != 0 {
		t.Fatalf("dataset = %+v, want empty", ds)
	}
}

func TestMaterialize_CopiesProjectsBoardsAndUsers(t *testing.T) {
	defer guard(t)
	fx, ds := matTiny(t)
	if !reflect.DeepEqual(ds.Projects, fx.Projects) {
		t.Errorf("Projects = %+v, want %+v", ds.Projects, fx.Projects)
	}
	if len(ds.Users) != len(fx.Users) {
		t.Fatalf("Users = %d, want %d", len(ds.Users), len(fx.Users))
	}
	for i, u := range fx.Users {
		if ds.Users[i].AccountID != u.AccountID || ds.Users[i].DisplayName != u.DisplayName {
			t.Errorf("user %d = %+v, want %+v", i, ds.Users[i], u)
		}
	}
	if len(ds.Boards) != 3 {
		t.Fatalf("Boards = %d, want 3", len(ds.Boards))
	}
	for i, b := range fx.Boards {
		got := ds.Boards[i]
		if got.ID != b.ID || got.Name != b.Name || got.Type != b.Type || got.FilterID != b.FilterID || got.SubQuery != b.SubQuery ||
			got.ProjectKey != b.ProjectKey || !reflect.DeepEqual(got.Columns, b.Columns) {
			t.Errorf("board %d = %+v, want fields of %+v", i, got.FxBoard, b)
		}
	}
}

func TestMaterialize_FixtureSprintBecomesActiveSprintWithAbsoluteDates(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	if n := len(ds.Boards[0].Sprints); n != 1 {
		t.Fatalf("board 1 sprints = %d, want 1", n)
	}
	sp := ds.Boards[0].Sprints[0]
	if sp.ID != 42 || sp.Name != "Sprint 42" || sp.BoardID != 1 || sp.State != "active" {
		t.Errorf("sprint = %+v, want id 42 / Sprint 42 / board 1 / active", sp)
	}
	if got, want := localDate(sp.Start, nyc), dayStr(testNow, nyc, -13); got != want {
		t.Errorf("sprint start date = %s, want %s", got, want)
	}
	if got, want := localDate(sp.End, nyc), dayStr(testNow, nyc, 0); got != want {
		t.Errorf("sprint end date = %s, want %s", got, want)
	}
	if !sp.Start.Before(sp.End) {
		t.Errorf("sprint start %v not before end %v", sp.Start, sp.End)
	}
	if len(ds.Boards[1].Sprints) != 0 || len(ds.Boards[2].Sprints) != 0 {
		t.Errorf("boards without a fixture sprint got sprints: %+v / %+v", ds.Boards[1].Sprints, ds.Boards[2].Sprints)
	}
}

func TestMaterialize_StatusesDerivedFromColumnsWithCategories(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	got := map[string]string{}
	ids := map[string]bool{}
	for _, st := range ds.Statuses {
		got[st.Name] = st.Category
		if st.ID == "" || ids[st.ID] {
			t.Errorf("status id %q empty or repeated", st.ID)
		}
		ids[st.ID] = true
	}
	want := map[string]string{"To Do": "new", "In Progress": "indeterminate", "Done": "done"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %v, want %v (three boards share the same names: one status each)", got, want)
	}
}

func TestMaterialize_MultiStatusColumnsKeepPositionalCategories(t *testing.T) {
	defer guard(t)
	fx := &Fixture{
		Projects: []FxProject{{ID: "1", Key: "A", Name: "Alpha"}},
		Boards: []FxBoard{{ID: 1, Name: "A board", Type: "kanban", ProjectKey: "A", FilterID: 1, Columns: []FxColumn{
			{Name: "Open", Statuses: []string{"Open", "Reopened"}},
			{Name: "Review", Statuses: []string{"Code Review", "Ready for QA"}},
			{Name: "Closed", Statuses: []string{"Closed", "Won't Do"}},
		}}},
		Issues: []FxIssue{{Key: "A-1", Type: "Task", Summary: "x", Board: 1, History: []FxHistory{{Status: "Open", Days: 1}}}},
	}
	ds := Materialize(fx, testNow, nyc)
	got := map[string]string{}
	for _, st := range ds.Statuses {
		got[st.Name] = st.Category
	}
	want := map[string]string{"Open": "new", "Reopened": "new", "Code Review": "indeterminate", "Ready for QA": "indeterminate", "Closed": "done", "Won't Do": "done"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
}

func TestMaterialize_TypesCarrySubtaskAndHierarchy(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	byName := map[string]DSType{}
	ids := map[string]bool{}
	for _, ty := range ds.Types {
		byName[ty.Name] = ty
		if ty.ID == "" || ids[ty.ID] {
			t.Errorf("type id %q empty or repeated", ty.ID)
		}
		ids[ty.ID] = true
	}
	for _, name := range []string{"Story", "Bug", "Task", "Sub-task", "Epic"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("type %s missing from Types %+v", name, ds.Types)
		}
	}
	if !byName["Sub-task"].Subtask || byName["Story"].Subtask || byName["Epic"].Subtask {
		t.Errorf("Subtask flags wrong: %+v", byName)
	}
	if byName["Epic"].Hierarchy != 1 || byName["Story"].Hierarchy != 0 || byName["Bug"].Hierarchy != 0 {
		t.Errorf("Hierarchy wrong: %+v", byName)
	}
	for _, is := range ds.Issues {
		found := false
		for _, ty := range ds.Types {
			found = found || ty.ID == is.TypeID
		}
		if !found {
			t.Errorf("issue %s TypeID %q not in Types", is.Key, is.TypeID)
		}
	}
}

func TestMaterialize_IssueOrderIsBoardIssuesThenBacklog(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	var keys []string
	for _, is := range ds.Issues {
		keys = append(keys, is.Key)
	}
	want := []string{"MOB-1", "MOB-2", "MOB-3", "MOB-4", "MOB-5", "MOB-10", "MOB-11", "MOB-12", "PLAT-1", "MOB-6", "MOB-7"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("issue order = %v, want %v", keys, want)
	}
}

func TestMaterialize_IdsAreNumericAndUnique(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	seen := map[string]string{}
	check := func(kind, id string) {
		t.Helper()
		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			t.Errorf("%s id %q is not a numeric string", kind, id)
		}
	}
	for _, is := range ds.Issues {
		check("issue "+is.Key, is.ID)
		if prev, dup := seen["issue/"+is.ID]; dup {
			t.Errorf("issue id %s used by %s and %s", is.ID, prev, is.Key)
		}
		seen["issue/"+is.ID] = is.Key
	}
	changeIDs, commentIDs, shas := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, is := range ds.Issues {
		for _, c := range is.Changelog {
			if c.ID == "" || changeIDs[c.ID] {
				t.Errorf("changelog id %q on %s empty or repeated across issues", c.ID, is.Key)
			}
			changeIDs[c.ID] = true
		}
		for _, c := range is.Comments {
			if c.ID == "" || commentIDs[c.ID] {
				t.Errorf("comment id %q on %s empty or repeated", c.ID, is.Key)
			}
			commentIDs[c.ID] = true
		}
		for _, c := range is.Commits {
			if c.SHA == "" || shas[c.SHA] {
				t.Errorf("commit sha %q on %s empty or repeated", c.SHA, is.Key)
			}
			shas[c.SHA] = true
		}
	}
}

func TestMaterialize_IssueBasicFields(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	todo, prog, done := statusIDByName(ds, "To Do"), statusIDByName(ds, "In Progress"), statusIDByName(ds, "Done")
	typeName := func(id string) string {
		for _, ty := range ds.Types {
			if ty.ID == id {
				return ty.Name
			}
		}
		return ""
	}
	cases := []struct {
		key, project, typ, summary, assignee, status string
		board                                        int
		sprints                                      []int
	}{
		{"MOB-1", "MOB", "Story", "Login page", "u-ann", prog, 1, []int{42}},
		{"MOB-2", "MOB", "Bug", "Crash on save", "", todo, 1, []int{42}},
		{"MOB-3", "MOB", "Task", "Write docs", "u-bob", done, 1, []int{42}},
		{"MOB-4", "MOB", "Story", "Not in the sprint", "u-cy", todo, 1, nil},
		{"MOB-11", "MOB", "Sub-task", "Kanban subtask", "u-bob", prog, 2, nil},
		{"PLAT-1", "PLAT", "Task", "Ops thing", "", todo, 3, nil},
	}
	for _, c := range cases {
		sub(t, c.key, func(t *testing.T) {
			is := mustIssue(t, ds, c.key)
			if is.ProjectKey != c.project || is.BoardID != c.board || is.Summary != c.summary || is.Assignee != c.assignee || is.StatusID != c.status {
				t.Errorf("issue = project %s board %d summary %q assignee %q status %s; want %s %d %q %q %s",
					is.ProjectKey, is.BoardID, is.Summary, is.Assignee, is.StatusID, c.project, c.board, c.summary, c.assignee, c.status)
			}
			if got := typeName(is.TypeID); got != c.typ {
				t.Errorf("type = %q, want %q", got, c.typ)
			}
			if !(len(is.SprintIDs) == 0 && len(c.sprints) == 0) && !reflect.DeepEqual(is.SprintIDs, c.sprints) {
				t.Errorf("SprintIDs = %v, want %v", is.SprintIDs, c.sprints)
			}
		})
	}
}

func TestMaterialize_RanksFollowSliceOrderWithinEachBoard(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	last := map[int]string{}
	for _, is := range ds.Issues {
		if is.Rank == "" {
			t.Fatalf("issue %s has no rank", is.Key)
		}
		if prev, ok := last[is.BoardID]; ok && !(prev < is.Rank) {
			t.Errorf("board %d: rank of %s (%q) is not after the previous issue's (%q)", is.BoardID, is.Key, is.Rank, prev)
		}
		last[is.BoardID] = is.Rank
	}
}

func TestMaterialize_RanksStayOrderedForLargeBoards(t *testing.T) {
	defer guard(t)
	// Search sorts by the rank STRING; unpadded counters ("0|i2" > "0|i10") would scramble boards of real size.
	fx := &Fixture{
		Projects: []FxProject{{ID: "1", Key: "BIG", Name: "Big"}},
		Boards: []FxBoard{{ID: 1, Name: "BIG board", Type: "kanban", ProjectKey: "BIG", FilterID: 1, Columns: []FxColumn{
			{Name: "To Do", Statuses: []string{"To Do"}}, {Name: "Done", Statuses: []string{"Done"}}}}},
	}
	var want []string
	for i := 1; i <= 120; i++ {
		key := "BIG-" + strconv.Itoa(i)
		fx.Issues = append(fx.Issues, FxIssue{Key: key, Type: "Task", Summary: key, Board: 1, History: []FxHistory{{Status: "To Do", Days: 1}}})
		want = append(want, key)
	}
	for i := 1001; i <= 1030; i++ {
		key := "BIG-" + strconv.Itoa(i)
		fx.Backlog = append(fx.Backlog, FxBacklog{Key: key, Type: "Task", Summary: key, Board: 1})
		want = append(want, key)
	}
	ds := Materialize(fx, testNow, nyc)
	if len(ds.Issues) != 150 {
		t.Fatalf("issues = %d, want 150", len(ds.Issues))
	}
	prev := ""
	for i, is := range ds.Issues {
		if is.Key != want[i] {
			t.Fatalf("issue %d = %s, want %s", i, is.Key, want[i])
		}
		if !(prev < is.Rank) {
			t.Fatalf("rank of %s (%q) is not strictly after the previous (%q): string order must equal slice order", is.Key, is.Rank, prev)
		}
		prev = is.Rank
	}
}

// ── Materialize: changelog and times ────────────────────────────────────────────────────────────

func TestMaterialize_StatusChangelogFromHistory(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	todo, prog, done := statusIDByName(ds, "To Do"), statusIDByName(ds, "In Progress"), statusIDByName(ds, "Done")

	// MOB-3: [To Do 4, In Progress 3, Done 1] -> To Do on -7..-4, In Progress -3..-1, Done today.
	ch := changesOf(mustIssue(t, ds, "MOB-3"), "status")
	if len(ch) != 2 {
		t.Fatalf("MOB-3 status changes = %d, want 2: %+v", len(ch), ch)
	}
	for i, w := range []struct {
		from, to string
		day      int
	}{{todo, prog, -3}, {prog, done, 0}} {
		c := ch[i]
		if c.From != w.from || c.To != w.to || c.Field != "status" {
			t.Errorf("MOB-3 change %d = %+v, want status %s -> %s", i, c, w.from, w.to)
		}
		assertLocalDayHour(t, "MOB-3 change "+strconv.Itoa(i), c.At, testNow, nyc, w.day, 10)
	}

	// MOB-1: [To Do 3, In Progress 2] -> one change on day -1.
	ch = changesOf(mustIssue(t, ds, "MOB-1"), "status")
	if len(ch) != 1 || ch[0].From != todo || ch[0].To != prog {
		t.Fatalf("MOB-1 status changes = %+v, want one To Do -> In Progress", ch)
	}
	assertLocalDayHour(t, "MOB-1 change", ch[0].At, testNow, nyc, -1, 10)
}

func TestMaterialize_SingleEntryHistoryHasNoStatusChange(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	for _, key := range []string{"MOB-2", "MOB-4", "MOB-5", "PLAT-1"} {
		if ch := changesOf(mustIssue(t, ds, key), "status"); len(ch) != 0 {
			t.Errorf("%s has %d status changes, want none for a single history entry: %+v", key, len(ch), ch)
		}
	}
}

func TestMaterialize_ChangelogIsOldestFirst(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	for _, is := range ds.Issues {
		for i := 1; i < len(is.Changelog); i++ {
			if is.Changelog[i].At.Before(is.Changelog[i-1].At) {
				t.Errorf("%s changelog not oldest first at index %d", is.Key, i)
			}
		}
	}
}

func TestMaterialize_CreatedOnFirstHistoryDay_ResolvedOnDoneTransition(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	for key, startDay := range map[string]int{"MOB-1": -4, "MOB-2": 0, "MOB-3": -7, "MOB-4": -4, "MOB-5": -1} {
		is := mustIssue(t, ds, key)
		if got, want := localDate(is.Created, nyc), dayStr(testNow, nyc, startDay); got != want {
			t.Errorf("%s created %s, want %s", key, got, want)
		}
		for _, c := range is.Changelog {
			if c.At.Before(is.Created) {
				t.Errorf("%s change %+v is before Created %v", key, c, is.Created)
			}
		}
	}
	svc3 := mustIssue(t, ds, "MOB-3")
	statusChanges := changesOf(svc3, "status")
	if svc3.Resolved.IsZero() || !svc3.Resolved.Equal(statusChanges[len(statusChanges)-1].At) {
		t.Errorf("MOB-3 Resolved = %v, want the time of the change into Done %v", svc3.Resolved, statusChanges[len(statusChanges)-1].At)
	}
	for _, key := range []string{"MOB-1", "MOB-2", "MOB-4", "MOB-5"} {
		if r := mustIssue(t, ds, key).Resolved; !r.IsZero() {
			t.Errorf("%s Resolved = %v, want zero (not done)", key, r)
		}
	}
}

func TestMaterialize_AssigneeChanges(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	svc1 := changesOf(mustIssue(t, ds, "MOB-1"), "assignee")
	if len(svc1) == 0 {
		t.Fatalf("MOB-1 (assigned to u-ann) has no assignee change")
	}
	if svc1[0].From != "" || svc1[len(svc1)-1].To != "u-ann" || svc1[0].Field != "assignee" {
		t.Errorf("MOB-1 assignee changes = %+v, want first From \"\" and last To u-ann", svc1)
	}
	if ch := changesOf(mustIssue(t, ds, "MOB-2"), "assignee"); len(ch) != 0 {
		t.Errorf("unassigned MOB-2 has assignee changes: %+v", ch)
	}
}

func TestMaterialize_OpenFlagRangeIsOneFlagChangeAndFlagged(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	is := mustIssue(t, ds, "MOB-5") // flaggedDays [[-1, 0]]
	ch := changesOf(is, FlaggedFieldID)
	if len(ch) != 1 {
		t.Fatalf("MOB-5 flag changes = %d, want 1: %+v", len(ch), ch)
	}
	if ch[0].Field != "Flagged" || ch[0].From != "" || ch[0].To != "Impediment" {
		t.Errorf("flag change = %+v, want Flagged \"\" -> Impediment", ch[0])
	}
	if got, want := localDate(ch[0].At, nyc), dayStr(testNow, nyc, -1); got != want {
		t.Errorf("flag set on %s, want %s", got, want)
	}
	if !is.Flagged {
		t.Errorf("MOB-5 Flagged = false, want true (range open to today)")
	}
	if mustIssue(t, ds, "MOB-1").Flagged {
		t.Errorf("MOB-1 Flagged = true, want false")
	}
}

func TestMaterialize_ClosedAndRepeatedFlagRanges(t *testing.T) {
	defer guard(t)
	fx := tinyFixture()
	fx.Issues = append(fx.Issues,
		FxIssue{Key: "MOB-20", Type: "Bug", Summary: "flag cleared", Board: 1, InSprint: true,
			FlaggedDays: [][2]int{{-5, -3}}, History: []FxHistory{{Status: "In Progress", Days: 8}}},
		FxIssue{Key: "MOB-21", Type: "Bug", Summary: "flag twice", Board: 1, InSprint: true,
			FlaggedDays: [][2]int{{-6, -5}, {-2, 0}}, History: []FxHistory{{Status: "In Progress", Days: 8}}},
	)
	ds := Materialize(fx, testNow, nyc)

	closed := mustIssue(t, ds, "MOB-20")
	ch := changesOf(closed, FlaggedFieldID)
	if len(ch) != 2 {
		t.Fatalf("MOB-20 flag changes = %d, want 2 (on, off): %+v", len(ch), ch)
	}
	if ch[0].To != "Impediment" || ch[0].From != "" || ch[1].From != "Impediment" || ch[1].To != "" {
		t.Errorf("MOB-20 flag changes = %+v, want on then off", ch)
	}
	if got, want := localDate(ch[0].At, nyc), dayStr(testNow, nyc, -5); got != want {
		t.Errorf("flag set on %s, want %s", got, want)
	}
	off := localDate(ch[1].At, nyc)
	if off != dayStr(testNow, nyc, -3) && off != dayStr(testNow, nyc, -2) {
		t.Errorf("flag cleared on %s, want the end of day -3 (%s or %s)", off, dayStr(testNow, nyc, -3), dayStr(testNow, nyc, -2))
	}
	if !ch[0].At.Before(ch[1].At) {
		t.Errorf("flag cleared before it was set")
	}
	if closed.Flagged {
		t.Errorf("MOB-20 Flagged = true, want false")
	}

	twice := mustIssue(t, ds, "MOB-21")
	tc := changesOf(twice, FlaggedFieldID)
	if len(tc) != 3 {
		t.Fatalf("MOB-21 flag changes = %d, want 3 (on, off, on): %+v", len(tc), tc)
	}
	if tc[0].To == "" || tc[1].To != "" || tc[2].To == "" {
		t.Errorf("MOB-21 flag changes = %+v, want on, off, on", tc)
	}
	if !twice.Flagged {
		t.Errorf("MOB-21 Flagged = false, want true")
	}
}

func TestMaterialize_CommentsCommitsAndPRsAt1100Local(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	svc1 := mustIssue(t, ds, "MOB-1")

	if len(svc1.Comments) != 3 {
		t.Fatalf("MOB-1 comments = %d, want 3", len(svc1.Comments))
	}
	var commentDays []string
	for _, c := range svc1.Comments {
		commentDays = append(commentDays, localDate(c.At, nyc))
		if c.At.In(nyc).Hour() != 11 || c.At.In(nyc).Minute() != 0 {
			t.Errorf("comment at %v, want 11:00 local", c.At.In(nyc))
		}
		if c.Author == "" {
			t.Errorf("comment %s has no author", c.ID)
		}
	}
	if want := sortedStrings([]string{dayStr(testNow, nyc, -3), dayStr(testNow, nyc, -1), dayStr(testNow, nyc, 0)}); !reflect.DeepEqual(sortedStrings(commentDays), want) {
		t.Errorf("comment days = %v, want %v", commentDays, want)
	}

	if len(svc1.Commits) != 3 {
		t.Fatalf("MOB-1 commits = %d, want 3 (one per entry)", len(svc1.Commits))
	}
	var commitDays []string
	for _, c := range svc1.Commits {
		commitDays = append(commitDays, localDate(c.At, nyc))
		if c.At.In(nyc).Hour() != 11 || c.At.In(nyc).Minute() != 0 {
			t.Errorf("commit at %v, want 11:00 local", c.At.In(nyc))
		}
		if c.Repo == "" {
			t.Errorf("commit %s has no repo", c.SHA)
		}
	}
	if want := sortedStrings([]string{dayStr(testNow, nyc, -1), dayStr(testNow, nyc, 0), dayStr(testNow, nyc, 0)}); !reflect.DeepEqual(sortedStrings(commitDays), want) {
		t.Errorf("commit days = %v, want %v", commitDays, want)
	}

	if len(svc1.PRs) != 1 {
		t.Fatalf("MOB-1 PRs = %d, want 1", len(svc1.PRs))
	}
	pr := svc1.PRs[0]
	if pr.ID != 12 || pr.Status != "MERGED" || pr.Repo == "" {
		t.Errorf("PR = %+v, want id 12 MERGED with a repo", pr)
	}
	assertLocalDayHour(t, "PR lastUpdate", pr.LastUpdate, testNow, nyc, -1, 11)

	if n := len(mustIssue(t, ds, "MOB-2").Comments) + len(mustIssue(t, ds, "MOB-2").Commits); n != 0 {
		t.Errorf("MOB-2 has %d comments+commits, want none", n)
	}
}

func TestMaterialize_UpdatedIsNotBeforeAnyActivityAndNotInTheFuture(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	for _, is := range ds.Issues {
		if is.Updated.Before(is.Created) {
			t.Errorf("%s Updated %v before Created %v", is.Key, is.Updated, is.Created)
		}
		if is.Updated.After(ds.Now) {
			t.Errorf("%s Updated %v is after Now %v", is.Key, is.Updated, ds.Now)
		}
		for _, c := range is.Changelog {
			if is.Updated.Before(c.At) {
				t.Errorf("%s Updated %v before change %+v", is.Key, is.Updated, c)
			}
		}
		for _, c := range is.Comments {
			if is.Updated.Before(c.At) {
				t.Errorf("%s Updated %v before comment %v", is.Key, is.Updated, c.At)
			}
		}
	}
}

func TestMaterialize_BacklogIssues(t *testing.T) {
	defer guard(t)
	_, ds := matTiny(t)
	todo := statusIDByName(ds, "To Do")
	var lastBoardRank string
	for _, is := range ds.Issues {
		if is.BoardID == 1 && (is.Key != "MOB-6" && is.Key != "MOB-7") && is.Rank > lastBoardRank {
			lastBoardRank = is.Rank
		}
	}
	for _, key := range []string{"MOB-6", "MOB-7"} {
		is := mustIssue(t, ds, key)
		if is.StatusID != todo {
			t.Errorf("%s status = %s, want the first column's status To Do (%s)", key, is.StatusID, todo)
		}
		if len(is.SprintIDs) != 0 || is.Assignee != "" || is.BoardID != 1 || is.ProjectKey != "MOB" {
			t.Errorf("%s = sprints %v assignee %q board %d project %s, want none/\"\"/1/MOB", key, is.SprintIDs, is.Assignee, is.BoardID, is.ProjectKey)
		}
		if len(is.Changelog) != 0 || len(is.Comments) != 0 || is.Flagged || !is.Resolved.IsZero() {
			t.Errorf("%s has activity: %+v", key, is)
		}
		if !(is.Rank > lastBoardRank) {
			t.Errorf("%s rank %q is not after the board issues' ranks (last %q)", key, is.Rank, lastBoardRank)
		}
		if is.Created.IsZero() || is.Updated.IsZero() || is.Updated.Before(is.Created) {
			t.Errorf("%s has unusable Created/Updated: %v / %v", key, is.Created, is.Updated)
		}
	}
	if is := mustIssue(t, ds, "MOB-6"); is.Summary != "Backlog one" {
		t.Errorf("MOB-6 summary = %q", is.Summary)
	}
}

func TestMaterialize_BacklogEntryDuplicatingABoardIssue_BoardWins(t *testing.T) {
	defer guard(t)
	fx := tinyFixture()
	fx.Backlog = append([]FxBacklog{{Key: "MOB-1", Type: "Story", Summary: "dup from backlog", Board: 1}}, fx.Backlog...)
	ds := Materialize(fx, testNow, nyc)
	n := 0
	for _, is := range ds.Issues {
		if is.Key == "MOB-1" {
			n++
		}
	}
	if n != 1 || len(ds.Issues) != 11 {
		t.Fatalf("MOB-1 appears %d times, %d issues in total; want 1 and 11", n, len(ds.Issues))
	}
	is := mustIssue(t, ds, "MOB-1")
	if is.Summary != "Login page" || is.StatusID != statusIDByName(ds, "In Progress") || len(is.SprintIDs) != 1 {
		t.Errorf("MOB-1 = %+v, want the board entry (Login page, In Progress, in sprint 42)", is)
	}
}

// ── Materialize: time zones, relative days ──────────────────────────────────────────────────────

func TestMaterialize_DayZeroIsTheLocalDateNotTheUTCDate(t *testing.T) {
	defer guard(t)
	auckland := mustLoc("Pacific/Auckland") // UTC+13 in November
	now := time.Date(2026, 11, 23, 23, 30, 0, 0, time.UTC)
	if now.In(auckland).Format("2006-01-02") != "2026-11-24" {
		t.Fatalf("test setup: expected 2026-11-24 in Auckland")
	}
	ds := Materialize(tinyFixture(), now, auckland)
	ch := changesOf(mustIssue(t, ds, "MOB-3"), "status")
	done := ch[len(ch)-1] // the transition into Done happens on day 0
	assertLocalDayHour(t, "MOB-3 Done change", done.At, now, auckland, 0, 10)
	if got := done.At.In(time.UTC).Format("2006-01-02"); got != "2026-11-23" {
		t.Errorf("UTC date of the change = %s, want 2026-11-23 (10:00 NZDT is the previous UTC day)", got)
	}
	if got := localDate(done.At, auckland); got != "2026-11-24" {
		t.Errorf("local date = %s, want 2026-11-24", got)
	}
}

func TestMaterialize_ShiftingNowByOneDayShiftsEveryTimeByOneLocalDay(t *testing.T) {
	defer guard(t)
	a := Materialize(tinyFixture(), testNow, nyc)
	b := Materialize(tinyFixture(), testNow.AddDate(0, 0, 1), nyc)
	for i, ia := range a.Issues {
		ib := b.Issues[i]
		if ia.Key != ib.Key || len(ia.Changelog) != len(ib.Changelog) || len(ia.Comments) != len(ib.Comments) || len(ia.Commits) != len(ib.Commits) {
			t.Fatalf("%s: shape differs between Now and Now+1d", ia.Key)
		}
		for j := range ia.Changelog {
			if want := ia.Changelog[j].At.In(nyc).AddDate(0, 0, 1); !ib.Changelog[j].At.Equal(want) {
				t.Errorf("%s change %d: %v, want %v", ia.Key, j, ib.Changelog[j].At, want)
			}
		}
		for j := range ia.Comments {
			if want := ia.Comments[j].At.In(nyc).AddDate(0, 0, 1); !ib.Comments[j].At.Equal(want) {
				t.Errorf("%s comment %d: %v, want %v", ia.Key, j, ib.Comments[j].At, want)
			}
		}
	}
}

func TestMaterialize_LocalHoursStayStableAcrossTheNovemberDSTChange(t *testing.T) {
	defer guard(t)
	// 28 days back from 2026-11-23 reaches 2026-10-27; US clocks fell back on 2026-11-01 (EDT -> EST).
	fx := tinyFixture()
	fx.Issues = append(fx.Issues, FxIssue{Key: "MOB-30", Type: "Story", Summary: "long lived", Board: 1, InSprint: true,
		History:  []FxHistory{{Status: "To Do", Days: 2}, {Status: "In Progress", Days: 20}, {Status: "Done", Days: 6}},
		Commits:  []int{-27, -22, 0},
		Comments: []int{-27, -22, 0},
		PRs:      []FxPR{{ID: 7, Day: -22, Status: "OPEN"}},
	})
	ds := Materialize(fx, testNow, nyc)
	is := mustIssue(t, ds, "MOB-30")
	st := changesOf(is, "status")
	if len(st) != 2 {
		t.Fatalf("status changes = %d, want 2", len(st))
	}
	assertLocalDayHour(t, "To Do -> In Progress (EDT side)", st[0].At, testNow, nyc, -25, 10)
	assertLocalDayHour(t, "In Progress -> Done (EST side)", st[1].At, testNow, nyc, -5, 10)
	if _, off0 := st[0].At.In(nyc).Zone(); off0 != -4*3600 {
		t.Errorf("first change zone offset = %d, want EDT (-14400)", off0)
	}
	if _, off1 := st[1].At.In(nyc).Zone(); off1 != -5*3600 {
		t.Errorf("second change zone offset = %d, want EST (-18000)", off1)
	}
	if got, want := localDate(is.Created, nyc), dayStr(testNow, nyc, -27); got != want {
		t.Errorf("created %s, want %s", got, want)
	}
	days := []int{-27, -22, 0}
	for i, c := range is.Comments {
		assertLocalDayHour(t, "comment "+strconv.Itoa(i), c.At, testNow, nyc, days[i], 11)
	}
	for i, c := range is.Commits {
		assertLocalDayHour(t, "commit "+strconv.Itoa(i), c.At, testNow, nyc, days[i], 11)
	}
	assertLocalDayHour(t, "PR", is.PRs[0].LastUpdate, testNow, nyc, -22, 11)
}

func TestMaterialize_WorksInUTC(t *testing.T) {
	defer guard(t)
	now := time.Date(2026, 11, 23, 22, 30, 0, 0, time.UTC)
	ds := Materialize(tinyFixture(), now, time.UTC)
	ch := changesOf(mustIssue(t, ds, "MOB-3"), "status")
	assertLocalDayHour(t, "MOB-3 first change", ch[0].At, now, time.UTC, -3, 10)
}
