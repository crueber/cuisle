package sync

// WP-30 tests: the atomic apply (jira-sync.md §4.6, data-model.md §2.5–2.7) and history recording (testing.md §2
// "History and retention"). Two kinds of tests:
//
//   - "Apply_..." tests that drive the whole orchestrator against the fake Jira (spans, never-delete, config history,
//     speakers, reconstruction), and
//   - "ApplyDirect_..." tests that call (*Orchestrator).apply with a hand-built Fetched on a temp DB (no network, no
//     jira package), for the rules that are hard to provoke through the fake. The Orchestrator there is built as a
//     struct literal (&Orchestrator{opts: ...}) so these tests do not depend on New (WP-29).
//
// SPEC-NOTE: apply never deletes change, comment or dev rows. Rows are upserted by natural key: status_change,
// assignee_change, flag_change by (issue, history_id); comment by (issue, comment_id); dev_commit by (issue, sha);
// dev_pr_event by (issue, pr_key, status, at). A re-fetched duplicate adds nothing.
// SPEC-NOTE: Membership time is the run's T (Fetched.StartedAt). Open span: valid_to NULL. Leaving closes the span at T;
// a role change closes the old span and opens a new one at T; returning opens a new span at T. issue.removed_at = T when an
// issue's LAST open span (across ALL standups) closes, and NULL again as soon as any span opens. Rank is not historised.
// SPEC-NOTE: issue.last_seen_at = T for every member scanned this run (Fetched.Members), hydrated_at = T only for
// Fetched.Details. Members that are not in Details keep their cached issue row.
// SPEC-NOTE: Speakers are appended from the assignees of BOARD-role members only (not backlog members, not comment
// authors, never "unassigned"); appended speakers are included=1, added_manually=0, last_seen_at=T, positioned after the
// existing ones in first-appearance (rank) order; a speaker the user excluded stays excluded; stale auto speakers
// (unseen 30 days, never touched, not manual) are pruned (settings.MergeSpeakersTx owns the detail).
// SPEC-NOTE: ApplyResult.ConfigChanged is true when the column map or the speaker list changed (so a first apply, which
// appends speakers, reports true and an identical re-apply reports false). Hydrated = len(Fetched.Details). Rev is the
// new standup.rev (old + 1, bumped on every successful apply even when nothing else changed).
// SPEC-NOTE: Dev: for each source id in Fetched.DevChecked, issue.dev_checked_at = T; for each id in Fetched.Dev,
// events are upserted and issue.dev_fingerprint = DevActivity.Fingerprint. DevState/SourceMeta go to the connection row
// (dev_activity_state, dev_activity_checked_at = T, source_meta JSON). Unknown PR statuses are stored as UNKNOWN.
// SPEC-NOTE: Fetched.Sprints are all upserted into sprint; standup_sync_state.active_sprint_ids holds the ids of those
// with State "active", as a JSON array of strings. backlog_total = Fetched.BacklogTotal. last_full_at only when
// Fetched.Mode is full. history_from is set by the first success only. needs_sync becomes 0.
// SPEC-NOTE: config_json has exactly the keys of data-model §2.6 (scope, extraFilter, doneWindow, backlogRows, pulseDays,
// shadeWeekends, show, rowsMode, walkOrder, limits, statusLanes, speakers, unassignedPosition, activeSprintIds);
// fingerprint = sha256 hex of config_json. A new row opens (and the open one closes, valid_to = T) only when the
// fingerprint differs; limits, walk order, speaker inclusion/order and the status→lane map are part of it.
// SPEC-NOTE: The reconstruction test compares history.Reconstruct at T_d + 12 h (inside the d-th run's interval, so the
// valid_from inclusive / valid_to exclusive convention is not at stake) with the membership, lane and assignee that the
// cache tables showed right after that run (what the view is built from). It imports internal/history, which
// scripts/layercheck does not yet allow for internal/sync tests (testOnly["internal/sync"]); the orchestrator adds it.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/history"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
	"github.com/crueber/cuisle/internal/store"
	"github.com/crueber/cuisle/internal/store/storetest"
)

// ================================================================================================================
// integration: through the orchestrator and the fake Jira

type issueExpect struct {
	ID, Key, StatusID, Assignee string
	Flagged                     bool
	Status, Assign, Flag        int // changelog entries by kind
	Comments, Commits, PRs      int
	CommitSHAs                  []string
	InSprint                    bool
}

// expectations reads the fake's dataset (the source of truth the cache must mirror).
func (e *env) expectations() map[string]issueExpect {
	out := map[string]issueExpect{}
	e.srv.Mutate(func(ds *jiratest.Dataset) {
		for _, is := range ds.Issues {
			x := issueExpect{ID: is.ID, Key: is.Key, StatusID: is.StatusID, Assignee: is.Assignee, Flagged: is.Flagged,
				Comments: len(is.Comments), Commits: len(is.Commits), PRs: len(is.PRs), InSprint: len(is.SprintIDs) > 0}
			for _, c := range is.Changelog {
				switch c.FieldID {
				case "status":
					x.Status++
				case "assignee":
					x.Assign++
				case jiratest.FlaggedFieldID:
					x.Flag++
				}
			}
			for _, c := range is.Commits {
				x.CommitSHAs = append(x.CommitSHAs, c.SHA)
			}
			out[is.Key] = x
		}
	})
	return out
}

func TestApply_FirstFullSync_PopulatesEveryTable(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	revBefore := e.rev(e.sid)
	id := e.sync(ModeIncremental)
	r := e.mustOK(id)
	T := ms(e.T())
	exp := e.expectations()
	if len(exp) != 10 {
		t.Fatalf("test setup: the fake has %d issues, want 10", len(exp))
	}

	// issue rows mirror the fake
	if n := e.count("issue", ""); n != 10 {
		t.Errorf("issue rows = %d, want 10", n)
	}
	targets := map[string]bool{}
	for _, sid := range e.devTargets(e.sid, true) {
		targets[sid] = true
	}
	for key, x := range exp {
		if got := e.qStr(`SELECT status_id FROM issue WHERE key = ?`, key); got != x.StatusID {
			t.Errorf("%s status_id = %q, want %q", key, got, x.StatusID)
		}
		if got := e.qStr(`SELECT COALESCE(assignee_account_id, '') FROM issue WHERE key = ?`, key); got != x.Assignee {
			t.Errorf("%s assignee = %q, want %q", key, got, x.Assignee)
		}
		if got := e.qInt(`SELECT flagged FROM issue WHERE key = ?`, key); (got == 1) != x.Flagged {
			t.Errorf("%s flagged = %d, want %v", key, got, x.Flagged)
		}
		if got := e.qInt(`SELECT last_seen_at FROM issue WHERE key = ?`, key); got != T {
			t.Errorf("%s last_seen_at = %d, want T = %d", key, got, T)
		}
		if got := e.qInt(`SELECT hydrated_at FROM issue WHERE key = ?`, key); got != T {
			t.Errorf("%s hydrated_at = %d, want T = %d", key, got, T)
		}
		if !e.qIsNull(`SELECT removed_at FROM issue WHERE key = ?`, key) {
			t.Errorf("%s has removed_at set while it is a member", key)
		}
		if e.qStr(`SELECT rank FROM issue WHERE key = ?`, key) == "" {
			t.Errorf("%s has no rank", key)
		}
		iid := e.issueID(key)
		if got := e.count("status_change", "issue_id = ?", iid); got != x.Status {
			t.Errorf("%s status_change rows = %d, want %d", key, got, x.Status)
		}
		if got := e.count("assignee_change", "issue_id = ?", iid); got != x.Assign {
			t.Errorf("%s assignee_change rows = %d, want %d", key, got, x.Assign)
		}
		if got := e.count("flag_change", "issue_id = ?", iid); got != x.Flag {
			t.Errorf("%s flag_change rows = %d, want %d", key, got, x.Flag)
		}
		if got := e.count("comment", "issue_id = ?", iid); got != x.Comments {
			t.Errorf("%s comment rows = %d, want %d", key, got, x.Comments)
		}
		if got := e.qInt(`SELECT comment_total FROM issue WHERE key = ?`, key); int(got) != x.Comments {
			t.Errorf("%s comment_total = %d, want %d", key, got, x.Comments)
		}
		wantCommits, wantPRs := 0, 0
		if targets[x.ID] {
			wantCommits, wantPRs = x.Commits, x.PRs
			if got := e.qInt(`SELECT dev_checked_at FROM issue WHERE key = ?`, key); got != T {
				t.Errorf("%s (dev target) dev_checked_at = %d, want T", key, got)
			}
		} else if !e.qIsNull(`SELECT dev_checked_at FROM issue WHERE key = ?`, key) {
			t.Errorf("%s is not a dev target but has dev_checked_at", key)
		}
		if got := e.count("dev_commit", "issue_id = ?", iid); got != wantCommits {
			t.Errorf("%s dev_commit rows = %d, want %d", key, got, wantCommits)
		}
		if got := e.count("dev_pr_event", "issue_id = ?", iid); got != wantPRs {
			t.Errorf("%s dev_pr_event rows = %d, want %d", key, got, wantPRs)
		}
		if wantCommits+wantPRs > 0 && e.qStr(`SELECT COALESCE(dev_fingerprint, '') FROM issue WHERE key = ?`, key) == "" {
			t.Errorf("%s has dev rows but no dev_fingerprint", key)
		}
	}
	for _, want := range exp["MOB-1"].CommitSHAs {
		if e.count("dev_commit", "issue_id = ? AND commit_id = ?", e.issueID("MOB-1"), want) != 1 {
			t.Errorf("MOB-1 commit %s was not stored", want)
		}
	}
	if e.count("flag_change", "issue_id = ? AND flagged = 1", e.issueID("MOB-7")) < 1 {
		t.Errorf("MOB-7 (flagged) has no flag_change row with flagged = 1")
	}

	// membership
	if got := memberKeys(e.members(e.sid), model.RoleBoard); !equalStrings(got, []string{"MOB-1", "MOB-2", "MOB-3", "MOB-4", "MOB-5", "MOB-6", "MOB-7"}) {
		t.Errorf("board members = %v", got)
	}
	if got := memberKeys(e.members(e.sid), model.RoleBacklog); !equalStrings(got, []string{"MOB-100", "MOB-101", "MOB-102"}) {
		t.Errorf("backlog members = %v", got)
	}
	for i, key := range []string{"MOB-1", "MOB-2", "MOB-3", "MOB-4", "MOB-5", "MOB-6", "MOB-7"} {
		if got := e.qInt(`SELECT rank_index FROM standup_issue si JOIN issue i ON i.id = si.issue_id WHERE si.standup_id = ? AND i.key = ?`, e.sid, key); int(got) != i {
			t.Errorf("%s rank_index = %d, want %d (the source's order within the role)", key, got, i)
		}
	}
	for i, key := range []string{"MOB-100", "MOB-101", "MOB-102"} {
		if got := e.qInt(`SELECT rank_index FROM standup_issue si JOIN issue i ON i.id = si.issue_id WHERE si.standup_id = ? AND i.key = ?`, e.sid, key); int(got) != i {
			t.Errorf("%s rank_index = %d, want %d", key, got, i)
		}
	}
	for key := range exp {
		sp := e.spans(e.sid, key)
		if len(sp) != 1 || sp[0].From != T || sp[0].To != 0 {
			t.Errorf("%s spans = %+v, want one open span from T", key, sp)
		}
	}
	if n := e.count("standup_issue_span", "standup_id = ?", e.sid); n != 10 {
		t.Errorf("standup_issue_span rows = %d, want 10", n)
	}

	// config history
	if n := e.count("standup_config_history", "standup_id = ?", e.sid); n != 1 {
		t.Fatalf("standup_config_history rows = %d, want 1", n)
	}
	cfgText := e.qStr(`SELECT config_json FROM standup_config_history WHERE standup_id = ?`, e.sid)
	if got := e.qStr(`SELECT fingerprint FROM standup_config_history WHERE standup_id = ?`, e.sid); got != sha256hex([]byte(cfgText)) {
		t.Errorf("fingerprint %q is not the sha256 of config_json", got)
	}
	if got := e.qInt(`SELECT valid_from FROM standup_config_history WHERE standup_id = ?`, e.sid); got != T {
		t.Errorf("config history valid_from = %d, want T", got)
	}
	if !e.qIsNull(`SELECT valid_to FROM standup_config_history WHERE standup_id = ?`, e.sid) {
		t.Errorf("config history row is not open")
	}
	assertConfigJSON(t, cfgText, []string{"qa", "review", "prog", "todo"}, 5, 3, []string{"42"})

	// sync state, rev, run, speakers
	if got := e.qInt(`SELECT needs_sync FROM standup_sync_state WHERE standup_id = ?`, e.sid); got != 0 {
		t.Errorf("needs_sync = %d, want 0", got)
	}
	for _, col := range []string{"last_success_at", "last_full_at", "history_from"} {
		if got := e.qInt(`SELECT `+col+` FROM standup_sync_state WHERE standup_id = ?`, e.sid); got != T {
			t.Errorf("%s = %d, want T = %d", col, got, T)
		}
	}
	if got := e.qInt(`SELECT backlog_total FROM standup_sync_state WHERE standup_id = ?`, e.sid); got != 3 {
		t.Errorf("backlog_total = %d, want 3", got)
	}
	if got := e.qStr(`SELECT active_sprint_ids FROM standup_sync_state WHERE standup_id = ?`, e.sid); got != `["42"]` {
		t.Errorf("active_sprint_ids = %s, want [\"42\"]", got)
	}
	if got := e.rev(e.sid); got != revBefore+1 {
		t.Errorf("standup.rev = %d, want %d", got, revBefore+1)
	}
	if r.Hydrated != 10 {
		t.Errorf("hydrated = %d, want 10", r.Hydrated)
	}
	speakers := e.qCol(`SELECT account_id || '|' || display_name || '|' || included || '|' || added_manually || '|' || last_seen_at FROM standup_speaker WHERE standup_id = ? ORDER BY account_id`, e.sid)
	wantSp := []string{
		fmt.Sprintf("u-iris|Iris Calloway|1|0|%d", T),
		fmt.Sprintf("u-noor|Noor Abadi|1|0|%d", T),
		fmt.Sprintf("u-silas|Silas Marchetti|1|0|%d", T),
	}
	if !equalStrings(speakers, wantSp) {
		t.Errorf("speakers = %v, want %v", speakers, wantSp)
	}
	if got := e.qInt(`SELECT count(DISTINCT position) FROM standup_speaker WHERE standup_id = ?`, e.sid); got != 3 {
		t.Errorf("speaker positions are not distinct")
	}

	// reference data and connection bookkeeping
	if n := e.count("status", "connection_id = ?", e.connID); n != 5 {
		t.Errorf("status rows = %d, want 5", n)
	}
	if n := e.count("issue_type", "connection_id = ?", e.connID); n < 3 {
		t.Errorf("issue_type rows = %d, want at least story, bug, task", n)
	}
	if got := e.qStr(`SELECT state FROM sprint WHERE connection_id = ? AND id = '42'`, e.connID); got != "active" {
		t.Errorf("sprint 42 state = %q, want active", got)
	}
	if got := e.qStr(`SELECT display_name FROM person WHERE connection_id = ? AND account_id = 'u-iris'`, e.connID); got != "Iris Calloway" {
		t.Errorf("person u-iris = %q", got)
	}
	if n := e.count("board_column", "connection_id = ? AND board_id = ?", e.connID, e.boardID); n != 5 {
		t.Errorf("board_column rows = %d, want 5", n)
	}
	if got := e.qStr(`SELECT board_name FROM standup WHERE id = ?`, e.sid); got != "MOB board" {
		t.Errorf("standup.board_name = %q", got)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(e.qStr(`SELECT source_meta FROM connection WHERE id = ?`, e.connID)), &meta); err != nil {
		t.Fatalf("connection.source_meta is not JSON: %v", err)
	}
	for k, want := range map[string]string{"sprintFieldId": jiratest.SprintFieldID, "rankFieldId": jiratest.RankFieldID, "flaggedFieldId": jiratest.FlaggedFieldID} {
		if got, _ := meta[k].(string); got != want {
			t.Errorf("connection.source_meta[%s] = %v, want %q", k, meta[k], want)
		}
	}
	if got := e.qStr(`SELECT dev_activity_state FROM connection WHERE id = ?`, e.connID); got != "ok" {
		t.Errorf("connection.dev_activity_state = %q, want ok", got)
	}
	if got := e.qInt(`SELECT dev_activity_checked_at FROM connection WHERE id = ?`, e.connID); got != T {
		t.Errorf("connection.dev_activity_checked_at = %d, want T", got)
	}
	cols := e.qCol(`SELECT column_name || '=' || lane FROM standup_column_map WHERE standup_id = ? ORDER BY position`, e.sid)
	if !equalStrings(cols, []string{"To Do=todo", "In Progress=prog", "Code Review=review", "QA=qa", "Done=done"}) {
		t.Errorf("standup_column_map = %v", cols)
	}
	if n := e.count("standup_status_lane", "standup_id = ?", e.sid); n != 5 {
		t.Errorf("standup_status_lane rows = %d, want 5", n)
	}
}

