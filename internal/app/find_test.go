package app_test

// The palette's local reads (F14, ux-spec §15): FindByKey, ByIDs and People, end to end over the synced fake board.
// Every call is local: the zero-network assertion uses the same switch transport the ticket tests use.

import (
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/keyfind"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
	"github.com/crueber/cuisle/internal/view"
)

func digitsOf(key string) string {
	_, n, _ := keyfind.SplitKey(key)
	return n
}

func TestFindByKey_ExactPrefixCloseAndHomeStandup(t *testing.T) {
	ts := newTicketApp(t)
	var tk view.TicketView
	for _, x := range ts.v.Tickets {
		if x.Key == "MOB-1421" {
			tk = x
		}
	}
	if tk.ID == 0 {
		t.Fatal("MOB-1421 missing")
	}
	ts.Fake.ResetRequests()
	mark := ts.networkCalls()
	got, err := ts.Svc.Ticket.FindByKey(ctx, "1421", ts.standup, 20)
	noErr(t, err, "FindByKey")
	ts.assertNoNetwork(mark, "TicketService.FindByKey")
	if len(ts.Fake.Requests()) != 0 {
		t.Errorf("FindByKey reached the fake Jira: %v", ts.requests())
	}
	if len(got.Hits) == 0 || got.Hits[0].Key != "MOB-1421" || got.Hits[0].Tier != "exact" {
		t.Fatalf("hits = %+v", got.Hits)
	}
	h := got.Hits[0]
	if h.IssueID != tk.ID || h.StandupID != ts.standup || len(h.StandupIDs) != 1 || h.StandupIDs[0] != ts.standup || h.Removed {
		t.Errorf("home/members wrong: %+v", h)
	}
	if h.Lane != tk.Lane || h.DaysInLane != tk.DaysInLane || h.OverLimit != tk.OverLimit || h.StatusName != tk.StatusName ||
		h.Assignee != tk.Assignee || h.Type != tk.Type || h.Summary != tk.Summary {
		t.Errorf("hit %+v differs from the board row %+v", h, tk)
	}
	if want := []int{4, 5, 6, 7}; len(h.Match) != 4 || h.Match[0] != want[0] || h.Match[3] != want[3] {
		t.Errorf("match = %v, want %v", h.Match, want)
	}

	// every board ticket's own digits find it as exact, and with the project letters too
	for _, x := range ts.v.Tickets {
		r, err := ts.Svc.Ticket.FindByKey(ctx, strings.ToLower(x.Key), ts.standup, 20)
		noErr(t, err, "FindByKey "+x.Key)
		if len(r.Hits) == 0 || r.Hits[0].Key != x.Key || r.Hits[0].Tier != "exact" {
			t.Errorf("%s: hits %+v", x.Key, r.Hits)
		}
	}

	// a short prefix: starts-with hits, ordered by key; a project letter narrows it
	r, err := ts.Svc.Ticket.FindByKey(ctx, "14", ts.standup, 20)
	noErr(t, err, "FindByKey 14")
	for _, x := range r.Hits {
		if x.Tier == "prefix" && !strings.HasPrefix(digitsOf(x.Key), "14") {
			t.Errorf("prefix hit %s", x.Key)
		}
	}
	r2, err := ts.Svc.Ticket.FindByKey(ctx, "zzz14", ts.standup, 20)
	noErr(t, err, "FindByKey zzz14")
	if len(r2.Hits) != 0 {
		t.Errorf("unknown project prefix found %+v", r2.Hits)
	}
	for _, q := range []string{"", "mob", "  ", "-"} {
		r3, err := ts.Svc.Ticket.FindByKey(ctx, q, ts.standup, 20)
		noErr(t, err, "FindByKey "+q)
		if len(r3.Hits) != 0 || r3.Hits == nil {
			t.Errorf("query %q: hits = %#v, want an empty non-nil list", q, r3.Hits)
		}
	}

	// one character off: 1421 → 1420 .. 1429 and the transposition 1241
	r4, err := ts.Svc.Ticket.FindByKey(ctx, "1422", ts.standup, 20)
	noErr(t, err, "FindByKey 1422")
	found := false
	for _, x := range r4.Hits {
		if x.Key == "MOB-1421" {
			found = x.Tier == "close"
		}
	}
	if !found {
		t.Errorf("1422 did not list MOB-1421 as close: %+v", r4.Hits)
	}
}

