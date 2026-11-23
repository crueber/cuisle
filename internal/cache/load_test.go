package cache_test

// Tests for the read side of internal/cache (WP-20): LoadCurrentTx, Tabs, GetSyncState, IssueStamps and
// MemberSourceIDs (data-model.md §2.5–2.7, §4, §4.1 Q1–Q6; jira-sync.md §5.6).
//
// The shared helpers of the WP-20 test files (prefix "rd") live here. They only use database/sql-level seeding
// through storetest, never a cache.* function, so a failure points at the behaviour under test.
//
// SPEC-NOTE: pulse window start = local midnight, in asOf.Location(), of (asOf's local date − (PulseDays−1) days)
// (data-model §4: ":win_start_ms = local midnight of today − (pulse_days − 1)"). It is computed with calendar
// arithmetic, not 24h multiples, so it crosses the America/Chicago DST change of 2026-03-08 correctly. An event exactly
// at the window start is included (≥); one a second earlier is not.
// SPEC-NOTE: History.Dev includes merge commits with Merge=true. Q3 literally says "d.is_merge = 0", but model.DevEvent
// has a Merge flag and pulse.Marks is documented as taking "non-merge commits", which only makes sense if merges
// reach it. So the loader keeps them (and the view/pulse drops them). History.Dev is ordered oldest first across kinds.
// SPEC-NOTE: History.Comments includes comments by "app" people (pulse.Marks filters them using People[…].Kind) and
// a NULL author comes back as AuthorID "". Status and flag changes are never window-bounded (Q2, Q3's last line).
// SPEC-NOTE: AssigneeChanges and the History entry of a member with no events are deliberately not asserted.
// SPEC-NOTE: BacklogRows is not applied by the loader (the view takes "the first backlog_rows", jira-sync §5.6); the
// tests use BacklogRows 12 with 2 backlog members so either reading passes.
// SPEC-NOTE: Warnings come from the newest run of THIS standup whose status is not 'running'.
// SPEC-NOTE: Tabs for a standup with no standup_sync_state row: NeedsSync is not asserted (Q6's LEFT JOIN leaves it
// NULL); the tests give such rows an explicit state where they assert it.
// SPEC-NOTE: the helpers prefixed "rd" are shared by load/connection/runs/reference tests; WP-30's apply tests live
// in the same package, so avoid that prefix there.

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
	"github.com/crueber/cuisle/internal/store/storetest"
)

// ---- shared helpers -------------------------------------------------------------------------------------------

var rdCtx = context.Background()

// rdNow is the injected "now" of every test DB (and the Trim reference time).
var rdNow = time.Date(2026, 3, 18, 20, 0, 0, 0, time.UTC)

func rdClock() time.Time { return rdNow }

// rdRecover turns a panicking Wave 0 stub (or storetest stub) into a per-test failure.
func rdRecover(t *testing.T) {
	if r := recover(); r != nil {
		t.Fatalf("panic (stub not implemented?): %v", r)
	}
}

func rdTS(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v
}

func rdMs(s string) int64 { return rdTS(s).UnixMilli() }

func rdChicago(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return loc
}

// rdAsOf is 2026-03-18 15:00 CDT (= 20:00Z), a Wednesday, 10 days after the DST change.
func rdAsOf(t *testing.T) time.Time { return rdNow.In(rdChicago(t)) }

func rdOpen(t *testing.T) *store.DB {
	t.Helper()
	return storetest.Open(t, rdClock)
}

func rdExec(t *testing.T, db *store.DB, q string, args ...any) {
	t.Helper()
	storetest.Exec(t, db, q, args...)
}

func rdInsert(t *testing.T, db *store.DB, table, cols string, vals ...any) {
	t.Helper()
	ph := strings.TrimSuffix(strings.Repeat("?,", len(vals)), ",")
	rdExec(t, db, "INSERT INTO "+table+" ("+cols+") VALUES ("+ph+")", vals...)
}