// assertConfigJSON checks the §2.6 shape of a config_json document.
func assertConfigJSON(t *testing.T, text string, walk []string, nStatusLanes, nSpeakers int, activeSprints []string) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("config_json is not JSON: %v\n%s", err, text)
	}
	for _, k := range []string{"scope", "extraFilter", "doneWindow", "backlogRows", "pulseDays", "shadeWeekends", "show", "rowsMode",
		"walkOrder", "limits", "statusLanes", "speakers", "unassignedPosition", "activeSprintIds"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("config_json lacks %q", k)
		}
	}
	if doc["scope"] != "sprint" || doc["pulseDays"] != float64(21) || doc["unassignedPosition"] != "last" {
		t.Errorf("config_json defaults = scope %v pulseDays %v unassignedPosition %v", doc["scope"], doc["pulseDays"], doc["unassignedPosition"])
	}
	var gotWalk []string
	if arr, ok := doc["walkOrder"].([]any); ok {
		for _, v := range arr {
			gotWalk = append(gotWalk, fmt.Sprint(v))
		}
	}
	if !equalStrings(gotWalk, walk) {
		t.Errorf("walkOrder = %v, want %v", gotWalk, walk)
	}
	if lim, ok := doc["limits"].(map[string]any); !ok || lim["qa"] != float64(3) || lim["review"] != float64(2) || lim["prog"] != float64(5) || lim["todo"] != float64(10) {
		t.Errorf("limits = %v, want qa 3, review 2, prog 5, todo 10", doc["limits"])
	}
	if arr, _ := doc["statusLanes"].([]any); len(arr) != nStatusLanes {
		t.Errorf("statusLanes has %d entries, want %d", len(arr), nStatusLanes)
	}
	if arr, _ := doc["speakers"].([]any); len(arr) != nSpeakers {
		t.Errorf("speakers has %d entries, want %d", len(arr), nSpeakers)
	}
	var gotSprints []string
	if arr, ok := doc["activeSprintIds"].([]any); ok {
		for _, v := range arr {
			gotSprints = append(gotSprints, fmt.Sprint(v))
		}
	}
	if !equalStrings(gotSprints, activeSprints) {
		t.Errorf("activeSprintIds = %v, want %v", gotSprints, activeSprints)
	}
}

func TestApply_DefaultFixture_FirstSyncBuildsMembershipFromTheScan(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	id := e.sync(ModeIncremental)
	r := e.mustOK(id)
	T := ms(e.T())
	members := e.count("standup_issue", "standup_id = ?", e.sid)
	if members == 0 || r.Scanned != int64(members) || r.Hydrated != r.Scanned {
		t.Errorf("members/scanned/hydrated = %d/%d/%d, want equal and > 0", members, r.Scanned, r.Hydrated)
	}
	if n := e.count("standup_issue_span", "standup_id = ? AND valid_from = ? AND valid_to IS NULL", e.sid, T); n != members {
		t.Errorf("open spans from T = %d, want one per member (%d)", n, members)
	}
	for _, table := range []string{"issue", "status_change", "comment", "dev_commit", "standup_speaker", "standup_config_history", "person", "status", "sprint"} {
		if e.count(table, "") == 0 {
			t.Errorf("table %s is empty after a first full sync of the default fixture", table)
		}
	}
	if n := e.count("issue", "removed_at IS NOT NULL"); n != 0 {
		t.Errorf("%d issues have removed_at after the first sync", n)
	}
}

func TestApply_IssueLeavesSprint_DisappearsClosesSpanKeepsRows(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeIncremental))
	t1 := ms(e.T())
	iid := e.issueID("MOB-2") // Code Review: the backlog query cannot catch it
	rows := map[string]int{}
	for _, tbl := range []string{"status_change", "assignee_change", "flag_change", "comment", "dev_commit", "dev_pr_event"} {
		rows[tbl] = e.count(tbl, "issue_id = ?", iid)
	}
	if rows["status_change"] == 0 || rows["dev_commit"] == 0 || rows["dev_pr_event"] == 0 {
		t.Fatalf("test setup: MOB-2 should have history after the first sync: %v", rows)
	}
	e.leaveSprint("MOB-2", e.clock.Now().Add(30*time.Minute))

	id := e.sync(ModeIncremental)
	r := e.mustOK(id)
	t2 := ms(e.T())
	if r.Scanned != 9 {
		t.Errorf("scanned = %d, want 9", r.Scanned)
	}
	if got := memberKeys(e.members(e.sid), ""); contains(got, "MOB-2") || len(got) != 9 {
		t.Errorf("members = %v, want 9 without MOB-2", got)
	}
	sp := e.spans(e.sid, "MOB-2")
	if len(sp) != 1 || sp[0].Role != model.RoleBoard || sp[0].From != t1 || sp[0].To != t2 {
		t.Errorf("MOB-2 spans = %+v, want one board span closed at T2 (%d..%d)", sp, t1, t2)
	}
	if got := e.qInt(`SELECT removed_at FROM issue WHERE key = 'MOB-2'`); got != t2 {
		t.Errorf("MOB-2 removed_at = %d, want T2 = %d", got, t2)
	}
	if got := e.qInt(`SELECT last_seen_at FROM issue WHERE key = 'MOB-2'`); got != t1 {
		t.Errorf("MOB-2 last_seen_at = %d, want T1 (it was not scanned)", got)
	}
	for tbl, want := range rows {
		if got := e.count(tbl, "issue_id = ?", iid); got != want {
			t.Errorf("MOB-2 %s rows = %d, want %d (kept)", tbl, got, want)
		}
	}
	if n := e.count("issue", "key = 'MOB-2'"); n != 1 {
		t.Errorf("the MOB-2 issue row was deleted")
	}
	for _, key := range []string{"MOB-1", "MOB-3", "MOB-100"} {
		if sp := e.spans(e.sid, key); len(sp) != 1 || sp[0].To != 0 {
			t.Errorf("%s spans = %+v, want one open span", key, sp)
		}
	}
}

