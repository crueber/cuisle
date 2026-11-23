package history_test

// SPEC-NOTE (WP-22 history):
//  1. LoadAsOf returns an *apperr.Error with code not_implemented (v1 stub). It is called with a nil *store.DB: the stub
//     must not touch the database.
//  2. Reconstruct follows data-model.md Q8 exactly:
//     - membership: valid_from <= t < valid_to (valid_from inclusive, valid_to exclusive; NULL = open); an issue with two
//       spans appears once at an instant covered by one span and is absent in the gap between them;
//     - status as of t: the latest status_change with changed_at <= t (to_status_id); before the first change, the first
//       change's from_status_id; with no changes, issue.status_id;
//     - assignee as of t (account id; "" = unassigned): latest assignee_change with changed_at <= t (to_account_id, a NULL
//       to means unassigned); before the first change, the first change's from_account_id; with no changes at all,
//       issue.assignee_account_id;
//     - lane: the statusLanes entry for the status in the standup_config_history row with valid_from <= t < valid_to
//       (NULL open), so the same status maps to different lanes at different instants; an unmapped status, or no
//       config-history row, gives "";
//     - rows are sorted by Key (string order), NOT by rank; other standups' spans never leak in; before the standup's
//       first span the result is empty and nil error.

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/history"
	"github.com/crueber/cuisle/internal/store/storetest"
)

func TestLoadAsOf_ReturnsNotImplemented(t *testing.T) {
	_, err := history.LoadAsOf(context.Background(), nil, 1, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("LoadAsOf returned nil error; v1 ships the stub")
	}
	if !apperr.Is(err, apperr.NotImplementedCode) {
		t.Fatalf("code = %q, want not_implemented (err=%v)", apperr.CodeOf(err), err)
	}
}

// ---- seed ------------------------------------------------------------------

// t0 is "day 0, 09:00 UTC"; at(d, h) is d days and h hours later. Spans, changes and config rows below sit on these instants.
var t0 = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func at(day, hour int) time.Time {
	return t0.Add(time.Duration(day)*24*time.Hour + time.Duration(hour)*time.Hour)
}

func ms(day, hour int) int64 { return at(day, hour).UnixMilli() }

const (
	insConnection = `INSERT INTO connection(id,source,base_url,account,created_at) VALUES(1,'jira','https://acme.example','me',1)`
	insStandup    = `INSERT INTO standup(id,connection_id,name,position,created_at,updated_at) VALUES(?,1,?,?,1,1)`
	insIssue      = `INSERT INTO issue(id,connection_id,source_id,key,space_id,summary,issue_type_id,status_id,assignee_account_id,rank,
		created_at,updated_at,hydrated_at,last_seen_at) VALUES(?,1,?,?,'10000','summary','1',?,?,?,1,1,1,1)`
	insStatusChange   = `INSERT INTO status_change(issue_id,history_id,changed_at,from_status_id,to_status_id) VALUES(?,?,?,?,?)`
	insAssigneeChange = `INSERT INTO assignee_change(issue_id,history_id,changed_at,from_account_id,to_account_id) VALUES(?,?,?,?,?)`
	insSpan           = `INSERT INTO standup_issue_span(standup_id,issue_id,role,valid_from,valid_to) VALUES(?,?,?,?,?)`
	insConfig         = `INSERT INTO standup_config_history(standup_id,valid_from,valid_to,fingerprint,config_json) VALUES(?,?,?,?,?)`
)

func cfgJSON(reviewLane string) string {
	return fmt.Sprintf(`{"scope":"sprint","statusLanes":[`+
		`{"statusId":"10","statusName":"To Do","columnName":"To Do","lane":"todo"},`+
		`{"statusId":"20","statusName":"In Progress","columnName":"Doing","lane":"prog"},`+
		`{"statusId":"30","statusName":"In Review","columnName":"Review","lane":%q},`+
		`{"statusId":"40","statusName":"Done","columnName":"Done","lane":"done"}]}`, reviewLane)
}

