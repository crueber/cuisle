package sync

// WP-29 tests: the orchestrator's runs, modes, phases, progress, cancellation, faults and request budgets
// (jira-sync.md §3, §4.1–4.5, §6 "Tests that must exist"; architecture.md §3.3; AGENTS.md invariants 2 and 6).
// Shared infrastructure is in harness_test.go. The golden StandupView bullet of jira-sync §6 needs internal/view and
// is NOT covered here; it belongs to the view package tests.
//
// SPEC-NOTE: A run's T (started_at, span and config-history time) is clock.Now() when Start is called. sync_run.started_at
// is T in Unix ms, and a finished run's standup_sync_state.last_success_at is T as well (the fake clock does not move
// during a run).
// SPEC-NOTE: Promotions known before the run starts (never synced, needs_sync=1) make the run `full` from the start.
// Promotions found during the run (column set changed, active sprint ids differ) turn the run full after the scan.
// Tests therefore assert the effect (sync_run.mode = full, last_full_at = T, hydrated = scanned) and only require the
// /field and /status requests for pre-known promotions (jira-sync §4.3: "full: fields, statuses").
// SPEC-NOTE: Phase names reported through sync:progress are config, scan, issues, changelog, comments, dev, apply.
// A phase change always emits at least one event for the new phase, even inside ProgressInterval; coalescing may only
// drop intermediate counts. Tests assert first appearances: config < scan < issues < apply, with changelog and dev
// (when there are dev targets) before apply. Phases 4–6 run concurrently, so no order is asserted among them.
// SPEC-NOTE: sync:finished.warnings is a non-nil slice (empty when there are none) so it encodes as [].
// SPEC-NOTE: SyncFinished.Stats.Issues is the number of distinct issues scanned; Stats.Requests equals
// sync_run.requests, which equals the number of requests the fake logged for that run (retries included).
// SPEC-NOTE: Status.State is "idle" with no run and after ok, partial and cancelled runs, "running" during a run and
// "failed" while the last run failed. Status.LastStatus is the last finished run's status, Status.ErrorCode its code.
// SPEC-NOTE: A token missing from the keychain is a failed RUN (Start returns a run id; ErrorCode auth_missing;
// connection.auth_state='missing'; `connection:auth {missing}`; zero HTTP requests). A connection already marked
// invalid refuses Start with apperr auth_invalid, creating no run and making no request (Start's contract comment).
// SPEC-NOTE: Dev activity "unavailable" is remembered in connection.dev_activity_state: an INCREMENTAL run within 24 h
// of dev_activity_checked_at makes no dev-status request but still reports warning dev_activity_unavailable; a full
// run re-probes (jira-sync §2.11). A DevActivity error that is not source.ErrUnsupported is a per-issue failure:
// warning dev_activity_partial, status partial, existing dev rows kept, and the apply still happens.
// SPEC-NOTE: Error mapping at the adapter (404 board → not_found, 403 board → forbidden, 400 search → filter_invalid)
// is relied on here (jira-sync §3); the orchestrator must carry the code into sync_run.error_code and sync:finished
// without retrying those statuses.
// SPEC-NOTE: Status() has an entry for every configured (non-draft, live) standup, including one that has never run
// (State idle, NeedsSync true, zero times).
// SPEC-NOTE: Cancel is a no-op for an idle or unknown standup (returns nil). Shutdown cancels every run and waits.
// SPEC-NOTE: The dev-target rule counts only board-role members; backlog members are never queried (jira-sync §2.11).

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
)

func (e *env) status(sid int64) Status {
	e.t.Helper()
	sts, err := e.o.Status(context.Background())
	if err != nil {
		e.t.Fatalf("Status: %v", err)
	}
	for _, s := range sts {
		if s.StandupID == sid {
			return s
		}
	}
	e.t.Fatalf("Status() has no entry for standup %d (got %d entries)", sid, len(sts))
	return Status{}
}

// ------------------------------------------------------------------------------------------------ no trigger

func TestNewAndStatus_NoTrigger_MakeZeroRequests(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	for i := 0; i < 3; i++ {
		st := e.status(e.sid)
		if st.State != "idle" {
			t.Errorf("State = %q, want idle", st.State)
		}
		if !st.NeedsSync {
			t.Errorf("NeedsSync = false for a standup that never synced")
		}
		if !st.LastSuccessAt.IsZero() {
			t.Errorf("LastSuccessAt = %v, want zero", st.LastSuccessAt)
		}
		if st.RunID != 0 {
			t.Errorf("RunID = %d, want 0", st.RunID)
		}
	}
	if n := len(e.requests()); n != 0 {
		t.Errorf("constructing the orchestrator and calling Status made %d requests, want 0", n)
	}
	if names := e.em.names(); len(names) != 0 {
		t.Errorf("events emitted without a trigger: %v", names)
	}
	if n := e.count("sync_run", ""); n != 0 {
		t.Errorf("sync_run rows = %d, want 0", n)
	}
}

func TestStatus_AfterOKRun_MergesSyncState(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	id := e.sync(ModeIncremental)
	e.mustOK(id)
	st := e.status(e.sid)
	if st.State != "idle" || st.LastStatus != StatusOK || st.NeedsSync || st.ErrorCode != "" {
		t.Errorf("Status = %+v, want idle/ok/no needs_sync/no error", st)
	}
	if !st.LastSuccessAt.Equal(e.T()) {
		t.Errorf("LastSuccessAt = %v, want %v", st.LastSuccessAt, e.T())
	}
	if !st.LastAttemptAt.Equal(e.T()) {
		t.Errorf("LastAttemptAt = %v, want %v", st.LastAttemptAt, e.T())
	}
}

// ------------------------------------------------------------------------------------------------ first run