func TestApply_IssueReturnsToSprint_ThreeSyncsGiveTwoSpans(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeIncremental))
	t1 := ms(e.T())
	e.leaveSprint("MOB-2", e.clock.Now().Add(30*time.Minute))
	e.mustOK(e.sync(ModeIncremental))
	t2 := ms(e.T())
	statusRows := e.count("status_change", "issue_id = ?", e.issueID("MOB-2"))
	e.joinSprint("MOB-2", e.clock.Now().Add(30*time.Minute))
	r := e.mustOK(e.sync(ModeIncremental))
	t3 := ms(e.T())

	sp := e.spans(e.sid, "MOB-2")
	if len(sp) != 2 {
		t.Fatalf("MOB-2 spans = %+v, want 2 (three syncs: in, out, in)", sp)
	}
	if sp[0].From != t1 || sp[0].To != t2 || sp[1].From != t3 || sp[1].To != 0 {
		t.Errorf("MOB-2 spans = %+v, want [T1..T2] and [T3..open]", sp)
	}
	if !e.qIsNull(`SELECT removed_at FROM issue WHERE key = 'MOB-2'`) {
		t.Errorf("removed_at must be cleared when the issue returns")
	}
	if got := memberKeys(e.members(e.sid), model.RoleBoard); !contains(got, "MOB-2") {
		t.Errorf("MOB-2 is not a board member again: %v", got)
	}
	if r.Hydrated != 1 {
		t.Errorf("hydrated = %d, want 1 (only the issue whose updated changed)", r.Hydrated)
	}
	if got := e.count("status_change", "issue_id = ?", e.issueID("MOB-2")); got != statusRows {
		t.Errorf("MOB-2 status_change rows = %d, want %d (history kept, nothing duplicated)", got, statusRows)
	}
	if n := e.count("standup_issue_span", "standup_id = ? AND valid_to IS NULL", e.sid); n != 10 {
		t.Errorf("open spans = %d, want 10", n)
	}
}

func TestApply_IssueMovesFromSprintToBacklog_ClosesAndOpensASpan(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeIncremental))
	t1 := ms(e.T())
	e.leaveSprint("MOB-4", e.clock.Now().Add(30*time.Minute)) // To Do: the scrum backlog query catches it
	e.mustOK(e.sync(ModeIncremental))
	t2 := ms(e.T())

	sp := e.spans(e.sid, "MOB-4")
	if len(sp) != 2 || sp[0].Role != model.RoleBoard || sp[0].From != t1 || sp[0].To != t2 ||
		sp[1].Role != model.RoleBacklog || sp[1].From != t2 || sp[1].To != 0 {
		t.Errorf("MOB-4 spans = %+v, want board [T1..T2] then backlog [T2..open]", sp)
	}
	if !e.qIsNull(`SELECT removed_at FROM issue WHERE key = 'MOB-4'`) {
		t.Errorf("MOB-4 is still a member (backlog), so removed_at must stay NULL")
	}
	if got := e.qStr(`SELECT role FROM standup_issue si JOIN issue i ON i.id = si.issue_id WHERE si.standup_id = ? AND i.key = 'MOB-4'`, e.sid); got != model.RoleBacklog {
		t.Errorf("MOB-4 role = %q, want backlog", got)
	}
	ranks := e.qCol(`SELECT rank_index FROM standup_issue WHERE standup_id = ? AND role = 'backlog' ORDER BY rank_index`, e.sid)
	if !equalStrings(ranks, []string{"0", "1", "2", "3"}) {
		t.Errorf("backlog rank_index values = %v, want 0..3", ranks)
	}
	if got := e.qInt(`SELECT backlog_total FROM standup_sync_state WHERE standup_id = ?`, e.sid); got != 4 {
		t.Errorf("backlog_total = %d, want 4", got)
	}
}

func TestApply_Rehydration_NeverDeletesChangeCommentOrDevRows(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeIncremental))
	s1, s2 := e.issueID("MOB-1"), e.issueID("MOB-2")
	oldHist := e.qStr(`SELECT history_id FROM status_change WHERE issue_id = ? ORDER BY changed_at LIMIT 1`, s1)
	oldSHAs := e.qCol(`SELECT commit_id FROM dev_commit WHERE issue_id = ? ORDER BY commit_id`, s1)
	before := map[string]int{
		"status1": e.count("status_change", "issue_id = ?", s1), "comment1": e.count("comment", "issue_id = ?", s1),
		"commit1": e.count("dev_commit", "issue_id = ?", s1), "commit2": e.count("dev_commit", "issue_id = ?", s2),
		"pr2": e.count("dev_pr_event", "issue_id = ?", s2), "status2": e.count("status_change", "issue_id = ?", s2),
	}
	if before["status1"] < 1 || before["comment1"] < 1 || before["commit1"] < 2 || before["commit2"] < 1 || before["pr2"] < 1 {
		t.Fatalf("test setup: not enough history after the first sync: %v", before)
	}

	// The fake now "forgets" old history (a provider that truncates), and one new fact each arrives.
	at := e.clock.Now().Add(30 * time.Minute)
	e.srv.Mutate(func(ds *jiratest.Dataset) {
		is1, is2 := findIssue(ds, "MOB-1"), findIssue(ds, "MOB-2")
		is1.Changelog = nil
		is1.Comments = nil
		is1.Commits = []jiratest.DSCommit{{SHA: "feedface00000000000000000000000000000001", Repo: "svc-api", At: at.Add(-time.Minute)}}
		is1.Updated = at
		is2.Commits = nil
		is2.PRs = nil
		is2.Changelog = is2.Changelog[:0]
		is2.Updated = at
	})
	e.addComment("MOB-1", "u-iris", at)
	e.moveIssue("MOB-1", "Code Review", at)
	e.mustOK(e.sync(ModeIncremental))

	if got := e.count("status_change", "issue_id = ? AND history_id = ?", s1, oldHist); got != 1 {
		t.Errorf("the status_change row %s that the fake no longer returns was deleted", oldHist)
	}
	if got := e.count("status_change", "issue_id = ?", s1); got != before["status1"]+1 {
		t.Errorf("MOB-1 status_change rows = %d, want %d (old kept, one new)", got, before["status1"]+1)
	}
	if got := e.count("comment", "issue_id = ?", s1); got != before["comment1"]+1 {
		t.Errorf("MOB-1 comment rows = %d, want %d", got, before["comment1"]+1)
	}
	for _, sha := range oldSHAs {
		if e.count("dev_commit", "issue_id = ? AND commit_id = ?", s1, sha) != 1 {
			t.Errorf("dev_commit %s was deleted by a re-hydration", sha)
		}
	}
	if got := e.count("dev_commit", "issue_id = ?", s1); got != before["commit1"]+1 {
		t.Errorf("MOB-1 dev_commit rows = %d, want %d", got, before["commit1"]+1)
	}
	if got := e.count("dev_commit", "issue_id = ?", s2); got != before["commit2"] {
		t.Errorf("MOB-2 dev_commit rows = %d, want %d (kept)", got, before["commit2"])
	}
	if got := e.count("dev_pr_event", "issue_id = ?", s2); got != before["pr2"] {
		t.Errorf("MOB-2 dev_pr_event rows = %d, want %d (kept)", got, before["pr2"])
	}
	if got := e.count("status_change", "issue_id = ?", s2); got != before["status2"] {
		t.Errorf("MOB-2 status_change rows = %d, want %d (kept)", got, before["status2"])
	}

	// A full re-hydration of unchanged data adds nothing (duplicates are ignored by source id).
	snapshot := func() []int {
		var out []int
		for _, tbl := range []string{"status_change", "assignee_change", "flag_change", "comment", "dev_commit", "dev_pr_event", "issue"} {
			out = append(out, e.count(tbl, ""))
		}
		return out
	}
	a := snapshot()
	e.mustOK(e.sync(ModeFull))
	if b := snapshot(); fmt.Sprint(a) != fmt.Sprint(b) {
		t.Errorf("a full re-sync of unchanged data changed row counts %v -> %v (duplicates must be ignored)", a, b)
	}
}

func TestApply_AssigneeChanges_DecodeToAndFromUnassigned(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeIncremental))
	exp := e.expectations()
	for _, key := range []string{"MOB-2", "MOB-4", "MOB-6"} {
		if exp[key].Assign < 1 {
			t.Fatalf("test setup: fake has no assignee change for %s", key)
		}
		if got := e.count("assignee_change", "issue_id = ?", e.issueID(key)); got != exp[key].Assign {
			t.Errorf("%s assignee_change rows = %d, want %d", key, got, exp[key].Assign)
		}
	}
	if n := e.count("assignee_change", "issue_id = ? AND from_account_id IS NULL AND to_account_id = 'u-iris'", e.issueID("MOB-4")); n < 1 {
		// The fake already records unassigned→assignee at creation and the prep adds one more; the exact total is
		// asserted against expectations() above.
		t.Errorf("MOB-4: rows from unassigned (NULL) to u-iris = %d, want ≥ 1", n)
	}
	if n := e.count("assignee_change", "issue_id = ? AND from_account_id = 'u-noor' AND to_account_id IS NULL", e.issueID("MOB-6")); n != 1 {
		t.Errorf("MOB-6: rows from u-noor to unassigned (NULL) = %d, want 1", n)
	}
	if n := e.count("assignee_change", "issue_id = ? AND from_account_id = 'u-iris' AND to_account_id = 'u-silas'", e.issueID("MOB-2")); n != 1 {
		t.Errorf("MOB-2: rows u-iris to u-silas = %d, want 1", n)
	}

	// to unassigned and back
	at := e.clock.Now().Add(20 * time.Minute)
	e.reassign("MOB-3", "", at)
	e.reassign("MOB-3", "u-silas", at.Add(time.Minute))
	e.mustOK(e.sync(ModeIncremental))
	rows := e.qCol(`SELECT COALESCE(from_account_id, '<null>') || '>' || COALESCE(to_account_id, '<null>') FROM assignee_change WHERE issue_id = ? AND changed_at >= ? ORDER BY changed_at`, e.issueID("MOB-3"), ms(at))
	if !equalStrings(rows, []string{"u-noor><null>", "<null>>u-silas"}) {
		t.Errorf("MOB-3 assignee changes = %v, want u-noor to unassigned, then unassigned to u-silas", rows)
	}
	if got := e.qStr(`SELECT assignee_account_id FROM issue WHERE key = 'MOB-3'`); got != "u-silas" {
		t.Errorf("MOB-3 current assignee = %q", got)
	}
}

