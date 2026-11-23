package jira

// WP-19 tests for plan.go (Plan): the exact JQL of jira-sync.md §2.8 for every row of the query table. Expr strings
// are asserted literally. No HTTP is involved (the adapter points at a closed port; any request would fail the test).
//
// SPEC-NOTE: Literal formatting pinned here (the doc leaves it open): clauses are joined by single spaces with no
// double spaces when {K}/{X} are empty; lists are comma+space separated: `status in (10002, 10003)`,
// `sprint in (42, 43)`; the tail is always ` ORDER BY Rank ASC`. {X} is `AND (<extra>)`, {K} is `AND (<subQuery>)` and
// {K} comes before {X}. `[ OR sprint in (S…)]` is only present when there are active sprints (scrum); with none the
// clause reads `(updated >= -15d)`.
// SPEC-NOTE: Status id lists are sorted ascending (numeric order; the ids used here are equal-length so lexicographic
// order gives the same). The test input deliberately lists column statuses in DESCENDING order, so column order is
// NOT the contract. `hidden` statuses appear in no list. W = lanes todo+prog+review+qa, P = prog+review+qa, D = done,
// BL = backlog, T = todo; the scrum backlog list is BL ∪ T (merged, sorted, no duplicates).
// SPEC-NOTE: Query order is the table order: sprint|open, done, backlog. Name/Role: sprint/board, open/board,
// done/board, backlog/backlog. backlog has Count true and Limit = in.BacklogLimit when > 0, else 50; other queries
// have Limit 0 and Count false.
// SPEC-NOTE: board kind comes from in.Board.Board.Kind ("scrum"|"kanban"). Scope "sprint" on a scrum board yields
// `sprint` (only when active sprints exist) + `backlog`; "sprint" scope with no active sprint yields only `backlog`
// (empty board, §2.7). Kanban ignores Scope and ActiveSprints.
// SPEC-NOTE: An empty working set W (no status in lanes todo/prog/review/qa) is a validation error for every scope
// and kind: apperr.Validation whose message contains "Map at least one column". Other empty lists drop only their
// query. When P is empty on a scrum board scope the open query must still be valid JQL (never `in ()`); the exact
// text is not pinned.
// SPEC-NOTE: filterId arrives in Board.Meta as int, int64, float64 (JSON round trip), string or json.Number and is
// rendered as a plain integer (8-digit ids must not turn into 1.0234567e+07). A missing filterId is a validation error.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

func wp19PlanAdapter(t *testing.T) *Adapter {
	return newWP19Adapter(t, "http://127.0.0.1:1", wp19Meta())
}

// wp19Lanes maps: 10001 backlog · 10002 todo · 10003,10004 prog · 10005 review · 10006 qa · 10007,10008 done ·
// 10009 hidden.
func wp19Lanes() map[string]string {
	return map[string]string{
		"10001": model.LaneBacklog, "10002": model.LaneTodo, "10003": model.LaneProg, "10004": model.LaneProg,
		"10005": model.LaneReview, "10006": model.LaneQA, "10007": model.LaneDone, "10008": model.LaneDone,
		"10009": model.LaneHidden,
	}
}

func wp19Board(kind string, meta map[string]any) model.BoardConfig {
	return model.BoardConfig{
		Board: model.Board{ID: "1", Name: "MOB board", Kind: kind, RawType: kind, Meta: meta},
		Columns: []model.Column{
			{Name: "Backlog", Position: 0, StatusIDs: []string{"10001"}},
			{Name: "To Do", Position: 1, StatusIDs: []string{"10002"}},
			{Name: "In Progress", Position: 2, StatusIDs: []string{"10004", "10003"}},
			{Name: "Review", Position: 3, StatusIDs: []string{"10005"}},
			{Name: "QA", Position: 4, StatusIDs: []string{"10006"}},
			{Name: "Done", Position: 5, StatusIDs: []string{"10008", "10007"}},
		},
	}
}

func wp19Sprints(ids ...string) []model.Sprint {
	var out []model.Sprint
	for _, id := range ids {
		out = append(out, model.Sprint{ID: id, BoardID: "1", Name: "Sprint " + id, State: "active"})
	}
	return out
}

type wp19WantQuery struct {
	Name, Role string
	Limit      int
	Count      bool
	Expr       string
}