func TestRun_FirstSyncRequestedIncremental_IsPromotedToFull(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	id := e.sync(ModeIncremental)
	r := e.mustOK(id)

	if r.Mode != ModeFull {
		t.Errorf("sync_run.mode = %q, want full (a standup that never synced is promoted)", r.Mode)
	}
	if r.Trigger != TriggerManual || r.StandupID != e.sid {
		t.Errorf("trigger/standup = %q/%d, want manual/%d", r.Trigger, r.StandupID, e.sid)
	}
	if r.StartedAt != ms(e.T()) {
		t.Errorf("started_at = %d, want T = %d", r.StartedAt, ms(e.T()))
	}
	if r.FinishedAt < r.StartedAt {
		t.Errorf("finished_at %d is before started_at %d", r.FinishedAt, r.StartedAt)
	}
	if r.Scanned != 10 || r.Hydrated != 10 {
		t.Errorf("scanned/hydrated = %d/%d, want 10/10 (7 board + 3 backlog, full hydrates all)", r.Scanned, r.Hydrated)
	}
	if r.Throttl != 0 || r.ErrorCode != "" {
		t.Errorf("throttled=%d error=%q, want 0 and none", r.Throttl, r.ErrorCode)
	}
	if len(r.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", r.Warnings)
	}
	reqs := e.requests()
	if int(r.Requests) != len(reqs) {
		t.Errorf("sync_run.requests = %d, fake logged %d", r.Requests, len(reqs))
	}
	if n := countPath(reqs, isMyself); n != 1 {
		t.Errorf("/myself requests = %d, want 1 (preflight)", n)
	}
	if countPath(reqs, isField) < 1 || countPath(reqs, isStatus) < 1 {
		t.Errorf("a full sync fetches /field and /status (field=%d status=%d)", countPath(reqs, isField), countPath(reqs, isStatus))
	}
	if got := e.qInt(`SELECT last_full_at FROM standup_sync_state WHERE standup_id = ?`, e.sid); got != ms(e.T()) {
		t.Errorf("last_full_at = %d, want %d", got, ms(e.T()))
	}

	fin := e.em.finished()
	if len(fin) != 1 {
		t.Fatalf("sync:finished events = %d, want 1", len(fin))
	}
	f := fin[0]
	if f.RunID != id || f.StandupID != e.sid || f.Status != StatusOK || f.ErrorCode != "" {
		t.Errorf("sync:finished = %+v", f)
	}
	if f.Warnings == nil {
		t.Errorf("sync:finished.Warnings is nil, want a non-nil empty slice")
	}
	if f.FinishedAt != ms(e.T()) {
		t.Errorf("sync:finished.FinishedAt = %d, want %d (fake clock)", f.FinishedAt, ms(e.T()))
	}
	if f.Stats.Issues != 10 || f.Stats.Requests != int(r.Requests) {
		t.Errorf("stats = %+v, want issues 10 and requests %d", f.Stats, r.Requests)
	}
}

func TestRun_ExplicitFullRequest_HydratesEverything(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeIncremental))
	id := e.sync(ModeFull)
	r := e.mustOK(id)
	if r.Mode != ModeFull || r.Hydrated != r.Scanned || r.Scanned == 0 {
		t.Errorf("mode=%q hydrated=%d scanned=%d, want full with hydrated == scanned", r.Mode, r.Hydrated, r.Scanned)
	}
	if got := e.qInt(`SELECT last_full_at FROM standup_sync_state WHERE standup_id = ?`, e.sid); got != ms(e.T()) {
		t.Errorf("last_full_at = %d, want %d", got, ms(e.T()))
	}
}

// ------------------------------------------------------------------------------------------------ budgets

func TestRun_IncrementalNoChange_MakesOnlyBudgetedRequestsAndHydratesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		fx   func(*testing.T) *jiratest.Fixture
	}{
		{"mini fixture", func(*testing.T) *jiratest.Fixture { return nil }},
		{"default fixture", defaultFixture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer recoverStub(t)
			e := newEnv(t, envOpts{fx: tc.fx(t)})
			e.mustOK(e.sync(ModeIncremental))
			members := e.count("standup_issue", "standup_id = ?", e.sid)
			if members == 0 {
				t.Fatalf("the first sync produced no members")
			}
			t1 := e.T()
			targets := e.devTargets(e.sid, false)
			if len(targets) == 0 {
				t.Fatalf("test setup: expected dev targets (prog/review/qa members) on the MOB board")
			}
			e.srv.ResetRequests()
			e.em.reset()

			id := e.sync(ModeIncremental)
			r := e.mustOK(id)
			if r.Mode != ModeIncremental {
				t.Fatalf("second run mode = %q, want incremental (nothing promotes it)", r.Mode)
			}
			if r.Hydrated != 0 {
				t.Errorf("hydrated = %d, want 0 (nothing changed)", r.Hydrated)
			}
			if int(r.Scanned) != members {
				t.Errorf("scanned = %d, want %d", r.Scanned, members)
			}
			if got := e.qInt(`SELECT last_full_at FROM standup_sync_state WHERE standup_id = ?`, e.sid); got != ms(t1) {
				t.Errorf("last_full_at moved to %d on an incremental run, want %d", got, ms(t1))
			}

			reqs := e.requests()
			if n := countPath(reqs, isMyself); n != 1 {
				t.Errorf("/myself = %d, want 1", n)
			}
			if n := countPath(reqs, isBoardConfig); n != 1 {
				t.Errorf("board configuration = %d, want 1", n)
			}
			if n := countPath(reqs, isSprintList); n != 1 {
				t.Errorf("active sprints = %d, want 1", n)
			}
			if n := countPath(reqs, isSearch); n < 1 || n > 3 {
				t.Errorf("search/jql = %d, want 1..3 scans and no hydrate page", n)
			}
			if n := countPath(reqs, isCount); n != 1 {
				t.Errorf("approximate-count = %d, want 1", n)
			}
			if n := countPath(reqs, isSummary); n != len(targets) {
				t.Errorf("dev summary requests = %d, want one per dev target (%d: %v)", n, len(targets), targets)
			}
			for name, pred := range map[string]func(string) bool{
				"/field": isField, "/status": isStatus, "changelog/bulkfetch": isBulk, "dev detail": isDetail,
				"comment top-up": isCommentTopUp, "per-issue changelog": isPerIssueChangelog,
			} {
				if n := countPath(reqs, pred); n != 0 {
					t.Errorf("%s requests = %d, want 0 on an incremental run with no changes", name, n)
				}
			}
			searches := countPath(reqs, isSearch)
			want := 1 + 1 + 1 + searches + 1 + len(targets)
			if len(reqs) != want {
				t.Errorf("total requests = %d, want %d (1 myself + config + sprints + %d scans + count + %d dev)", len(reqs), want, searches, len(targets))
			}
			if int(r.Requests) != len(reqs) {
				t.Errorf("sync_run.requests = %d, fake logged %d", r.Requests, len(reqs))
			}
		})
	}
}