func TestApply_ConfigHistory_RowOnlyWhenTheFingerprintChanges(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	rows := func() int { return e.count("standup_config_history", "standup_id = ?", e.sid) }

	e.mustOK(e.sync(ModeIncremental))
	t1 := ms(e.T())
	if rows() != 1 {
		t.Fatalf("config history rows after the first sync = %d, want 1", rows())
	}
	e.mustOK(e.sync(ModeIncremental))
	if rows() != 1 {
		t.Errorf("a re-sync without config changes added a row (%d)", rows())
	}

	if _, err := e.st.SetLaneLimit(ctx, e.sid, model.LaneQA, 7); err != nil {
		t.Fatalf("SetLaneLimit: %v", err)
	}
	e.mustOK(e.sync(ModeIncremental))
	t3 := ms(e.T())
	if rows() != 2 {
		t.Fatalf("config history rows after changing a lane limit = %d, want 2", rows())
	}
	if got := e.qInt(`SELECT valid_to FROM standup_config_history WHERE standup_id = ? AND valid_from = ?`, e.sid, t1); got != t3 {
		t.Errorf("first row valid_to = %d, want T3 = %d", got, t3)
	}
	if !e.qIsNull(`SELECT valid_to FROM standup_config_history WHERE standup_id = ? AND valid_from = ?`, e.sid, t3) {
		t.Errorf("the new row (valid_from T3) is not open")
	}
	if a, b := e.qStr(`SELECT fingerprint FROM standup_config_history WHERE standup_id = ? AND valid_from = ?`, e.sid, t1),
		e.qStr(`SELECT fingerprint FROM standup_config_history WHERE standup_id = ? AND valid_from = ?`, e.sid, t3); a == b || a == "" {
		t.Errorf("fingerprints %q and %q must differ", a, b)
	}
	var doc struct {
		Limits map[string]int `json:"limits"`
	}
	if err := json.Unmarshal([]byte(e.qStr(`SELECT config_json FROM standup_config_history WHERE standup_id = ? AND valid_from = ?`, e.sid, t3)), &doc); err != nil || doc.Limits["qa"] != 7 {
		t.Errorf("new config_json limits = %v (err %v), want qa 7", doc.Limits, err)
	}
	if got := e.count("standup_config_history", "standup_id = ? AND valid_to IS NULL", e.sid); got != 1 {
		t.Errorf("open config rows = %d, want exactly 1", got)
	}

	// saving the same value twice adds nothing
	if _, err := e.st.SetLaneLimit(ctx, e.sid, model.LaneQA, 7); err != nil {
		t.Fatalf("SetLaneLimit: %v", err)
	}
	e.mustOK(e.sync(ModeIncremental))
	if rows() != 2 {
		t.Errorf("saving the same lane limit again added a row (%d)", rows())
	}

	// excluding a speaker changes the speaking order config
	if _, err := e.st.SetSpeakerIncluded(ctx, e.sid, "u-noor", false); err != nil {
		t.Fatalf("SetSpeakerIncluded: %v", err)
	}
	e.mustOK(e.sync(ModeIncremental))
	if rows() != 3 {
		t.Errorf("config history rows after excluding a speaker = %d, want 3", rows())
	}
}

func TestApply_ColumnMapChange_RebuildsStatusLanesAndRecordsNewConfig(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeIncremental))
	qaStatus := e.qStr(`SELECT status_id FROM issue WHERE key = 'MOB-3'`)
	e.srv.Mutate(func(ds *jiratest.Dataset) {
		b := &ds.Boards[0]
		var cols []jiratest.FxColumn
		for _, c := range b.Columns {
			if c.Name == "QA" {
				continue
			}
			if c.Name == "Code Review" {
				c.Statuses = append(append([]string{}, c.Statuses...), "QA")
			}
			cols = append(cols, c)
		}
		b.Columns = cols
	})
	e.em.reset()
	e.mustOK(e.sync(ModeIncremental))

	cols := e.qCol(`SELECT column_name FROM standup_column_map WHERE standup_id = ? ORDER BY position`, e.sid)
	if !equalStrings(cols, []string{"To Do", "In Progress", "Code Review", "Done"}) {
		t.Errorf("standup_column_map = %v, want the QA column gone", cols)
	}
	if got := e.qStr(`SELECT lane || '/' || column_name FROM standup_status_lane WHERE standup_id = ? AND status_id = ?`, e.sid, qaStatus); got != "review/Code Review" {
		t.Errorf("QA status now maps to %q, want review/Code Review", got)
	}
	if n := e.count("board_column", "connection_id = ? AND board_id = ?", e.connID, e.boardID); n != 4 {
		t.Errorf("board_column rows = %d, want 4", n)
	}
	if n := e.count("standup_config_history", "standup_id = ?", e.sid); n != 2 {
		t.Errorf("config history rows = %d, want 2 (the status lanes changed)", n)
	}
	var whats []string
	for _, d := range e.em.dataChanged() {
		whats = append(whats, d.What)
	}
	if !contains(whats, "config") || !contains(whats, "view") {
		t.Errorf("data:changed whats = %v, want view and config", whats)
	}
	if got := e.members(e.sid); len(got) != 10 {
		t.Errorf("members = %d, want 10", len(got))
	} else {
		for _, m := range got {
			if m.Key == "MOB-3" && m.Lane != model.LaneReview {
				t.Errorf("MOB-3 lane = %q, want review", m.Lane)
			}
		}
	}
}

func TestApply_Speakers_AppendedFromBoardAssigneesOnly(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.mustOK(e.sync(ModeIncremental))
	order := e.qCol(`SELECT account_id FROM standup_speaker WHERE standup_id = ? ORDER BY position`, e.sid)
	if len(order) != 3 {
		t.Fatalf("speakers after the first sync = %v, want 3", order)
	}
	if _, err := e.st.SetSpeakerIncluded(ctx, e.sid, "u-noor", false); err != nil {
		t.Fatalf("SetSpeakerIncluded: %v", err)
	}

	e.srv.Mutate(func(ds *jiratest.Dataset) {
		ds.Users = append(ds.Users,
			jiratest.FxUser{AccountID: "u-nia", DisplayName: "Nia Novak"},
			jiratest.FxUser{AccountID: "u-cora", DisplayName: "Cora Bell"},
			jiratest.FxUser{AccountID: "u-bea", DisplayName: "Bea Backlog"})
		for i := range ds.Users {
			if ds.Users[i].AccountID == "u-iris" {
				ds.Users[i].DisplayName = "Iris R. Raman"
			}
		}
	})
	at := e.clock.Now().Add(30 * time.Minute)
	e.reassign("MOB-6", "u-nia", at)   // board ticket, new assignee: becomes a speaker
	e.reassign("MOB-101", "u-bea", at) // backlog ticket: assignee is NOT a speaker
	e.addComment("MOB-3", "u-cora", at)
	e.mutateIssue("MOB-1", func(ds *jiratest.Dataset, is *jiratest.DSIssue) { is.Updated = at }) // re-hydrate: u-iris's new display name
	e.mustOK(e.sync(ModeIncremental))
	t2 := ms(e.T())

	got := e.qCol(`SELECT account_id FROM standup_speaker WHERE standup_id = ? ORDER BY position`, e.sid)
	want := append(append([]string{}, order...), "u-nia")
	if !equalStrings(got, want) {
		t.Errorf("speakers in order = %v, want the old order %v then u-nia appended (no u-bea, no u-cora, no unassigned)", got, want)
	}
	if got := e.qStr(`SELECT included || '|' || added_manually FROM standup_speaker WHERE standup_id = ? AND account_id = 'u-nia'`, e.sid); got != "1|0" {
		t.Errorf("u-nia included|manual = %s, want 1|0", got)
	}
	if got := e.qStr(`SELECT included || '|' || user_touched FROM standup_speaker WHERE standup_id = ? AND account_id = 'u-noor'`, e.sid); got != "0|1" {
		t.Errorf("u-noor included|user_touched = %s, want the exclusion kept (0|1)", got)
	}
	if got := e.qStr(`SELECT display_name FROM standup_speaker WHERE standup_id = ? AND account_id = 'u-iris'`, e.sid); got != "Iris R. Raman" {
		t.Errorf("speaker display_name = %q, want it refreshed", got)
	}
	if got := e.qStr(`SELECT display_name FROM person WHERE connection_id = ? AND account_id = 'u-iris'`, e.connID); got != "Iris R. Raman" {
		t.Errorf("person display_name = %q, want it refreshed", got)
	}
	if got := e.qInt(`SELECT last_seen_at FROM standup_speaker WHERE standup_id = ? AND account_id = 'u-nia'`, e.sid); got != t2 {
		t.Errorf("u-nia last_seen_at = %d, want T2", got)
	}
	if n := e.count("standup_speaker", "account_id IN ('u-bea', 'u-cora', 'unassigned')"); n != 0 {
		t.Errorf("%d speakers were added from backlog assignees, comment authors or the unassigned pseudo person", n)
	}
}

func TestApply_SyncState_IncrementalKeepsFullTimestampsAndEachSuccessBumpsRev(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	rev0 := e.rev(e.sid)
	e.mustOK(e.sync(ModeIncremental))
	t1 := ms(e.T())
	e.mustOK(e.sync(ModeIncremental))
	t2 := ms(e.T())
	if got := e.rev(e.sid); got != rev0+2 {
		t.Errorf("rev = %d, want %d: every successful run bumps it", got, rev0+2)
	}
	q := func(col string) int64 {
		return e.qInt(`SELECT `+col+` FROM standup_sync_state WHERE standup_id = ?`, e.sid)
	}
	if q("last_success_at") != t2 || q("last_attempt_at") != t2 {
		t.Errorf("last_success_at/last_attempt_at = %d/%d, want T2 = %d", q("last_success_at"), q("last_attempt_at"), t2)
	}
	if q("last_full_at") != t1 || q("history_from") != t1 {
		t.Errorf("last_full_at/history_from = %d/%d, want T1 = %d (incremental runs keep them)", q("last_full_at"), q("history_from"), t1)
	}
	if q("needs_sync") != 0 {
		t.Errorf("needs_sync = %d", q("needs_sync"))
	}

	// a failed run changes the attempt time only
	e.srv.SetUnauthorized(true)
	e.sync(ModeIncremental)
	t3 := ms(e.T())
	if q("last_success_at") != t2 || q("last_attempt_at") != t3 {
		t.Errorf("after a failed run last_success_at/last_attempt_at = %d/%d, want %d/%d", q("last_success_at"), q("last_attempt_at"), t2, t3)
	}
	if got := e.rev(e.sid); got != rev0+2 {
		t.Errorf("rev = %d after a failed run, want %d", got, rev0+2)
	}
}

func TestApply_ArchivingAStandup_KeepsSpansHistoryAndRecords(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeIncremental))
	e.leaveSprint("MOB-2", e.clock.Now().Add(30*time.Minute))
	e.mustOK(e.sync(ModeIncremental))
	tables := []string{"issue", "status_change", "assignee_change", "comment", "dev_commit", "dev_pr_event", "standup_issue_span",
		"standup_issue", "standup_config_history", "standup_speaker", "sync_run"}
	before := map[string]int{}
	for _, tbl := range tables {
		before[tbl] = e.count(tbl, "")
	}
	if _, err := e.st.Archive(context.Background(), e.sid); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if e.qIsNull(`SELECT deleted_at FROM standup WHERE id = ?`, e.sid) {
		t.Fatalf("Archive did not set deleted_at")
	}
	for _, tbl := range tables {
		if got := e.count(tbl, ""); got != before[tbl] {
			t.Errorf("%s rows = %d after archiving, want %d (nothing is purged)", tbl, got, before[tbl])
		}
	}
}