func rdSame(t *testing.T, what string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func rdZero(t *testing.T, what string, got time.Time) {
	t.Helper()
	if !got.IsZero() {
		t.Errorf("%s = %v, want the zero time", what, got)
	}
}

func rdCount(t *testing.T, db *store.DB, table, where string, args ...any) int {
	t.Helper()
	return storetest.Count(t, db, table, where, args...)
}

func rdChecksum(t *testing.T, db *store.DB, table string) string {
	t.Helper()
	return storetest.Checksum(t, db, table)
}

// Standup ids of the fixture.
const (
	rdSA = 1 // "Platform": configured, the standup the loader tests read
	rdSB = 2 // "Draft": no board yet, position 0 (so it is the first tab)
	rdSD = 3 // archived (deleted_at set)
)

// rdSeedBase inserts the connection and three standups with their config tables and sync state.
func rdSeedBase(t *testing.T, db *store.DB) {
	t.Helper()
	rdInsert(t, db, "connection",
		"id,source,base_url,account,account_id,display_name,auth_state,verified_at,source_meta,dev_activity_state,dev_activity_checked_at,created_at",
		1, "jira", "https://acme.atlassian.net", "chris@example.com", "acc-chris", "Chris", "ok", rdMs("2026-03-01T10:00:00Z"),
		`{"flaggedFieldId":"customfield_10021"}`, "ok", rdMs("2026-03-18T18:00:00Z"), rdMs("2026-02-01T10:00:00Z"))

	st := "id,connection_id,name,position,space_id,space_key,space_name,board_id,board_name,board_type,scope,rev,created_at,updated_at,deleted_at"
	c := rdMs("2026-02-01T10:00:00Z")
	rdInsert(t, db, "standup", st, rdSA, 1, "Platform", 1, "100", "MOB", "Service", "77", "MOB board", "scrum", "sprint", 4, c, c, nil)
	rdInsert(t, db, "standup", st, rdSB, 1, "Draft", 0, "100", "MOB", "Service", nil, nil, nil, "sprint", 1, c, c, nil)
	rdInsert(t, db, "standup", st, rdSD, 1, "Old team", 2, "100", "MOB", "Service", "77", "MOB board", "scrum", "sprint", 2, c, c, rdMs("2026-03-10T10:00:00Z"))

	rdInsert(t, db, "standup_settings", "standup_id", rdSA)
	rdInsert(t, db, "standup_settings", "standup_id", rdSD)
	for i, l := range []string{"qa", "review", "prog", "todo"} {
		rdInsert(t, db, "standup_lane", "standup_id,lane,walk_position,limit_days", rdSA, l, i, []int{3, 2, 5, 10}[i])
	}

	cols := []struct {
		name string
		lane string
	}{{"Backlog", "backlog"}, {"To Do", "todo"}, {"In Progress", "prog"}, {"Code Review", "review"}, {"QA", "qa"}, {"Done", "done"}, {"Parked", "hidden"}}
	for i, c := range cols {
		rdInsert(t, db, "standup_column_map", "standup_id,column_name,position,lane,auto", rdSA, c.name, i, c.lane, 1)
	}
	// status id → (name, column, lane). Status 18 is deliberately in no column.
	for _, s := range [][4]string{
		{"10", "Open", "Backlog", "backlog"},
		{"11", "To Do", "To Do", "todo"},
		{"12", "In Progress", "In Progress", "prog"},
		{"13", "Blocked", "In Progress", "prog"},
		{"14", "In Review", "Code Review", "review"},
		{"15", "In QA", "QA", "qa"},
		{"16", "Done", "Done", "done"},
		{"17", "Parked", "Parked", "hidden"},
	} {
		rdInsert(t, db, "standup_status_lane", "standup_id,status_id,status_name,column_name,lane", rdSA, s[0], s[1], s[2], s[3])
	}

	sp := "standup_id,account_id,display_name,position,included,added_manually,user_touched,created_at"
	rdInsert(t, db, "standup_speaker", sp, rdSA, "acc-ann", "Ann Lee", 0, 1, 0, 0, c)
	rdInsert(t, db, "standup_speaker", sp, rdSA, "acc-bob", "Bob Ray", 1, 1, 0, 0, c)
	rdInsert(t, db, "standup_speaker", sp, rdSA, "acc-carol", "Carol Fox", 2, 0, 0, 1, c)

	ss := "standup_id,needs_sync,last_attempt_at,last_success_at,last_full_at,history_from,backlog_total,active_sprint_ids"
	rdInsert(t, db, "standup_sync_state", ss, rdSA, 0, rdMs("2026-03-18T19:00:00Z"), rdMs("2026-03-18T18:30:00Z"),
		rdMs("2026-03-10T09:00:00Z"), rdMs("2026-03-01T09:00:00Z"), 37, `["S42"]`)
	rdInsert(t, db, "standup_sync_state", ss, rdSB, 1, nil, nil, nil, nil, -1, `[]`)
}

// rdIssue inserts one issue row. assignee/resolved/dev* may be nil.
func rdIssue(t *testing.T, db *store.DB, id int64, src, key, typ, status string, assignee any, flagged int, rank, parent, sprints, created, updated string, resolved any) {
	t.Helper()
	var res any
	if s, ok := resolved.(string); ok {
		res = rdMs(s)
	}
	var par any
	if parent != "" {
		par = parent
	}
	rdInsert(t, db, "issue",
		"id,connection_id,source_id,key,space_id,summary,issue_type_id,status_id,assignee_account_id,flagged,rank,parent_key,sprint_ids,created_at,updated_at,resolved_at,hydrated_at,last_seen_at",
		id, 1, src, key, "100", "Summary of "+key, typ, status, assignee, flagged, rank, par, sprints,
		rdMs(created), rdMs(updated), res, rdMs("2026-03-18T18:30:00Z"), rdMs("2026-03-18T18:30:00Z"))
}

// rdSeedRecords inserts reference rows, issues, membership, spans, config history and every kind of history row.
//
//	local id  key     status (lane)        board rank_index   notes
//	1         MOB-1   12 In Progress       2                  ann, sprints S42+S41, parent MOB-100, dev fp
//	2         MOB-2   11 To Do             0                  unassigned, bug
//	3         MOB-3   13 Blocked (prog)    3                  ann, flagged
//	4         MOB-4   16 Done              1                  bob, resolved, sub-task
//	5         MOB-5   17 Parked (hidden)   4                  not shown
//	6         MOB-6   18 (no column)       5                  not shown
//	7         MOB-7   14 In Review         6                  bob
//	9         MOB-9   10 Open              backlog 0          (issue.rank sorts it AFTER MOB-8)
//	8         MOB-8   10 Open              backlog 1
//	10        MOB-10  12                   standup B only
//	11        MOB-11  12                   in no standup now (removed_at set)
//
// issue.rank tokens are ordered opposite to rank_index so that ordering by rank instead of rank_index fails.
func rdSeedRecords(t *testing.T, db *store.DB) {
	t.Helper()
	f := rdMs("2026-03-18T18:30:00Z")

	for _, s := range []struct {
		id, name, cat string
		hint          int
	}{
		{"10", "Open", "new", 0}, {"11", "To Do", "new", 0}, {"12", "In Progress", "indeterminate", 0},
		{"13", "Blocked", "indeterminate", 1}, {"14", "In Review", "indeterminate", 0}, {"15", "In QA", "indeterminate", 0},
		{"16", "Done", "done", 0}, {"17", "Parked", "indeterminate", 0}, {"18", "Orphan", "new", 0},
	} {
		rdInsert(t, db, "status", "connection_id,id,name,category,blocked_hint", 1, s.id, s.name, s.cat, s.hint)
	}
	for _, ty := range [][3]string{{"T1", "Story", "story"}, {"T2", "Bug", "bug"}, {"T3", "Sub-task", "subtask"}} {
		rdInsert(t, db, "issue_type", "connection_id,id,name,kind", 1, ty[0], ty[1], ty[2])
	}
	pc := "connection_id,account_id,display_name,active,account_type,fetched_at"
	rdInsert(t, db, "person", pc, 1, "acc-ann", "Ann Lee", 1, "user", f)
	rdInsert(t, db, "person", pc, 1, "acc-bob", "Bob Ray", 1, "user", f)
	rdInsert(t, db, "person", pc, 1, "acc-bot", "Deploy Bot", 1, "app", f)
	rdInsert(t, db, "person", pc, 1, "acc-zed", "Zed Old", 0, "user", f)

	sc := "connection_id,id,board_id,name,state,start_at,end_at,complete_at,fetched_at"
	rdInsert(t, db, "sprint", sc, 1, "S42", "77", "Sprint 42", "active", rdMs("2026-03-09T14:00:00Z"), rdMs("2026-03-23T14:00:00Z"), nil, f)
	rdInsert(t, db, "sprint", sc, 1, "S41", "77", "Sprint 41", "closed", rdMs("2026-02-23T14:00:00Z"), rdMs("2026-03-09T14:00:00Z"), rdMs("2026-03-09T13:00:00Z"), f)
	// active in the sprint table but NOT in standup_sync_state.active_sprint_ids: must be excluded.
	rdInsert(t, db, "sprint", sc, 1, "S43", "77", "Sprint 43", "active", rdMs("2026-03-09T14:00:00Z"), rdMs("2026-03-23T14:00:00Z"), nil, f)

	rdIssue(t, db, 1, "9001", "MOB-1", "T1", "12", "acc-ann", 0, "x", "MOB-100", `["S42","S41"]`, "2026-02-20T09:00:00Z", "2026-03-17T12:00:00Z", nil)
	rdExec(t, db, "UPDATE issue SET dev_fingerprint='fp-1', dev_checked_at=? WHERE id=1", rdMs("2026-03-17T12:05:00Z"))
	rdIssue(t, db, 2, "9002", "MOB-2", "T2", "11", nil, 0, "z", "", `[]`, "2026-03-02T09:00:00Z", "2026-03-10T12:00:00Z", nil)
	rdIssue(t, db, 3, "9003", "MOB-3", "T1", "13", "acc-ann", 1, "w", "", `["S42"]`, "2026-03-03T09:00:00Z", "2026-03-17T09:00:00Z", nil)
	rdIssue(t, db, 4, "9004", "MOB-4", "T3", "16", "acc-bob", 0, "y", "", `["S42"]`, "2026-03-01T09:00:00Z", "2026-03-16T16:00:00Z", "2026-03-16T16:00:00Z")
	rdIssue(t, db, 5, "9005", "MOB-5", "T1", "17", "acc-bob", 0, "u", "", `[]`, "2026-03-01T09:00:00Z", "2026-03-12T16:00:00Z", nil)
	rdIssue(t, db, 6, "9006", "MOB-6", "T1", "18", nil, 0, "t", "", `[]`, "2026-03-01T09:00:00Z", "2026-03-12T16:00:00Z", nil)
	rdIssue(t, db, 7, "9007", "MOB-7", "T1", "14", "acc-bob", 0, "v", "", `["S42"]`, "2026-03-01T09:00:00Z", "2026-03-17T08:00:00Z", nil)
	rdIssue(t, db, 8, "9008", "MOB-8", "T1", "10", nil, 0, "a", "", `[]`, "2026-03-01T09:00:00Z", "2026-03-12T16:00:00Z", nil)
	rdIssue(t, db, 9, "9009", "MOB-9", "T2", "10", nil, 0, "b", "", `[]`, "2026-03-01T09:00:00Z", "2026-03-12T16:00:00Z", nil)
	rdIssue(t, db, 10, "9010", "MOB-10", "T1", "12", "acc-ann", 0, "s", "", `[]`, "2026-03-01T09:00:00Z", "2026-03-12T16:00:00Z", nil)
	rdIssue(t, db, 11, "9011", "MOB-11", "T1", "12", "acc-ann", 0, "r", "", `[]`, "2026-03-01T09:00:00Z", "2026-03-12T16:00:00Z", nil)
	rdExec(t, db, "UPDATE issue SET removed_at=? WHERE id=11", rdMs("2026-03-14T09:00:00Z"))

	for _, m := range []struct {
		standup, issue int64
		role           string
		rank           int
	}{
		{rdSA, 2, "board", 0}, {rdSA, 4, "board", 1}, {rdSA, 1, "board", 2}, {rdSA, 3, "board", 3}, {rdSA, 5, "board", 4},
		{rdSA, 6, "board", 5}, {rdSA, 7, "board", 6}, {rdSA, 9, "backlog", 0}, {rdSA, 8, "backlog", 1}, {rdSB, 10, "board", 0},
	} {
		rdInsert(t, db, "standup_issue", "standup_id,issue_id,role,rank_index", m.standup, m.issue, m.role, m.rank)
		rdInsert(t, db, "standup_issue_span", "standup_id,issue_id,role,valid_from,valid_to", m.standup, m.issue, m.role, rdMs("2026-03-01T09:00:00Z"), nil)
	}
	// issue 11 left standup A on 03-14; the archived standup D once held issue 1.
	rdInsert(t, db, "standup_issue_span", "standup_id,issue_id,role,valid_from,valid_to", rdSA, 11, "board", rdMs("2026-03-01T09:00:00Z"), rdMs("2026-03-14T09:00:00Z"))
	rdInsert(t, db, "standup_issue_span", "standup_id,issue_id,role,valid_from,valid_to", rdSD, 1, "board", rdMs("2026-03-01T09:00:00Z"), nil)

	ch := "standup_id,valid_from,valid_to,fingerprint,config_json"
	rdInsert(t, db, "standup_config_history", ch, rdSA, rdMs("2026-03-01T09:00:00Z"), rdMs("2026-03-10T09:00:00Z"), "fp-a", `{"scope":"sprint"}`)
	rdInsert(t, db, "standup_config_history", ch, rdSA, rdMs("2026-03-10T09:00:00Z"), nil, "fp-b", `{"scope":"sprint","pulseDays":21}`)
	rdInsert(t, db, "standup_config_history", ch, rdSD, rdMs("2026-03-01T09:00:00Z"), nil, "fp-d", `{"scope":"sprint"}`)

	// status changes (inserted newest first to prove ordering)
	sch := "issue_id,history_id,changed_at,from_status_id,to_status_id"
	rdInsert(t, db, "status_change", sch, 1, "h2", rdMs("2026-03-05T14:00:00Z"), "11", "12")
	rdInsert(t, db, "status_change", sch, 1, "h1", rdMs("2026-02-01T10:00:00Z"), "10", "11")
	rdInsert(t, db, "status_change", sch, 3, "h1", rdMs("2026-03-17T09:00:00Z"), "12", "13")
	rdInsert(t, db, "status_change", sch, 3, "h0", rdMs("2026-03-03T09:00:00Z"), "11", "12")
	rdInsert(t, db, "status_change", sch, 4, "h1", rdMs("2026-03-16T16:00:00Z"), "12", "16")
	rdInsert(t, db, "status_change", sch, 4, "h0", rdMs("2026-03-01T10:00:00Z"), "11", "12")
	rdInsert(t, db, "status_change", sch, 7, "h1", rdMs("2026-03-10T10:00:00Z"), "12", "14")
	rdInsert(t, db, "status_change", sch, 10, "h1", rdMs("2026-03-16T10:00:00Z"), "11", "12")
	rdInsert(t, db, "status_change", sch, 11, "h1", rdMs("2026-03-16T10:00:00Z"), "11", "12")

	fch := "issue_id,history_id,changed_at,flagged"
	rdInsert(t, db, "flag_change", fch, 3, "f3", rdMs("2026-03-17T09:00:00Z"), 1)
	rdInsert(t, db, "flag_change", fch, 3, "f1", rdMs("2026-03-01T09:00:00Z"), 1)
	rdInsert(t, db, "flag_change", fch, 3, "f2", rdMs("2026-03-02T09:00:00Z"), 0)
	rdInsert(t, db, "flag_change", fch, 10, "f1", rdMs("2026-03-16T09:00:00Z"), 1)

	cm := "issue_id,comment_id,author_account_id,created_at"
	rdInsert(t, db, "comment", cm, 1, "c00", "acc-ann", rdMs("2026-02-19T05:30:00Z")) // 28d: naive 24h*27 window would include it
	rdInsert(t, db, "comment", cm, 1, "c0", "acc-ann", rdMs("2026-03-01T10:00:00Z"))
	rdInsert(t, db, "comment", cm, 1, "c1", "acc-ann", rdMs("2026-03-05T05:30:00Z")) // 14d: naive 24h*13 window would include it
	rdInsert(t, db, "comment", cm, 1, "c2", "acc-ann", rdMs("2026-03-05T06:00:00Z")) // exactly the 14d window start
	rdInsert(t, db, "comment", cm, 1, "c3", "acc-ann", rdMs("2026-03-12T04:59:59Z")) // one second before the 7d window start
	rdInsert(t, db, "comment", cm, 1, "c4", "acc-ann", rdMs("2026-03-12T05:00:00Z")) // exactly the 7d window start
	rdInsert(t, db, "comment", cm, 1, "c5", "acc-bot", rdMs("2026-03-16T10:00:00Z"))
	rdInsert(t, db, "comment", cm, 1, "c6", nil, rdMs("2026-03-17T10:00:00Z"))
	rdInsert(t, db, "comment", cm, 4, "k1", "acc-bob", rdMs("2026-03-15T10:00:00Z"))
	rdInsert(t, db, "comment", cm, 10, "x1", "acc-ann", rdMs("2026-03-16T10:00:00Z"))
	rdInsert(t, db, "comment", cm, 11, "x2", "acc-ann", rdMs("2026-03-16T10:00:00Z"))

	dc := "issue_id,commit_id,repo,authored_at,is_merge"
	rdInsert(t, db, "dev_commit", dc, 1, "aaa", "svc-api", rdMs("2026-03-10T12:00:00Z"), 0)
	rdInsert(t, db, "dev_commit", dc, 1, "bbb", "svc-api", rdMs("2026-03-13T12:00:00Z"), 0)
	rdInsert(t, db, "dev_commit", dc, 1, "ccc", "svc-api", rdMs("2026-03-15T12:00:00Z"), 1)
	rdInsert(t, db, "dev_commit", dc, 7, "ddd", "svc-web", rdMs("2026-03-17T08:00:00Z"), 0)
	rdInsert(t, db, "dev_commit", dc, 10, "eee", "svc-web", rdMs("2026-03-17T08:00:00Z"), 0)

	pr := "issue_id,pr_key,status,at"
	rdInsert(t, db, "dev_pr_event", pr, 1, "svc-api#6", "MERGED", rdMs("2026-03-01T00:00:00Z"))
	rdInsert(t, db, "dev_pr_event", pr, 1, "svc-api#7", "OPEN", rdMs("2026-03-14T10:00:00Z"))
	rdInsert(t, db, "dev_pr_event", pr, 1, "svc-api#7", "MERGED", rdMs("2026-03-16T10:00:00Z"))
	rdInsert(t, db, "dev_pr_event", pr, 10, "svc-web#9", "OPEN", rdMs("2026-03-16T10:00:00Z"))
}

// rdSeedRuns inserts four sync runs: two finished and one running for standup A, one finished for standup B.
func rdSeedRuns(t *testing.T, db *store.DB) {
	t.Helper()
	rc := "id,standup_id,mode,trigger,status,started_at,finished_at,warnings"
	rdInsert(t, db, "sync_run", rc, 1, rdSA, "full", "manual", "ok", rdMs("2026-03-17T09:00:00Z"), rdMs("2026-03-17T09:01:00Z"), `["old_warning"]`)
	rdInsert(t, db, "sync_run", rc, 2, rdSA, "incremental", "open", "partial", rdMs("2026-03-18T18:00:00Z"), rdMs("2026-03-18T18:30:00Z"), `["dev_activity_unavailable","throttled"]`)
	rdInsert(t, db, "sync_run", rc, 3, rdSA, "incremental", "manual", "running", rdMs("2026-03-18T19:00:00Z"), nil, `[]`)
	rdInsert(t, db, "sync_run", rc, 4, rdSB, "incremental", "manual", "ok", rdMs("2026-03-18T19:30:00Z"), rdMs("2026-03-18T19:31:00Z"), `["other_standup"]`)
	rdInsert(t, db, "sync_log", "run_id,at,level,phase,message", 2, rdMs("2026-03-18T18:10:00Z"), "info", "scan", "scanned 12 issues")
	rdInsert(t, db, "sync_log", "run_id,at,level,phase,message", 2, rdMs("2026-03-18T18:20:00Z"), "warn", "dev", "dev activity unavailable")
}

func rdSeedAll(t *testing.T, db *store.DB) {
	t.Helper()
	rdSeedBase(t, db)
	rdSeedRecords(t, db)
	rdSeedRuns(t, db)
}

// rdCfg builds the effective config of standup A by hand (the cache must not call settings).
func rdCfg(pulseDays int) model.EffectiveConfig {
	return model.EffectiveConfig{
		StandupID: rdSA, Rev: 4, Name: "Platform",
		SpaceID: "100", SpaceKey: "MOB", SpaceName: "Service",
		BoardID: "77", BoardName: "MOB board", BoardType: "scrum", Scope: "sprint",
		Lanes: []model.LaneConfig{
			{Lane: "qa", Position: 0, LimitDays: 3}, {Lane: "review", Position: 1, LimitDays: 2},
			{Lane: "prog", Position: 2, LimitDays: 5}, {Lane: "todo", Position: 3, LimitDays: 10},
		},
		Columns: []model.ColumnLane{
			{ColumnName: "Backlog", Position: 0, Lane: "backlog", Auto: true}, {ColumnName: "To Do", Position: 1, Lane: "todo", Auto: true},
			{ColumnName: "In Progress", Position: 2, Lane: "prog", Auto: true}, {ColumnName: "Code Review", Position: 3, Lane: "review", Auto: true},
			{ColumnName: "QA", Position: 4, Lane: "qa", Auto: true}, {ColumnName: "Done", Position: 5, Lane: "done", Auto: true},
			{ColumnName: "Parked", Position: 6, Lane: "hidden", Auto: true},
		},
		Speakers: []model.SpeakerConfig{
			{AccountID: "acc-ann", Name: "Ann Lee", Position: 0, Included: true},
			{AccountID: "acc-bob", Name: "Bob Ray", Position: 1, Included: true},
		},
		UnassignedPosition: "last", PulseDays: pulseDays, ShowCommits: true, ShowPRs: true, ShowComments: true,
		ShadeWeekends: true, RowsMode: "fit", DoneWindow: "sprint", BacklogRows: 12,
	}
}

// rdLoad runs LoadCurrentTx for cfg inside a read transaction.
func rdLoad(t *testing.T, db *store.DB, cfg model.EffectiveConfig, asOf time.Time) model.BoardSnapshot {
	t.Helper()
	var snap model.BoardSnapshot
	err := db.ReadTx(rdCtx, func(tx *sql.Tx) error {
		var e error
		snap, e = cache.LoadCurrentTx(rdCtx, tx, cfg, asOf)
		return e
	})
	if err != nil {
		t.Fatalf("LoadCurrentTx: %v", err)
	}
	return snap
}

func rdKeys(ms []model.Member) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Issue.Key)
	}
	return out
}