func TestRun_StatusChange_RehydratesOnlyThatIssue(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeIncremental))
	t1 := e.T()
	before := e.count("status_change", "issue_id = ?", e.issueID("MOB-1"))
	e.moveIssue("MOB-1", "Code Review", e.clock.Now().Add(30*time.Minute))
	e.srv.ResetRequests()

	id := e.sync(ModeIncremental)
	r := e.mustOK(id)
	t2 := e.T()
	if r.Mode != ModeIncremental {
		t.Fatalf("mode = %q, want incremental", r.Mode)
	}
	if r.Hydrated != 1 || r.Scanned != 10 {
		t.Errorf("hydrated/scanned = %d/%d, want 1/10", r.Hydrated, r.Scanned)
	}
	rows := e.qCol(`SELECT key || '=' || hydrated_at FROM issue ORDER BY key`)
	for _, row := range rows {
		key, at, _ := strings.Cut(row, "=")
		want := ms(t1)
		if key == "MOB-1" {
			want = ms(t2)
		}
		if at != itoa64(want) {
			t.Errorf("%s hydrated_at = %s, want %d", key, at, want)
		}
	}
	if n := e.qInt(`SELECT count(*) FROM issue WHERE last_seen_at <> ?`, ms(t2)); n != 0 {
		t.Errorf("%d issues were scanned but did not get last_seen_at = T", n)
	}
	if got := e.count("status_change", "issue_id = ?", e.issueID("MOB-1")); got != before+1 {
		t.Errorf("MOB-1 status_change rows = %d, want %d (one new transition appended)", got, before+1)
	}
	if e.qStr(`SELECT status_id FROM issue WHERE key = 'MOB-1'`) != e.qStr(`SELECT status_id FROM issue WHERE key = 'MOB-2'`) {
		t.Errorf("MOB-1 should now share MOB-2's status (Code Review)")
	}
	if n := countPath(e.requests(), isBulk); n != 1 {
		t.Errorf("changelog bulkfetch requests = %d, want 1 (hydrated issues only)", n)
	}
}

// ------------------------------------------------------------------------------------------------ promotion (§4.1)

func TestRun_IncrementalPromotion(t *testing.T) {
	setNeedsSync := func(e *env) {
		err := e.db.WriteTx(context.Background(), func(tx *sql.Tx) error {
			return settings.SetNeedsSyncTx(context.Background(), tx, e.sid)
		})
		if err != nil {
			e.t.Fatalf("SetNeedsSyncTx: %v", err)
		}
	}
	mergeQAColumn := func(e *env) {
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
	}
	newSprint := func(e *env) {
		e.srv.Mutate(func(ds *jiratest.Dataset) {
			old := ds.Boards[0].Sprints[0].ID
			ds.Boards[0].Sprints[0].ID = old + 1
			for _, is := range ds.Issues {
				for i, sp := range is.SprintIDs {
					if sp == old {
						is.SprintIDs[i] = old + 1
					}
				}
			}
		})
	}
	tests := []struct {
		name     string
		prepare  func(e *env)
		advance  time.Duration
		mode     string
		wantFull bool
		preKnown bool // known before the run starts, so /field and /status are fetched
	}{
		{name: "needs_sync flag", prepare: setNeedsSync, advance: time.Hour, mode: ModeIncremental, wantFull: true, preKnown: true},
		{name: "last full sync older than 24h", advance: 25 * time.Hour, mode: ModeIncremental, wantFull: true, preKnown: true},
		{name: "last full sync 23h ago stays incremental", advance: 23 * time.Hour, mode: ModeIncremental},
		{name: "column to status set changed in this run", prepare: mergeQAColumn, advance: time.Hour, mode: ModeIncremental, wantFull: true},
		{name: "active sprint ids differ", prepare: newSprint, advance: time.Hour, mode: ModeIncremental, wantFull: true},
		{name: "explicit full request", advance: time.Hour, mode: ModeFull, wantFull: true, preKnown: true},
		{name: "plain incremental stays incremental", advance: time.Hour, mode: ModeIncremental},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer recoverStub(t)
			e := newEnv(t, envOpts{srv: jiratest.Options{DevStatus: "404"}})
			e.mustOK(e.sync(ModeIncremental))
			t1 := e.T()
			if tc.prepare != nil {
				tc.prepare(e)
			}
			e.srv.ResetRequests()
			id := e.syncAfter(tc.advance, tc.mode)
			r := e.mustOK(id)
			lastFull := e.qInt(`SELECT last_full_at FROM standup_sync_state WHERE standup_id = ?`, e.sid)
			if tc.wantFull {
				if r.Mode != ModeFull {
					t.Errorf("sync_run.mode = %q, want full", r.Mode)
				}
				if lastFull != ms(e.T()) {
					t.Errorf("last_full_at = %d, want T = %d", lastFull, ms(e.T()))
				}
				if r.Hydrated != r.Scanned || r.Scanned == 0 {
					t.Errorf("hydrated/scanned = %d/%d, want every scanned issue hydrated", r.Hydrated, r.Scanned)
				}
				if tc.preKnown && (countPath(e.requests(), isStatus) < 1 || countPath(e.requests(), isField) < 1) {
					t.Errorf("a pre-known full run must fetch /field and /status")
				}
			} else {
				if r.Mode != ModeIncremental {
					t.Errorf("sync_run.mode = %q, want incremental", r.Mode)
				}
				if lastFull != ms(t1) {
					t.Errorf("last_full_at = %d, want unchanged %d", lastFull, ms(t1))
				}
				if r.Hydrated != 0 {
					t.Errorf("hydrated = %d, want 0", r.Hydrated)
				}
			}
		})
	}
}

// ------------------------------------------------------------------------------------------------ one run per standup