// ================================================================================================================
// reconstruction (testing.md §2 "History and retention")

func TestApply_TenFakeDays_ReconstructedDayMatchesWhatTheViewShowed(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{srv: jiratest.Options{DevStatus: "404"}})
	ctx := context.Background()
	const days = 10
	at := func() time.Time { return e.clock.Now().Add(23*time.Hour + 30*time.Minute) } // before the next day's run

	script := map[int]func(){
		1: func() { e.moveIssue("MOB-1", "Code Review", at()) },
		2: func() { e.reassign("MOB-2", "u-noor", at()) },
		3: func() {
			e.setFlag("MOB-3", true, at())
			e.moveIssue("MOB-4", "In Progress", at())
			if _, err := e.st.SetColumnLane(ctx, e.sid, "QA", model.LaneReview); err != nil { // the QA column is now walked as review
				t.Fatalf("SetColumnLane: %v", err)
			}
		},
		4: func() { e.leaveSprint("MOB-2", at()); e.reassign("MOB-6", "u-iris", at()) },
		5: func() { e.moveIssue("MOB-3", "Done", at()); e.joinSprint("MOB-2", at()) },
		6: func() { e.moveIssue("MOB-5", "In Progress", at()) },
		7: func() { e.setFlag("MOB-7", false, at()) },
		8: func() { e.moveIssue("MOB-1", "Done", at()); e.leaveSprint("MOB-6", at()) },
		9: func() { e.leaveSprint("MOB-4", at()); e.reassign("MOB-2", "", at()) },
	}

	viewDay := make([][]memberRow, days)
	runTime := make([]time.Time, days)
	for d := 0; d < days; d++ {
		if d > 0 {
			script[d]() // changes made during day d-1's evening and night
		}
		advance := 24 * time.Hour
		if d == 0 {
			advance = time.Hour
		}
		e.mustOK(e.syncAfter(advance, ModeIncremental))
		runTime[d] = e.T()
		viewDay[d] = e.members(e.sid) // what the view is built from, right after the run
	}

	// The script really happened (the comparison below cannot pass vacuously).
	find := func(d int, key string) (memberRow, bool) {
		for _, m := range viewDay[d] {
			if m.Key == key {
				return m, true
			}
		}
		return memberRow{}, false
	}
	if _, ok := find(3, "MOB-2"); !ok {
		t.Errorf("setup: MOB-2 should be a member on day 3")
	}
	if _, ok := find(4, "MOB-2"); ok {
		t.Errorf("setup: MOB-2 left the sprint on day 4, but the day 4 view still has it")
	}
	if m, _ := find(4, "MOB-6"); m.Assignee != "u-iris" || m.Role != model.RoleBoard {
		t.Errorf("setup: day 4 MOB-6 = %+v, want assigned to u-iris on the board", m)
	}
	if m, _ := find(3, "MOB-6"); m.Assignee != "" {
		t.Errorf("setup: day 3 MOB-6 = %+v, want unassigned", m)
	}
	if m, _ := find(2, "MOB-3"); m.Lane != model.LaneQA {
		t.Errorf("setup: day 2 MOB-3 lane = %q, want qa", m.Lane)
	}
	if m, _ := find(4, "MOB-3"); m.Lane != model.LaneReview {
		t.Errorf("setup: day 4 MOB-3 lane = %q, want review (QA column remapped on day 3)", m.Lane)
	}
	if m, _ := find(9, "MOB-6"); m.Role != model.RoleBacklog {
		t.Errorf("setup: day 9 MOB-6 = %+v, want a backlog member", m)
	}

	for d := 0; d < days; d++ {
		d := d
		t.Run(fmt.Sprintf("day %d", d), func(t *testing.T) {
			defer recoverStub(t)
			got, err := history.Reconstruct(ctx, e.db, e.sid, runTime[d].Add(12*time.Hour))
			if err != nil {
				t.Fatalf("Reconstruct: %v", err)
			}
			var rows []memberRow
			for _, m := range got {
				rows = append(rows, memberRow{Key: m.Key, Role: m.Role, Lane: m.Lane, Assignee: m.Assignee})
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
			if fmt.Sprint(rows) != fmt.Sprint(viewDay[d]) {
				t.Errorf("reconstructed membership differs from what the view showed on day %d\n got:  %v\n want: %v", d, rows, viewDay[d])
			}
		})
	}
}

// ================================================================================================================
// direct apply: hand-built Fetched, no network

type denv struct {
	t        *testing.T
	db       *store.DB
	clk      *fakeClock
	st       *settings.Store
	o        *Orchestrator
	connID   int64
	sids     []int64
	cfg      model.BoardConfig
	statuses []model.Status
}

func newDirectEnv(t *testing.T, standups int) *denv {
	t.Helper()
	clk := &fakeClock{now: baseTime(), loc: chicago}
	db := storetest.Open(t, clk.Now)
	d := &denv{t: t, db: db, clk: clk, st: settings.New(db, clk.Now)}
	d.o = &Orchestrator{opts: Options{DB: db, Clock: clk, Logger: discardLogger()}}
	storetest.Exec(t, db, `INSERT INTO connection (source, base_url, account, account_id, display_name, auth_state, created_at)
		VALUES ('jira', 'https://acme.atlassian.net', 'me@acme.com', 'u-me', 'Me', 'ok', ?)`, ms(clk.Now()))
	d.connID = qInt(t, db, `SELECT id FROM connection`)
	d.statuses = []model.Status{
		{ID: "1", Name: "To Do", Category: "new"}, {ID: "2", Name: "In Progress", Category: "indeterminate"},
		{ID: "3", Name: "Code Review", Category: "indeterminate"}, {ID: "4", Name: "QA", Category: "indeterminate"},
		{ID: "5", Name: "Done", Category: "done"},
	}
	d.cfg = model.BoardConfig{
		Board: model.Board{ID: "1", Name: "MOB board", Kind: "scrum", RawType: "scrum", SpaceID: "10001",
			Meta: map[string]any{"filterId": 9001, "subQuery": "", "rankFieldId": "customfield_10019"}},
		Columns: []model.Column{
			{Name: "To Do", Position: 0, StatusIDs: []string{"1"}}, {Name: "In Progress", Position: 1, StatusIDs: []string{"2"}},
			{Name: "Code Review", Position: 2, StatusIDs: []string{"3"}}, {Name: "QA", Position: 3, StatusIDs: []string{"4"}},
			{Name: "Done", Position: 4, StatusIDs: []string{"5"}},
		},
	}
	ctx := context.Background()
	for i := 0; i < standups; i++ {
		id, err := d.st.Create(ctx)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := d.st.SetSpace(ctx, id, model.Space{ID: "10001", Key: "MOB", Name: "Saffron Mobile"}); err != nil {
			t.Fatalf("SetSpace: %v", err)
		}
		if _, err := d.st.SetBoard(ctx, id, d.cfg, d.statuses); err != nil {
			t.Fatalf("SetBoard: %v", err)
		}
		d.sids = append(d.sids, id)
	}
	return d
}

func (d *denv) apply(f *Fetched) ApplyResult {
	d.t.Helper()
	res, err := d.o.apply(context.Background(), f)
	if err != nil {
		d.t.Fatalf("apply: %v", err)
	}
	return res
}

func (d *denv) qInt(q string, args ...any) int64  { d.t.Helper(); return qInt(d.t, d.db, q, args...) }
func (d *denv) qStr(q string, args ...any) string { d.t.Helper(); return qStr(d.t, d.db, q, args...) }
func (d *denv) qNull(q string, args ...any) bool  { d.t.Helper(); return qIsNull(d.t, d.db, q, args...) }
func (d *denv) qCol(q string, args ...any) []string {
	d.t.Helper()
	return qCol(d.t, d.db, q, args...)
}
func (d *denv) count(table, where string, args ...any) int {
	d.t.Helper()
	return storetest.Count(d.t, d.db, table, where, args...)
}

func (d *denv) after(base time.Time, n int) time.Time { return base.Add(time.Duration(n) * time.Hour) }

// fetched builds a first-run payload for the standup: four issues.
//
//	10001 MOB-1 In Progress  assignee p1  board 0   comment by p4, 1 status change, dev events
//	10002 MOB-2 Code Review  assignee p2  board 1   flagged, assignee change "" -> p2, 2 status changes
//	10003 MOB-3 To Do        unassigned   board 2   assignee change p1 -> ""
//	10004 MOB-4 To Do        assignee p3  backlog 0
func (d *denv) fetched(sid int64, T time.Time) *Fetched {
	day := 24 * time.Hour
	// Every event timestamp is anchored to a fixed instant (the first T the tests use), so that applying the same
	// payload at a later T presents the same facts: only StartedAt moves.
	A := baseTime().Add(time.Hour)
	p1 := model.Person{AccountID: "p1", DisplayName: "Iris Calloway", Active: true, Kind: "user"}
	p2 := model.Person{AccountID: "p2", DisplayName: "Silas Marchetti", Active: true, Kind: "user"}
	p3 := model.Person{AccountID: "p3", DisplayName: "Noor Abadi", Active: true, Kind: "user"}
	p4 := model.Person{AccountID: "p4", DisplayName: "Cora Bell", Active: true, Kind: "user"}
	story := model.IssueType{ID: "3", Name: "Story", Kind: "story"}
	bug := model.IssueType{ID: "1", Name: "Bug", Kind: "bug"}
	sprint42 := model.Sprint{ID: "42", BoardID: "1", Name: "Sprint 42", State: "active", StartAt: A.Add(-13 * day), EndAt: A.Add(day)}
	sprint41 := model.Sprint{ID: "41", BoardID: "1", Name: "Sprint 41", State: "closed", StartAt: A.Add(-27 * day), EndAt: A.Add(-14 * day), CompleteAt: A.Add(-14 * day)}
	mk := func(srcID, key, summary string, typ model.IssueType, st model.Status, who *model.Person, rank string, flagged bool) model.IssueDetail {
		is := model.Issue{SourceID: srcID, Key: key, SpaceID: "10001", Summary: summary, TypeID: typ.ID, StatusID: st.ID,
			Flagged: flagged, Rank: rank, SprintIDs: []string{"42"}, CreatedAt: A.Add(-10 * day), UpdatedAt: A.Add(-time.Hour)}
		if who != nil {
			is.AssigneeID = who.AccountID
		}
		return model.IssueDetail{Issue: is, Type: typ, Status: st, Assignee: who, Sprints: []model.Sprint{sprint42}}
	}
	d1 := mk("10001", "MOB-1", "First", story, d.statuses[1], &p1, "0|a", false)
	d1.StatusChanges = []model.StatusChange{{ID: "h1", At: A.Add(-2 * day), From: "1", To: "2"}}
	d1.Comments = []model.Comment{{ID: "c1", At: A.Add(-day), AuthorID: "p4"}}
	d1.CommentTotal = 1
	d1.People = []model.Person{p1, p4}
	d2 := mk("10002", "MOB-2", "Second", bug, d.statuses[2], &p2, "0|b", true)
	d2.StatusChanges = []model.StatusChange{{ID: "h2", At: A.Add(-4 * day), From: "1", To: "2"}, {ID: "h3", At: A.Add(-day), From: "2", To: "3"}}
	d2.AssigneeChanges = []model.AssigneeChange{{ID: "a1", At: A.Add(-5 * day), From: "", To: "p2"}}
	d2.FlagChanges = []model.FlagChange{{ID: "f1", At: A.Add(-day), Flagged: true}}
	d2.People = []model.Person{p2}
	d3 := mk("10003", "MOB-3", "Third", story, d.statuses[0], nil, "0|c", false)
	d3.AssigneeChanges = []model.AssigneeChange{{ID: "a2", At: A.Add(-3 * day), From: "p1", To: ""}}
	d4 := mk("10004", "MOB-4", "Fourth", story, d.statuses[0], &p3, "0|d", false)
	d4.People = []model.Person{p3}

	return &Fetched{
		RunID: 1, StandupID: sid, ConnectionID: d.connID, StartedAt: T, Mode: ModeFull,
		Board: d.cfg, Sprints: []model.Sprint{sprint42, sprint41}, Statuses: d.statuses,
		Types: []model.IssueType{story, bug}, People: []model.Person{p1, p2, p3, p4},
		SourceMeta: map[string]any{"sprintFieldId": "customfield_10020"}, DevState: "ok",
		Members: []Member{
			{SourceID: "10001", Key: "MOB-1", Role: model.RoleBoard, RankIndex: 0, UpdatedAt: d1.UpdatedAt},
			{SourceID: "10002", Key: "MOB-2", Role: model.RoleBoard, RankIndex: 1, UpdatedAt: d2.UpdatedAt},
			{SourceID: "10003", Key: "MOB-3", Role: model.RoleBoard, RankIndex: 2, UpdatedAt: d3.UpdatedAt},
			{SourceID: "10004", Key: "MOB-4", Role: model.RoleBacklog, RankIndex: 0, UpdatedAt: d4.UpdatedAt},
		},
		Details: []model.IssueDetail{d1, d2, d3, d4},
		Dev: map[string]model.DevActivity{"10001": {Fingerprint: "fp1", Changed: true, Events: []model.DevEvent{
			{Kind: model.DevCommit, ID: "aaa111", Repo: "svc-api", At: A.Add(-day)},
			{Kind: model.DevCommit, ID: "bbb222", Repo: "svc-api", At: A.Add(-2 * time.Hour), Merge: true},
			{Kind: model.DevPR, ID: "svc-api#12", Repo: "svc-api", At: A.Add(-time.Hour), Status: "OPEN"},
		}}},
		DevChecked:   []string{"10001", "10002"},
		BacklogTotal: 17,
	}
}

