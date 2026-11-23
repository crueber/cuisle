package main

// Tests for WP-08: cmd/fixturegen (design/reference/sample-data.json -> jiratest.Fixture; jira-sync.md §6 "Conversion
// rules"). Literal expectations come from the committed sample (MOB-1390 etc.).
//
// SPEC-NOTE (sample overlap): in the real sample 14 keys are BOTH a ticket and a backlog entry (MOB has 8, e.g.
// MOB-1539). Convert is literal about the conversion rules: Fixture.Backlog holds every sample backlog entry in array
// order (MOB: 40, total 90), duplicates included. De-duplication is Materialize's job (board entry wins; see
// fixture_test.go), and TestConvert_SampleMaterializesToUniqueKeys pins the end-to-end result: 223 keys -> 209 issues.
//
// SPEC-NOTE (users): people[] -> users with accountId "u-<person id>" and displayName = the person's name (doc example
// "u-iris"). FxUser has no project link, so "MOB has 7 users" is asserted as the seven u-<id> entries being present;
// the whole fixture holds 24 users (7+5+4+4+4, all distinct).
//
// SPEC-NOTE (ids): the MOB board is the first board and has id 1 (doc example); board and project ids are otherwise only
// required to be unique and non-empty. Every issue's Board refers to its standup's board id.
//
// SPEC-NOTE (sprint): scrum boards get Sprint{Name: text before " · " of the standup's `sprint` (e.g. "Sprint 42"), ID: the
// number in that name}; MOB ("ends today") has EndDay 0 and StartDay -13 (doc example). Kanban boards have no sprint.
// Every issue of a scrum board is inSprint (Done ones too: "done this sprint"); kanban issues are not.
//
// SPEC-NOTE (columns): main.go's list is authoritative: To Do / In Progress / Code Review / QA / Done, each column
// holding the status of the same name (the §6 JSON example's "Selected for Development" is only illustrative).
//
// SPEC-NOTE (project name): Project.Name is the standup's `name` ("Web & Checkout", "Platform"), Project.Key its `key`; the
// `space` label ("WEB · Web & Checkout") is not used. The sample's board label "MOB board · Scrum" gives Board.Name
// "MOB board" and Type "scrum" (Kanban in the label -> "kanban").
//
// SPEC-NOTE (missing input): with an unreadable -in file the command prints a one-line error naming the file to stderr
// (no stack trace), exits non-zero and writes no output.
//
// SPEC-NOTE (types): ty story|bug|task|spike -> Story|Bug|Task|Spike for issues and backlog entries.
//
// SPEC-NOTE (drift): the committed jiratest.DefaultFixture must equal Convert(sample) (parsed, not byte-wise), so
// WP-08 has to actually regenerate testdata/fixture.json. `go run . -in X -out Y` writes it deterministically.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/crueber/cuisle/internal/source/jira/jiratest"
)

const samplePath = "../../design/reference/sample-data.json"

func guard(t *testing.T) {
	t.Helper()
	if r := recover(); r != nil {
		t.Fatalf("panicked (stub not implemented?): %v", r)
	}
}

func loadSample(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	return b
}

func convertSample(t *testing.T) *jiratest.Fixture {
	t.Helper()
	fx, err := Convert(loadSample(t))
	if err != nil {
		t.Fatalf("Convert(sample): %v", err)
	}
	if fx == nil {
		t.Fatalf("Convert(sample) returned a nil fixture")
	}
	return fx
}

func boardByName(fx *jiratest.Fixture, name string) *jiratest.FxBoard {
	for i := range fx.Boards {
		if fx.Boards[i].Name == name {
			return &fx.Boards[i]
		}
	}
	return nil
}

func issuesOfBoard(fx *jiratest.Fixture, board int) []jiratest.FxIssue {
	var out []jiratest.FxIssue
	for _, is := range fx.Issues {
		if is.Board == board {
			out = append(out, is)
		}
	}
	return out
}