// ---- LoadCurrentTx: members -----------------------------------------------------------------------------------

func TestLoadCurrentTx_Members_BoardRoleInRankOrderThenBacklogRoleInRankOrder(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	snap := rdLoad(t, db, rdCfg(7), rdAsOf(t))

	want := []string{"MOB-2", "MOB-4", "MOB-1", "MOB-3", "MOB-7", "MOB-9", "MOB-8"}
	if got := rdKeys(snap.Members); !reflect.DeepEqual(got, want) {
		t.Fatalf("member keys = %v, want %v (board by rank_index, then backlog by rank_index; issue.rank must not be used)", got, want)
	}
	roles := []string{"board", "board", "board", "board", "board", "backlog", "backlog"}
	for i, m := range snap.Members {
		if m.Role != roles[i] {
			t.Errorf("member %s Role = %q, want %q", m.Issue.Key, m.Role, roles[i])
		}
	}
}

func TestLoadCurrentTx_Members_ExcludeHiddenLaneUnmappedStatusAndOtherStandups(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	snap := rdLoad(t, db, rdCfg(7), rdAsOf(t))

	have := map[string]bool{}
	for _, k := range rdKeys(snap.Members) {
		have[k] = true
	}
	for _, k := range []string{
		"MOB-5",  // board member, but its status maps to the hidden lane
		"MOB-6",  // board member, but its status is in no board column (not shown, as in Jira)
		"MOB-10", // member of standup B only
		"MOB-11", // removed from every standup
	} {
		if have[k] {
			t.Errorf("%s must not be in the snapshot", k)
		}
	}
	// the done ticket IS a member (the view applies the done window later)
	if !have["MOB-4"] {
		t.Errorf("done-lane member MOB-4 missing from the snapshot")
	}
}