func (d *denv) noDetailsFetched(sid int64, T time.Time) *Fetched {
	f := d.fetched(sid, T)
	f.Details, f.Dev, f.DevChecked, f.Mode = nil, nil, nil, ModeIncremental
	return f
}

func TestApplyDirect_FirstApply_WritesRecordsSpansConfigAndState(t *testing.T) {
	defer recoverStub(t)
	d := newDirectEnv(t, 1)
	sid := d.sids[0]
	T := d.clk.Now().Add(time.Hour)
	T0 := ms(T)
	rev0 := d.qInt(`SELECT rev FROM standup WHERE id = ?`, sid)
	res := d.apply(d.fetched(sid, T))

	if res.Rev != rev0+1 || d.qInt(`SELECT rev FROM standup WHERE id = ?`, sid) != rev0+1 {
		t.Errorf("rev = %d (result %d), want %d", d.qInt(`SELECT rev FROM standup WHERE id = ?`, sid), res.Rev, rev0+1)
	}
	if !res.ConfigChanged {
		t.Errorf("ConfigChanged = false on a first apply that appended speakers")
	}
	if res.Hydrated != 4 {
		t.Errorf("Hydrated = %d, want 4", res.Hydrated)
	}

	// issues
	if n := d.count("issue", ""); n != 4 {
		t.Fatalf("issue rows = %d, want 4", n)
	}
	issueRow := func(key string) string {
		return d.qStr(`SELECT source_id || '|' || space_id || '|' || summary || '|' || issue_type_id || '|' || status_id || '|' ||
			COALESCE(assignee_account_id, '<null>') || '|' || flagged || '|' || COALESCE(rank, '') || '|' || sprint_ids || '|' ||
			comment_total || '|' || hydrated_at || '|' || last_seen_at FROM issue WHERE key = ?`, key)
	}
	for key, want := range map[string]string{
		"MOB-1": fmt.Sprintf(`10001|10001|First|3|2|p1|0|0|a|["42"]|1|%d|%d`, T0, T0),
		"MOB-2": fmt.Sprintf(`10002|10001|Second|1|3|p2|1|0|b|["42"]|0|%d|%d`, T0, T0),
		"MOB-3": fmt.Sprintf(`10003|10001|Third|3|1|<null>|0|0|c|["42"]|0|%d|%d`, T0, T0),
		"MOB-4": fmt.Sprintf(`10004|10001|Fourth|3|1|p3|0|0|d|["42"]|0|%d|%d`, T0, T0),
	} {
		if got := issueRow(key); got != want {
			t.Errorf("%s row = %s\n want %s", key, got, want)
		}
	}
	if d.qInt(`SELECT created_at FROM issue WHERE key = 'MOB-1'`) != ms(T.Add(-240*time.Hour)) ||
		d.qInt(`SELECT updated_at FROM issue WHERE key = 'MOB-1'`) != ms(T.Add(-time.Hour)) {
		t.Errorf("MOB-1 created_at/updated_at not stored from the source")
	}
	if n := d.count("issue", "removed_at IS NOT NULL"); n != 0 {
		t.Errorf("%d issues have removed_at", n)
	}

	// change tables
	if n := d.count("status_change", ""); n != 3 {
		t.Errorf("status_change rows = %d, want 3", n)
	}
	if got := d.qStr(`SELECT from_status_id || '>' || to_status_id || '@' || changed_at FROM status_change WHERE history_id = 'h3'`); got != fmt.Sprintf("2>3@%d", ms(T.Add(-24*time.Hour))) {
		t.Errorf("status_change h3 = %s", got)
	}
	if got := d.qCol(`SELECT history_id || ':' || COALESCE(from_account_id, '<null>') || '>' || COALESCE(to_account_id, '<null>') FROM assignee_change ORDER BY history_id`); !equalStrings(got, []string{"a1:<null>>p2", "a2:p1><null>"}) {
		t.Errorf("assignee_change rows = %v, want a1 NULL>p2 and a2 p1>NULL", got)
	}
	if got := d.qStr(`SELECT history_id || ':' || flagged FROM flag_change`); got != "f1:1" {
		t.Errorf("flag_change = %s, want f1:1", got)
	}
	if got := d.qStr(`SELECT comment_id || ':' || author_account_id || '@' || created_at FROM comment`); got != fmt.Sprintf("c1:p4@%d", ms(T.Add(-24*time.Hour))) {
		t.Errorf("comment = %s (metadata only: no body column exists)", got)
	}

	// dev
	if got := d.qCol(`SELECT commit_id || ':' || repo || ':' || is_merge FROM dev_commit ORDER BY commit_id`); !equalStrings(got, []string{"aaa111:svc-api:0", "bbb222:svc-api:1"}) {
		t.Errorf("dev_commit = %v", got)
	}
	if got := d.qStr(`SELECT pr_key || ':' || status || '@' || at FROM dev_pr_event`); got != fmt.Sprintf("svc-api#12:OPEN@%d", ms(T.Add(-time.Hour))) {
		t.Errorf("dev_pr_event = %s", got)
	}
	if got := d.qStr(`SELECT COALESCE(dev_fingerprint, '<null>') || '@' || COALESCE(dev_checked_at, 0) FROM issue WHERE key = 'MOB-1'`); got != fmt.Sprintf("fp1@%d", T0) {
		t.Errorf("MOB-1 dev fingerprint/checked = %s", got)
	}
	if got := d.qStr(`SELECT COALESCE(dev_fingerprint, '<null>') || '@' || COALESCE(dev_checked_at, 0) FROM issue WHERE key = 'MOB-2'`); got != fmt.Sprintf("<null>@%d", T0) {
		t.Errorf("MOB-2 (checked, no change reported) dev fingerprint/checked = %s, want NULL fingerprint and dev_checked_at = T", got)
	}
	if got := d.qStr(`SELECT COALESCE(dev_checked_at, 0) FROM issue WHERE key = 'MOB-3'`); got != "0" {
		t.Errorf("MOB-3 was not checked but has dev_checked_at %s", got)
	}

	// membership, spans
	mem := d.qCol(`SELECT i.key || ':' || si.role || ':' || si.rank_index FROM standup_issue si JOIN issue i ON i.id = si.issue_id WHERE si.standup_id = ? ORDER BY i.key`, sid)
	if !equalStrings(mem, []string{"MOB-1:board:0", "MOB-2:board:1", "MOB-3:board:2", "MOB-4:backlog:0"}) {
		t.Errorf("standup_issue = %v", mem)
	}
	spans := d.qCol(`SELECT i.key || ':' || s.role || ':' || s.valid_from || ':' || COALESCE(s.valid_to, 'open') FROM standup_issue_span s JOIN issue i ON i.id = s.issue_id WHERE s.standup_id = ? ORDER BY i.key`, sid)
	var wantSpans []string
	for key, role := range map[string]string{"MOB-1": "board", "MOB-2": "board", "MOB-3": "board", "MOB-4": "backlog"} {
		wantSpans = append(wantSpans, fmt.Sprintf("%s:%s:%d:open", key, role, T0))
	}
	sort.Strings(wantSpans)
	if !equalStrings(spans, wantSpans) {
		t.Errorf("spans = %v, want %v", spans, wantSpans)
	}

	// speakers: board assignees only
	sp := d.qCol(`SELECT account_id || ':' || display_name || ':' || included || ':' || added_manually || ':' || last_seen_at FROM standup_speaker WHERE standup_id = ? ORDER BY position`, sid)
	if !equalStrings(sp, []string{fmt.Sprintf("p1:Iris Calloway:1:0:%d", T0), fmt.Sprintf("p2:Silas Marchetti:1:0:%d", T0)}) {
		t.Errorf("speakers = %v, want p1 and p2 only (p3 is a backlog assignee, p4 only commented, unassigned is never a speaker)", sp)
	}

	// config history
	if n := d.count("standup_config_history", "standup_id = ?", sid); n != 1 {
		t.Fatalf("config history rows = %d, want 1", n)
	}
	cfgText := d.qStr(`SELECT config_json FROM standup_config_history WHERE standup_id = ?`, sid)
	assertConfigJSON(t, cfgText, []string{"qa", "review", "prog", "todo"}, 5, 2, []string{"42"})
	if d.qStr(`SELECT fingerprint FROM standup_config_history WHERE standup_id = ?`, sid) != sha256hex([]byte(cfgText)) {
		t.Errorf("fingerprint is not sha256(config_json)")
	}
	if d.qInt(`SELECT valid_from FROM standup_config_history WHERE standup_id = ?`, sid) != T0 || !d.qNull(`SELECT valid_to FROM standup_config_history WHERE standup_id = ?`, sid) {
		t.Errorf("config history row is not open from T")
	}

	// sync state
	for col, want := range map[string]int64{"needs_sync": 0, "last_success_at": T0, "last_full_at": T0, "history_from": T0, "backlog_total": 17} {
		if got := d.qInt(`SELECT `+col+` FROM standup_sync_state WHERE standup_id = ?`, sid); got != want {
			t.Errorf("standup_sync_state.%s = %d, want %d", col, got, want)
		}
	}
	if got := d.qStr(`SELECT active_sprint_ids FROM standup_sync_state WHERE standup_id = ?`, sid); got != `["42"]` {
		t.Errorf("active_sprint_ids = %s, want [\"42\"] (the closed sprint 41 is not active)", got)
	}

	// reference data and connection
	if n := d.count("sprint", "connection_id = ?", d.connID); n != 2 {
		t.Errorf("sprint rows = %d, want 2 (active and closed)", n)
	}
	if got := d.qStr(`SELECT state FROM sprint WHERE connection_id = ? AND id = '41'`, d.connID); got != "closed" {
		t.Errorf("sprint 41 state = %q", got)
	}
	if n := d.count("person", "connection_id = ?", d.connID); n != 4 {
		t.Errorf("person rows = %d, want 4", n)
	}
	if got := d.qStr(`SELECT display_name || ':' || active || ':' || COALESCE(account_type, '') FROM person WHERE account_id = 'p4'`); got != "Cora Bell:1:user" {
		t.Errorf("person p4 = %s", got)
	}
	if n := d.count("status", "connection_id = ?", d.connID); n != 5 {
		t.Errorf("status rows = %d, want 5", n)
	}
	if got := d.qStr(`SELECT category FROM status WHERE id = '5'`); got != "done" {
		t.Errorf("status 5 category = %q", got)
	}
	if n := d.count("issue_type", "connection_id = ?", d.connID); n != 2 {
		t.Errorf("issue_type rows = %d, want 2", n)
	}
	if got := d.qStr(`SELECT source_meta FROM connection WHERE id = ?`, d.connID); got != `{"sprintFieldId":"customfield_10020"}` {
		t.Errorf("connection.source_meta = %s", got)
	}
	if got := d.qStr(`SELECT dev_activity_state || '@' || dev_activity_checked_at FROM connection WHERE id = ?`, d.connID); got != fmt.Sprintf("ok@%d", T0) {
		t.Errorf("connection dev state = %s, want ok@T", got)
	}
	if got := d.qStr(`SELECT board_name || ':' || board_type FROM standup WHERE id = ?`, sid); got != "MOB board:scrum" {
		t.Errorf("standup board snapshot = %s", got)
	}
}