func backlogOfBoard(fx *jiratest.Fixture, board int) []jiratest.FxBacklog {
	var out []jiratest.FxBacklog
	for _, b := range fx.Backlog {
		if b.Board == board {
			out = append(out, b)
		}
	}
	return out
}

func issueByKey(fx *jiratest.Fixture, key string) *jiratest.FxIssue {
	for i := range fx.Issues {
		if fx.Issues[i].Key == key {
			return &fx.Issues[i]
		}
	}
	return nil
}

// sampleShape mirrors just enough of sample-data.json to compute expectations independently of Convert.
type sampleShape struct {
	Standups []struct {
		ID      string                      `json:"id"`
		Name    string                      `json:"name"`
		Key     string                      `json:"key"`
		Board   string                      `json:"board"`
		People  []struct{ ID, Name string } `json:"people"`
		Tickets []struct {
			K string `json:"k"`
			T string `json:"t"`
		} `json:"tickets"`
		Backlog []struct {
			K  string `json:"k"`
			Ty string `json:"ty"`
			T  string `json:"t"`
		} `json:"backlog"`
	} `json:"standups"`
}

func loadShape(t *testing.T) sampleShape {
	t.Helper()
	var s sampleShape
	if err := json.Unmarshal(loadSample(t), &s); err != nil {
		t.Fatalf("decode sample: %v", err)
	}
	return s
}

// ── Convert on the real sample ──────────────────────────────────────────────────────────────────

func TestConvert_FiveProjectsAndBoardsMatchTheFiveStandups(t *testing.T) {
	defer guard(t)
	fx := convertSample(t)
	sample := loadShape(t)
	if len(fx.Projects) != 5 || len(fx.Boards) != 5 || len(sample.Standups) != 5 {
		t.Fatalf("projects %d boards %d (sample standups %d), want 5 each", len(fx.Projects), len(fx.Boards), len(sample.Standups))
	}
	wantBoards := []struct {
		key, project, name, typ string
	}{
		{"MOB", "Saffron Mobile", "MOB board", "scrum"},
		{"WEB", "Web & Checkout", "WEB board", "scrum"},
		{"DATA", "Recipe Data", "DATA board", "kanban"},
		{"GRO", "Growth", "GRO board", "scrum"},
		{"PLAT", "Platform", "PLAT board", "kanban"},
	}
	ids, boardIDs, filters, pkeys := map[string]bool{}, map[int]bool{}, map[int]bool{}, map[string]bool{}
	for i, w := range wantBoards {
		p, b := fx.Projects[i], fx.Boards[i]
		if p.Key != w.key || p.Name != w.project || p.ID == "" || ids[p.ID] {
			t.Errorf("project %d = %+v, want key %s name %q with a unique id", i, p, w.key, w.project)
		}
		ids[p.ID], pkeys[p.Key] = true, true
		if b.Name != w.name || b.Type != w.typ || b.ProjectKey != w.key {
			t.Errorf("board %d = name %q type %q project %q, want %q %q %q", i, b.Name, b.Type, b.ProjectKey, w.name, w.typ, w.key)
		}
		if b.ID <= 0 || boardIDs[b.ID] || b.FilterID <= 0 || filters[b.FilterID] {
			t.Errorf("board %d ids = id %d filter %d, want unique positive ids", i, b.ID, b.FilterID)
		}
		boardIDs[b.ID], filters[b.FilterID] = true, true
	}
	if fx.Boards[0].ID != 1 {
		t.Errorf("MOB board id = %d, want 1", fx.Boards[0].ID)
	}
	wantCols := []jiratest.FxColumn{
		{Name: "To Do", Statuses: []string{"To Do"}},
		{Name: "In Progress", Statuses: []string{"In Progress"}},
		{Name: "Code Review", Statuses: []string{"Code Review"}},
		{Name: "QA", Statuses: []string{"QA"}},
		{Name: "Done", Statuses: []string{"Done"}},
	}
	for _, b := range fx.Boards {
		if !reflect.DeepEqual(b.Columns, wantCols) {
			t.Errorf("board %s columns = %+v, want %+v", b.Name, b.Columns, wantCols)
		}
	}
}