func TestLoadCurrentTx_Members_FieldsMappedFromIssue(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	snap := rdLoad(t, db, rdCfg(7), rdAsOf(t))

	byKey := map[string]model.Member{}
	for _, m := range snap.Members {
		byKey[m.Issue.Key] = m
	}
	cases := []struct {
		key        string
		localID    int64
		sourceID   string
		typeID     string
		typeKind   string
		statusID   string
		statusName string
		assignee   string
		flagged    bool
		rank       string
		parent     string
		sprints    []string
		created    string
		updated    string
		resolved   string // "" = zero
		rankIndex  int
	}{
		{"MOB-1", 1, "9001", "T1", "story", "12", "In Progress", "acc-ann", false, "x", "MOB-100", []string{"S42", "S41"}, "2026-02-20T09:00:00Z", "2026-03-17T12:00:00Z", "", 2},
		{"MOB-2", 2, "9002", "T2", "bug", "11", "To Do", "", false, "z", "", nil, "2026-03-02T09:00:00Z", "2026-03-10T12:00:00Z", "", 0},
		{"MOB-3", 3, "9003", "T1", "story", "13", "Blocked", "acc-ann", true, "w", "", []string{"S42"}, "2026-03-03T09:00:00Z", "2026-03-17T09:00:00Z", "", 3},
		{"MOB-4", 4, "9004", "T3", "subtask", "16", "Done", "acc-bob", false, "y", "", []string{"S42"}, "2026-03-01T09:00:00Z", "2026-03-16T16:00:00Z", "2026-03-16T16:00:00Z", 1},
		{"MOB-7", 7, "9007", "T1", "story", "14", "In Review", "acc-bob", false, "v", "", []string{"S42"}, "2026-03-01T09:00:00Z", "2026-03-17T08:00:00Z", "", 6},
		{"MOB-9", 9, "9009", "T2", "bug", "10", "Open", "", false, "b", "", nil, "2026-03-01T09:00:00Z", "2026-03-12T16:00:00Z", "", 0},
		{"MOB-8", 8, "9008", "T1", "story", "10", "Open", "", false, "a", "", nil, "2026-03-01T09:00:00Z", "2026-03-12T16:00:00Z", "", 1},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			m, ok := byKey[c.key]
			if !ok {
				t.Fatalf("%s missing from members %v", c.key, rdKeys(snap.Members))
			}
			if m.LocalID != c.localID {
				t.Errorf("LocalID = %d, want %d", m.LocalID, c.localID)
			}
			is := m.Issue
			if is.SourceID != c.sourceID || is.TypeID != c.typeID || is.StatusID != c.statusID {
				t.Errorf("SourceID/TypeID/StatusID = %q/%q/%q, want %q/%q/%q", is.SourceID, is.TypeID, is.StatusID, c.sourceID, c.typeID, c.statusID)
			}
			if is.Summary != "Summary of "+c.key || is.SpaceID != "100" {
				t.Errorf("Summary/SpaceID = %q/%q", is.Summary, is.SpaceID)
			}
			if m.TypeKind != c.typeKind || m.StatusName != c.statusName {
				t.Errorf("TypeKind/StatusName = %q/%q, want %q/%q", m.TypeKind, m.StatusName, c.typeKind, c.statusName)
			}
			if is.AssigneeID != c.assignee {
				t.Errorf("AssigneeID = %q, want %q (NULL must map to \"\")", is.AssigneeID, c.assignee)
			}
			if is.Flagged != c.flagged {
				t.Errorf("Flagged = %v, want %v", is.Flagged, c.flagged)
			}
			if is.Rank != c.rank || is.ParentKey != c.parent {
				t.Errorf("Rank/ParentKey = %q/%q, want %q/%q", is.Rank, is.ParentKey, c.rank, c.parent)
			}
			if len(is.SprintIDs) != len(c.sprints) || (len(c.sprints) > 0 && !reflect.DeepEqual(is.SprintIDs, c.sprints)) {
				t.Errorf("SprintIDs = %v, want %v (JSON decoded)", is.SprintIDs, c.sprints)
			}
			rdSame(t, "CreatedAt", is.CreatedAt, rdTS(c.created))
			rdSame(t, "UpdatedAt", is.UpdatedAt, rdTS(c.updated))
			if c.resolved == "" {
				rdZero(t, "ResolvedAt", is.ResolvedAt)
			} else {
				rdSame(t, "ResolvedAt", is.ResolvedAt, rdTS(c.resolved))
			}
			if m.RankIndex != c.rankIndex {
				t.Errorf("RankIndex = %d, want %d (standup_issue.rank_index)", m.RankIndex, c.rankIndex)
			}
		})
	}
}

