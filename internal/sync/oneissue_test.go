package sync

// Tests for the one-issue apply of WP-T6 (jira-sync.md §4.8, architecture.md §3.2 "Light writes" steps 3-4, ADR 0015):
// ApplyIssue after a write or a ticket Refresh, and ApplyIssueDelta when the refresh failed.
//
// SPEC-NOTE: ApplyIssue bumps the rev of every LIVE standup that has the issue as a current member, also when the stored
// issue row was newer and was skipped (the merged change and comment rows may have changed what the standup shows).
// SPEC-NOTE: ApplyIssue and ApplyIssueDelta never emit; the caller (internal/app) emits ticket:updated and data:changed.
// SPEC-NOTE: after a refresh whose changelog was stale (jiratest StaleChangelog) the real entry reaches the DB with the
// next FULL sync: an incremental scan compares updated times and the refreshed row already carries the new one.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/source/jira"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
	"github.com/crueber/cuisle/internal/store/storetest"
)

// oiEnv is a synced mini-fixture environment plus a direct source for the writes.
func oiEnv(t *testing.T, srv jiratest.Options) (*env, source.Source) {
	t.Helper()
	e := newEnv(t, envOpts{srv: srv, standups: 2})
	e.mustOK(e.sync(ModeFull))
	e.mustOK(e.syncStandup(e.sids[1], time.Hour, ModeFull))
	e.em.reset()
	return e, oiOpen(t, e)
}

// oiOpen opens an adapter the way a ticket write does: with the meta the sync saved (the discovered Flagged field).
func oiOpen(t *testing.T, e *env) source.Source {
	t.Helper()
	var meta map[string]any
	if err := json.Unmarshal([]byte(e.qStr(`SELECT source_meta FROM connection WHERE id = ?`, e.connID)), &meta); err != nil {
		t.Fatalf("source_meta: %v", err)
	}
	s, err := jira.New(model.Connection{ID: e.connID, Source: jira.Kind, BaseURL: e.srv.URL, Account: e.email},
		jiratest.Token, source.Options{Logger: discardLogger(), Meta: meta})
	if err != nil {
		t.Fatalf("jira.New: %v", err)
	}
	return s
}

func checksumTables(t *testing.T, e *env, tables ...string) string {
	t.Helper()
	return storetest.Checksum(t, e.db, tables...)
}

func oiSourceID(t *testing.T, e *env, key string) string {
	t.Helper()
	return e.qStr(`SELECT source_id FROM issue WHERE key = ?`, key)
}

func oiRefresh(t *testing.T, e *env, src source.Source, key string) IssueApplied {
	t.Helper()
	d, err := src.GetIssue(context.Background(), oiSourceID(t, e, key))
	if err != nil {
		t.Fatalf("GetIssue(%s): %v", key, err)
	}
	res, err := e.o.ApplyIssue(context.Background(), e.connID, d)
	if err != nil {
		t.Fatalf("ApplyIssue(%s): %v", key, err)
	}
	return res
}

func TestApplyIssue_AssignRecordsTheProvidersChangelogEntryAndBumpsEveryContainingStandup(t *testing.T) {
	defer recoverStub(t)
	e, src := oiEnv(t, jiratest.Options{})
	id := e.issueID("MOB-1")
	revA, revB := e.rev(e.sids[0]), e.rev(e.sids[1])
	assigns := e.count("assignee_change", "issue_id = ?", id)

	if err := src.Assign(context.Background(), oiSourceID(t, e, "MOB-1"), "u-noor"); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	res := oiRefresh(t, e, src, "MOB-1")

	if res.IssueID != id || res.Key != "MOB-1" || res.Skipped || res.StatusChanged {
		t.Errorf("result = %+v", res)
	}
	if !equalStrings(res.Changed, []string{ChangedAssignee}) {
		t.Errorf("Changed = %v, want [assignee]", res.Changed)
	}
	if len(res.StandupIDs) != 2 || res.StandupIDs[0] != e.sids[0] || res.StandupIDs[1] != e.sids[1] {
		t.Errorf("StandupIDs = %v, want both standups %v", res.StandupIDs, e.sids)
	}
	if got := e.qStr(`SELECT assignee_account_id FROM issue WHERE id = ?`, id); got != "u-noor" {
		t.Errorf("assignee = %q, want u-noor", got)
	}
	if n := e.count("assignee_change", "issue_id = ?", id); n != assigns+1 {
		t.Errorf("assignee_change rows = %d, want %d (the provider's entry recorded)", n, assigns+1)
	}
	if hid := e.qStr(`SELECT history_id FROM assignee_change WHERE issue_id = ? ORDER BY changed_at DESC, history_id DESC LIMIT 1`, id); hid != "w1" {
		t.Errorf("newest history_id = %q, want Jira's w1 (not a locally invented id)", hid)
	}
	if e.rev(e.sids[0]) != revA+1 || e.rev(e.sids[1]) != revB+1 {
		t.Errorf("revs = %d/%d, want %d/%d", e.rev(e.sids[0]), e.rev(e.sids[1]), revA+1, revB+1)
	}
	if n := len(e.em.all()); n != 0 {
		t.Errorf("ApplyIssue emitted %v; the caller emits", e.em.names())
	}
}