func TestStart_OnRunningStandup_ReturnsTheSameRunID(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	g := e.gate(pathHasPrefix("/rest/api/3/myself"))
	id1, err := e.o.Start(context.Background(), e.sid, ModeFull, TriggerManual)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	g.waitReached(t)
	id2, err := e.o.Start(context.Background(), e.sid, ModeIncremental, TriggerOpen)
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if id2 != id1 {
		t.Errorf("second Start returned run %d, want the running run %d", id2, id1)
	}
	st := e.status(e.sid)
	if st.State != "running" || st.RunID != id1 {
		t.Errorf("Status while running = %+v, want running with RunID %d", st, id1)
	}
	g.Release()
	e.o.Wait(e.sid)
	if n := e.count("sync_run", "standup_id = ?", e.sid); n != 1 {
		t.Errorf("sync_run rows = %d, want 1", n)
	}
	if n := len(e.em.started()); n != 1 {
		t.Errorf("sync:started events = %d, want 1", n)
	}
	e.mustOK(id1)
	// The standup is free again: a new run gets a new id.
	id3 := e.sync(ModeIncremental)
	if id3 == id1 {
		t.Errorf("a run started after the first finished reused id %d", id1)
	}
}

func TestRun_AtMostMaxParallelStandupsAtOnce(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{standups: 3})
	g := e.gate(pathHasPrefix("/rest/api/3/myself"))
	for _, sid := range e.sids {
		if _, err := e.o.Start(context.Background(), sid, ModeFull, TriggerManual); err != nil {
			t.Fatalf("Start(%d): %v", sid, err)
		}
	}
	g.waitArrivals(t, 2)
	g.ReleaseOne(t) // one run proceeds to the end; only then may the third begin
	g.waitArrivals(t, 1)
	if _, _, maxBlocked := g.counts(); maxBlocked > 2 {
		t.Errorf("%d runs were in flight at once, want at most 2 (MaxParallel default)", maxBlocked)
	}
	g.Release()
	for _, sid := range e.sids {
		e.o.Wait(sid)
	}
	for _, f := range e.em.finished() {
		if f.Status != StatusOK {
			t.Errorf("run for standup %d finished %q (%s)", f.StandupID, f.Status, f.ErrorMessage)
		}
	}
	if n := len(e.em.finished()); n != 3 {
		t.Errorf("sync:finished events = %d, want 3", n)
	}
}

// ------------------------------------------------------------------------------------------------ events

func TestRun_Events_StartedProgressFinishedThenDataChanged(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	id := e.sync(ModeIncremental)
	e.mustOK(id)

	all := e.em.all()
	iStart, iFin := e.em.index(EvSyncStarted, 0), e.em.index(EvSyncFinished, 0)
	if iStart != 0 {
		t.Errorf("first event is %q (index %d), want sync:started first (names: %v)", all[0].Name, iStart, e.em.names())
	}
	if iFin < 0 {
		t.Fatalf("no sync:finished event (names: %v)", e.em.names())
	}
	st := e.em.started()
	if len(st) != 1 || st[0].RunID != id || st[0].StandupID != e.sid || st[0].Trigger != TriggerManual {
		t.Errorf("sync:started = %+v, want one event for run %d standup %d trigger manual", st, id, e.sid)
	}
	if st[0].Mode != ModeFull && st[0].Mode != ModeIncremental {
		t.Errorf("sync:started.Mode = %q", st[0].Mode)
	}
	for i, ev := range all {
		switch ev.Name {
		case EvSyncProgress:
			if i < iStart || i > iFin {
				t.Errorf("sync:progress at index %d outside started(%d)..finished(%d)", i, iStart, iFin)
			}
		case EvDataChanged:
			if i < iFin {
				t.Errorf("data:changed at index %d came before sync:finished (%d)", i, iFin)
			}
		}
	}
	if len(e.em.finished()) != 1 {
		t.Errorf("sync:finished count = %d, want 1", len(e.em.finished()))
	}
	var whats []string
	for _, d := range e.em.dataChanged() {
		if d.StandupID != e.sid {
			t.Errorf("data:changed standupId = %d, want %d", d.StandupID, e.sid)
		}
		whats = append(whats, d.What)
	}
	if !contains(whats, "view") {
		t.Errorf("data:changed whats = %v, want view", whats)
	}
	if !contains(whats, "config") {
		t.Errorf("data:changed whats = %v: the first sync merges the column map and speakers, so what=config is also due", whats)
	}
	if len(e.em.connAuth()) != 0 {
		t.Errorf("connection:auth emitted on a healthy run: %v", e.em.connAuth())
	}

	// A second run with nothing changed: view only, no config.
	e.em.reset()
	e.mustOK(e.sync(ModeIncremental))
	whats = nil
	for _, d := range e.em.dataChanged() {
		whats = append(whats, d.What)
	}
	if !equalStrings(whats, []string{"view"}) {
		t.Errorf("second run data:changed whats = %v, want [view]", whats)
	}
}

func TestRun_Progress_PhasesInOrderWithSaneCounts(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	id := e.sync(ModeIncremental)
	e.mustOK(id)
	prog := e.em.progress()
	first := map[string]int{}
	for i, p := range prog {
		if p.RunID != id || p.StandupID != e.sid {
			t.Errorf("progress %d has run/standup %d/%d, want %d/%d", i, p.RunID, p.StandupID, id, e.sid)
		}
		if p.Done < 0 || p.Total < 0 || p.Done > p.Total {
			t.Errorf("progress %d = %+v: want 0 <= done <= total", i, p)
		}
		if _, ok := first[p.Phase]; !ok {
			first[p.Phase] = i
		}
	}
	for _, ph := range []string{PhaseConfig, PhaseScan, source.PhaseIssues, source.PhaseChangelog, source.PhaseDev, PhaseApply} {
		if _, ok := first[ph]; !ok {
			t.Errorf("no progress event for phase %q (saw %v)", ph, first)
		}
	}
	order := []string{PhaseConfig, PhaseScan, source.PhaseIssues, PhaseApply}
	for i := 1; i < len(order); i++ {
		a, b := first[order[i-1]], first[order[i]]
		if a >= b {
			t.Errorf("phase %q first appears at %d, not before %q at %d", order[i-1], a, order[i], b)
		}
	}
	for _, ph := range []string{source.PhaseChangelog, source.PhaseDev} {
		if first[ph] >= first[PhaseApply] {
			t.Errorf("phase %q first appears at %d, want before apply (%d)", ph, first[ph], first[PhaseApply])
		}
	}
	if len(prog) == 0 || prog[len(prog)-1].Phase != PhaseApply {
		t.Errorf("last progress event should be the apply phase, got %+v", prog)
	} else if last := prog[len(prog)-1]; last.Done != last.Total {
		t.Errorf("last apply progress = %d/%d, want done == total", last.Done, last.Total)
	}
	// every scanned/hydrated total is the issue count the run reports
	for _, p := range prog {
		if p.Phase == source.PhaseIssues && p.Total != 10 {
			t.Errorf("issues progress total = %d, want 10 (full sync of the mini fixture)", p.Total)
		}
	}
}