func wp19AssertPlan(t *testing.T, got []source.Query, want []wp19WantQuery) {
	t.Helper()
	if len(got) != len(want) {
		var names []string
		for _, q := range got {
			names = append(names, q.Name+": "+q.Expr)
		}
		t.Fatalf("queries = %d, want %d:\n%s", len(got), len(want), strings.Join(names, "\n"))
	}
	for i, w := range want {
		g := got[i]
		if g.Name != w.Name || g.Role != w.Role || g.Limit != w.Limit || g.Count != w.Count {
			t.Errorf("query[%d] = {Name:%q Role:%q Limit:%d Count:%v}, want {Name:%q Role:%q Limit:%d Count:%v}",
				i, g.Name, g.Role, g.Limit, g.Count, w.Name, w.Role, w.Limit, w.Count)
		}
		if g.Expr != w.Expr {
			t.Errorf("query[%d] (%s) Expr:\n got  %s\n want %s", i, w.Name, g.Expr, w.Expr)
		}
		if strings.Contains(g.Expr, "  ") {
			t.Errorf("query[%d] Expr contains a double space: %q", i, g.Expr)
		}
		if strings.Contains(g.Expr, "in ()") {
			t.Errorf("query[%d] Expr contains an empty list: %q", i, g.Expr)
		}
	}
}

const (
	wp19W      = "(10002, 10003, 10004, 10005, 10006)"
	wp19P      = "(10003, 10004, 10005, 10006)"
	wp19Done   = "(10007, 10008)"
	wp19BLT    = "(10001, 10002)"
	wp19TailOB = " ORDER BY Rank ASC"
)

func TestPlan_ScrumSprintScope_SprintAndBacklogQueries(t *testing.T) {
	defer wp19Guard(t)
	a := wp19PlanAdapter(t)
	got, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("scrum", map[string]any{"filterId": 9001}), Scope: "sprint",
		ActiveSprints: wp19Sprints("42", "43"), StatusLanes: wp19Lanes(), BacklogLimit: 50,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wp19AssertPlan(t, got, []wp19WantQuery{
		{"sprint", "board", 0, false, "filter = 9001 AND sprint in (42, 43)" + wp19TailOB},
		{"backlog", "backlog", 50, true, "filter = 9001 AND status in " + wp19BLT + " AND (sprint is EMPTY OR sprint not in openSprints())" + wp19TailOB},
	})
}

func TestPlan_ScrumSprintScope_ExtraFilterIsWrappedAndAppended(t *testing.T) {
	defer wp19Guard(t)
	a := wp19PlanAdapter(t)
	got, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("scrum", map[string]any{"filterId": 9001}), Scope: "sprint", ExtraFilter: "labels = standup OR component = api",
		ActiveSprints: wp19Sprints("42"), StatusLanes: wp19Lanes(), BacklogLimit: 50,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wp19AssertPlan(t, got, []wp19WantQuery{
		{"sprint", "board", 0, false, "filter = 9001 AND sprint in (42) AND (labels = standup OR component = api)" + wp19TailOB},
		{"backlog", "backlog", 50, true, "filter = 9001 AND status in " + wp19BLT + " AND (sprint is EMPTY OR sprint not in openSprints()) AND (labels = standup OR component = api)" + wp19TailOB},
	})
}

func TestPlan_ScrumSprintScope_NoActiveSprintYieldsOnlyBacklog(t *testing.T) {
	defer wp19Guard(t)
	a := wp19PlanAdapter(t)
	got, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("scrum", map[string]any{"filterId": 9001}), Scope: "sprint",
		StatusLanes: wp19Lanes(), BacklogLimit: 50,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wp19AssertPlan(t, got, []wp19WantQuery{
		{"backlog", "backlog", 50, true, "filter = 9001 AND status in " + wp19BLT + " AND (sprint is EMPTY OR sprint not in openSprints())" + wp19TailOB},
	})
}

func TestPlan_ScrumBoardScope_OpenDoneAndBacklogWithActiveSprints(t *testing.T) {
	defer wp19Guard(t)
	a := wp19PlanAdapter(t)
	got, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("scrum", map[string]any{"filterId": 9001}), Scope: "board",
		ActiveSprints: wp19Sprints("42", "43"), StatusLanes: wp19Lanes(), BacklogLimit: 50,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wp19AssertPlan(t, got, []wp19WantQuery{
		{"open", "board", 0, false, "filter = 9001 AND status in " + wp19W + " AND (sprint in openSprints() OR status in " + wp19P + ")" + wp19TailOB},
		{"done", "board", 0, false, "filter = 9001 AND status in " + wp19Done + " AND (updated >= -15d OR sprint in (42, 43))" + wp19TailOB},
		{"backlog", "backlog", 50, true, "filter = 9001 AND status in " + wp19BLT + " AND (sprint is EMPTY OR sprint not in openSprints())" + wp19TailOB},
	})
}