func TestConvert_SprintsOnlyOnScrumBoards(t *testing.T) {
	defer guard(t)
	fx := convertSample(t)
	svc := fx.Boards[0].Sprint
	if svc == nil || svc.Name != "Sprint 42" || svc.ID != 42 || svc.StartDay != -13 || svc.EndDay != 0 {
		t.Errorf("MOB sprint = %+v, want {42 Sprint 42 -13 0}", svc)
	}
	if s := fx.Boards[1].Sprint; s == nil || s.Name != "Sprint 18" || s.ID != 18 || s.StartDay >= 0 || s.EndDay < 0 {
		t.Errorf("WEB sprint = %+v, want Sprint 18 spanning today", s)
	}
	if s := fx.Boards[3].Sprint; s == nil || s.Name != "Sprint 9" || s.ID != 9 {
		t.Errorf("GRO sprint = %+v, want Sprint 9", s)
	}
	for _, i := range []int{2, 4} {
		if fx.Boards[i].Sprint != nil {
			t.Errorf("kanban board %s has a sprint: %+v", fx.Boards[i].Name, fx.Boards[i].Sprint)
		}
	}
}

func TestConvert_UsersFromPeople(t *testing.T) {
	defer guard(t)
	fx := convertSample(t)
	got := map[string]string{}
	for _, u := range fx.Users {
		if _, dup := got[u.AccountID]; dup {
			t.Errorf("duplicate user %s", u.AccountID)
		}
		got[u.AccountID] = u.DisplayName
		if u.AccountType != "" && u.AccountType != "atlassian" {
			t.Errorf("user %s accountType %q, want default/atlassian", u.AccountID, u.AccountType)
		}
	}
	sample := loadShape(t)
	svcPeople := sample.Standups[0].People
	if len(svcPeople) != 7 {
		t.Fatalf("sample MOB people = %d, want 7", len(svcPeople))
	}
	for _, p := range svcPeople {
		if got["u-"+p.ID] != p.Name || p.Name == "" {
			t.Errorf("MOB person %s -> u-%s = %q, want %q", p.ID, p.ID, got["u-"+p.ID], p.Name)
		}
	}
	if got["u-iris"] != "Iris Calloway" {
		t.Errorf("u-iris = %q, want Iris Calloway", got["u-iris"])
	}
	if len(fx.Users) != 24 {
		t.Errorf("users = %d, want 24 (7+5+4+4+4)", len(fx.Users))
	}
}

func TestConvert_SvcTicketsAndBacklogCounts(t *testing.T) {
	defer guard(t)
	fx := convertSample(t)
	sample := loadShape(t)
	svc := fx.Boards[0].ID
	if n := len(issuesOfBoard(fx, svc)); n != 55 || n != len(sample.Standups[0].Tickets) {
		t.Fatalf("MOB issues = %d, want 55 (the sample's MOB tickets)", n)
	}
	if n := len(fx.Issues); n != 133 {
		t.Errorf("all issues = %d, want 133", n)
	}
	if n := len(fx.Backlog); n != 90 {
		t.Errorf("all backlog entries = %d, want 90 (duplicates with tickets kept)", n)
	}
	// Issue order: standups in sample order, tickets in sample order.
	var wantKeys, gotKeys []string
	for _, st := range sample.Standups {
		for _, tk := range st.Tickets {
			wantKeys = append(wantKeys, tk.K)
		}
	}
	for _, is := range fx.Issues {
		gotKeys = append(gotKeys, is.Key)
	}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Errorf("issue key order differs from the sample")
	}
	// Every issue refers to a real board; summaries come from t.
	boardIDs := map[int]bool{}
	for _, b := range fx.Boards {
		boardIDs[b.ID] = true
	}
	for _, is := range fx.Issues {
		if !boardIDs[is.Board] || is.Summary == "" {
			t.Errorf("issue %s board %d summary %q", is.Key, is.Board, is.Summary)
		}
	}
}

