package sync

// budget_test.go is the request-budget gate of WP-T0 (jira-sync §3.1 "Request economy", decided by Chris on 2026-11-23,
// docs/wp-notes/F12-freeze.md decision 1). It asserts against jiratest's request log, for the default fixture board
// (MOB, 55 issues):
//
//   - a full sync: all requests except /rest/dev-status/** ≤ 250; dev-status summaries ≤ one per active dev target;
//     dev-status details ≤ 2 per target whose fingerprint changed (on a first sync every target is new);
//   - an incremental sync with 3 changed issues: non-dev requests ≤ 50; dev summaries ≤ one per active dev target;
//     no detail request at all, because no dev fingerprint changed;
//   - an incremental sync with nothing changed: ≤ 8 requests in total (jira-sync §2.11, §3.1).
//
// Since 2026-11-24 the dev fingerprint comes from the Development summary field in the scan response, so a summary
// request is only left for a target whose field is stale or absent (the fake serves exactly one stale issue,
// jiratest.StaleDevKey) and detail is requested directly, without a summary, for a changed fingerprint.
//
// Measured before that change (fake fixture board, 55 issues): full 208 = 79 non-dev + 44 summary + 85 detail;
// incremental-3 40 = 11 non-dev + 29 summary; no change 35 = 6 non-dev + 29 summary.
// Measured after: full 165 = 79 + 1 + 85; incremental-3 12 = 11 + 1; no change 7 = 6 + 1 (the stale target's summary;
// 6 when no field is stale).
// The limiter must not add requests, and the F12 content phases (T4/T6) must stay inside these caps.

import (
	"net/http"
	"net/url"
	gosync "sync"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source/jira"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
)

// syncLane indexes limiter.Stats.Requests (interactive 0, sync 1, media 2); sync tests may not import the limiter package.
const syncLane = 1

const (
	budgetFullNonDev        = 250
	budgetIncrementalNonDev = 50
	budgetChangedIssues     = 3
	budgetNoChangeTotal     = 8
)