// ---- LoadCurrentTx: history -----------------------------------------------------------------------------------

func TestLoadCurrentTx_History_StatusAndFlagChangesAreFullAndOldestFirst(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	// PulseDays 7: every status change of MOB-1 is older than the window and must still be there.
	snap := rdLoad(t, db, rdCfg(7), rdAsOf(t))

	h1 := snap.History["9001"].StatusChanges
	if len(h1) != 2 {
		t.Fatalf("MOB-1 status changes = %+v, want 2 (full history, including before the window)", h1)
	}
	want := []model.StatusChange{
		{ID: "h1", At: rdTS("2026-02-01T10:00:00Z"), From: "10", To: "11"},
		{ID: "h2", At: rdTS("2026-03-05T14:00:00Z"), From: "11", To: "12"},
	}
	for i, w := range want {
		g := h1[i]
		if g.ID != w.ID || g.From != w.From || g.To != w.To || !g.At.Equal(w.At) {
			t.Errorf("MOB-1 status change %d = %+v, want %+v", i, g, w)
		}
	}
	if got := snap.History["9004"].StatusChanges; len(got) != 2 || got[1].To != "16" || !got[1].At.Equal(rdTS("2026-03-16T16:00:00Z")) {
		t.Errorf("MOB-4 (done) status changes = %+v, want h0 then the move to 16 at 2026-03-16T16:00Z", got)
	}

	fl := snap.History["9003"].FlagChanges
	wantFlags := []model.FlagChange{
		{ID: "f1", At: rdTS("2026-03-01T09:00:00Z"), Flagged: true},
		{ID: "f2", At: rdTS("2026-03-02T09:00:00Z"), Flagged: false},
		{ID: "f3", At: rdTS("2026-03-17T09:00:00Z"), Flagged: true},
	}
	if len(fl) != len(wantFlags) {
		t.Fatalf("MOB-3 flag changes = %+v, want %d (no window bound: the prior state is needed)", fl, len(wantFlags))
	}
	for i, w := range wantFlags {
		if fl[i].ID != w.ID || fl[i].Flagged != w.Flagged || !fl[i].At.Equal(w.At) {
			t.Errorf("MOB-3 flag change %d = %+v, want %+v", i, fl[i], w)
		}
	}
}

func rdDevString(e model.DevEvent) string {
	s := e.Kind + ":" + e.ID
	if e.Kind == model.DevPR {
		s += ":" + e.Status
	}
	if e.Merge {
		s += ":merge"
	}
	return s
}

func rdCommentIDs(cs []model.Comment) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func TestLoadCurrentTx_History_CommentsCommitsAndPREventsBeforeWindowStartAreDropped(t *testing.T) {
	defer rdRecover(t)
	// asOf is 2026-03-18 15:00 CDT. Window starts (local midnight, calendar days, America/Chicago):
	//   7d  2026-03-12 00:00 CDT = 05:00Z     14d 2026-03-05 00:00 CST = 06:00Z (DST began 03-08)
	//   28d 2026-02-19 00:00 CST = 06:00Z
	cases := []struct {
		pulseDays int
		comments  []string
		dev       []string
	}{
		{7, []string{"c4", "c5", "c6"},
			[]string{"commit:bbb", "pr:svc-api#7:OPEN", "commit:ccc:merge", "pr:svc-api#7:MERGED"}},
		{14, []string{"c2", "c3", "c4", "c5", "c6"},
			[]string{"commit:aaa", "commit:bbb", "pr:svc-api#7:OPEN", "commit:ccc:merge", "pr:svc-api#7:MERGED"}},
		{28, []string{"c0", "c1", "c2", "c3", "c4", "c5", "c6"},
			[]string{"pr:svc-api#6:MERGED", "commit:aaa", "commit:bbb", "pr:svc-api#7:OPEN", "commit:ccc:merge", "pr:svc-api#7:MERGED"}},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%dd", c.pulseDays), func(t *testing.T) {
			defer rdRecover(t)
			db := rdOpen(t)
			rdSeedAll(t, db)
			snap := rdLoad(t, db, rdCfg(c.pulseDays), rdAsOf(t))
			h := snap.History["9001"]
			if got := rdCommentIDs(h.Comments); !reflect.DeepEqual(got, c.comments) {
				t.Errorf("comments = %v, want %v", got, c.comments)
			}
			got := []string{}
			for _, e := range h.Dev {
				got = append(got, rdDevString(e))
			}
			if !reflect.DeepEqual(got, c.dev) {
				t.Errorf("dev events = %v, want %v (oldest first, merge commits kept and flagged)", got, c.dev)
			}
		})
	}
}

