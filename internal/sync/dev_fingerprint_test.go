package sync

// dev_fingerprint_test.go: the dev phase with the fingerprint that rides along with the scan (jira-sync §2.11, §3.1).
// A target whose scan fingerprint equals the stored one costs no dev-status request; a changed one goes straight to
// detail; an unknown one (stale or absent Development field) falls back to the summary. The fixture board is the
// default fake (MOB, 55 issues); jiratest serves one stale field (jiratest.StaleDevKey).

import (
	"net/url"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/source/jira/jiratest"
)

// devFPTarget is an active dev target with commits in the default fixture (In Progress, MOB-1421).
const devFPTarget = jiratest.StaleDevKey

func TestDevFingerprint_NoChange_MakesNoDevStatusRequests(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	e.setDevStale(false) // every field usable from the very first scan
	e.mustOK(e.sync(ModeFull))
	targets := e.devTargets(e.sid, false)
	if len(targets) < 20 {
		t.Fatalf("test setup: only %d dev targets", len(targets))
	}

	e.srv.ResetRequests()
	r := e.mustOK(e.sync(ModeIncremental))
	reqs := e.requests()
	if n := countPath(reqs, isDevStatus); n != 0 {
		t.Errorf("no-change incremental made %d dev-status requests, want 0 (%d targets)", n, len(targets))
	}
	// 1 myself + 1 board configuration + 1 sprints + the scans + 1 approximate count
	if want := 4 + countPath(reqs, isSearch); len(reqs) != want {
		t.Errorf("total requests = %d, want %d", len(reqs), want)
	}
	if int(r.Requests) != len(reqs) {
		t.Errorf("sync_run.requests = %d, fake logged %d", r.Requests, len(reqs))
	}
	// The targets were still checked: dev_checked_at moved to this run for every one of them.
	for _, id := range targets {
		if got, want := e.qInt(`SELECT dev_checked_at FROM issue WHERE source_id = ?`, id), ms(e.T()); got != want {
			t.Errorf("issue %s dev_checked_at = %d, want %d (checked without a request)", id, got, want)
			break
		}
	}
}

func TestDevFingerprint_ChangedDevActivity_AsksDetailOnlyForThatTarget(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	e.setDevStale(false)
	e.mustOK(e.sync(ModeFull))
	id := e.qStr(`SELECT source_id FROM issue WHERE key = ?`, devFPTarget)
	commits := e.count("dev_commit", "issue_id = ?", e.issueID(devFPTarget))

	e.addCommit(devFPTarget)
	e.srv.ResetRequests()
	e.mustOK(e.sync(ModeIncremental))
	reqs := e.requests()
	if n := countPath(reqs, isSummary); n != 0 {
		t.Errorf("dev summaries = %d, want 0: the changed fingerprint came with the scan", n)
	}
	details := 0
	for _, q := range reqs {
		if !isDetail(q.Path) {
			continue
		}
		details++
		if vals, err := url.ParseQuery(q.Query); err != nil || vals.Get("issueId") != id {
			t.Errorf("dev detail for %q, want only %s", q.Query, id)
		}
	}
	if details < 1 || details > 2 {
		t.Errorf("dev details = %d, want 1 or 2 (repository, pullrequest) for the one changed target", details)
	}
	if got := e.count("dev_commit", "issue_id = ?", e.issueID(devFPTarget)); got != commits+1 {
		t.Errorf("%s commits = %d, want %d (the new commit)", devFPTarget, got, commits+1)
	}

	// And the new fingerprint is stored: the next run is quiet again.
	e.srv.ResetRequests()
	e.mustOK(e.sync(ModeIncremental))
	if n := countPath(e.requests(), isDevStatus); n != 0 {
		t.Errorf("run after the change made %d dev-status requests, want 0", n)
	}
}