func TestApplyIssue_TransitionRecordsStatusChangeAndDropsTheTransitionCache(t *testing.T) {
	defer recoverStub(t)
	e, src := oiEnv(t, jiratest.Options{})
	id := e.issueID("MOB-4")
	e.db.W.ExecContext(context.Background(), `INSERT INTO transition_cache (issue_id, json, fetched_at) VALUES (?, '[]', 1)`, id)
	trs, err := src.Transitions(context.Background(), oiSourceID(t, e, "MOB-4"))
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	var pick model.Transition
	for _, tr := range trs {
		if !tr.NeedsScreen && tr.ToStatusName == "QA" {
			pick = tr
		}
	}
	if pick.ID == "" {
		t.Fatalf("no QA transition in %+v", trs)
	}
	before := e.count("status_change", "issue_id = ?", id)
	if err := src.Transition(context.Background(), oiSourceID(t, e, "MOB-4"), pick.ID); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	res := oiRefresh(t, e, src, "MOB-4")
	if !res.StatusChanged || !equalStrings(res.Changed, []string{ChangedStatus}) {
		t.Errorf("result = %+v, want StatusChanged and [status]", res)
	}
	if got := e.qStr(`SELECT status_id FROM issue WHERE id = ?`, id); got != pick.ToStatusID {
		t.Errorf("status_id = %q, want %q", got, pick.ToStatusID)
	}
	if n := e.count("status_change", "issue_id = ?", id); n != before+1 {
		t.Errorf("status_change rows = %d, want %d", n, before+1)
	}
	if n := e.count("transition_cache", "issue_id = ?", id); n != 0 {
		t.Errorf("transition_cache rows = %d, want 0 after a status change", n)
	}
}

func TestApplyIssue_FlagAndCommentWrites(t *testing.T) {
	defer recoverStub(t)
	e, src := oiEnv(t, jiratest.Options{})
	id := e.issueID("MOB-2")
	sid := oiSourceID(t, e, "MOB-2")

	if err := src.SetFlag(context.Background(), sid, true); err != nil {
		t.Fatalf("SetFlag: %v", err)
	}
	res := oiRefresh(t, e, src, "MOB-2")
	if !equalStrings(res.Changed, []string{ChangedFlag}) {
		t.Errorf("flag: Changed = %v, want [flag]", res.Changed)
	}
	if e.qInt(`SELECT flagged FROM issue WHERE id = ?`, id) != 1 || e.count("flag_change", "issue_id = ? AND flagged = 1 AND history_id LIKE 'w%'", id) != 1 {
		t.Errorf("flag not applied or its change row not recorded")
	}

	comments := e.count("comment", "issue_id = ?", id)
	cid, err := src.AddComment(context.Background(), sid, model.NewComment{Text: "ship it"})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	res = oiRefresh(t, e, src, "MOB-2")
	if !equalStrings(res.Changed, []string{ChangedComments}) {
		t.Errorf("comment: Changed = %v, want [comments]", res.Changed)
	}
	if n := e.count("comment", "issue_id = ?", id); n != comments+1 {
		t.Errorf("comment rows = %d, want %d", n, comments+1)
	}
	if e.qIsNull(`SELECT body_adf FROM comment WHERE issue_id = ? AND comment_id = ?`, id, cid) {
		t.Errorf("the new comment was stored without its body")
	}
}

func TestApplyIssue_DoesNotTouchMembershipSpansConfigHistoryOrSyncState(t *testing.T) {
	defer recoverStub(t)
	e, src := oiEnv(t, jiratest.Options{})
	tables := []string{"standup_issue", "standup_issue_span", "standup_config_history", "standup_sync_state", "standup_speaker", "sync_run"}
	before := checksumTables(t, e, tables...)
	if err := src.Assign(context.Background(), oiSourceID(t, e, "MOB-4"), "u-silas"); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	oiRefresh(t, e, src, "MOB-4")
	if after := checksumTables(t, e, tables...); after != before {
		t.Errorf("membership, spans, config history, speakers, sync state or runs changed by a one-issue apply")
	}
}