func TestLoadCurrentTx_History_EventFieldsAreMapped(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	snap := rdLoad(t, db, rdCfg(7), rdAsOf(t))
	h := snap.History["9001"]

	byID := map[string]model.Comment{}
	for _, c := range h.Comments {
		byID[c.ID] = c
	}
	if c := byID["c5"]; c.AuthorID != "acc-bot" || !c.At.Equal(rdTS("2026-03-16T10:00:00Z")) {
		t.Errorf("comment c5 = %+v, want author acc-bot at 2026-03-16T10:00Z (app authors are kept; pulse filters them)", c)
	}
	if c := byID["c6"]; c.AuthorID != "" || !c.At.Equal(rdTS("2026-03-17T10:00:00Z")) {
		t.Errorf("comment c6 = %+v, want empty author (NULL) at 2026-03-17T10:00Z", c)
	}

	var bbb, ccc, pr7 *model.DevEvent
	for i := range h.Dev {
		switch rdDevString(h.Dev[i]) {
		case "commit:bbb":
			bbb = &h.Dev[i]
		case "commit:ccc:merge":
			ccc = &h.Dev[i]
		case "pr:svc-api#7:OPEN":
			pr7 = &h.Dev[i]
		}
	}
	if bbb == nil || ccc == nil || pr7 == nil {
		t.Fatalf("dev events missing: %v", h.Dev)
	}
	if bbb.Kind != model.DevCommit || bbb.Repo != "svc-api" || bbb.Merge || !bbb.At.Equal(rdTS("2026-03-13T12:00:00Z")) {
		t.Errorf("commit bbb = %+v", *bbb)
	}
	if !ccc.Merge || !ccc.At.Equal(rdTS("2026-03-15T12:00:00Z")) {
		t.Errorf("commit ccc = %+v, want Merge=true at 2026-03-15T12:00Z", *ccc)
	}
	if pr7.Kind != model.DevPR || pr7.Status != "OPEN" || !pr7.At.Equal(rdTS("2026-03-14T10:00:00Z")) {
		t.Errorf("pr event = %+v, want kind pr, status OPEN at 2026-03-14T10:00Z", *pr7)
	}
}

func TestLoadCurrentTx_History_OnlyForMembersOfThisStandup(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	snap := rdLoad(t, db, rdCfg(28), rdAsOf(t))

	if snap.History == nil {
		t.Fatal("History is nil, want a non-nil map")
	}
	for _, src := range []string{"9010", "9011"} {
		if h, ok := snap.History[src]; ok {
			t.Errorf("History has an entry for %s (not a member of this standup): %+v", src, h)
		}
	}
	// events of other members are kept apart per issue
	if got := rdCommentIDs(snap.History["9004"].Comments); !reflect.DeepEqual(got, []string{"k1"}) {
		t.Errorf("MOB-4 comments = %v, want [k1]", got)
	}
	if got := snap.History["9007"].Dev; len(got) != 1 || got[0].ID != "ddd" || got[0].Repo != "svc-web" {
		t.Errorf("MOB-7 dev events = %+v, want only commit ddd in svc-web", got)
	}
}

// ---- LoadCurrentTx: reference maps, sprints, sync facts ---------------------------------------------------------

func TestLoadCurrentTx_PeopleAndStatusesMaps(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	snap := rdLoad(t, db, rdCfg(7), rdAsOf(t))

	wantPeople := map[string]model.Person{
		"acc-ann": {AccountID: "acc-ann", DisplayName: "Ann Lee", Active: true, Kind: "user"},
		"acc-bob": {AccountID: "acc-bob", DisplayName: "Bob Ray", Active: true, Kind: "user"},
		"acc-bot": {AccountID: "acc-bot", DisplayName: "Deploy Bot", Active: true, Kind: "app"},
	}
	for id, w := range wantPeople {
		if g, ok := snap.People[id]; !ok || g != w {
			t.Errorf("People[%s] = %+v (present=%v), want %+v", id, g, ok, w)
		}
	}
	wantStatus := map[string]model.Status{
		"11": {ID: "11", Name: "To Do", Category: "new"},
		"12": {ID: "12", Name: "In Progress", Category: "indeterminate"},
		"13": {ID: "13", Name: "Blocked", Category: "indeterminate", BlockedHint: true},
		"14": {ID: "14", Name: "In Review", Category: "indeterminate"},
		"16": {ID: "16", Name: "Done", Category: "done"},
	}
	for id, w := range wantStatus {
		if g, ok := snap.Statuses[id]; !ok || g != w {
			t.Errorf("Statuses[%s] = %+v (present=%v), want %+v", id, g, ok, w)
		}
	}
}

func TestLoadCurrentTx_Sprints_AreTheActiveIDsOfTheSyncState(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	snap := rdLoad(t, db, rdCfg(7), rdAsOf(t))

	// S41 is closed and S43 is 'active' in the sprint table but not in active_sprint_ids: only S42 is returned.
	if len(snap.Sprints) != 1 {
		t.Fatalf("Sprints = %+v, want exactly [S42]", snap.Sprints)
	}
	s := snap.Sprints[0]
	if s.ID != "S42" || s.BoardID != "77" || s.Name != "Sprint 42" || s.State != "active" {
		t.Errorf("sprint = %+v, want S42 / board 77 / Sprint 42 / active", s)
	}
	rdSame(t, "StartAt", s.StartAt, rdTS("2026-03-09T14:00:00Z"))
	rdSame(t, "EndAt", s.EndAt, rdTS("2026-03-23T14:00:00Z"))
	rdZero(t, "CompleteAt", s.CompleteAt)
}

func TestLoadCurrentTx_SyncFactsConfigAndLiveFlag(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	asOf := rdAsOf(t)
	cfg := rdCfg(7)
	snap := rdLoad(t, db, cfg, asOf)

	if !snap.Live {
		t.Errorf("Live = false, want true")
	}
	if snap.StandupID != rdSA {
		t.Errorf("StandupID = %d, want %d", snap.StandupID, rdSA)
	}
	if !snap.AsOf.Equal(asOf) || snap.AsOf.Location() != asOf.Location() {
		t.Errorf("AsOf = %v (%v), want %v in its original location", snap.AsOf, snap.AsOf.Location(), asOf)
	}
	if snap.Config.StandupID != cfg.StandupID || snap.Config.Name != cfg.Name || snap.Config.PulseDays != 7 || snap.Config.BoardID != "77" {
		t.Errorf("Config = %+v, want the cfg that was passed in", snap.Config)
	}
	if snap.BacklogTotal != 37 {
		t.Errorf("BacklogTotal = %d, want 37", snap.BacklogTotal)
	}
	rdSame(t, "LastSyncedAt", snap.LastSyncedAt, rdTS("2026-03-18T18:30:00Z"))
	if snap.IssueURLPrefix != "" {
		t.Errorf("IssueURLPrefix = %q, want \"\" (the app sets it)", snap.IssueURLPrefix)
	}
}