func TestPlan_ScrumBoardScope_NoActiveSprintDropsTheSprintOrClause(t *testing.T) {
	defer wp19Guard(t)
	a := wp19PlanAdapter(t)
	got, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("scrum", map[string]any{"filterId": 9001}), Scope: "board", ExtraFilter: "labels = standup",
		StatusLanes: wp19Lanes(), BacklogLimit: 50,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wp19AssertPlan(t, got, []wp19WantQuery{
		{"open", "board", 0, false, "filter = 9001 AND status in " + wp19W + " AND (sprint in openSprints() OR status in " + wp19P + ") AND (labels = standup)" + wp19TailOB},
		{"done", "board", 0, false, "filter = 9001 AND status in " + wp19Done + " AND (updated >= -15d) AND (labels = standup)" + wp19TailOB},
		{"backlog", "backlog", 50, true, "filter = 9001 AND status in " + wp19BLT + " AND (sprint is EMPTY OR sprint not in openSprints()) AND (labels = standup)" + wp19TailOB},
	})
}

func TestPlan_Kanban_OpenDoneBacklogWithSubQueryBeforeExtraFilter(t *testing.T) {
	defer wp19Guard(t)
	a := wp19PlanAdapter(t)
	got, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("kanban", map[string]any{"filterId": 9001, "subQuery": "fixVersion in unreleasedVersions()"}), Scope: "board",
		ExtraFilter: "labels = standup", StatusLanes: wp19Lanes(), BacklogLimit: 50,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	k := " AND (fixVersion in unreleasedVersions()) AND (labels = standup)"
	wp19AssertPlan(t, got, []wp19WantQuery{
		{"open", "board", 0, false, "filter = 9001 AND status in " + wp19W + k + wp19TailOB},
		{"done", "board", 0, false, "filter = 9001 AND status in " + wp19Done + " AND (updated >= -15d)" + k + wp19TailOB},
		{"backlog", "backlog", 50, true, "filter = 9001 AND status in (10001)" + k + wp19TailOB},
	})
}

func TestPlan_Kanban_WithoutSubQueryOrExtraFilter(t *testing.T) {
	defer wp19Guard(t)
	a := wp19PlanAdapter(t)
	got, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("kanban", map[string]any{"filterId": 9001}), Scope: "board",
		ActiveSprints: wp19Sprints("42"), // ignored for kanban
		StatusLanes:   wp19Lanes(), BacklogLimit: 50,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wp19AssertPlan(t, got, []wp19WantQuery{
		{"open", "board", 0, false, "filter = 9001 AND status in " + wp19W + wp19TailOB},
		{"done", "board", 0, false, "filter = 9001 AND status in " + wp19Done + " AND (updated >= -15d)" + wp19TailOB},
		{"backlog", "backlog", 50, true, "filter = 9001 AND status in (10001)" + wp19TailOB},
	})
}

func TestPlan_Kanban_BacklogQueryOnlyWhenABacklogLaneStatusExists(t *testing.T) {
	defer wp19Guard(t)
	lanes := wp19Lanes()
	delete(lanes, "10001") // the only backlog-lane status
	a := wp19PlanAdapter(t)
	got, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("kanban", map[string]any{"filterId": 9001}), Scope: "board", StatusLanes: lanes, BacklogLimit: 50,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wp19AssertPlan(t, got, []wp19WantQuery{
		{"open", "board", 0, false, "filter = 9001 AND status in " + wp19W + wp19TailOB},
		{"done", "board", 0, false, "filter = 9001 AND status in " + wp19Done + " AND (updated >= -15d)" + wp19TailOB},
	})
}

func TestPlan_KanbanBacklogIgnoresTodoStatuses(t *testing.T) {
	defer wp19Guard(t)
	a := wp19PlanAdapter(t)
	got, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("kanban", map[string]any{"filterId": 9001}), Scope: "board", StatusLanes: wp19Lanes(), BacklogLimit: 50,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, q := range got {
		if q.Name == "backlog" && strings.Contains(q.Expr, "10002") {
			t.Errorf("kanban backlog contains the todo status 10002: %s", q.Expr)
		}
	}
}