func TestConvert_SvcBacklogPreservedInOrder(t *testing.T) {
	defer guard(t)
	fx := convertSample(t)
	sample := loadShape(t)
	svc := fx.Boards[0].ID
	got := backlogOfBoard(fx, svc)
	want := sample.Standups[0].Backlog
	if len(got) != 40 || len(got) != len(want) {
		t.Fatalf("MOB backlog = %d, want 40", len(got))
	}
	typeName := map[string]string{"story": "Story", "bug": "Bug", "task": "Task", "spike": "Spike"}
	for i, w := range want {
		if got[i].Key != w.K || got[i].Summary != w.T || got[i].Type != typeName[w.Ty] {
			t.Fatalf("backlog[%d] = %+v, want %s / %q / %s", i, got[i], w.K, w.T, typeName[w.Ty])
		}
	}
	if got[0].Key != "MOB-1525" || got[0].Type != "Story" || got[2].Key != "MOB-1533" || got[2].Type != "Bug" || got[39].Key != "MOB-1657" {
		t.Errorf("backlog anchors wrong: %+v … %+v", got[:3], got[39])
	}
}

func TestConvert_Svc1390HistoryCommitsPRsAndComments(t *testing.T) {
	defer guard(t)
	fx := convertSample(t)
	is := issueByKey(fx, "MOB-1390")
	if is == nil {
		t.Fatalf("MOB-1390 missing")
	}
	if is.Type != "Story" || is.Assignee != "u-iris" || is.Board != fx.Boards[0].ID || !is.InSprint ||
		is.Summary != "Swipe to check off items on the shopping list" {
		t.Errorf("MOB-1390 = %+v", *is)
	}
	wantHist := []jiratest.FxHistory{{Status: "To Do", Days: 5}, {Status: "In Progress", Days: 6}, {Status: "Code Review", Days: 3}, {Status: "QA", Days: 3}}
	if !reflect.DeepEqual(is.History, wantHist) {
		t.Errorf("history = %+v, want %+v", is.History, wantHist)
	}
	// ev: [2,1,0,2,1,1,0,0,0,1,2,2,0,2,0,0,0,0,3,0,0,1,1,0,0,0,2,2] -> commits per day i-27 (1|2 = that many), PR at ev==3.
	wantCommits := []int{-27, -27, -26, -24, -24, -23, -22, -18, -17, -17, -16, -16, -14, -14, -6, -5, -1, -1, 0, 0}
	got := append([]int(nil), is.Commits...)
	sort.Ints(got)
	if !reflect.DeepEqual(got, wantCommits) {
		t.Errorf("commits = %v, want %v", got, wantCommits)
	}
	if len(is.PRs) != 1 || is.PRs[0].Day != -9 {
		t.Errorf("PRs = %+v, want one PR on day -9", is.PRs)
	}
	if len(is.PRs) == 1 && is.PRs[0].Status != "OPEN" && is.PRs[0].Status != "MERGED" && is.PRs[0].Status != "DECLINED" {
		t.Errorf("PR status %q not one of OPEN|MERGED|DECLINED", is.PRs[0].Status)
	}
	gotC := append([]int(nil), is.Comments...)
	sort.Ints(gotC)
	if want := []int{-27, -24, -16, -9}; !reflect.DeepEqual(gotC, want) {
		t.Errorf("comments = %v, want %v", gotC, want)
	}
	if len(is.FlaggedDays) != 0 {
		t.Errorf("MOB-1390 flaggedDays = %v, want none", is.FlaggedDays)
	}
}