func TestDevFingerprint_ChangedPullRequest_AsksDetailOnlyAndStoresTheEvent(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	e.setDevStale(false)
	e.mustOK(e.sync(ModeFull))
	id := e.qStr(`SELECT source_id FROM issue WHERE key = ?`, devFPTarget)
	events := e.count("dev_pr_event", "issue_id = ?", e.issueID(devFPTarget))
	commits := e.count("dev_commit", "issue_id = ?", e.issueID(devFPTarget))

	// A pull request changes state later, as in Jira: the issue's `updated` does not move, the summary's does.
	moved := false
	e.srv.Mutate(func(ds *jiratest.Dataset) {
		for _, is := range ds.Issues {
			if is.Key == devFPTarget && len(is.PRs) > 0 {
				p := &is.PRs[len(is.PRs)-1]
				p.Status = map[string]string{"OPEN": "MERGED"}[p.Status]
				if p.Status == "" {
					p.Status = "OPEN"
				}
				p.LastUpdate = e.clock.Now().Add(time.Minute)
				moved = true
			}
		}
	})
	if !moved {
		t.Fatalf("test setup: %s has no pull request", devFPTarget)
	}
	e.srv.ResetRequests()
	e.mustOK(e.sync(ModeIncremental))
	reqs := e.requests()
	if n := countPath(reqs, isSummary); n != 0 {
		t.Errorf("dev summaries = %d, want 0: the changed fingerprint came with the scan", n)
	}
	if got := devIssueIDs(t, reqs); len(got) < 1 || len(got) > 2 {
		t.Errorf("dev-status requests = %v, want 1 or 2 detail requests", got)
	} else {
		for _, g := range got {
			if g != id {
				t.Errorf("dev-status request for issue %s, want only %s", g, id)
			}
		}
	}
	if got := e.count("dev_pr_event", "issue_id = ?", e.issueID(devFPTarget)); got != events+1 {
		t.Errorf("%s pull request events = %d, want %d (the new state)", devFPTarget, got, events+1)
	}
	if got := e.count("dev_commit", "issue_id = ?", e.issueID(devFPTarget)); got != commits {
		t.Errorf("%s commits = %d, want %d (unchanged)", devFPTarget, got, commits)
	}
	e.srv.ResetRequests()
	e.mustOK(e.sync(ModeIncremental))
	if n := countPath(e.requests(), isDevStatus); n != 0 {
		t.Errorf("run after the change made %d dev-status requests, want 0", n)
	}
}

func TestDevFingerprint_StaleField_FallsBackToSummaryThenDetail(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)}) // the default fake serves MOB-1421 as stale
	e.mustOK(e.sync(ModeFull))
	id := e.qStr(`SELECT source_id FROM issue WHERE key = ?`, devFPTarget)

	// Unchanged: only the stale target is asked, with a summary and no detail.
	e.srv.ResetRequests()
	e.mustOK(e.sync(ModeIncremental))
	reqs := e.requests()
	if s, d := countPath(reqs, isSummary), countPath(reqs, isDetail); s != 1 || d != 0 {
		t.Fatalf("stale target, no change: %d summaries, %d details, want 1 and 0", s, d)
	}
	if got := devIssueIDs(t, reqs); len(got) != 1 || got[0] != id {
		t.Errorf("dev-status requests for %v, want only the stale target %s", got, id)
	}

	// Changed: summary first, then detail (today's path).
	e.addCommit(devFPTarget)
	e.srv.ResetRequests()
	e.mustOK(e.sync(ModeIncremental))
	reqs = e.requests()
	if s, d := countPath(reqs, isSummary), countPath(reqs, isDetail); s != 1 || d < 1 || d > 2 {
		t.Errorf("stale target, changed: %d summaries, %d details, want 1 and 1..2", s, d)
	}
}

func TestDevFingerprint_NoDevelopmentField_FallsBackToSummaryForEveryTarget(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t), srv: jiratest.Options{NoDevField: true}})
	e.mustOK(e.sync(ModeFull))
	targets := e.devTargets(e.sid, false)

	e.srv.ResetRequests()
	e.mustOK(e.sync(ModeIncremental))
	reqs := e.requests()
	if n := countPath(reqs, isSummary); n != len(targets) {
		t.Errorf("dev summaries = %d, want one per dev target (%d): the site has no Development field", n, len(targets))
	}
	if n := countPath(reqs, isDetail); n != 0 {
		t.Errorf("dev details = %d, want 0 (nothing changed)", n)
	}
	if n := countPath(reqs, isField); n != 0 {
		t.Errorf("/field requests = %d, want 0: the absence of the field is remembered", n)
	}
}

func devIssueIDs(t *testing.T, reqs []jiratest.Request) []string {
	t.Helper()
	var out []string
	for _, q := range reqs {
		if !isDevStatus(q.Path) {
			continue
		}
		vals, err := url.ParseQuery(q.Query)
		if err != nil {
			t.Fatalf("dev-status query %q: %v", q.Query, err)
		}
		out = append(out, vals.Get("issueId"))
	}
	return out
}