func TestRequestBudget_FullAndIncrementalSyncOnTheFixtureBoard(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})

	// ---- full sync -------------------------------------------------------------------------------------------
	r := e.mustOK(e.sync(ModeFull))
	if r.Mode != ModeFull {
		t.Fatalf("first run mode = %q, want full", r.Mode)
	}
	reqs := e.requests()
	targets := e.devTargets(e.sid, true) // every target of the first run is unchecked, so this is the exact set
	if len(targets) == 0 {
		t.Fatalf("test setup: the fixture board has no dev targets")
	}
	nonDev := len(reqs) - countPath(reqs, isDevStatus)
	summaries, details := countPath(reqs, isSummary), countPath(reqs, isDetail)
	t.Logf("full sync: %d requests = %d non-dev + %d summary + %d detail (%d dev targets)",
		len(reqs), nonDev, summaries, details, len(targets))
	if nonDev > budgetFullNonDev {
		t.Errorf("full sync issue/content requests = %d, budget %d", nonDev, budgetFullNonDev)
	}
	if summaries > len(e.staleDevIDs(targets)) {
		t.Errorf("full sync dev-status summaries = %d, want only the stale-field targets (%d)", summaries, len(e.staleDevIDs(targets)))
	}
	if summaries > len(targets) {
		t.Errorf("full sync dev-status summaries = %d, want at most one per dev target (%d)", summaries, len(targets))
	}
	if details > 2*len(targets) { // first sync: every fingerprint is new, so every target may change (≤ 2 details each)
		t.Errorf("full sync dev-status details = %d, want at most 2 per changed target (%d targets)", details, len(targets))
	}
	checkDevPerTarget(t, "full sync", reqs, targets, true)
	if int(r.Requests) != len(reqs) {
		t.Errorf("sync_run.requests = %d, fake logged %d", r.Requests, len(reqs))
	}

	// ---- incremental sync with three changed issues ----------------------------------------------------------
	changed := e.mutateIssues(budgetChangedIssues)
	if len(changed) != budgetChangedIssues {
		t.Fatalf("test setup: mutated %d issues, want %d", len(changed), budgetChangedIssues)
	}
	targets = e.devTargets(e.sid, false)
	e.srv.ResetRequests()
	r = e.mustOK(e.sync(ModeIncremental))
	if r.Mode != ModeIncremental {
		t.Fatalf("second run mode = %q, want incremental", r.Mode)
	}
	if r.Hydrated != int64(budgetChangedIssues) {
		t.Errorf("hydrated = %d, want %d (only the changed issues: %v)", r.Hydrated, budgetChangedIssues, changed)
	}
	reqs = e.requests()
	nonDev = len(reqs) - countPath(reqs, isDevStatus)
	summaries, details = countPath(reqs, isSummary), countPath(reqs, isDetail)
	t.Logf("incremental sync (%d changed): %d requests = %d non-dev + %d summary + %d detail (%d dev targets)",
		budgetChangedIssues, len(reqs), nonDev, summaries, details, len(targets))
	if nonDev > budgetIncrementalNonDev {
		t.Errorf("incremental issue/content requests = %d, budget %d", nonDev, budgetIncrementalNonDev)
	}
	if summaries > len(targets) {
		t.Errorf("incremental dev-status summaries = %d, want at most one per dev target (%d)", summaries, len(targets))
	}
	if details != 0 {
		t.Errorf("incremental dev-status details = %d, want 0 (no dev fingerprint changed)", details)
	}
	checkDevPerTarget(t, "incremental sync", reqs, targets, false)
	if int(r.Requests) != len(reqs) {
		t.Errorf("sync_run.requests = %d, fake logged %d", r.Requests, len(reqs))
	}

	// ---- incremental sync with nothing changed ---------------------------------------------------------------
	targets = e.devTargets(e.sid, false)
	e.srv.ResetRequests()
	r = e.mustOK(e.sync(ModeIncremental))
	if r.Mode != ModeIncremental || r.Hydrated != 0 {
		t.Fatalf("no-change run: mode %q, hydrated %d, want incremental and 0", r.Mode, r.Hydrated)
	}
	reqs = e.requests()
	stale := e.staleDevIDs(targets)
	t.Logf("incremental sync (no change): %d requests = %d non-dev + %d summary + %d detail (%d dev targets, %d stale fields)",
		len(reqs), len(reqs)-countPath(reqs, isDevStatus), countPath(reqs, isSummary), countPath(reqs, isDetail), len(targets), len(stale))
	if len(reqs) > budgetNoChangeTotal {
		t.Errorf("no-change incremental sync = %d requests, budget %d", len(reqs), budgetNoChangeTotal)
	}
	if got := countPath(reqs, isSummary); got != len(stale) {
		t.Errorf("no-change incremental dev summaries = %d, want %d (only the stale-field targets)", got, len(stale))
	}
	if got := countPath(reqs, isDetail); got != 0 {
		t.Errorf("no-change incremental dev details = %d, want 0", got)
	}
	checkDevPerTarget(t, "no-change sync", reqs, targets, false)
	if int(r.Requests) != len(reqs) {
		t.Errorf("sync_run.requests = %d, fake logged %d", r.Requests, len(reqs))
	}
}