func TestFindByKey_NotOnAnyBoardOpensInTheCurrentStandup(t *testing.T) {
	ts := newTicketApp(t)
	// archive-style tombstone: drop the membership of one issue (what a sync does when it leaves every board)
	id := ts.idOf("MOB-1421")
	if _, err := ts.DB.W.Exec(`DELETE FROM standup_issue WHERE issue_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	r, err := ts.Svc.Ticket.FindByKey(ctx, "1421", ts.standup, 20)
	noErr(t, err, "FindByKey")
	if len(r.Hits) == 0 || r.Hits[0].Key != "MOB-1421" {
		t.Fatalf("hits = %+v", r.Hits)
	}
	h := r.Hits[0]
	if !h.Removed || h.StandupID != ts.standup || len(h.StandupIDs) != 0 || h.Lane == "" {
		t.Errorf("tombstone hit = %+v", h)
	}
	r0, err := ts.Svc.Ticket.FindByKey(ctx, "1421", 0, 20) // nothing to look from
	noErr(t, err, "FindByKey no standup")
	if h := r0.Hits[0]; h.StandupID != 0 || h.Lane != "" || !h.Removed {
		t.Errorf("no-standup hit = %+v", h)
	}
}

func TestByIDs_OrderUnknownSkippedAndLocal(t *testing.T) {
	ts := newTicketApp(t)
	a, b := ts.idOf("MOB-1421"), ts.v.Tickets[0].ID
	ts.Fake.ResetRequests()
	mark := ts.networkCalls()
	got, err := ts.Svc.Ticket.ByIDs(ctx, []int64{b, 987654, a}, ts.standup)
	noErr(t, err, "ByIDs")
	ts.assertNoNetwork(mark, "TicketService.ByIDs")
	if len(ts.Fake.Requests()) != 0 {
		t.Errorf("ByIDs reached the fake Jira")
	}
	if len(got) != 2 || got[0].IssueID != b || got[1].IssueID != a {
		t.Fatalf("ByIDs = %+v", got)
	}
	for _, h := range got {
		if h.Tier != "" || h.Match == nil || len(h.Match) != 0 || h.StandupID != ts.standup || h.Lane == "" || h.StandupIDs == nil {
			t.Errorf("hit %+v", h)
		}
	}
	empty, err := ts.Svc.Ticket.ByIDs(ctx, nil, ts.standup)
	noErr(t, err, "ByIDs nil")
	if empty == nil || len(empty) != 0 {
		t.Errorf("empty = %#v", empty)
	}
}

func TestPeople_DedupedAcrossStandupsInTabOrderLocal(t *testing.T) {
	ts := newTicketApp(t)
	ts.Fake.ResetRequests()
	mark := ts.networkCalls()
	people, err := ts.Svc.Standup.People(ctx)
	noErr(t, err, "People")
	ts.assertNoNetwork(mark, "StandupService.People")
	if len(people) == 0 {
		t.Fatal("no people")
	}
	seen := map[string]bool{}
	active := 0
	for _, p := range people {
		if seen[p.AccountID] {
			t.Errorf("%s twice", p.AccountID)
		}
		seen[p.AccountID] = true
		if p.DisplayName == "" || p.Initials == "" || len(p.Standups) == 0 || p.Standups[0].StandupID != ts.standup {
			t.Errorf("person %+v", p)
		}
		active += p.Standups[0].Active
	}
	wantActive := 0
	for _, sp := range ts.v.Speakers {
		if !sp.Unassigned {
			wantActive += sp.Active
		}
	}
	if active != wantActive {
		t.Errorf("active counts %d, the view's speakers hold %d", active, wantActive)
	}
}

// unsyncedKey is a fixture issue the scrum standup never synced (another board's), found through the finder itself.
func unsyncedKey(t *testing.T, ts *ticketApp) string {
	t.Helper()
	for _, is := range ts.Fx.fx.Issues {
		r, err := ts.Svc.Ticket.FindByKey(ctx, is.Key, ts.standup, 5)
		noErr(t, err, "FindByKey")
		if len(r.Hits) == 0 || r.Hits[0].Key != is.Key {
			return is.Key
		}
	}
	t.Fatal("every fixture issue is synced")
	return ""
}

func TestFetchByKey_StoresWithoutMembershipOneRequestInteractive(t *testing.T) {
	ts := newTicketApp(t)
	key := unsyncedKey(t, ts)
	ts.Em.reset()
	ts.Fake.ResetRequests()
	d, err := ts.Svc.Ticket.FetchByKey(ctx, ts.standup, strings.ToLower(" "+key+" "))
	noErr(t, err, "FetchByKey")
	if d.Row.Key != key || d.StandupID != ts.standup || d.IssueID == 0 {
		t.Fatalf("detail row = %+v", d.Row)
	}
	// GetIssue only: the issue itself (+ its comments/changelog when the adapter pages them), never a search or the board
	issueReqs := 0
	for _, r := range ts.requests() {
		if strings.Contains(r, "/issue/"+key) || strings.Contains(r, "/issue/"+d.Row.SourceID) {
			issueReqs++
		}
		if strings.Contains(r, "/search") || strings.Contains(r, "/agile/") {
			t.Errorf("FetchByKey made a board/search request: %s", r)
		}
	}
	fetchReqs := len(ts.requests())
	updated := len(ts.Em.named("ticket:updated"))
	if issueReqs == 0 {
		t.Errorf("requests = %v", ts.requests())
	}
	// exactly what Refresh sends for one issue (GetIssue: the issue, its changelog, its comments), nothing more
	ts.Fake.ResetRequests()
	_, err = ts.Svc.Ticket.Refresh(ctx, ts.standup, ts.idOf("MOB-1421"))
	noErr(t, err, "Refresh")
	ts.Svc.WaitBackground()
	refreshReqs := 0
	for _, r := range ts.requests() {
		if !strings.Contains(r, "/media/") && !strings.Contains(r, "/attachment/") && !strings.Contains(r, "/thumbnail/") {
			refreshReqs++
		}
	}
	if fetchReqs != refreshReqs {
		t.Errorf("FetchByKey sent %d requests, Refresh sends %d", fetchReqs, refreshReqs)
	}
	// the interactive lane: the same marker Refresh uses; the issue GET was the first request
	if first := ts.requests()[0]; !strings.Contains(first, "/issue/") && !strings.Contains(first, "/field") {
		t.Errorf("first request = %s", first)
	}
	if updated != 1 {
		t.Errorf("ticket:updated events = %d", updated)
	}

	// stored with no membership: findable as "not on board", opens in the standup it was fetched for
	r, err := ts.Svc.Ticket.FindByKey(ctx, key, ts.standup, 5)
	noErr(t, err, "FindByKey after")
	if len(r.Hits) == 0 || r.Hits[0].Key != key || !r.Hits[0].Removed || len(r.Hits[0].StandupIDs) != 0 || r.Hits[0].StandupID != ts.standup {
		t.Fatalf("hit after fetch = %+v", r.Hits)
	}
	// and the modal opens it offline (local)
	ts.Fake.ResetRequests()
	mark := ts.networkCalls()
	if _, err := ts.Svc.Ticket.Get(ctx, ts.standup, d.IssueID); err != nil {
		t.Fatalf("Get after fetch: %v", err)
	}
	ts.assertNoNetwork(mark, "Get of a fetched ticket")

	// a later sync of the standup neither purges nor re-members it
	ts.syncNow(ts.standup)
	r, err = ts.Svc.Ticket.FindByKey(ctx, key, ts.standup, 5)
	noErr(t, err, "FindByKey after sync")
	if len(r.Hits) == 0 || r.Hits[0].Key != key || !r.Hits[0].Removed {
		t.Errorf("after a sync: %+v", r.Hits)
	}
	ts.Svc.WaitBackground()
}

func TestFetchByKey_ErrorsAndNoRequestForABadKey(t *testing.T) {
	ts := newTicketApp(t, withFakeOptions(func(o *jiratest.Options) {
		o.Fail = append(o.Fail, jiratest.FailStatus{PathPrefix: "/rest/api/3/issue/FORB-7", Code: 403})
	}))
	for _, bad := range []string{"", "mob", "MOB-", "MOB-0", "../MOB-1", "MOB-1/comment", "1-2", "MOB-12 34"} {
		ts.Fake.ResetRequests()
		mark := ts.networkCalls()
		_, err := ts.Svc.Ticket.FetchByKey(ctx, ts.standup, bad)
		wantCode(t, err, apperr.Validation, "FetchByKey "+bad)
		ts.assertNoNetwork(mark, "a bad key "+bad)
		if len(ts.Fake.Requests()) != 0 {
			t.Errorf("bad key %q made requests", bad)
		}
	}
	_, err := ts.Svc.Ticket.FetchByKey(ctx, 9999, "MOB-1")
	if err == nil {
		t.Error("a missing standup fetched")
	}

	_, err = ts.Svc.Ticket.FetchByKey(ctx, ts.standup, "MOB-987654")
	wantCode(t, err, apperr.NotFound, "unknown key")
	_, err = ts.Svc.Ticket.FetchByKey(ctx, ts.standup, "FORB-7")
	wantCode(t, err, apperr.Forbidden, "403")

	ts.Net.down.Store(true)
	ts.Fake.ResetRequests()
	_, err = ts.Svc.Ticket.FetchByKey(ctx, ts.standup, "MOB-12345")
	wantCode(t, err, apperr.Offline, "offline")
	ts.Net.down.Store(false)
}

// The finder reuses a standup's lane mapping by rev; a Configure change must show in the next find.
func TestFindByKey_LaneLimitChangeShowsAtOnce(t *testing.T) {
	ts := newTicketApp(t)
	var tk view.TicketView
	for _, x := range ts.v.Tickets {
		if x.Lane != "done" && x.DaysInLane >= 2 {
			tk = x
			break
		}
	}
	if tk.ID == 0 {
		t.Skip("no ticket with 2+ days in a working lane")
	}
	find := func() view.TicketHit {
		r, err := ts.Svc.Ticket.FindByKey(ctx, tk.Key, ts.standup, 5)
		noErr(t, err, "FindByKey")
		return r.Hits[0]
	}
	find() // fills the cache
	_, err := ts.Svc.Config.SetLaneLimit(ctx, ts.standup, tk.Lane, 1)
	noErr(t, err, "SetLaneLimit")
	if h := find(); !h.OverLimit {
		t.Errorf("after limit 1 a %d-day ticket is not over the limit: %+v", tk.DaysInLane, h)
	}
	_, err = ts.Svc.Config.SetLaneLimit(ctx, ts.standup, tk.Lane, 0)
	noErr(t, err, "SetLaneLimit 0")
	if h := find(); h.OverLimit {
		t.Errorf("limit 0 means no limit: %+v", h)
	}
}