func TestApplyDirect_FailureRollsBackEverything(t *testing.T) {
	defer recoverStub(t)
	d := newDirectEnv(t, 1)
	before := storetest.Checksum(t, d.db, historyTables...)
	metaBefore := d.qStr(`SELECT source_meta FROM connection WHERE id = ?`, d.connID)
	f := d.fetched(d.sids[0], d.clk.Now().Add(time.Hour))
	f.StandupID = 987654 // does not exist: the apply must fail, and nothing it wrote before failing may stay
	if _, err := d.o.apply(context.Background(), f); err == nil {
		t.Fatalf("apply for an unknown standup returned nil")
	}
	if after := storetest.Checksum(t, d.db, historyTables...); after != before {
		t.Errorf("a failed apply left rows behind (one transaction, all or nothing)")
	}
	if got := d.qStr(`SELECT source_meta FROM connection WHERE id = ?`, d.connID); got != metaBefore {
		t.Errorf("connection.source_meta = %s after a failed apply, want %s", got, metaBefore)
	}
	if n := d.count("issue", ""); n != 0 {
		t.Errorf("issue rows = %d after a failed apply", n)
	}
}

func TestApplyDirect_IdenticalReapply_ChangesNoSpansConfigOrRows(t *testing.T) {
	defer recoverStub(t)
	d := newDirectEnv(t, 1)
	sid := d.sids[0]
	T1 := d.clk.Now().Add(time.Hour)
	d.apply(d.fetched(sid, T1))
	T2 := d.after(T1, 1)
	res := d.apply(d.fetched(sid, T2))

	if res.ConfigChanged {
		t.Errorf("ConfigChanged = true for an identical re-apply")
	}
	if n := d.count("standup_config_history", "standup_id = ?", sid); n != 1 {
		t.Errorf("config history rows = %d, want 1", n)
	}
	if n := d.count("standup_issue_span", "standup_id = ?", sid); n != 4 {
		t.Errorf("span rows = %d, want 4 (an unchanged member keeps its span)", n)
	}
	if n := d.count("standup_issue_span", "standup_id = ? AND valid_from = ? AND valid_to IS NULL", sid, ms(T1)); n != 4 {
		t.Errorf("open spans from T1 = %d, want 4", n)
	}
	for tbl, want := range map[string]int{"status_change": 3, "assignee_change": 2, "flag_change": 1, "comment": 1, "dev_commit": 2, "dev_pr_event": 1, "issue": 4} {
		if got := d.count(tbl, ""); got != want {
			t.Errorf("%s rows = %d, want %d", tbl, got, want)
		}
	}
	if got := d.qInt(`SELECT rev FROM standup WHERE id = ?`, sid); got != res.Rev {
		t.Errorf("rev = %d, ApplyResult.Rev = %d", got, res.Rev)
	}
	if got := d.qInt(`SELECT last_success_at FROM standup_sync_state WHERE standup_id = ?`, sid); got != ms(T2) {
		t.Errorf("last_success_at = %d, want T2", got)
	}
	if got := d.qInt(`SELECT history_from FROM standup_sync_state WHERE standup_id = ?`, sid); got != ms(T1) {
		t.Errorf("history_from = %d, want T1 (first success only)", got)
	}
}

func TestApplyDirect_UnhydratedMembers_KeepTheirRowsButAreSeen(t *testing.T) {
	defer recoverStub(t)
	d := newDirectEnv(t, 1)
	sid := d.sids[0]
	T1 := d.clk.Now().Add(time.Hour)
	d.apply(d.fetched(sid, T1))
	T2 := d.after(T1, 1)
	res := d.apply(d.noDetailsFetched(sid, T2))
	if res.Hydrated != 0 {
		t.Errorf("Hydrated = %d, want 0", res.Hydrated)
	}
	if n := d.count("issue", "hydrated_at = ?", ms(T1)); n != 4 {
		t.Errorf("issues keeping hydrated_at = T1: %d, want 4", n)
	}
	if n := d.count("issue", "last_seen_at = ?", ms(T2)); n != 4 {
		t.Errorf("issues with last_seen_at = T2: %d, want 4 (every scanned member)", n)
	}
	if got := d.qInt(`SELECT last_full_at FROM standup_sync_state WHERE standup_id = ?`, sid); got != ms(T1) {
		t.Errorf("last_full_at = %d, want T1: an incremental apply does not touch it", got)
	}
	if got := d.qStr(`SELECT COALESCE(dev_fingerprint, '<null>') FROM issue WHERE key = 'MOB-1'`); got != "fp1" {
		t.Errorf("MOB-1 dev_fingerprint = %s, want fp1 kept", got)
	}
	if got := d.qInt(`SELECT dev_checked_at FROM issue WHERE key = 'MOB-1'`); got != ms(T1) {
		t.Errorf("MOB-1 dev_checked_at = %d, want T1 (not re-checked)", got)
	}
	// and a later full apply moves last_full_at
	T3 := d.after(T2, 1)
	d.apply(d.fetched(d.sids[0], T3))
	if got := d.qInt(`SELECT last_full_at FROM standup_sync_state WHERE standup_id = ?`, sid); got != ms(T3) {
		t.Errorf("last_full_at = %d, want T3", got)
	}
}

func TestApplyDirect_RehydrationWithFewerEntries_NeverDeletesRows(t *testing.T) {
	defer recoverStub(t)
	d := newDirectEnv(t, 1)
	sid := d.sids[0]
	T1 := d.clk.Now().Add(time.Hour)
	d.apply(d.fetched(sid, T1))

	T2 := d.after(T1, 1)
	f := d.fetched(sid, T2)
	for i := range f.Details {
		det := &f.Details[i]
		det.StatusChanges, det.AssigneeChanges, det.FlagChanges, det.Comments = nil, nil, nil, nil
		det.CommentTotal = 0
	}
	f.Details[0].StatusChanges = []model.StatusChange{{ID: "h9", At: T2.Add(-time.Minute), From: "2", To: "3"}}
	f.Details[0].Issue.StatusID = "3"
	f.Details[0].Status = d.statuses[2]
	f.Dev = map[string]model.DevActivity{"10001": {Fingerprint: "fp2", Changed: true, Events: []model.DevEvent{
		{Kind: model.DevCommit, ID: "ccc333", Repo: "svc-api", At: T2.Add(-time.Minute)},
		{Kind: model.DevPR, ID: "svc-api#12", Repo: "svc-api", At: T2.Add(-time.Minute), Status: "MERGED"},
	}}}
	d.apply(f)

	for tbl, want := range map[string]int{"status_change": 4, "assignee_change": 2, "flag_change": 1, "comment": 1, "dev_commit": 3, "dev_pr_event": 2} {
		if got := d.count(tbl, ""); got != want {
			t.Errorf("%s rows = %d, want %d (old rows kept, new ones appended)", tbl, got, want)
		}
	}
	if got := d.qStr(`SELECT dev_fingerprint FROM issue WHERE key = 'MOB-1'`); got != "fp2" {
		t.Errorf("dev_fingerprint = %q, want fp2", got)
	}

	// the same PR event delivered again (same pr_key, status, at) is ignored; a new status is a new row
	T3 := d.after(T2, 1)
	f2 := d.fetched(sid, T3)
	f2.Dev = map[string]model.DevActivity{"10001": {Fingerprint: "fp2", Changed: true, Events: []model.DevEvent{
		{Kind: model.DevPR, ID: "svc-api#12", Repo: "svc-api", At: T2.Add(-time.Minute), Status: "MERGED"},
		{Kind: model.DevPR, ID: "svc-api#12", Repo: "svc-api", At: T3.Add(-time.Minute), Status: "DECLINED"},
		{Kind: model.DevPR, ID: "svc-api#13", Repo: "svc-api", At: T3.Add(-time.Minute), Status: "WEIRD"},
	}}}
	d.apply(f2)
	if got := d.count("dev_pr_event", ""); got != 4 {
		t.Errorf("dev_pr_event rows = %d, want 4 (one duplicate ignored, two new)", got)
	}
	if got := d.qStr(`SELECT status FROM dev_pr_event WHERE pr_key = 'svc-api#13'`); got != "UNKNOWN" {
		t.Errorf("unknown PR status stored as %q, want UNKNOWN", got)
	}
}