func TestRun_FinishedEvent_IsEmittedAfterCommitAndRunFinalised(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	revBefore := e.rev(e.sid)
	e.em.setHook(func(name string, payload any) {
		if name != EvSyncFinished {
			return
		}
		f, _ := asFinished(payload)
		if n := qInt(t, e.db, `SELECT count(*) FROM standup_issue WHERE standup_id = ?`, e.sid); n == 0 {
			t.Errorf("sync:finished emitted before the apply transaction committed (no membership yet)")
		}
		if rev := qInt(t, e.db, `SELECT rev FROM standup WHERE id = ?`, e.sid); rev != revBefore+1 {
			t.Errorf("standup.rev at sync:finished = %d, want %d", rev, revBefore+1)
		}
		if st := qStr(t, e.db, `SELECT status FROM sync_run WHERE id = ?`, f.RunID); st != StatusOK {
			t.Errorf("sync_run.status at sync:finished = %q, want the final status ok", st)
		}
	})
	e.mustOK(e.sync(ModeIncremental))
	if len(e.em.finished()) != 1 {
		t.Fatalf("sync:finished events = %d, want 1", len(e.em.finished()))
	}
}

// ------------------------------------------------------------------------------------------------ cancellation (§4.4)

func TestRun_CancelMidPhase_LeavesTheDatabaseIdentical(t *testing.T) {
	for _, tc := range []struct {
		name  string
		gated string // request path prefix at which the run is held
	}{
		{"config phase", "/rest/agile/1.0/board/"},
		{"scan phase", "/rest/api/3/search/jql"},
		{"changelog phase", "/rest/api/3/changelog/bulkfetch"},
		{"dev phase", "/rest/dev-status/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer recoverStub(t)
			e := newEnv(t, envOpts{})
			before := e.checksum()
			revBefore := e.rev(e.sid)
			var metaBefore, devBefore string
			metaBefore = e.qStr(`SELECT source_meta FROM connection WHERE id = ?`, e.connID)
			devBefore = e.qStr(`SELECT dev_activity_state FROM connection WHERE id = ?`, e.connID)
			g := e.gate(pathHasPrefix(tc.gated))

			id, err := e.o.Start(context.Background(), e.sid, ModeFull, TriggerManual)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			g.waitReached(t)
			if err := e.o.Cancel(e.sid); err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			e.o.Wait(e.sid)

			r := e.run(id)
			if r.Status != StatusCancelled {
				t.Errorf("sync_run.status = %q, want cancelled", r.Status)
			}
			if r.FinishedAt == 0 {
				t.Errorf("finished_at not set on a cancelled run")
			}
			e.assertNothingApplied(before, revBefore)
			if got := e.qStr(`SELECT source_meta FROM connection WHERE id = ?`, e.connID); got != metaBefore {
				t.Errorf("connection.source_meta changed by a cancelled run: %s", got)
			}
			if got := e.qStr(`SELECT dev_activity_state FROM connection WHERE id = ?`, e.connID); got != devBefore {
				t.Errorf("connection.dev_activity_state changed by a cancelled run: %s", got)
			}
			fin := e.em.finished()
			if len(fin) != 1 || fin[0].Status != StatusCancelled || fin[0].RunID != id {
				t.Errorf("sync:finished = %+v, want exactly one cancelled for run %d", fin, id)
			}
			if len(e.em.dataChanged()) != 0 {
				t.Errorf("data:changed emitted for a cancelled run: %v", e.em.dataChanged())
			}
			if len(e.afterCalls()) != 0 {
				t.Errorf("AfterSync ran for a cancelled run")
			}
			st := e.status(e.sid)
			if st.State != "idle" || st.LastStatus != StatusCancelled {
				t.Errorf("Status = %+v, want idle with LastStatus cancelled", st)
			}

			// nothing is stuck: the next run works
			g.Release()
			e.mustOK(e.sync(ModeFull))
			if n := e.count("standup_issue", "standup_id = ?", e.sid); n != 10 {
				t.Errorf("after the cancelled run, a full sync produced %d members, want 10", n)
			}
		})
	}
}

func TestCancel_WhenIdleOrUnknown_IsANoOp(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	if err := e.o.Cancel(e.sid); err != nil {
		t.Errorf("Cancel on an idle standup: %v", err)
	}
	if err := e.o.Cancel(987654); err != nil {
		t.Errorf("Cancel on an unknown standup: %v", err)
	}
	e.o.Wait(e.sid) // no run: returns immediately
	if n := len(e.em.names()); n != 0 {
		t.Errorf("events emitted by no-op Cancel/Wait: %v", e.em.names())
	}
}