func TestApplyIssue_IsOneTransaction_AFailureLeavesNothingBehind(t *testing.T) {
	defer recoverStub(t)
	e, src := oiEnv(t, jiratest.Options{})
	if err := src.Assign(context.Background(), oiSourceID(t, e, "MOB-1"), "u-noor"); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	d, err := src.GetIssue(context.Background(), oiSourceID(t, e, "MOB-1"))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	before := e.checksum()
	revs := e.rev(e.sids[0])
	// An unknown connection id violates the issue foreign key at the first write: nothing may be applied.
	if _, err := e.o.ApplyIssue(context.Background(), e.connID+999, d); err == nil {
		t.Fatalf("ApplyIssue with an unknown connection succeeded")
	}
	if e.checksum() != before || e.rev(e.sids[0]) != revs {
		t.Errorf("a failed ApplyIssue left changes behind")
	}
}

func TestApplyIssue_AnOlderFetchNeverOverwritesANewerRow(t *testing.T) {
	defer recoverStub(t)
	e, src := oiEnv(t, jiratest.Options{})
	id := e.issueID("MOB-1")
	sid := oiSourceID(t, e, "MOB-1")
	d, err := src.GetIssue(context.Background(), sid)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	// A newer row was written by an earlier refresh; the incoming copy is an older snapshot.
	e.db.W.ExecContext(context.Background(), `UPDATE issue SET summary = 'Newer local', updated_at = ? WHERE id = ?`, ms(d.UpdatedAt.Add(time.Hour)), id)
	rev := e.rev(e.sids[0])
	res, err := e.o.ApplyIssue(context.Background(), e.connID, d)
	if err != nil {
		t.Fatalf("ApplyIssue: %v", err)
	}
	if !res.Skipped {
		t.Errorf("Skipped = false for an older incoming issue")
	}
	if got := e.qStr(`SELECT summary FROM issue WHERE id = ?`, id); got != "Newer local" {
		t.Errorf("summary = %q: the older fetch overwrote the newer row", got)
	}
	if e.rev(e.sids[0]) != rev+1 {
		t.Errorf("rev = %d, want %d (change and comment rows may have been merged)", e.rev(e.sids[0]), rev+1)
	}
}

func TestApplyIssue_ArchivedStandupsAreNotBumped(t *testing.T) {
	defer recoverStub(t)
	e, src := oiEnv(t, jiratest.Options{})
	if _, err := e.st.Archive(context.Background(), e.sids[1]); err != nil {
		t.Fatalf("archive standup: %v", err)
	}
	revLive, revArchived := e.rev(e.sids[0]), e.rev(e.sids[1])
	if err := src.Assign(context.Background(), oiSourceID(t, e, "MOB-1"), "u-noor"); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	res := oiRefresh(t, e, src, "MOB-1")
	if len(res.StandupIDs) != 1 || res.StandupIDs[0] != e.sids[0] {
		t.Errorf("StandupIDs = %v, want only the live standup %d", res.StandupIDs, e.sids[0])
	}
	if e.rev(e.sids[0]) != revLive+1 || e.rev(e.sids[1]) != revArchived {
		t.Errorf("revs = %d/%d, want %d/%d", e.rev(e.sids[0]), e.rev(e.sids[1]), revLive+1, revArchived)
	}
}

func TestApplyIssue_NewPeopleAndTypesInTheRefreshAreSaved(t *testing.T) {
	defer recoverStub(t)
	e, src := oiEnv(t, jiratest.Options{})
	e.db.W.ExecContext(context.Background(), `DELETE FROM person WHERE account_id = 'u-noor'`)
	if err := src.Assign(context.Background(), oiSourceID(t, e, "MOB-1"), "u-noor"); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	oiRefresh(t, e, src, "MOB-1")
	if got := e.qStr(`SELECT display_name FROM person WHERE account_id = 'u-noor'`); got != "Noor Abadi" {
		t.Errorf("person u-noor = %q, want Noor Abadi saved with the refresh", got)
	}
}