func TestLoadCurrentTx_Warnings_FromNewestFinishedRunOfThisStandup(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	// run 3 (running, newest of A) and run 4 (finished, newest overall, standup B) must both be ignored.
	snap := rdLoad(t, db, rdCfg(7), rdAsOf(t))
	if want := []string{"dev_activity_unavailable", "throttled"}; !reflect.DeepEqual(snap.Warnings, want) {
		t.Errorf("Warnings = %v, want %v (last finished run of standup A)", snap.Warnings, want)
	}
}

func TestLoadCurrentTx_DevActivityComesFromTheConnection(t *testing.T) {
	for _, state := range []string{"ok", "unavailable", "unknown"} {
		t.Run(state, func(t *testing.T) {
			defer rdRecover(t)
			db := rdOpen(t)
			rdSeedAll(t, db)
			rdExec(t, db, "UPDATE connection SET dev_activity_state=? WHERE id=1", state)
			snap := rdLoad(t, db, rdCfg(7), rdAsOf(t))
			if snap.DevActivity != state {
				t.Errorf("DevActivity = %q, want %q", snap.DevActivity, state)
			}
		})
	}
}

func TestLoadCurrentTx_StandupWithNoMembersRunsOrSyncState_ReturnsEmptyNonNilSnapshot(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdInsert(t, db, "connection", "id,base_url,account,created_at", 1, "https://acme.atlassian.net", "chris@example.com", rdMs("2026-02-01T10:00:00Z"))
	rdInsert(t, db, "standup", "id,connection_id,name,position,created_at,updated_at", 1, 1, "Empty", 0, rdMs("2026-02-01T10:00:00Z"), rdMs("2026-02-01T10:00:00Z"))
	cfg := model.EffectiveConfig{StandupID: 1, Name: "Empty", PulseDays: 21, Scope: "sprint"}
	snap := rdLoad(t, db, cfg, rdAsOf(t))

	if !snap.Live {
		t.Errorf("Live = false, want true")
	}
	if snap.Members == nil || snap.Sprints == nil || snap.Warnings == nil || snap.History == nil || snap.People == nil || snap.Statuses == nil {
		t.Errorf("nil list or map in an empty snapshot: members=%v sprints=%v warnings=%v history=%v people=%v statuses=%v",
			snap.Members == nil, snap.Sprints == nil, snap.Warnings == nil, snap.History == nil, snap.People == nil, snap.Statuses == nil)
	}
	if len(snap.Members) != 0 || len(snap.Sprints) != 0 || len(snap.Warnings) != 0 {
		t.Errorf("want empty lists, got members=%v sprints=%v warnings=%v", snap.Members, snap.Sprints, snap.Warnings)
	}
	if snap.BacklogTotal != -1 {
		t.Errorf("BacklogTotal = %d, want -1 (unknown) without a sync state row", snap.BacklogTotal)
	}
	rdZero(t, "LastSyncedAt", snap.LastSyncedAt)
	if snap.DevActivity != "unknown" {
		t.Errorf("DevActivity = %q, want unknown (connection default)", snap.DevActivity)
	}
}

// ---- Tabs (Q6) ------------------------------------------------------------------------------------------------

func TestTabs_LiveStandupsOrderedByPositionWithQ6Fields(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	tabs, err := cache.Tabs(rdCtx, db.R)
	if err != nil {
		t.Fatalf("Tabs: %v", err)
	}
	if len(tabs) != 2 {
		t.Fatalf("Tabs = %+v, want 2 rows (Draft at position 0, Platform at position 1; the archived standup is excluded)", tabs)
	}

	d, p := tabs[0], tabs[1]
	if d.ID != rdSB || d.Name != "Draft" || d.Position != 0 || d.Rev != 1 || d.SpaceKey != "MOB" {
		t.Errorf("tab 0 = %+v, want the Draft standup (id 2, position 0, rev 1, space MOB)", d)
	}
	if d.Configured {
		t.Errorf("Draft Configured = true, want false (board_id is NULL)")
	}
	if !d.NeedsSync {
		t.Errorf("Draft NeedsSync = false, want true (its sync state says so)")
	}
	rdZero(t, "Draft LastSuccessAt", d.LastSuccessAt)
	if d.ActiveCount != 0 || d.SpeakerCount != 0 {
		t.Errorf("Draft ActiveCount/SpeakerCount = %d/%d, want 0/0", d.ActiveCount, d.SpeakerCount)
	}

	if p.ID != rdSA || p.Name != "Platform" || p.Position != 1 || p.Rev != 4 || p.SpaceKey != "MOB" {
		t.Errorf("tab 1 = %+v, want the Platform standup (id 1, position 1, rev 4, space MOB)", p)
	}
	if !p.Configured {
		t.Errorf("Platform Configured = false, want true (board_id is set)")
	}
	if p.NeedsSync {
		t.Errorf("Platform NeedsSync = true, want false")
	}
	rdSame(t, "Platform LastSuccessAt", p.LastSuccessAt, rdTS("2026-03-18T18:30:00Z"))
	// working-lane board members: MOB-2 (todo), MOB-1 and MOB-3 (prog), MOB-7 (review). Not Done, not Parked, not the
	// unmapped status, not the backlog role.
	if p.ActiveCount != 4 {
		t.Errorf("Platform ActiveCount = %d, want 4", p.ActiveCount)
	}
	if p.SpeakerCount != 2 {
		t.Errorf("Platform SpeakerCount = %d, want 2 (the excluded speaker does not count)", p.SpeakerCount)
	}
}

func TestTabs_ArchivedStandupsAreExcluded_RestoreBringsThemBack(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	ids := func() []int64 {
		tabs, err := cache.Tabs(rdCtx, db.R)
		if err != nil {
			t.Fatalf("Tabs: %v", err)
		}
		out := []int64{}
		for _, x := range tabs {
			out = append(out, x.ID)
		}
		return out
	}
	if got := ids(); !reflect.DeepEqual(got, []int64{rdSB, rdSA}) {
		t.Fatalf("tab ids = %v, want [2 1]", got)
	}
	rdExec(t, db, "UPDATE standup SET deleted_at=NULL WHERE id=?", rdSD)
	if got := ids(); !reflect.DeepEqual(got, []int64{rdSB, rdSA, rdSD}) {
		t.Errorf("after restore tab ids = %v, want [2 1 3]", got)
	}
}

func TestTabs_NoStandups_ReturnsEmpty(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	tabs, err := cache.Tabs(rdCtx, db.R)
	if err != nil {
		t.Fatalf("Tabs: %v", err)
	}
	if len(tabs) != 0 {
		t.Errorf("Tabs = %+v, want none", tabs)
	}
}

// ---- GetSyncState ---------------------------------------------------------------------------------------------

