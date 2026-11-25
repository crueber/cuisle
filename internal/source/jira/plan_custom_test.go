package jira

import (
	"context"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/source"
)

// Custom lanes (a lane the user named, id c<N>) are working lanes past To Do: their statuses are fetched by the open
// query on every board kind and scope, and count as "in progress" for the scrum board-scope OR clause.
func TestPlan_CustomLaneStatusesAreWorkingAndInProgress(t *testing.T) {
	defer wp19Guard(t)
	a := wp19PlanAdapter(t)
	lanes := wp19Lanes()
	lanes["10010"] = "c1"
	for _, c := range []struct{ kind, scope, query, want string }{
		{"kanban", "board", "open", "status in (10002, 10003, 10004, 10005, 10006, 10010)"},
		{"scrum", "board", "open", "OR status in (10003, 10004, 10005, 10006, 10010))"},
	} {
		got, err := a.Plan(context.Background(), source.PlanInput{
			Board: wp19Board(c.kind, map[string]any{"filterId": 9001}), Scope: c.scope, StatusLanes: lanes, BacklogLimit: 50,
		})
		if err != nil {
			t.Fatalf("%s/%s: %v", c.kind, c.scope, err)
		}
		found := false
		for _, q := range got {
			if q.Name == c.query {
				found = true
				if !strings.Contains(q.Expr, c.want) {
					t.Errorf("%s/%s %s = %q, want it to contain %q", c.kind, c.scope, c.query, q.Expr, c.want)
				}
			}
		}
		if !found {
			t.Errorf("%s/%s: no %s query", c.kind, c.scope, c.query)
		}
	}
	// A board whose only working column is a custom lane can still load.
	only := map[string]string{"10010": "c1", "10007": "done"}
	if _, err := a.Plan(context.Background(), source.PlanInput{
		Board: wp19Board("kanban", map[string]any{"filterId": 9001}), Scope: "board", StatusLanes: only,
	}); err != nil {
		t.Errorf("custom lane only: %v", err)
	}
}