func TestConvert_FlaggedTicketsGetFlaggedDays(t *testing.T) {
	defer guard(t)
	fx := convertSample(t)
	for _, key := range []string{"MOB-1429", "MOB-1539"} { // bl: true, blD: 3
		is := issueByKey(fx, key)
		if is == nil || !reflect.DeepEqual(is.FlaggedDays, [][2]int{{-2, 0}}) {
			t.Errorf("%s flaggedDays = %+v, want [[-2 0]] (blD 3)", key, is)
		}
	}
	flagged := 0
	for _, is := range fx.Issues {
		if len(is.FlaggedDays) > 0 {
			flagged++
		}
	}
	if flagged < 2 {
		t.Errorf("flagged issues = %d, want at least the 2 MOB ones", flagged)
	}
}

func TestConvert_UnassignedTicketsHaveNoAssignee(t *testing.T) {
	defer guard(t)
	fx := convertSample(t)
	for _, key := range []string{"MOB-1647", "MOB-1648", "MOB-1651"} { // "a": null in the sample
		if is := issueByKey(fx, key); is == nil || is.Assignee != "" {
			t.Errorf("%s = %+v, want an empty assignee", key, is)
		}
	}
}

func TestConvert_IsDeterministic(t *testing.T) {
	defer guard(t)
	a, b := convertSample(t), convertSample(t)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if !bytes.Equal(ja, jb) {
		t.Fatalf("two Convert calls produced different fixtures")
	}
}