func TestGetSyncState_ExistingRow_MapsEveryColumn(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	s, err := cache.GetSyncState(rdCtx, db.R, rdSA)
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if s.StandupID != rdSA || s.NeedsSync || s.BacklogTotal != 37 {
		t.Errorf("state = %+v, want standup 1, NeedsSync false, BacklogTotal 37", s)
	}
	if !reflect.DeepEqual(s.ActiveSprintIDs, []string{"S42"}) {
		t.Errorf("ActiveSprintIDs = %v, want [S42]", s.ActiveSprintIDs)
	}
	rdSame(t, "LastAttemptAt", s.LastAttemptAt, rdTS("2026-03-18T19:00:00Z"))
	rdSame(t, "LastSuccessAt", s.LastSuccessAt, rdTS("2026-03-18T18:30:00Z"))
	rdSame(t, "LastFullAt", s.LastFullAt, rdTS("2026-03-10T09:00:00Z"))
	rdSame(t, "HistoryFrom", s.HistoryFrom, rdTS("2026-03-01T09:00:00Z"))
}

func TestGetSyncState_NullTimesAreZero(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	s, err := cache.GetSyncState(rdCtx, db.R, rdSB)
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if !s.NeedsSync || s.BacklogTotal != -1 || len(s.ActiveSprintIDs) != 0 {
		t.Errorf("state = %+v, want NeedsSync true, BacklogTotal -1, no sprint ids", s)
	}
	rdZero(t, "LastAttemptAt", s.LastAttemptAt)
	rdZero(t, "LastSuccessAt", s.LastSuccessAt)
	rdZero(t, "LastFullAt", s.LastFullAt)
	rdZero(t, "HistoryFrom", s.HistoryFrom)
}

func TestGetSyncState_MissingRow_ReturnsDefaultsNotAnError(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	for _, id := range []int64{rdSD, 999} { // D has no row; 999 does not exist at all
		s, err := cache.GetSyncState(rdCtx, db.R, id)
		if err != nil {
			t.Fatalf("GetSyncState(%d): %v", id, err)
		}
		if !s.NeedsSync || s.BacklogTotal != -1 {
			t.Errorf("GetSyncState(%d) = %+v, want NeedsSync true and BacklogTotal -1", id, s)
		}
		if s.StandupID != id {
			t.Errorf("GetSyncState(%d).StandupID = %d, want the id asked for", id, s.StandupID)
		}
		rdZero(t, "LastSuccessAt", s.LastSuccessAt)
		rdZero(t, "HistoryFrom", s.HistoryFrom)
		if len(s.ActiveSprintIDs) != 0 {
			t.Errorf("ActiveSprintIDs = %v, want none", s.ActiveSprintIDs)
		}
	}
}

// ---- IssueStamps / MemberSourceIDs ----------------------------------------------------------------------------

func TestIssueStamps_ReturnsLocalIDAndTimestamps_NewestStatusChangeWins(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	got, err := cache.IssueStamps(rdCtx, db.R, 1, []string{"9001", "9002", "9003"})
	if err != nil {
		t.Fatalf("IssueStamps: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("stamps = %+v, want 3 entries", got)
	}

	s1 := got["9001"]
	if s1.LocalID != 1 || s1.DevFingerprint != "fp-1" {
		t.Errorf("MOB-1 stamp = %+v, want LocalID 1 and fingerprint fp-1", s1)
	}
	rdSame(t, "MOB-1 UpdatedAt", s1.UpdatedAt, rdTS("2026-03-17T12:00:00Z"))
	rdSame(t, "MOB-1 DevCheckedAt", s1.DevCheckedAt, rdTS("2026-03-17T12:05:00Z"))
	// h1 (02-01) and h2 (03-05) exist: the newest wins
	rdSame(t, "MOB-1 LastStatusAt", s1.LastStatusAt, rdTS("2026-03-05T14:00:00Z"))

	s2 := got["9002"]
	if s2.LocalID != 2 || s2.DevFingerprint != "" {
		t.Errorf("MOB-2 stamp = %+v, want LocalID 2 and an empty fingerprint (NULL)", s2)
	}
	rdZero(t, "MOB-2 DevCheckedAt", s2.DevCheckedAt)
	rdZero(t, "MOB-2 LastStatusAt (no status changes)", s2.LastStatusAt)
	rdSame(t, "MOB-2 UpdatedAt", s2.UpdatedAt, rdTS("2026-03-10T12:00:00Z"))

	rdSame(t, "MOB-3 LastStatusAt", got["9003"].LastStatusAt, rdTS("2026-03-17T09:00:00Z"))
}

func TestIssueStamps_MissingIDsAreAbsentAndOtherConnectionsAreIgnored(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	rdInsert(t, db, "connection", "id,base_url,account,created_at", 2, "https://other.atlassian.net", "o@example.com", rdMs("2026-02-01T10:00:00Z"))
	rdInsert(t, db, "issue",
		"id,connection_id,source_id,key,space_id,summary,issue_type_id,status_id,created_at,updated_at,hydrated_at,last_seen_at",
		50, 2, "9001", "OTH-1", "200", "other", "T1", "12", rdMs("2026-03-01T09:00:00Z"), rdMs("2026-03-01T09:00:00Z"), rdMs("2026-03-01T09:00:00Z"), rdMs("2026-03-01T09:00:00Z"))

	got, err := cache.IssueStamps(rdCtx, db.R, 1, []string{"9001", "no-such-id"})
	if err != nil {
		t.Fatalf("IssueStamps: %v", err)
	}
	if _, ok := got["no-such-id"]; ok || len(got) != 1 {
		t.Errorf("stamps = %+v, want only 9001", got)
	}
	if got["9001"].LocalID != 1 {
		t.Errorf("9001 LocalID = %d, want 1 (connection 1's issue, not connection 2's id 50)", got["9001"].LocalID)
	}

	empty, err := cache.IssueStamps(rdCtx, db.R, 1, nil)
	if err != nil {
		t.Fatalf("IssueStamps(nil): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Errorf("IssueStamps(nil) = %#v, want an empty non-nil map", empty)
	}
}

func TestIssueStamps_HandlesLongIDLists(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	ids := make([]string, 0, 1200)
	for i := 0; i < 1200; i++ {
		ids = append(ids, fmt.Sprintf("none-%d", i))
	}
	ids = append(ids, "9001", "9004")
	got, err := cache.IssueStamps(rdCtx, db.R, 1, ids)
	if err != nil {
		t.Fatalf("IssueStamps with %d ids: %v", len(ids), err)
	}
	if len(got) != 2 || got["9001"].LocalID != 1 || got["9004"].LocalID != 4 {
		t.Errorf("stamps = %+v, want exactly 9001 and 9004", got)
	}
}

func TestMemberSourceIDs_AllCurrentMembersOfTheStandupWithRole(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	got, err := cache.MemberSourceIDs(rdCtx, db.R, rdSA)
	if err != nil {
		t.Fatalf("MemberSourceIDs: %v", err)
	}
	// every standup_issue row, whatever lane its status is in (membership is the source's answer, not the lane's)
	want := map[string]string{
		"9001": "board", "9002": "board", "9003": "board", "9004": "board", "9005": "board", "9006": "board", "9007": "board",
		"9008": "backlog", "9009": "backlog",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MemberSourceIDs = %v, want %v (not 9010 of standup B, not removed 9011)", got, want)
	}
}

func TestMemberSourceIDs_UnknownStandup_ReturnsEmptyNonNilMap(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	got, err := cache.MemberSourceIDs(rdCtx, db.R, 999)
	if err != nil {
		t.Fatalf("MemberSourceIDs: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("MemberSourceIDs(999) = %#v, want an empty non-nil map", got)
	}
}