func TestApplyDirect_MemberLeavesReturnsAndChangesRole(t *testing.T) {
	defer recoverStub(t)
	d := newDirectEnv(t, 1)
	sid := d.sids[0]
	T1 := d.clk.Now().Add(time.Hour)
	d.apply(d.fetched(sid, T1))
	rowsBefore := d.count("status_change", "") + d.count("comment", "") + d.count("dev_commit", "")

	// T2: MOB-2 leaves, MOB-3 moves from board to backlog
	T2 := d.after(T1, 1)
	f := d.noDetailsFetched(sid, T2)
	f.Members = []Member{f.Members[0], {SourceID: "10003", Key: "MOB-3", Role: model.RoleBacklog, RankIndex: 0}, {SourceID: "10004", Key: "MOB-4", Role: model.RoleBacklog, RankIndex: 1}}
	d.apply(f)
	span := func(key string) []string {
		return d.qCol(`SELECT s.role || ':' || s.valid_from || ':' || COALESCE(s.valid_to, 'open') FROM standup_issue_span s JOIN issue i ON i.id = s.issue_id
			WHERE s.standup_id = ? AND i.key = ? ORDER BY s.valid_from, s.role`, sid, key)
	}
	if got := span("MOB-2"); !equalStrings(got, []string{fmt.Sprintf("board:%d:%d", ms(T1), ms(T2))}) {
		t.Errorf("MOB-2 spans = %v", got)
	}
	if got := d.qInt(`SELECT removed_at FROM issue WHERE key = 'MOB-2'`); got != ms(T2) {
		t.Errorf("MOB-2 removed_at = %d, want T2", got)
	}
	if got := span("MOB-3"); !equalStrings(got, []string{fmt.Sprintf("board:%d:%d", ms(T1), ms(T2)), fmt.Sprintf("backlog:%d:open", ms(T2))}) {
		t.Errorf("MOB-3 spans = %v, want the board span closed and a backlog span opened at T2", got)
	}
	if !d.qNull(`SELECT removed_at FROM issue WHERE key = 'MOB-3'`) {
		t.Errorf("MOB-3 changed role but is still a member: removed_at must be NULL")
	}
	if got := d.qCol(`SELECT i.key FROM standup_issue si JOIN issue i ON i.id = si.issue_id WHERE si.standup_id = ? ORDER BY i.key`, sid); !equalStrings(got, []string{"MOB-1", "MOB-3", "MOB-4"}) {
		t.Errorf("standup_issue = %v", got)
	}
	if got := d.qStr(`SELECT role || ':' || rank_index FROM standup_issue si JOIN issue i ON i.id = si.issue_id WHERE si.standup_id = ? AND i.key = 'MOB-4'`, sid); got != "backlog:1" {
		t.Errorf("MOB-4 = %s, want backlog:1 (rank_index follows the new scan)", got)
	}

	// T3: MOB-2 returns
	T3 := d.after(T2, 1)
	f3 := d.noDetailsFetched(sid, T3)
	d.apply(f3)
	if got := span("MOB-2"); !equalStrings(got, []string{fmt.Sprintf("board:%d:%d", ms(T1), ms(T2)), fmt.Sprintf("board:%d:open", ms(T3))}) {
		t.Errorf("MOB-2 spans = %v, want two spans (three applies: in, out, in)", got)
	}
	if !d.qNull(`SELECT removed_at FROM issue WHERE key = 'MOB-2'`) {
		t.Errorf("MOB-2 removed_at not cleared when it returned")
	}
	if got := d.count("status_change", "") + d.count("comment", "") + d.count("dev_commit", ""); got != rowsBefore {
		t.Errorf("history rows changed across leave and return: %d -> %d", rowsBefore, got)
	}
	if n := d.count("standup_issue_span", "standup_id = ? AND valid_to IS NULL", sid); n != 4 {
		t.Errorf("open spans = %d, want 4 (one per member)", n)
	}
}

func TestApplyDirect_TwoStandupsSharingAnIssue_RemovedAtOnlyWhenNoStandupHasIt(t *testing.T) {
	defer recoverStub(t)
	d := newDirectEnv(t, 2)
	a, b := d.sids[0], d.sids[1]
	T1 := d.clk.Now().Add(time.Hour)
	d.apply(d.fetched(a, T1))
	d.apply(d.fetched(b, T1))

	without2 := func(sid int64, T time.Time) *Fetched {
		f := d.noDetailsFetched(sid, T)
		var m []Member
		for _, x := range f.Members {
			if x.Key != "MOB-2" {
				m = append(m, x)
			}
		}
		f.Members = m
		return f
	}
	T2 := d.after(T1, 1)
	d.apply(without2(a, T2))
	if !d.qNull(`SELECT removed_at FROM issue WHERE key = 'MOB-2'`) {
		t.Errorf("removed_at set although standup B still has MOB-2 as a member")
	}
	if got := d.count("standup_issue_span", "standup_id = ? AND valid_to = ?", a, ms(T2)); got != 1 {
		t.Errorf("standup A closed spans at T2 = %d, want 1", got)
	}
	if got := d.count("standup_issue_span", "standup_id = ? AND valid_to IS NOT NULL", b); got != 0 {
		t.Errorf("standup B lost a span because standup A applied: %d closed", got)
	}
	if got := d.count("standup_issue", "standup_id = ?", b); got != 4 {
		t.Errorf("standup B members = %d, want 4", got)
	}

	T3 := d.after(T2, 1)
	d.apply(without2(b, T3))
	if got := d.qInt(`SELECT removed_at FROM issue WHERE key = 'MOB-2'`); got != ms(T3) {
		t.Errorf("removed_at = %d, want T3: B's span was the last open one", got)
	}

	T4 := d.after(T3, 1)
	d.apply(d.noDetailsFetched(a, T4))
	if !d.qNull(`SELECT removed_at FROM issue WHERE key = 'MOB-2'`) {
		t.Errorf("removed_at not cleared when standup A took MOB-2 back")
	}
	if got := d.count("standup_issue_span", "standup_id = ? AND issue_id = (SELECT id FROM issue WHERE key = 'MOB-2')", a); got != 2 {
		t.Errorf("standup A spans for MOB-2 = %d, want 2", got)
	}
	if got := d.count("standup_issue_span", "standup_id = ? AND issue_id = (SELECT id FROM issue WHERE key = 'MOB-2')", b); got != 1 {
		t.Errorf("standup B spans for MOB-2 = %d, want 1", got)
	}
}

func TestApplyDirect_ConfigHistory_FingerprintDrivenAndClosesThePreviousRow(t *testing.T) {
	defer recoverStub(t)
	d := newDirectEnv(t, 1)
	sid := d.sids[0]
	ctx := context.Background()
	T1 := d.clk.Now().Add(time.Hour)
	d.apply(d.fetched(sid, T1))
	T2 := d.after(T1, 1)
	d.apply(d.noDetailsFetched(sid, T2))
	if n := d.count("standup_config_history", "standup_id = ?", sid); n != 1 {
		t.Fatalf("rows after an unchanged apply = %d, want 1", n)
	}

	if _, err := d.st.SetLaneLimit(ctx, sid, model.LaneProg, 9); err != nil {
		t.Fatalf("SetLaneLimit: %v", err)
	}
	T3 := d.after(T2, 1)
	d.apply(d.noDetailsFetched(sid, T3))
	rows := d.qCol(`SELECT valid_from || '-' || COALESCE(valid_to, 'open') FROM standup_config_history WHERE standup_id = ? ORDER BY valid_from`, sid)
	if !equalStrings(rows, []string{fmt.Sprintf("%d-%d", ms(T1), ms(T3)), fmt.Sprintf("%d-open", ms(T3))}) {
		t.Errorf("config history = %v, want [T1-T3, T3-open]", rows)
	}

	// a new active sprint is part of the config as well
	T4 := d.after(T3, 1)
	f := d.noDetailsFetched(sid, T4)
	f.Sprints = []model.Sprint{{ID: "43", BoardID: "1", Name: "Sprint 43", State: "active"}}
	d.apply(f)
	if n := d.count("standup_config_history", "standup_id = ?", sid); n != 3 {
		t.Errorf("config history rows after the active sprint changed = %d, want 3", n)
	}
	if got := d.qStr(`SELECT active_sprint_ids FROM standup_sync_state WHERE standup_id = ?`, sid); got != `["43"]` {
		t.Errorf("active_sprint_ids = %s", got)
	}
}

func TestApplyDirect_ColumnMapMerge_RebuildsStatusLanesAndReportsConfigChanged(t *testing.T) {
	defer recoverStub(t)
	d := newDirectEnv(t, 1)
	sid := d.sids[0]
	T1 := d.clk.Now().Add(time.Hour)
	d.apply(d.fetched(sid, T1))

	T2 := d.after(T1, 1)
	f := d.noDetailsFetched(sid, T2)
	f.BoardChanged = true
	f.Board.Columns = []model.Column{
		{Name: "To Do", Position: 0, StatusIDs: []string{"1"}}, {Name: "Doing", Position: 1, StatusIDs: []string{"2"}},
		{Name: "Code Review", Position: 2, StatusIDs: []string{"3", "4"}}, {Name: "Done", Position: 3, StatusIDs: []string{"5"}},
	}
	f.Board.Board.Name = "Renamed board"
	res := d.apply(f)
	if !res.ConfigChanged {
		t.Errorf("ConfigChanged = false after the column map changed")
	}
	if got := d.qCol(`SELECT column_name FROM standup_column_map WHERE standup_id = ? ORDER BY position`, sid); !equalStrings(got, []string{"To Do", "Doing", "Code Review", "Done"}) {
		t.Errorf("standup_column_map = %v", got)
	}
	if got := d.qCol(`SELECT status_id || '=' || lane FROM standup_status_lane WHERE standup_id = ? ORDER BY status_id`, sid); !equalStrings(got, []string{"1=todo", "2=prog", "3=review", "4=review", "5=done"}) {
		t.Errorf("standup_status_lane = %v, want QA (4) merged into the review column", got)
	}
	if n := d.count("board_column", "connection_id = ? AND board_id = '1'", d.connID); n != 4 {
		t.Errorf("board_column rows = %d, want 4", n)
	}
	if got := d.qStr(`SELECT board_name FROM standup WHERE id = ?`, sid); got != "Renamed board" {
		t.Errorf("standup.board_name = %q, want the new snapshot", got)
	}
	if n := d.count("standup_config_history", "standup_id = ?", sid); n != 2 {
		t.Errorf("config history rows = %d, want 2 (status lanes changed)", n)
	}
}

func TestApplyDirect_SpeakerPruneKeepsTouchedManualAndRecentSpeakers(t *testing.T) {
	defer recoverStub(t)
	d := newDirectEnv(t, 1)
	sid := d.sids[0]
	T := d.clk.Now().Add(time.Hour)
	old := ms(T.Add(-31 * 24 * time.Hour))
	recent := ms(T.Add(-24 * time.Hour))
	for i, row := range []struct {
		id              string
		manual, touched int
		seen            int64
	}{{"p9", 0, 0, old}, {"p8", 0, 1, old}, {"p7", 1, 0, old}, {"p6", 0, 0, recent}} {
		storetest.Exec(t, d.db, `INSERT INTO standup_speaker (standup_id, account_id, display_name, position, included, added_manually, user_touched, last_seen_at, created_at)
			VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?)`, sid, row.id, "Old "+row.id, i, row.manual, row.touched, row.seen, old)
	}
	d.apply(d.fetched(sid, T))
	got := d.qCol(`SELECT account_id FROM standup_speaker WHERE standup_id = ? ORDER BY position`, sid)
	if !equalStrings(got, []string{"p8", "p7", "p6", "p1", "p2"}) {
		t.Errorf("speakers in order = %v, want p9 (auto, untouched, unseen 31 days) pruned, p8 p7 p6 kept in place and p1 p2 appended", got)
	}
}