func TestShutdown_CancelsRunsAndWaits(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	before := e.checksum()
	revBefore := e.rev(e.sid)
	g := e.gate(pathHasPrefix("/rest/api/3/search/jql"))
	id, err := e.o.Start(context.Background(), e.sid, ModeFull, TriggerManual)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	g.waitReached(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.o.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if r := e.run(id); r.Status != StatusCancelled {
		t.Errorf("sync_run.status after Shutdown = %q, want cancelled", r.Status)
	}
	e.assertNothingApplied(before, revBefore)
}

// ------------------------------------------------------------------------------------------------ 429 (§3)

func TestRun_429Storm_RespectsRetryAfterAndSucceeds(t *testing.T) {
	defer recoverStub(t)
	// Retry-After is whole seconds, so keep the number of throttles small: every 11th request answers 429.
	e := newEnv(t, envOpts{srv: jiratest.Options{Throttle: &jiratest.Throttle{Every: 11, RetryAfter: time.Second}}})
	start := time.Now()
	id := e.sync(ModeFull)
	elapsed := time.Since(start)
	r := e.mustOK(id)

	reqs := e.requests()
	n429 := 0
	for _, q := range reqs {
		if q.Status == 429 {
			n429++
		}
	}
	if n429 < 1 {
		t.Fatalf("the fake never answered 429 (%d requests logged); the test needs at least one", len(reqs))
	}
	if int(r.Throttl) != n429 {
		t.Errorf("sync_run.throttled = %d, want the %d rate-limit responses the fake sent", r.Throttl, n429)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("run took %v with %d throttled requests; Retry-After of 1 s was not honoured", elapsed, n429)
	}
	for i, q := range reqs {
		if q.Status != 429 {
			continue
		}
		retried := false
		for _, later := range reqs[i+1:] {
			if later.Path == q.Path && later.Status == 200 {
				retried = true
				break
			}
		}
		if !retried {
			t.Errorf("request %s that got 429 was never retried successfully", q.Path)
		}
	}
	if n := e.count("standup_issue", "standup_id = ?", e.sid); n != 10 {
		t.Errorf("members after a throttled sync = %d, want 10", n)
	}
	if fin := e.em.finished(); len(fin) != 1 || fin[0].Status != StatusOK {
		t.Errorf("sync:finished = %+v, want one ok", fin)
	}
}

// ------------------------------------------------------------------------------------------------ dev activity (§2.11)

func TestRun_DevStatus404_RunOKWithWarningAndNoDevRows(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{srv: jiratest.Options{DevStatus: "404"}})
	id := e.sync(ModeFull)
	r := e.mustOK(id)
	if !contains(r.Warnings, "dev_activity_unavailable") {
		t.Errorf("sync_run.warnings = %v, want dev_activity_unavailable", r.Warnings)
	}
	if contains(r.Warnings, "dev_activity_partial") {
		t.Errorf("warnings %v contain dev_activity_partial; an unavailable API is not a partial failure", r.Warnings)
	}
	fin := e.em.finished()
	if len(fin) != 1 || fin[0].Status != StatusOK || !contains(fin[0].Warnings, "dev_activity_unavailable") {
		t.Errorf("sync:finished = %+v, want ok with warning dev_activity_unavailable", fin)
	}
	if n := e.count("dev_commit", ""); n != 0 {
		t.Errorf("dev_commit rows = %d, want 0", n)
	}
	if n := e.count("dev_pr_event", ""); n != 0 {
		t.Errorf("dev_pr_event rows = %d, want 0", n)
	}
	if got := e.qStr(`SELECT dev_activity_state FROM connection WHERE id = ?`, e.connID); got != "unavailable" {
		t.Errorf("connection.dev_activity_state = %q, want unavailable", got)
	}
	if e.qIsNull(`SELECT dev_activity_checked_at FROM connection WHERE id = ?`, e.connID) {
		t.Errorf("connection.dev_activity_checked_at not set")
	}
	if n := countPath(e.requests(), isSummary); n < 1 || n > 4 {
		t.Errorf("dev summary requests = %d, want 1..4: the first call probes, the rest of the phase is skipped (4 in flight at most)", n)
	}
	if n := e.count("standup_issue", "standup_id = ?", e.sid); n != 10 {
		t.Errorf("members = %d, want 10: everything else is applied", n)
	}

	// Incremental within 24 h: no dev-status request, warning still reported.
	e.srv.ResetRequests()
	r2 := e.mustOK(e.sync(ModeIncremental))
	if r2.Mode != ModeIncremental {
		t.Fatalf("second run mode = %q, want incremental", r2.Mode)
	}
	if n := countPath(e.requests(), isDevStatus); n != 0 {
		t.Errorf("incremental run made %d dev-status requests while dev activity is known unavailable", n)
	}
	if !contains(r2.Warnings, "dev_activity_unavailable") {
		t.Errorf("incremental run warnings = %v, want dev_activity_unavailable to persist", r2.Warnings)
	}

	// A full run re-probes.
	e.srv.ResetRequests()
	r3 := e.mustOK(e.sync(ModeFull))
	if n := countPath(e.requests(), isSummary); n < 1 {
		t.Errorf("full run made %d dev summary requests, want a re-probe", n)
	}
	if !contains(r3.Warnings, "dev_activity_unavailable") {
		t.Errorf("full run warnings = %v", r3.Warnings)
	}
}

func TestRun_DevActivityFailsForOneIssue_StatusPartialAndRowsKept(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeFull))
	commits, prs := e.count("dev_commit", ""), e.count("dev_pr_event", "")
	if commits == 0 || prs == 0 {
		t.Fatalf("test setup: first sync stored %d commits and %d PR events, want both > 0", commits, prs)
	}
	svc1 := e.qStr(`SELECT source_id FROM issue WHERE key = 'MOB-1'`)
	svc2commits := e.count("dev_commit", "issue_id = ?", e.issueID("MOB-2"))
	e.failDev(svc1, apperr.New(apperr.SourceHTTP, "dev status failed"))
	revBefore := e.rev(e.sid)
	e.em.reset()

	id := e.sync(ModeIncremental)
	r := e.run(id)
	if r.Status != StatusPartial {
		t.Fatalf("status = %q (error %q), want partial", r.Status, r.ErrorCode)
	}
	if !contains(r.Warnings, "dev_activity_partial") {
		t.Errorf("warnings = %v, want dev_activity_partial", r.Warnings)
	}
	if got := e.count("dev_commit", ""); got != commits {
		t.Errorf("dev_commit rows = %d, want %d (existing rows are kept)", got, commits)
	}
	if got := e.count("dev_pr_event", ""); got != prs {
		t.Errorf("dev_pr_event rows = %d, want %d", got, prs)
	}
	if got := e.count("dev_commit", "issue_id = ?", e.issueID("MOB-2")); got != svc2commits {
		t.Errorf("another issue's dev rows changed: %d -> %d", svc2commits, got)
	}
	if got := e.rev(e.sid); got != revBefore+1 {
		t.Errorf("rev = %d, want %d: a partial run still applies", got, revBefore+1)
	}
	if fin := e.em.finished(); len(fin) != 1 || fin[0].Status != StatusPartial || !contains(fin[0].Warnings, "dev_activity_partial") {
		t.Errorf("sync:finished = %+v, want partial with dev_activity_partial", fin)
	}
	if len(e.em.dataChanged()) == 0 {
		t.Errorf("no data:changed after a partial run that applied")
	}
	if st := e.status(e.sid); st.State != "idle" || st.LastStatus != StatusPartial {
		t.Errorf("Status = %+v, want idle with LastStatus partial", st)
	}

	e.failDev(svc1, nil)
	e.mustOK(e.sync(ModeIncremental))
}