func TestPlan_EmptyStatusListsDropTheirQueries(t *testing.T) {
	defer wp19Guard(t)
	subset := func(keep ...string) map[string]string {
		all := wp19Lanes()
		out := map[string]string{}
		for _, id := range keep {
			out[id] = all[id]
		}
		return out
	}
	tests := []struct {
		name  string
		kind  string
		scope string
		lanes map[string]string
		want  []wp19WantQuery
	}{
		{
			name: "scrum board scope without a done column has no done query", kind: "scrum", scope: "board",
			lanes: subset("10001", "10002", "10003", "10004", "10005", "10006"),
			want: []wp19WantQuery{
				{"open", "board", 0, false, "filter = 9001 AND status in " + wp19W + " AND (sprint in openSprints() OR status in " + wp19P + ")" + wp19TailOB},
				{"backlog", "backlog", 50, true, "filter = 9001 AND status in " + wp19BLT + " AND (sprint is EMPTY OR sprint not in openSprints())" + wp19TailOB},
			},
		},
		{
			name: "scrum without backlog and todo statuses has no backlog query", kind: "scrum", scope: "board",
			lanes: subset("10003", "10004", "10005", "10006", "10007", "10008"),
			want: []wp19WantQuery{
				{"open", "board", 0, false, "filter = 9001 AND status in (10003, 10004, 10005, 10006) AND (sprint in openSprints() OR status in " + wp19P + ")" + wp19TailOB},
				{"done", "board", 0, false, "filter = 9001 AND status in " + wp19Done + " AND (updated >= -15d)" + wp19TailOB},
			},
		},
		{
			name: "scrum backlog list is just the todo statuses when nothing is backlog", kind: "scrum", scope: "board",
			lanes: subset("10002", "10003", "10007"),
			want: []wp19WantQuery{
				{"open", "board", 0, false, "filter = 9001 AND status in (10002, 10003) AND (sprint in openSprints() OR status in (10003))" + wp19TailOB},
				{"done", "board", 0, false, "filter = 9001 AND status in (10007) AND (updated >= -15d)" + wp19TailOB},
				{"backlog", "backlog", 50, true, "filter = 9001 AND status in (10002) AND (sprint is EMPTY OR sprint not in openSprints())" + wp19TailOB},
			},
		},
		{
			name: "kanban without a done column has no done query", kind: "kanban", scope: "board",
			lanes: subset("10001", "10002", "10003", "10004", "10005", "10006"),
			want: []wp19WantQuery{
				{"open", "board", 0, false, "filter = 9001 AND status in " + wp19W + wp19TailOB},
				{"backlog", "backlog", 50, true, "filter = 9001 AND status in (10001)" + wp19TailOB},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp19Guard(t)
			a := wp19PlanAdapter(t)
			got, err := a.Plan(context.Background(), source.PlanInput{
				Board: wp19Board(tc.kind, map[string]any{"filterId": 9001}), Scope: tc.scope, StatusLanes: tc.lanes, BacklogLimit: 50,
			})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			wp19AssertPlan(t, got, tc.want)
		})
	}
}

func TestPlan_ScrumBoardScope_WithOnlyTodoWorkingStatusesStaysValidJQL(t *testing.T) {
	defer wp19Guard(t)
	a := wp19PlanAdapter(t)
	got, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("scrum", map[string]any{"filterId": 9001}), Scope: "board",
		StatusLanes: map[string]string{"10002": model.LaneTodo, "10007": model.LaneDone}, BacklogLimit: 50,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got) == 0 || got[0].Name != "open" {
		t.Fatalf("queries = %+v, want an open query first", got)
	}
	for _, q := range got {
		if strings.Contains(q.Expr, "in ()") || strings.Contains(q.Expr, "OR status in )") || strings.Contains(q.Expr, "  ") {
			t.Errorf("query %s is not valid JQL: %s", q.Name, q.Expr)
		}
	}
}

func TestPlan_HiddenStatusesAppearInNoList(t *testing.T) {
	defer wp19Guard(t)
	a := wp19PlanAdapter(t)
	for _, kind := range []string{"scrum", "kanban"} {
		got, err := a.Plan(context.Background(), source.PlanInput{
			Board: wp19Board(kind, map[string]any{"filterId": 9001}), Scope: "board", StatusLanes: wp19Lanes(), BacklogLimit: 50,
		})
		if err != nil {
			t.Fatalf("%s: Plan: %v", kind, err)
		}
		for _, q := range got {
			if strings.Contains(q.Expr, "10009") {
				t.Errorf("%s/%s contains the hidden status: %s", kind, q.Name, q.Expr)
			}
		}
	}
}