// seedAll builds the scenario (expectations in TestReconstruct_AsOfInstants):
//
//	standup 1: MOB-1 board span day1..day8; status 10->20 (d2) ->30 (d4) ->40 (d7); assignee NULL->alice (d3) ->bob (d6) ->NULL (d7.12h)
//	           MOB-2 board spans day1..day3 and day6..open; current assignee carol, no change rows, status 10
//	           MOB-3 BACKLOG span day1..open; status 99 (unmapped), unassigned
//	           config history: day1..day5 maps 30 -> review; day5..open maps 30 -> qa
//	standup 2: MOB-1 board span day1..open (no config history), used to prove isolation.
//
// Issues are inserted in reverse key order and with ranks that disagree with key order.
func seedAll(exec func(sql string, args ...any)) {
	exec(insConnection)
	exec(insStandup, 1, "Alpha", 1)
	exec(insStandup, 2, "Beta", 2)

	// issue id, source id, key, status_id, assignee, rank
	exec(insIssue, 1, "s3", "MOB-3", "99", nil, "a")
	exec(insIssue, 2, "s2", "MOB-2", "10", "carol", "b")
	exec(insIssue, 3, "s1", "MOB-1", "40", nil, "c")

	exec(insStatusChange, 3, "h1", ms(2, 0), "10", "20")
	exec(insStatusChange, 3, "h2", ms(4, 0), "20", "30")
	exec(insStatusChange, 3, "h3", ms(7, 0), "30", "40")
	exec(insAssigneeChange, 3, "h1", ms(3, 0), nil, "alice")
	exec(insAssigneeChange, 3, "h2", ms(6, 0), "alice", "bob")
	exec(insAssigneeChange, 3, "h3", ms(7, 12), "bob", nil)

	exec(insSpan, 1, 3, "board", ms(1, 0), ms(8, 0))
	exec(insSpan, 1, 2, "board", ms(1, 0), ms(3, 0))
	exec(insSpan, 1, 2, "board", ms(6, 0), nil)
	exec(insSpan, 1, 1, "backlog", ms(1, 0), nil)
	exec(insSpan, 2, 3, "board", ms(1, 0), nil)

	exec(insConfig, 1, ms(1, 0), ms(5, 0), "fp1", cfgJSON("review"))
	exec(insConfig, 1, ms(5, 0), nil, "fp2", cfgJSON("qa"))
}

func seeded(t *testing.T) (ctx context.Context, reconstruct func(standup int64, at time.Time) ([]history.MemberAt, error)) {
	t.Helper()
	db := storetest.Open(t, nil)
	seedAll(func(sql string, args ...any) { storetest.Exec(t, db, sql, args...) })
	ctx = context.Background()
	return ctx, func(standup int64, when time.Time) ([]history.MemberAt, error) {
		return history.Reconstruct(ctx, db, standup, when)
	}
}

func m(key, role, lane, assignee string) history.MemberAt {
	return history.MemberAt{Key: key, Role: role, Lane: lane, Assignee: assignee}
}