// ------------------------------------------------------------------------------------------------ auth (§3, §4.7)

func TestRun_401MidRun_AuthInvalid_NoApply(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	before := e.checksum()
	revBefore := e.rev(e.sid)
	g := e.gate(pathHasPrefix("/rest/agile/1.0/board/")) // after /myself succeeded
	id, err := e.o.Start(context.Background(), e.sid, ModeFull, TriggerManual)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	g.waitReached(t)
	e.srv.SetUnauthorized(true)
	g.Release()
	e.o.Wait(e.sid)

	r := e.run(id)
	if r.Status != StatusFailed || r.ErrorCode != string(apperr.AuthInvalid) {
		t.Errorf("run = %q/%q, want failed/auth_invalid", r.Status, r.ErrorCode)
	}
	if r.ErrorMessage == "" || strings.Contains(r.ErrorMessage, jiratest.Token) {
		t.Errorf("error message %q must be set and must not contain the token", r.ErrorMessage)
	}
	e.assertNothingApplied(before, revBefore)
	if got := e.qStr(`SELECT auth_state FROM connection WHERE id = ?`, e.connID); got != "invalid" {
		t.Errorf("connection.auth_state = %q, want invalid", got)
	}
	var invalid bool
	for _, a := range e.em.connAuth() {
		if a.State == "invalid" {
			invalid = true
		}
	}
	if !invalid {
		t.Errorf("no connection:auth {invalid} event (names: %v)", e.em.names())
	}
	fin := e.em.finished()
	if len(fin) != 1 || fin[0].Status != StatusFailed || fin[0].ErrorCode != string(apperr.AuthInvalid) {
		t.Errorf("sync:finished = %+v", fin)
	}
	if len(e.em.dataChanged()) != 0 || len(e.afterCalls()) != 0 {
		t.Errorf("a failed run emitted data:changed or called AfterSync")
	}
	if st := e.status(e.sid); st.State != "failed" || st.ErrorCode != string(apperr.AuthInvalid) || st.LastStatus != StatusFailed {
		t.Errorf("Status = %+v, want failed with auth_invalid", st)
	}

	// All sync triggers are suppressed until Connect succeeds: Start refuses, no run, no request.
	e.srv.SetUnauthorized(false)
	e.srv.ResetRequests()
	_, err = e.o.Start(context.Background(), e.sid, ModeIncremental, TriggerOpen)
	if !hasCode(err, apperr.AuthInvalid) {
		t.Errorf("Start on an invalid connection returned %v, want apperr auth_invalid", err)
	}
	if n := e.count("sync_run", ""); n != 1 {
		t.Errorf("sync_run rows = %d, want 1 (the refused Start creates no run)", n)
	}
	if n := len(e.requests()); n != 0 {
		t.Errorf("refused Start made %d requests", n)
	}
}

func TestRun_MissingToken_FailsAuthMissingWithoutNetwork(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{noToken: true})
	before := e.checksum()
	revBefore := e.rev(e.sid)
	id := e.sync(ModeIncremental)
	r := e.run(id)
	if r.Status != StatusFailed || r.ErrorCode != string(apperr.AuthMissing) {
		t.Errorf("run = %q/%q, want failed/auth_missing", r.Status, r.ErrorCode)
	}
	if n := len(e.requests()); n != 0 {
		t.Errorf("a run without a token made %d requests", n)
	}
	e.assertNothingApplied(before, revBefore)
	var missing bool
	for _, a := range e.em.connAuth() {
		if a.State == "missing" {
			missing = true
		}
	}
	if !missing {
		t.Errorf("no connection:auth {missing} event (names: %v)", e.em.names())
	}
	if got := e.qStr(`SELECT auth_state FROM connection WHERE id = ?`, e.connID); got != "missing" {
		t.Errorf("connection.auth_state = %q, want missing", got)
	}
}

func TestRun_OpensTheSourceWithTheKeychainTokenAndPersistedMeta(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeIncremental))
	e.mustOK(e.sync(ModeIncremental))

	calls := e.openCalls()
	if len(calls) != 2 {
		t.Fatalf("OpenSource called %d times for 2 runs, want 1 per run", len(calls))
	}
	c := calls[0]
	if c.Secret != jiratest.Token {
		t.Errorf("OpenSource secret = %q, want the keychain token", c.Secret)
	}
	if c.Conn.Source != "jira" || c.Conn.BaseURL != e.srv.URL || c.Conn.Account != e.email || c.Conn.ID != e.connID {
		t.Errorf("OpenSource connection = %+v", c.Conn)
	}
	if !c.HasHTTP {
		t.Errorf("SourceOpts.HTTP was not passed through to OpenSource")
	}
	if _, ok := calls[0].Meta["sprintFieldId"]; ok {
		t.Errorf("first run already had sprintFieldId in Meta: %v", calls[0].Meta)
	}
	if got, _ := calls[1].Meta["sprintFieldId"].(string); got != jiratest.SprintFieldID {
		t.Errorf("second run Meta = %v, want the persisted sprintFieldId %q", calls[1].Meta, jiratest.SprintFieldID)
	}
	gets := e.kr.getAccounts()
	if len(gets) < 2 {
		t.Fatalf("keyring Get calls = %v, want one per run", gets)
	}
	for _, a := range gets {
		if a != e.account {
			t.Errorf("keyring account = %q, want %q", a, e.account)
		}
	}
	if want := "jira:" + strings.TrimPrefix(e.srv.URL, "http://") + ":" + e.email; e.account != want {
		t.Errorf("harness keychain account = %q, want %q (secrets.ConnectionAccount)", e.account, want)
	}
}

// ------------------------------------------------------------------------------------------------ failures abort without apply (§4.5)