// checkDevPerTarget counts dev-status requests per issue id (the issueId query parameter) and asserts the per-target
// budgets of jira-sync §3.1: at most 1 summary per active dev target, and at most 2 details for a target whose
// fingerprint changed (every target on a first sync, none otherwise), and no request for an issue that is not a target.
// A detail may come without a summary: the fingerprint of a target whose Development field is usable came with the scan.
func checkDevPerTarget(t *testing.T, label string, reqs []jiratest.Request, targets []string, anyChanged bool) {
	t.Helper()
	isTarget := map[string]bool{}
	for _, id := range targets {
		isTarget[id] = true
	}
	summaries, details := map[string]int{}, map[string]int{}
	for _, q := range reqs {
		var into map[string]int
		switch q.Path {
		case "/rest/dev-status/latest/issue/summary":
			into = summaries
		case "/rest/dev-status/latest/issue/detail":
			into = details
		default:
			continue
		}
		vals, err := url.ParseQuery(q.Query)
		if err != nil || vals.Get("issueId") == "" {
			t.Errorf("%s: dev-status request without a parsable issueId: %q", label, q.Query)
			continue
		}
		into[vals.Get("issueId")]++
	}
	for id, n := range summaries {
		if !isTarget[id] {
			t.Errorf("%s: dev summary requested for issue %s, which is not an active dev target", label, id)
		}
		if n > 1 {
			t.Errorf("%s: %d dev summaries for target %s, want at most 1", label, n, id)
		}
	}
	for id, n := range details {
		if !isTarget[id] {
			t.Errorf("%s: dev detail requested for issue %s, which is not an active dev target", label, id)
		}
		if !anyChanged {
			t.Errorf("%s: %d dev details for target %s although no fingerprint changed", label, n, id)
		} else if n > 2 {
			t.Errorf("%s: %d dev details for target %s, want at most 2", label, n, id)
		}
	}
}

// mutateIssues bumps Updated and Summary of the first n board-sprint issues (a real edit in Jira), and returns their keys.
func (e *env) mutateIssues(n int) []string {
	e.t.Helper()
	var keys []string
	bump := e.clock.Now().Add(time.Minute)
	e.srv.Mutate(func(ds *jiratest.Dataset) {
		for _, is := range ds.Issues {
			if len(keys) == n {
				return
			}
			if is.ProjectKey != e.project.Key || len(is.SprintIDs) == 0 {
				continue
			}
			is.Updated = bump
			is.Summary += " (edited)"
			keys = append(keys, is.Key)
		}
	})
	return keys
}

// bypassChecker is the innermost transport of the sync harness. Every request the orchestrator's Jira source sends must
// reach it holding a limiter permit (the permit is held from Acquire until the response body is closed), so a request
// that reached the network without passing the connection's limiter shows up as InFlight == 0 here.
type bypassChecker struct {
	base     http.RoundTripper
	inFlight func() int // the connection limiter's permits currently held
	mu       gosync.Mutex
	seen     int
	bad      []string
}

func (b *bypassChecker) RoundTrip(r *http.Request) (*http.Response, error) {
	admitted := b.inFlight() >= 1
	b.mu.Lock()
	b.seen++
	if !admitted {
		b.bad = append(b.bad, r.Method+" "+r.URL.Path)
	}
	b.mu.Unlock()
	return b.base.RoundTrip(r)
}

func TestLimiter_NoSyncRequestBypassesIt(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	lim := jira.LimiterFor(model.Connection{BaseURL: e.srv.URL, Account: e.email})
	orig := e.gt.base
	chk := &bypassChecker{base: orig, inFlight: func() int { return lim.Stats().InFlight }}
	e.gt.base = chk
	t.Cleanup(func() { e.gt.base = orig }) // runs before newEnv's cleanup, which expects the *http.Transport back
	before := lim.Stats()

	e.mustOK(e.sync(ModeFull))
	e.mutateIssues(budgetChangedIssues)
	e.mustOK(e.sync(ModeIncremental))

	chk.mu.Lock()
	seen, bad := chk.seen, append([]string(nil), chk.bad...)
	chk.mu.Unlock()
	if seen < 100 {
		t.Fatalf("only %d requests reached the network; the test is not exercising the sync", seen)
	}
	if len(bad) != 0 {
		t.Fatalf("%d of %d sync requests reached the network without a limiter permit: %v", len(bad), seen, bad)
	}
	st := lim.Stats()
	var granted int64
	for i := range st.Requests {
		granted += st.Requests[i] - before.Requests[i]
	}
	if granted != int64(seen) {
		t.Fatalf("limiter granted %d permits for %d requests on the wire", granted, seen)
	}
	if st.InFlight != 0 {
		t.Fatalf("%d permits leaked after the runs", st.InFlight)
	}
	if got := st.Requests[syncLane] - before.Requests[syncLane]; got != int64(seen) {
		t.Fatalf("sync-lane permits = %d of %d: every orchestrator request is sync traffic", got, seen)
	}
}