func TestPlan_EmptyWorkingSetIsAValidationError(t *testing.T) {
	defer wp19Guard(t)
	onlyBacklogAndDone := map[string]string{"10001": model.LaneBacklog, "10007": model.LaneDone, "10009": model.LaneHidden}
	tests := []struct {
		name string
		in   source.PlanInput
	}{
		{"scrum sprint scope", source.PlanInput{Board: wp19Board("scrum", map[string]any{"filterId": 9001}), Scope: "sprint", ActiveSprints: wp19Sprints("42")}},
		{"scrum board scope", source.PlanInput{Board: wp19Board("scrum", map[string]any{"filterId": 9001}), Scope: "board"}},
		{"kanban", source.PlanInput{Board: wp19Board("kanban", map[string]any{"filterId": 9001}), Scope: "board"}},
		{"nil status lanes", source.PlanInput{Board: wp19Board("kanban", map[string]any{"filterId": 9001}), Scope: "board", StatusLanes: map[string]string{}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp19Guard(t)
			in := tc.in
			if in.StatusLanes == nil {
				in.StatusLanes = onlyBacklogAndDone
			}
			in.BacklogLimit = 50
			a := wp19PlanAdapter(t)
			got, err := a.Plan(context.Background(), in)
			wp19AssertCode(t, err, apperr.Validation)
			if !strings.Contains(err.Error(), "Map at least one column") {
				t.Errorf("message = %q, want it to contain \"Map at least one column\"", err.Error())
			}
			if len(got) != 0 {
				t.Errorf("queries = %+v, want none on error", got)
			}
		})
	}
}

func TestPlan_BacklogLimit(t *testing.T) {
	defer wp19Guard(t)
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"50 is passed through", 50, 50},
		{"a smaller limit is respected", 25, 25},
		{"zero means the default of 50", 0, 50},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp19Guard(t)
			a := wp19PlanAdapter(t)
			got, err := a.Plan(context.Background(), source.PlanInput{
				Board: wp19Board("scrum", map[string]any{"filterId": 9001}), Scope: "board", StatusLanes: wp19Lanes(), BacklogLimit: tc.limit,
			})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			found := false
			for _, q := range got {
				if q.Name == "backlog" {
					found = true
					if q.Limit != tc.want || !q.Count || q.Role != "backlog" {
						t.Errorf("backlog = {Limit:%d Count:%v Role:%s}, want {%d true backlog}", q.Limit, q.Count, q.Role, tc.want)
					}
				} else if q.Limit != 0 || q.Count {
					t.Errorf("%s has Limit %d Count %v, want 0/false", q.Name, q.Limit, q.Count)
				}
			}
			if !found {
				t.Fatal("no backlog query")
			}
		})
	}
}

func TestPlan_FilterIdIsRenderedAsAPlainIntegerForEveryMetaType(t *testing.T) {
	defer wp19Guard(t)
	var fromJSON struct{ FilterID any }
	if err := json.Unmarshal([]byte(`{"FilterID":10234567}`), &fromJSON); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		v    any
	}{
		{"int", 10234567},
		{"int64", int64(10234567)},
		{"float64 from a JSON round trip", fromJSON.FilterID},
		{"string", "10234567"},
		{"json.Number", json.Number("10234567")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp19Guard(t)
			a := wp19PlanAdapter(t)
			got, err := a.Plan(context.Background(), source.PlanInput{
				Board: wp19Board("kanban", map[string]any{"filterId": tc.v}), Scope: "board", StatusLanes: wp19Lanes(), BacklogLimit: 50,
			})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if len(got) == 0 {
				t.Fatal("no queries")
			}
			if !strings.HasPrefix(got[0].Expr, "filter = 10234567 AND ") {
				t.Fatalf("Expr = %q, want it to start with `filter = 10234567 AND `", got[0].Expr)
			}
		})
	}
}

func TestPlan_MissingFilterIdIsAValidationError(t *testing.T) {
	defer wp19Guard(t)
	a := wp19PlanAdapter(t)
	_, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("kanban", map[string]any{}), Scope: "board", StatusLanes: wp19Lanes(), BacklogLimit: 50,
	})
	wp19AssertCode(t, err, apperr.Validation)
}

func TestPlan_MakesNoRequests(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{}) // any request is an error
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	if _, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("scrum", map[string]any{"filterId": 9001}), Scope: "board", ActiveSprints: wp19Sprints("42"),
		StatusLanes: wp19Lanes(), BacklogLimit: 50,
	}); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if n := len(srv.all()); n != 0 {
		t.Fatalf("requests = %d, want 0", n)
	}
}