func TestApplyIssue_RefreshFailsAfterAWrite_DeltaAppliedAndTheNextSyncMergesTheRealChangelogOnce(t *testing.T) {
	defer recoverStub(t)
	e, src := oiEnv(t, jiratest.Options{RefreshFails: true})
	id := e.issueID("MOB-4")
	sid := oiSourceID(t, e, "MOB-4")
	trs, _ := src.Transitions(context.Background(), sid)
	var pick model.Transition
	for _, tr := range trs {
		if !tr.NeedsScreen && tr.ToStatusName == "QA" {
			pick = tr
		}
	}
	if err := src.Transition(context.Background(), sid, pick.ID); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if _, err := src.GetIssue(context.Background(), sid); err == nil {
		t.Fatalf("GetIssue succeeded; the fake should fail the refresh after the write")
	}

	changes := e.count("status_change", "issue_id = ?", id)
	updated := e.qInt(`SELECT updated_at FROM issue WHERE id = ?`, id)
	rev := e.rev(e.sids[0])
	to := pick.ToStatusID
	res, err := e.o.ApplyIssueDelta(context.Background(), id, IssueDelta{StatusID: &to})
	if err != nil {
		t.Fatalf("ApplyIssueDelta: %v", err)
	}
	if res.IssueID != id || res.Key != "MOB-4" || !res.StatusChanged || !equalStrings(res.Changed, []string{ChangedStatus}) {
		t.Errorf("delta result = %+v", res)
	}
	if got := e.qStr(`SELECT status_id FROM issue WHERE id = ?`, id); got != to {
		t.Errorf("status_id = %q, want the delta's %q", got, to)
	}
	if n := e.count("status_change", "issue_id = ?", id); n != changes {
		t.Errorf("status_change rows = %d, want %d: the delta invents no history", n, changes)
	}
	if e.qInt(`SELECT updated_at FROM issue WHERE id = ?`, id) != updated {
		t.Errorf("the delta changed updated_at")
	}
	if e.rev(e.sids[0]) != rev+1 {
		t.Errorf("rev = %d, want %d", e.rev(e.sids[0]), rev+1)
	}
	if len(e.em.all()) != 0 {
		t.Errorf("ApplyIssueDelta emitted %v", e.em.names())
	}

	// The next sync merges the real changelog entry (once).
	e.mustOK(e.sync(ModeFull))
	if n := e.count("status_change", "issue_id = ?", id); n != changes+1 {
		t.Errorf("status_change rows after the sync = %d, want %d (the real entry merged once)", n, changes+1)
	}
	if n := e.count("status_change", "issue_id = ? AND history_id = 'w1'", id); n != 1 {
		t.Errorf("history w1 rows = %d, want exactly 1", n)
	}
}

func TestApplyIssue_StaleChangelogAfterAWrite_RowAppliedEntryArrivesWithTheNextFullSync(t *testing.T) {
	defer recoverStub(t)
	e, src := oiEnv(t, jiratest.Options{StaleChangelog: true})
	id := e.issueID("MOB-1")
	sid := oiSourceID(t, e, "MOB-1")
	assigns := e.count("assignee_change", "issue_id = ?", id)
	if err := src.Assign(context.Background(), sid, "u-noor"); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	res := oiRefresh(t, e, src, "MOB-1")
	if got := e.qStr(`SELECT assignee_account_id FROM issue WHERE id = ?`, id); got != "u-noor" {
		t.Errorf("assignee = %q: the refreshed row must carry the new assignee", got)
	}
	if n := e.count("assignee_change", "issue_id = ?", id); n != assigns {
		t.Errorf("assignee_change rows = %d, want %d (the stale changelog omitted the entry)", n, assigns)
	}
	_ = res

	e.srv.Mutate(func(ds *jiratest.Dataset) {}) // "until the next Mutate": the entry is served again
	e.mustOK(e.sync(ModeFull))
	if n := e.count("assignee_change", "issue_id = ?", id); n != assigns+1 {
		t.Errorf("assignee_change rows after the full sync = %d, want %d", n, assigns+1)
	}
}

func TestApplyIssueDelta_AssigneeFlagAndErrors(t *testing.T) {
	defer recoverStub(t)
	e, _ := oiEnv(t, jiratest.Options{})
	id := e.issueID("MOB-1")
	before := len(e.requests())
	none, on := "", true
	res, err := e.o.ApplyIssueDelta(context.Background(), id, IssueDelta{AssigneeID: &none, Flagged: &on})
	if err != nil {
		t.Fatalf("ApplyIssueDelta: %v", err)
	}
	if res.StatusChanged || !equalStrings(res.Changed, []string{ChangedAssignee, ChangedFlag}) {
		t.Errorf("result = %+v, want [assignee flag] and no status change", res)
	}
	if !e.qIsNull(`SELECT assignee_account_id FROM issue WHERE id = ?`, id) || e.qInt(`SELECT flagged FROM issue WHERE id = ?`, id) != 1 {
		t.Errorf("assignee/flag not applied")
	}
	if len(e.requests()) != before {
		t.Errorf("ApplyIssueDelta made %d requests, want none", len(e.requests())-before)
	}
	if _, err := e.o.ApplyIssueDelta(context.Background(), 987654, IssueDelta{Flagged: &on}); !hasCode(err, apperr.NotFound) {
		t.Errorf("unknown issue: err = %v, want not_found", err)
	}
	// An empty delta changes nothing, bumps nothing.
	rev := e.rev(e.sids[0])
	res, err = e.o.ApplyIssueDelta(context.Background(), id, IssueDelta{})
	if err != nil || len(res.Changed) != 0 {
		t.Errorf("empty delta: %+v, %v", res, err)
	}
	if e.rev(e.sids[0]) != rev {
		t.Errorf("an empty delta bumped the rev")
	}
}