func TestReconstruct_AsOfInstants(t *testing.T) {
	_, rec := seeded(t)
	cases := []struct {
		name string
		when time.Time
		want []history.MemberAt
	}{
		{"before the first sync is empty", at(0, 12), nil},
		{"valid_from is inclusive; before first changes uses from-values", at(1, 0), []history.MemberAt{
			m("MOB-1", "board", "todo", ""), // status before first change = from_status 10; assignee = first change's from (NULL)
			m("MOB-2", "board", "todo", "carol"),
			m("MOB-3", "backlog", "", ""), // unmapped status 99 -> no lane
		}},
		{"status 20 -> prog, still unassigned", at(2, 12), []history.MemberAt{
			m("MOB-1", "board", "prog", ""),
			m("MOB-2", "board", "todo", "carol"),
			m("MOB-3", "backlog", "", ""),
		}},
		{"span closed at valid_to (exclusive); change at t is inclusive", at(3, 0), []history.MemberAt{
			m("MOB-1", "board", "prog", "alice"),
			m("MOB-3", "backlog", "", ""),
		}},
		{"status 30 maps to review under the first config", at(4, 12), []history.MemberAt{
			m("MOB-1", "board", "review", "alice"),
			m("MOB-3", "backlog", "", ""),
		}},
		{"same status 30 maps to qa under the later config", at(5, 6), []history.MemberAt{
			m("MOB-1", "board", "qa", "alice"),
			m("MOB-3", "backlog", "", ""),
		}},
		{"config boundary: valid_from of the second row is inclusive", at(5, 0), []history.MemberAt{
			m("MOB-1", "board", "qa", "alice"),
			m("MOB-3", "backlog", "", ""),
		}},
		{"reopened span counted once, reassigned to bob", at(6, 12), []history.MemberAt{
			m("MOB-1", "board", "qa", "bob"),
			m("MOB-2", "board", "todo", "carol"),
			m("MOB-3", "backlog", "", ""),
		}},
		{"done lane", at(7, 6), []history.MemberAt{
			m("MOB-1", "board", "done", "bob"),
			m("MOB-2", "board", "todo", "carol"),
			m("MOB-3", "backlog", "", ""),
		}},
		{"assignee set back to unassigned (NULL to_account_id)", at(7, 18), []history.MemberAt{
			m("MOB-1", "board", "done", ""),
			m("MOB-2", "board", "todo", "carol"),
			m("MOB-3", "backlog", "", ""),
		}},
		{"span closed at its valid_to instant", at(8, 0), []history.MemberAt{
			m("MOB-2", "board", "todo", "carol"),
			m("MOB-3", "backlog", "", ""),
		}},
		{"long after", at(40, 0), []history.MemberAt{
			m("MOB-2", "board", "todo", "carol"),
			m("MOB-3", "backlog", "", ""),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := rec(1, tc.when)
			if err != nil {
				t.Fatalf("Reconstruct: %v", err)
			}
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("as of %s\n got  %+v\n want %+v", tc.when.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

func TestReconstruct_GapBetweenSpans_IssueAbsent(t *testing.T) {
	_, rec := seeded(t)
	// MOB-2 left at day 3 and came back at day 6.
	for _, when := range []time.Time{at(3, 0), at(4, 12), at(5, 23), at(5, 0)} {
		got, err := rec(1, when)
		if err != nil {
			t.Fatal(err)
		}
		for _, mem := range got {
			if mem.Key == "MOB-2" {
				t.Errorf("MOB-2 present at %s inside the gap between its spans", when.Format(time.RFC3339))
			}
		}
	}
	got, _ := rec(1, at(6, 0)) // reopened span: valid_from inclusive
	count := 0
	for _, mem := range got {
		if mem.Key == "MOB-2" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("MOB-2 appears %d times at the reopen instant, want 1", count)
	}
}

func TestReconstruct_SortedByKeyNotRank(t *testing.T) {
	_, rec := seeded(t)
	got, err := rec(1, at(6, 12))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, mem := range got {
		keys = append(keys, mem.Key)
	}
	// ranks are MOB-3 "a" < MOB-2 "b" < MOB-1 "c": rank order would reverse this.
	if !reflect.DeepEqual(keys, []string{"MOB-1", "MOB-2", "MOB-3"}) {
		t.Errorf("keys = %v, want key order", keys)
	}
}

func TestReconstruct_OtherStandupDoesNotLeak(t *testing.T) {
	_, rec := seeded(t)
	got, err := rec(2, at(40, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "MOB-1" || got[0].Role != "board" {
		t.Fatalf("standup 2 members = %+v, want only MOB-1 on the board", got)
	}
	// standup 2 has no config history: its lane is unmapped, and the status/assignee still come from the change tables.
	if got[0].Lane != "" || got[0].Assignee != "" {
		t.Errorf("standup 2 MOB-1 = %+v, want lane \"\" (no config history) and unassigned (last change set NULL)", got[0])
	}

	got1, _ := rec(1, at(40, 0))
	for _, mem := range got1 {
		if mem.Key == "MOB-1" {
			t.Error("standup 1 must not see MOB-1 (its span closed on day 8), even though standup 2 holds it")
		}
	}
}

func TestReconstruct_UnknownStandup_EmptyNoError(t *testing.T) {
	_, rec := seeded(t)
	got, err := rec(999, at(6, 12))
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v; want empty and nil error", got, err)
	}
}