func TestConvert_InvalidInputIsAnError(t *testing.T) {
	defer guard(t)
	for name, in := range map[string]string{
		"empty":     ``,
		"not json":  `nope`,
		"truncated": `{"standups":[`,
		"wrong":     `{"standups":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			defer guard(t)
			if fx, err := Convert([]byte(in)); err == nil {
				t.Fatalf("Convert(%q) = %+v, want an error", in, fx)
			}
		})
	}
}

// ── Convert on a tiny hand-written sample (rules in isolation) ──────────────────────────────────

func ev28(m map[int]int) []int {
	out := make([]int, 28)
	for i, v := range m {
		out[i] = v
	}
	return out
}

func cm28(days ...int) []bool {
	out := make([]bool, 28)
	for _, i := range days {
		out[i] = true
	}
	return out
}

type miniTicket struct {
	K    string     `json:"k"`
	A    *string    `json:"a"`
	S    string     `json:"s"`
	Ty   string     `json:"ty"`
	T    string     `json:"t"`
	Days int        `json:"days"`
	Bl   bool       `json:"bl"`
	BlD  int        `json:"blD"`
	Ev   []int      `json:"ev"`
	Cm   []bool     `json:"cm"`
	Hist []miniHist `json:"hist"`
}

type miniHist struct {
	S string `json:"s"`
	D int    `json:"d"`
}

func miniSample() []byte {
	zed := "zed"
	kanban := map[string]any{
		"id": "min", "name": "Mini Kanban Team", "key": "MIN", "board": "MIN board · Kanban", "space": "MIN · Mini",
		"sprint": "Kanban · continuous flow", "doneLabel": "last 14 days",
		"people": []map[string]string{{"id": "zed", "name": "Zed Zane", "ini": "ZZ"}},
		"tickets": []miniTicket{
			{K: "MIN-1", A: &zed, S: "prog", Ty: "story", T: "One", Days: 2, Bl: true, BlD: 4,
				Ev: ev28(map[int]int{27: 2, 26: 1, 0: 3, 5: 0}), Cm: cm28(10, 27),
				Hist: []miniHist{{"todo", 3}, {"prog", 2}}},
			{K: "MIN-2", A: nil, S: "todo", Ty: "bug", T: "Two", Days: 1, Ev: ev28(nil), Cm: cm28(),
				Hist: []miniHist{{"todo", 1}}},
			{K: "MIN-3", A: &zed, S: "done", Ty: "spike", T: "Three", Days: 1, Bl: true, BlD: 1, Ev: ev28(map[int]int{13: 3, 14: 3}), Cm: cm28(),
				Hist: []miniHist{{"todo", 1}, {"prog", 1}, {"review", 1}, {"qa", 1}, {"done", 1}}},
			{K: "MIN-4", A: &zed, S: "qa", Ty: "task", T: "Four", Days: 4, Ev: ev28(nil), Cm: cm28(),
				Hist: []miniHist{{"qa", 4}}},
		},
		"backlog": []map[string]string{{"k": "MIN-9", "ty": "spike", "t": "Nine"}, {"k": "MIN-8", "ty": "bug", "t": "Eight"}, {"k": "MIN-10", "ty": "task", "t": "Ten"}},
	}
	scrum := map[string]any{
		"id": "sc", "name": "Mini Scrum Team", "key": "SCR", "board": "SCR board · Scrum", "space": "SCR · Scrum",
		"sprint": "Sprint 7 · day 3 of 10", "doneLabel": "this sprint",
		"people":  []map[string]string{},
		"tickets": []miniTicket{{K: "SCR-1", A: nil, S: "todo", Ty: "story", T: "S one", Days: 1, Ev: ev28(nil), Cm: cm28(), Hist: []miniHist{{"todo", 1}}}},
		"backlog": []map[string]string{},
	}
	b, err := json.Marshal(map[string]any{"date": "Mon 23 Nov", "standups": []any{kanban, scrum}, "axis": []any{}})
	if err != nil {
		panic(err)
	}
	return b
}

func TestConvert_MiniSampleAppliesEachRule(t *testing.T) {
	defer guard(t)
	fx, err := Convert(miniSample())
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(fx.Projects) != 2 || len(fx.Boards) != 2 || len(fx.Issues) != 5 || len(fx.Backlog) != 3 || len(fx.Users) != 1 {
		t.Fatalf("shape = %d projects %d boards %d issues %d backlog %d users, want 2/2/5/3/1",
			len(fx.Projects), len(fx.Boards), len(fx.Issues), len(fx.Backlog), len(fx.Users))
	}
	if fx.Projects[0].Key != "MIN" || fx.Projects[0].Name != "Mini Kanban Team" || fx.Projects[1].Key != "SCR" {
		t.Errorf("projects = %+v", fx.Projects)
	}
	kb, sb := fx.Boards[0], fx.Boards[1]
	if kb.Type != "kanban" || kb.Sprint != nil || kb.Name != "MIN board" || kb.ProjectKey != "MIN" {
		t.Errorf("kanban board = %+v", kb)
	}
	if sb.Type != "scrum" || sb.Sprint == nil || sb.Sprint.Name != "Sprint 7" || sb.Sprint.ID != 7 || sb.Name != "SCR board" {
		t.Errorf("scrum board = %+v sprint %+v", sb, sb.Sprint)
	}
	if fx.Users[0].AccountID != "u-zed" || fx.Users[0].DisplayName != "Zed Zane" {
		t.Errorf("user = %+v", fx.Users[0])
	}

	one := issueByKey(fx, "MIN-1")
	if one.Type != "Story" || one.Assignee != "u-zed" || one.Board != kb.ID || one.InSprint {
		t.Errorf("MIN-1 = %+v", *one)
	}
	if !reflect.DeepEqual(one.FlaggedDays, [][2]int{{-3, 0}}) {
		t.Errorf("MIN-1 flaggedDays = %v, want [[-3 0]] (blD 4)", one.FlaggedDays)
	}
}

func TestConvert_MiniSampleRulesByDay(t *testing.T) {
	defer guard(t)
	fx, err := Convert(miniSample())
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	one := issueByKey(fx, "MIN-1")
	// index i -> day i-27: ev[27]=2 -> day 0 x2; ev[26]=1 -> day -1; ev[0]=3 -> PR on day -27; cm[10] -> -17; cm[27] -> 0.
	c := append([]int(nil), one.Commits...)
	sort.Ints(c)
	if want := []int{-1, 0, 0}; !reflect.DeepEqual(c, want) {
		t.Errorf("MIN-1 commits = %v, want %v", c, want)
	}
	if len(one.PRs) != 1 || one.PRs[0].Day != -27 {
		t.Errorf("MIN-1 PRs = %+v, want one on day -27", one.PRs)
	}
	cs := append([]int(nil), one.Comments...)
	sort.Ints(cs)
	if want := []int{-17, 0}; !reflect.DeepEqual(cs, want) {
		t.Errorf("MIN-1 comments = %v, want %v", cs, want)
	}
	if want := []jiratest.FxHistory{{Status: "To Do", Days: 3}, {Status: "In Progress", Days: 2}}; !reflect.DeepEqual(one.History, want) {
		t.Errorf("MIN-1 history = %+v, want %+v", one.History, want)
	}

	two := issueByKey(fx, "MIN-2")
	if two.Type != "Bug" || two.Assignee != "" || len(two.FlaggedDays) != 0 || len(two.Commits) != 0 || len(two.PRs) != 0 || len(two.Comments) != 0 {
		t.Errorf("MIN-2 = %+v, want an unassigned bug with no activity", *two)
	}

	three := issueByKey(fx, "MIN-3")
	if three.Type != "Spike" || !reflect.DeepEqual(three.FlaggedDays, [][2]int{{0, 0}}) {
		t.Errorf("MIN-3 type %q flaggedDays %v, want Spike and [[0 0]] (blD 1)", three.Type, three.FlaggedDays)
	}
	if len(three.PRs) != 2 {
		t.Errorf("MIN-3 PRs = %+v, want two (ev[13]=3, ev[14]=3)", three.PRs)
	} else {
		days := []int{three.PRs[0].Day, three.PRs[1].Day}
		sort.Ints(days)
		if !reflect.DeepEqual(days, []int{-14, -13}) {
			t.Errorf("MIN-3 PR days = %v, want [-14 -13]", days)
		}
		if three.PRs[0].ID == three.PRs[1].ID {
			t.Errorf("MIN-3 PR ids repeat: %+v", three.PRs)
		}
	}
	wantHist := []jiratest.FxHistory{{Status: "To Do", Days: 1}, {Status: "In Progress", Days: 1}, {Status: "Code Review", Days: 1}, {Status: "QA", Days: 1}, {Status: "Done", Days: 1}}
	if !reflect.DeepEqual(three.History, wantHist) {
		t.Errorf("MIN-3 history = %+v, want lane ids mapped to status names %+v", three.History, wantHist)
	}
	if four := issueByKey(fx, "MIN-4"); four.Type != "Task" || !reflect.DeepEqual(four.History, []jiratest.FxHistory{{Status: "QA", Days: 4}}) {
		t.Errorf("MIN-4 = %+v", *four)
	}

	scr := issueByKey(fx, "SCR-1")
	if scr.Board != fx.Boards[1].ID || !scr.InSprint || scr.Assignee != "" {
		t.Errorf("SCR-1 = %+v, want board %d, in sprint, unassigned", *scr, fx.Boards[1].ID)
	}

	// backlog: array order = rank order (NOT sorted by key), types mapped
	var keys []string
	for _, b := range fx.Backlog {
		keys = append(keys, b.Key)
		if b.Board != fx.Boards[0].ID {
			t.Errorf("backlog %s board = %d, want %d", b.Key, b.Board, fx.Boards[0].ID)
		}
	}
	if want := []string{"MIN-9", "MIN-8", "MIN-10"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("backlog order = %v, want %v", keys, want)
	}
	if fx.Backlog[0].Type != "Spike" || fx.Backlog[1].Type != "Bug" || fx.Backlog[2].Type != "Task" || fx.Backlog[0].Summary != "Nine" {
		t.Errorf("backlog = %+v", fx.Backlog)
	}
}

// ── drift, materialisation and the command ──────────────────────────────────────────────────────

func TestConvert_DefaultFixtureMatchesConvertedSample(t *testing.T) {
	defer guard(t)
	want := convertSample(t)
	got, err := jiratest.ParseFixture(jiratest.DefaultFixture)
	if err != nil {
		t.Fatalf("ParseFixture(DefaultFixture): %v", err)
	}
	jw, _ := json.Marshal(want)
	jg, _ := json.Marshal(got)
	if !bytes.Equal(jw, jg) {
		t.Fatalf("testdata/fixture.json is stale: run `go run ./cmd/fixturegen` (committed has %d issues, sample converts to %d)", len(got.Issues), len(want.Issues))
	}
}

func TestConvert_SampleMaterializesToUniqueKeys(t *testing.T) {
	defer guard(t)
	fx := convertSample(t)
	nyc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 23, 22, 30, 0, 0, nyc)
	ds := jiratest.Materialize(fx, now, nyc)
	seen := map[string]bool{}
	for _, is := range ds.Issues {
		if seen[is.Key] {
			t.Fatalf("duplicate key %s in the materialised sample", is.Key)
		}
		seen[is.Key] = true
	}
	if len(ds.Issues) != 209 {
		t.Errorf("materialised issues = %d, want 209 distinct keys (133 tickets + 90 backlog - 14 overlaps)", len(ds.Issues))
	}
	if len(ds.Boards) != 5 || len(ds.Projects) != 5 {
		t.Errorf("boards %d projects %d, want 5/5", len(ds.Boards), len(ds.Projects))
	}
	// The board copy of an overlapping key wins: MOB-1539 is a flagged QA ticket, not a backlog item.
	var found *jiratest.DSIssue
	for _, is := range ds.Issues {
		if is.Key == "MOB-1539" {
			found = is
		}
	}
	if found == nil || found.Flagged != true || len(found.Changelog) == 0 {
		t.Fatalf("MOB-1539 = %+v, want the flagged board ticket with history", found)
	}
}

func goBin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	return p
}

func runFixturegen(t *testing.T, in, out string) {
	t.Helper()
	cmd := exec.Command(goBin(t), "run", ".", "-in", in, "-out", out)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run . -in %s -out %s: %v\n%s", in, out, err, stderr.String())
	}
}

func TestMain_FlagsWriteADeterministicParsableFixture(t *testing.T) {
	defer guard(t)
	dir := t.TempDir()
	out1, out2 := filepath.Join(dir, "one.json"), filepath.Join(dir, "two", "two.json")
	if err := os.MkdirAll(filepath.Dir(out2), 0o755); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	runFixturegen(t, abs, out1)
	runFixturegen(t, abs, out2)
	b1, err1 := os.ReadFile(out1)
	b2, err2 := os.ReadFile(out2)
	if err1 != nil || err2 != nil {
		t.Fatalf("outputs not written: %v %v", err1, err2)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("two runs wrote different bytes")
	}
	fx, err := jiratest.ParseFixture(b1)
	if err != nil {
		t.Fatalf("output does not parse: %v", err)
	}
	want := convertSample(t)
	jw, _ := json.Marshal(want)
	jg, _ := json.Marshal(fx)
	if !bytes.Equal(jw, jg) {
		t.Fatalf("written fixture differs from Convert(sample)")
	}
	if strings.Contains(string(b1), "\"ev\"") {
		t.Errorf("output still contains sample-shaped fields")
	}
}

func TestMain_MissingInputFailsWithoutWritingOutput(t *testing.T) {
	defer guard(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.json")
	cmd := exec.Command(goBin(t), "run", ".", "-in", filepath.Join(dir, "does-not-exist.json"), "-out", out)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("fixturegen with a missing input succeeded, want a non-zero exit")
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("error = %v, want a non-zero exit\n%s", err, stderr.String())
	}
	if msg := stderr.String(); strings.Contains(msg, "goroutine ") || !strings.Contains(msg, "does-not-exist.json") {
		t.Errorf("stderr = %q, want a one-line error naming the input file and no stack trace", msg)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Errorf("output file written despite the failure")
	}
}