func TestRun_FailureInPhases0To4_AbortsWithoutApplyAndKeepsCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix string
		status int
		code   apperr.Code
		once   bool // the status is not retried: exactly one request
	}{
		{"board no longer exists", "/rest/agile/1.0/board/1/configuration", 404, apperr.NotFound, true},
		{"board not visible", "/rest/agile/1.0/board/1/configuration", 403, apperr.Forbidden, true},
		{"search rejects the query", "/rest/api/3/search/jql", 400, apperr.FilterInvalid, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer recoverStub(t)
			e := newEnv(t, envOpts{})
			f := e.failRequests(tc.prefix, tc.status) // after setup, so only the run sees it
			before := e.checksum()
			revBefore := e.rev(e.sid)
			id := e.sync(ModeFull)
			r := e.run(id)
			if r.Status != StatusFailed || r.ErrorCode != string(tc.code) {
				t.Fatalf("run = %q/%q (%s), want failed/%s", r.Status, r.ErrorCode, r.ErrorMessage, tc.code)
			}
			if r.ErrorMessage == "" || strings.Contains(r.ErrorMessage, jiratest.Token) {
				t.Errorf("error message %q must be set and must not carry the token", r.ErrorMessage)
			}
			e.assertNothingApplied(before, revBefore)
			if n := e.faultHits(f); n < 1 || (tc.once && n != 1) {
				t.Errorf("%s answered %d times, want the failure seen%s", tc.prefix, n, map[bool]string{true: " exactly once (no retry for this status)", false: ""}[tc.once])
			}
			fin := e.em.finished()
			if len(fin) != 1 || fin[0].Status != StatusFailed || fin[0].ErrorCode != string(tc.code) || fin[0].ErrorMessage == "" {
				t.Errorf("sync:finished = %+v", fin)
			}
			if len(e.em.dataChanged()) != 0 || len(e.afterCalls()) != 0 {
				t.Errorf("a failed run emitted data:changed or called AfterSync")
			}
			st := e.status(e.sid)
			if st.State != "failed" || st.ErrorCode != string(tc.code) || st.LastStatus != StatusFailed || !st.NeedsSync {
				t.Errorf("Status = %+v, want failed with %s and NeedsSync still true", st, tc.code)
			}
			if got := e.qStr(`SELECT auth_state FROM connection WHERE id = ?`, e.connID); got != "ok" {
				t.Errorf("connection.auth_state = %q, want ok: only a 401 marks the token invalid", got)
			}
		})
	}
}

func TestStart_DraftAndArchivedStandups_AreValidationErrorsWithoutARun(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	draft, err := e.st.Create(ctx)
	if err != nil {
		t.Fatalf("Create draft: %v", err)
	}
	if _, err := e.o.Start(ctx, draft, ModeIncremental, TriggerManual); !hasCode(err, apperr.Validation) {
		t.Errorf("Start on a draft standup returned %v, want apperr validation", err)
	}
	if _, err := e.st.Archive(ctx, e.sid); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, err := e.o.Start(ctx, e.sid, ModeIncremental, TriggerManual); !hasCode(err, apperr.Validation) {
		t.Errorf("Start on an archived standup returned %v, want apperr validation", err)
	}
	if n := e.count("sync_run", ""); n != 0 {
		t.Errorf("sync_run rows = %d, want 0", n)
	}
	if n := len(e.requests()); n != 0 {
		t.Errorf("refused Starts made %d requests", n)
	}
	if len(e.em.started()) != 0 {
		t.Errorf("sync:started emitted for a refused Start")
	}
}

// ------------------------------------------------------------------------------------------------ after-sync hook

func TestAfterSync_CalledOnceForOKRun_AfterEventsAndOutsideTheTransaction(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	id := e.sync(ModeIncremental)
	e.mustOK(id)
	calls := e.afterCalls()
	if len(calls) != 1 {
		t.Fatalf("AfterSync calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.StandupID != e.sid || c.RunID != id {
		t.Errorf("AfterSync(%d, %d), want (%d, %d)", c.StandupID, c.RunID, e.sid, id)
	}
	if c.TxFreeErr != nil {
		t.Errorf("AfterSync ran inside the apply transaction (a write transaction inside the hook failed: %v)", c.TxFreeErr)
	}
	if !contains(c.EventsSeen, EvSyncFinished) || !contains(c.EventsSeen, EvDataChanged) {
		t.Errorf("events seen by the hook = %v, want sync:finished and data:changed already emitted", c.EventsSeen)
	}
}

func TestAfterSync_CalledForPartialButNotForFailedOrCancelled(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeFull))
	if n := len(e.afterCalls()); n != 1 {
		t.Fatalf("after the ok run AfterSync calls = %d, want 1", n)
	}

	// partial
	e.failDev(e.qStr(`SELECT source_id FROM issue WHERE key = 'MOB-1'`), apperr.New(apperr.SourceHTTP, "dev status failed"))
	idPartial := e.sync(ModeIncremental)
	if r := e.run(idPartial); r.Status != StatusPartial {
		t.Fatalf("status = %q, want partial", r.Status)
	}
	calls := e.afterCalls()
	if len(calls) != 2 || calls[1].RunID != idPartial {
		t.Fatalf("AfterSync calls after a partial run = %+v, want a second call for run %d", calls, idPartial)
	}
	e.failDev(e.qStr(`SELECT source_id FROM issue WHERE key = 'MOB-1'`), nil)

	// cancelled
	g := e.gate(pathHasPrefix("/rest/api/3/search/jql"))
	if _, err := e.o.Start(context.Background(), e.sid, ModeFull, TriggerManual); err != nil {
		t.Fatalf("Start: %v", err)
	}
	g.waitReached(t)
	_ = e.o.Cancel(e.sid)
	e.o.Wait(e.sid)
	g.Release()
	// failed: the connection is rejected
	e.srv.SetUnauthorized(true)
	idFailed := e.sync(ModeIncremental)
	if r := e.run(idFailed); r.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", r.Status)
	}
	if n := len(e.afterCalls()); n != 2 {
		t.Errorf("AfterSync calls = %d after one cancelled and one failed run, want still 2", n)
	}
}

// ------------------------------------------------------------------------------------------------ logs

func TestRun_SyncLog_HasNoSecretsOrIssueContent(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeFull))
	msgs := e.qCol(`SELECT message FROM sync_log`)
	var summaries []string
	for _, is := range e.fx.Issues {
		summaries = append(summaries, is.Summary)
	}
	for _, m := range msgs {
		for _, bad := range append([]string{jiratest.Token, "Authorization", "Basic "}, summaries...) {
			if strings.Contains(m, bad) {
				t.Errorf("sync_log line %q contains %q", m, bad)
			}
		}
	}
}
