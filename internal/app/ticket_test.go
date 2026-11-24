package app_test

// The ticket modal end to end through the services (F12, WP-T9): the real fake Jira, the real adapter, orchestrator,
// store and media cache. docs/testing.md "Light writes", "Ticket refresh and throttling", "No network on ticket open".

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
	"github.com/crueber/cuisle/internal/view"
)

// ticketApp is a connected, synced standup with the media cache wired, plus MOB-1421 (the content-rich fixture ticket).
type ticketApp struct {
	*testApp
	standup int64
	v       view.StandupView
	issue   int64 // MOB-1421
}

func newTicketApp(t *testing.T, opts ...appOpt) *ticketApp {
	t.Helper()
	ta := newTestApp(t, append([]appOpt{withMedia()}, opts...)...)
	id := ta.scrumStandup()
	ta.Orch.WaitMedia()
	ts := &ticketApp{testApp: ta, standup: id, v: ta.view(id)}
	ts.issue = ts.idOf("MOB-1421")
	return ts
}

func (ts *ticketApp) idOf(key string) int64 {
	ts.t.Helper()
	for _, tk := range ts.v.Tickets {
		if tk.Key == key {
			return tk.ID
		}
	}
	ts.t.Fatalf("%s is not on the board", key)
	return 0
}

// me is the connected account (the fake's /myself is its first user).
func (ts *ticketApp) me() (id, name string) {
	u := ts.Fx.fx.Users[0]
	return u.AccountID, u.DisplayName
}

// other is a fixture user who is neither the connected one nor the active issue's assignee.
func (ts *ticketApp) other() string {
	for _, u := range ts.Fx.fx.Users {
		if u.AccountID != ts.Fx.fx.Users[0].AccountID && u.AccountID != ts.Fx.activeAssign {
			return u.AccountID
		}
	}
	ts.t.Fatal("no other user")
	return ""
}

func (ts *ticketApp) get(issue int64) view.TicketDetail {
	ts.t.Helper()
	d, err := ts.Svc.Ticket.Get(ctx, ts.standup, issue)
	noErr(ts.t, err, "Ticket.Get")
	return d
}

func (ts *ticketApp) requests() []string {
	var out []string
	for _, r := range ts.Fake.Requests() {
		out = append(out, r.Method+" "+r.Path)
	}
	return out
}

func countPrefix(reqs []string, prefix string) int {
	n := 0
	for _, r := range reqs {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func TestTicketGet_MatchesTheGoldenAndIsLocal(t *testing.T) {
	ts := newTicketApp(t)
	ts.Fake.ResetRequests()
	mark := ts.networkCalls()
	d := ts.get(ts.issue)
	ts.assertNoNetwork(mark, "TicketService.Get")
	if d.Row.Key != "MOB-1421" || d.IssueID != ts.issue || d.StandupID != ts.standup {
		t.Fatalf("detail = %+v", d.Row)
	}
	if !d.Can.Transition || !d.Can.Assign || !d.Can.Comment || d.Can.Reason != "" {
		t.Errorf("Can = %+v, want every write offered", d.Can)
	}
	if d.Description == nil || len(d.Comments) == 0 || len(d.Attachments) == 0 {
		t.Errorf("content missing: description %v comments %d attachments %d", d.Description != nil, len(d.Comments), len(d.Attachments))
	}
	for _, c := range d.Comments {
		if c.Author.Name == "" {
			t.Errorf("comment %s has no author name", c.ID)
		}
	}
}

// TestNoTrigger_TicketOpenStepAndDraftsMakeNoRequests extends the no-trigger test (testing.md section 3) to the modal:
// after a restart over synced data, opening and stepping through every ticket, drafts, field prefs and the /media routes
// leave the request log empty and touch no sink, outbox, run or event of a sync.
func TestNoTrigger_TicketOpenStepAndDraftsMakeNoRequests(t *testing.T) {
	q := newQuietApp(t, "never", withMedia())
	ts := &ticketApp{testApp: q.testApp, standup: q.scrum, v: q.view(q.scrum)}
	ts.issue = ts.idOf("MOB-1421")
	q.assertQuiet("setup")

	for _, tk := range ts.v.Tickets { // open and step through every ticket of the view
		ts.get(tk.ID)
		if _, err := ts.Svc.Ticket.Draft(ctx, tk.ID); err != nil {
			t.Fatalf("Draft: %v", err)
		}
	}
	for _, b := range ts.v.Backlog {
		if b.IssueID != 0 {
			ts.get(b.IssueID)
		}
	}
	in := view.CommentInput{Text: "hello @Dev", Mentions: []view.Mention{{Start: 6, End: 10, AccountID: "u-me", Name: "Dev"}}}
	if _, err := ts.Svc.Ticket.SaveDraft(ctx, ts.issue, in); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if d := ts.get(ts.issue); d.Draft.Text != in.Text {
		t.Errorf("draft in the detail = %q", d.Draft.Text)
	}
	prefs, err := ts.Svc.Connection.TicketFields(ctx)
	noErr(t, err, "TicketFields")
	if len(prefs.Available) == 0 {
		t.Fatal("no pinnable field in the fixture")
	}
	field := prefs.Available[0].FieldID
	_, err = ts.Svc.Connection.PinTicketField(ctx, field)
	noErr(t, err, "Pin")
	_, err = ts.Svc.Connection.MoveTicketField(ctx, field, 0)
	noErr(t, err, "Move")
	_, err = ts.Svc.Connection.SetTicketFieldHidden(ctx, field, true)
	noErr(t, err, "Hide")
	ts.get(ts.issue) // the modal with the preference applied
	_, err = ts.Svc.Connection.SetTicketFieldHidden(ctx, field, false)
	noErr(t, err, "Unhide")
	_, err = ts.Svc.Connection.UnpinTicketField(ctx, field)
	noErr(t, err, "Unpin")
	for _, order := range []string{"oldest", "newest"} {
		_, err = ts.Svc.App.SetAppSetting(ctx, "ticket.comment_order", order)
		noErr(t, err, "SetAppSetting(ticket.comment_order)")
	}
	if _, err := ts.Svc.App.MediaCache(ctx); err != nil {
		t.Fatal(err)
	}
	// the /media routes are cache-only: 200 for every cached image thumbnail, 404 for a miss, never a request
	h := app.AssetMiddleware(ts.Svc)(http.NotFoundHandler())
	served, fullServed := 0, 0
	for _, a := range ts.get(ts.issue).Attachments {
		if !a.Image {
			continue
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, a.ThumbURL, nil))
		if a.Cached == "none" {
			if rec.Code != 404 {
				t.Errorf("GET %s (not cached) = %d, want 404", a.ThumbURL, rec.Code)
			}
			continue
		}
		served++
		if rec.Code != 200 {
			t.Errorf("GET %s = %d, want 200", a.ThumbURL, rec.Code)
		}
		if a.Cached == "full" { // the full-size route is cache-only too
			fullServed++
			full := httptest.NewRecorder()
			h.ServeHTTP(full, httptest.NewRequest(http.MethodGet, a.URL, nil))
			if full.Code != 200 || !strings.HasPrefix(full.Header().Get("Content-Type"), "image/") {
				t.Errorf("GET %s = %d %q, want a cached image", a.URL, full.Code, full.Header().Get("Content-Type"))
			}
		}
	}
	if served == 0 || fullServed == 0 {
		t.Errorf("served %d thumbnails and %d full-size images: the test proves nothing", served, fullServed)
	}
	// with the network down the same calls still answer
	ts.Net.down.Store(true)
	ts.get(ts.issue)
	ts.Net.down.Store(false)
	q.assertQuiet("opening, stepping, drafts, field prefs and /media")
}

func TestMediaRoutes_ServeOnlyTheCache(t *testing.T) {
	ts := newTicketApp(t)
	d := ts.get(ts.issue)
	var img *view.AttachmentView
	for i := range d.Attachments {
		if d.Attachments[i].Image && d.Attachments[i].Cached != "none" {
			img = &d.Attachments[i]
			break
		}
	}
	if img == nil {
		t.Fatalf("no cached image after the media phase: %+v", d.Attachments)
	}
	h := app.AssetMiddleware(ts.Svc)(http.NotFoundHandler())
	ts.Fake.ResetRequests()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, img.ThumbURL, nil))
	if rec.Code != 200 {
		t.Fatalf("GET %s = %d", img.ThumbURL, rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("thumb Content-Type = %q", got)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("headers = %v", rec.Header())
	}
	for _, bad := range []string{"/media/999999", "/media/999999/thumb", "/media/abc", "/media/0", "/media/-1", "/media/1/../2", "/media/01"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, bad, nil))
		if rec.Code != 404 {
			t.Errorf("GET %s = %d, want 404", bad, rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, img.URL, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
	if reqs := ts.requests(); len(reqs) != 0 {
		t.Errorf("the media routes made requests: %v", reqs)
	}

	// an evicted original is a miss (404) and never triggers a fetch
	if err := ts.Media.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, img.URL, nil))
	if rec.Code != 404 || len(ts.requests()) != 0 {
		t.Errorf("after Clear: GET = %d, requests %v", rec.Code, ts.requests())
	}
}

func TestEnsureMedia_FetchesAnEvictedImageOnceAndEmitsReady(t *testing.T) {
	ts := newTicketApp(t)
	var img view.AttachmentView
	for _, a := range ts.get(ts.issue).Attachments {
		if a.Image {
			img = a
			break
		}
	}
	if img.ID == 0 {
		t.Fatal("no image attachment")
	}
	if err := ts.Media.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	ts.Em.reset()
	ts.Fake.ResetRequests()
	got, err := ts.Svc.Ticket.EnsureMedia(ctx, img.ID)
	noErr(t, err, "EnsureMedia")
	if got.Cached != "full" {
		t.Errorf("cached = %q, want full", got.Cached)
	}
	reqs := ts.requests()
	if n := countPrefix(reqs, "GET /rest/api/3/attachment/content/"); n != 1 {
		t.Errorf("attachment content requests = %d, want 1: %v", n, reqs)
	}
	if evs := ts.Em.named("media:ready"); len(evs) != 1 {
		t.Errorf("media:ready events = %+v", evs)
	}
	// now cached: a second call makes no request
	ts.Fake.ResetRequests()
	if _, err := ts.Svc.Ticket.EnsureMedia(ctx, img.ID); err != nil {
		t.Fatal(err)
	}
	if reqs := ts.requests(); len(reqs) != 0 {
		t.Errorf("second EnsureMedia made requests: %v", reqs)
	}
	if _, err := ts.Svc.Ticket.EnsureMedia(ctx, 987654); err == nil {
		t.Error("an unknown attachment must fail")
	}
}

func TestOpenAttachment_WritesATempFileAndOpensItWithTheOS(t *testing.T) {
	ts := newTicketApp(t)
	var file view.AttachmentView
	for _, a := range ts.get(ts.issue).Attachments {
		if !a.Image {
			file = a
			break
		}
	}
	if file.ID == 0 {
		t.Fatal("no file attachment on MOB-1421")
	}
	ts.Fake.ResetRequests()
	noErr(t, ts.Svc.Ticket.OpenAttachment(ctx, file.ID), "OpenAttachment")
	paths := ts.openedPaths()
	if len(paths) != 1 {
		t.Fatalf("opened = %v", paths)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(paths[0])) })
	if !strings.HasPrefix(paths[0], os.TempDir()) || filepath.Base(paths[0]) != file.Name {
		t.Errorf("opened %q, want a file named %q in the OS temp dir", paths[0], file.Name)
	}
	if fi, err := os.Stat(paths[0]); err != nil || fi.Size() == 0 {
		t.Errorf("temp file: %v %v", fi, err)
	}
	if n := countPrefix(ts.requests(), "GET /rest/api/3/attachment/content/"); n != 1 {
		t.Errorf("requests = %v", ts.requests())
	}
}

func TestOpenAttachment_NamesAreCheckedAgainstAnAllowlistBeforeAnythingHappens(t *testing.T) {
	ts := newTicketApp(t)
	id := int64(0)
	_ = ts.DB.R.QueryRow(`SELECT MIN(id) FROM attachment WHERE issue_id = ? AND is_image = 0`, ts.issue).Scan(&id)
	if id == 0 {
		t.Fatal("no file attachment on MOB-1421")
	}
	for _, c := range []struct {
		name string
		want string // "" = refused with a validation error; else the file name that must be opened
	}{
		{"a.EXE.", ""}, {"a.tool", ""}, {"a.html", ""}, {"a.svg", ""}, {"a.webloc", ""}, {"a.jnlp", ""},
		{"x.sh\x00", ""}, {"..\\a.exe", ""}, {"invoice\u202efdp.exe", ""}, {"noextension", ""}, {"a.PDF.exe", ""},
		{"a.command", ""}, {"a.app", ""}, {"a.bat", ""}, {"a.js", ""}, {"a.lnk", ""}, {"a.dmg", ""},
		{strings.Repeat("a", 296) + ".pdf", ""}, {"", ""}, {"...", ""},
		{"report.pdf", "report.pdf"}, {"Report.PDF", "Report.PDF"}, {"CON.txt", "_CON.txt"}, {"nul.tar.zip", "_nul.tar.zip"},
		{"com1.png", "_com1.png"}, {"a/b\\c.csv", "a_b_c.csv"}, {"con.ference.pdf", "_con.ference.pdf"}, {"console.pdf", "console.pdf"}, {"notes.md", "notes.md"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ts.DB.W.Exec(`UPDATE attachment SET name = ? WHERE id = ?`, c.name, id); err != nil {
				t.Fatal(err)
			}
			ts.Fake.ResetRequests()
			before := len(ts.openedPaths())
			err := ts.Svc.Ticket.OpenAttachment(ctx, id)
			if c.want == "" {
				wantCode(t, err, apperr.Validation, "OpenAttachment("+c.name+")")
				if len(ts.openedPaths()) != before || len(ts.requests()) != 0 {
					t.Errorf("a refused name was opened (%v) or fetched (%v)", ts.openedPaths()[before:], ts.requests())
				}
				return
			}
			noErr(t, err, "OpenAttachment("+c.name+")")
			paths := ts.openedPaths()
			if len(paths) != before+1 {
				t.Fatalf("opened = %v", paths)
			}
			got := paths[len(paths)-1]
			t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(got)) })
			if filepath.Base(got) != c.want || !strings.HasPrefix(got, os.TempDir()) {
				t.Errorf("opened %q, want a file named %q in the OS temp dir", got, c.want)
			}
			if n := countPrefix(ts.requests(), "GET /rest/api/3/attachment/content/"); n != 1 {
				t.Errorf("requests = %v", ts.requests())
			}
		})
	}
}

func TestTransitions_OneRequestCachedAndOfflineServesTheCache(t *testing.T) {
	ts := newTicketApp(t)
	ts.Fake.ResetRequests()
	list, err := ts.Svc.Ticket.Transitions(ctx, ts.issue)
	noErr(t, err, "Transitions")
	if reqs := ts.requests(); len(reqs) != 1 || !strings.HasPrefix(reqs[0], "GET /rest/api/3/issue/") || !strings.HasSuffix(reqs[0], "/transitions") {
		t.Fatalf("requests = %v, want exactly the transitions read", reqs)
	}
	if len(list.Items) == 0 || list.Offline {
		t.Fatalf("list = %+v", list)
	}
	for _, it := range list.Items {
		if it.ToLane == "" || it.URL == "" || it.ToStatusName == "" {
			t.Errorf("incomplete option %+v", it)
		}
	}
	var n int
	_ = ts.DB.R.QueryRow(`SELECT COUNT(*) FROM transition_cache WHERE issue_id = ?`, ts.issue).Scan(&n)
	if n != 1 {
		t.Errorf("transition_cache rows = %d", n)
	}
	ts.Net.down.Store(true)
	off, err := ts.Svc.Ticket.Transitions(ctx, ts.issue)
	noErr(t, err, "Transitions offline")
	if !off.Offline || len(off.Items) != len(list.Items) {
		t.Errorf("offline list = %+v", off)
	}
	if d := ts.get(ts.issue); d.Can.Reason != "offline" {
		t.Errorf("Can.Reason after an offline call = %q, want offline", d.Can.Reason)
	}
	ts.Net.down.Store(false)
}

// pick returns a transition that needs no screen and leads to another lane.
func pickTransition(t *testing.T, list view.TransitionList) view.TransitionOption {
	t.Helper()
	for _, it := range list.Items {
		if !it.NeedsScreen {
			return it
		}
	}
	t.Fatalf("no plain transition in %+v", list)
	return view.TransitionOption{}
}

func TestTransition_WriteRefreshApplyAndEvents(t *testing.T) {
	ts := newTicketApp(t)
	issue := ts.idOf(ts.Fx.activeKey)
	list, err := ts.Svc.Ticket.Transitions(ctx, issue)
	noErr(t, err, "Transitions")
	pick := pickTransition(t, list)
	before := ts.get(issue)
	revBefore := ts.view(ts.standup).Rev
	ts.Em.reset()
	ts.Fake.ResetRequests()
	d, err := ts.Svc.Ticket.Transition(ctx, ts.standup, issue, optionID(t, ts, issue, pick))
	noErr(t, err, "Transition")
	reqs := ts.requests()
	if n := countPrefix(reqs, "POST /rest/api/3/issue/"); n != 1 {
		t.Errorf("POST count = %d, want exactly 1: %v", n, reqs)
	}
	if n := countPrefix(reqs, "GET /rest/api/3/issue/"); n < 2 { // the transitions lookup inside the write + the refresh
		t.Errorf("expected the refresh GET: %v", reqs)
	}
	if d.Row.StatusName == before.Row.StatusName {
		t.Errorf("status still %q after the transition to %q", d.Row.StatusName, pick.ToStatusName)
	}
	if d.Row.StatusName != pick.ToStatusName {
		t.Errorf("status = %q, want %q", d.Row.StatusName, pick.ToStatusName)
	}
	if v := ts.view(ts.standup); v.Rev <= revBefore {
		t.Errorf("standup rev %d did not move past %d", v.Rev, revBefore)
	}
	upd := ts.Em.named("ticket:updated")
	if len(upd) != 1 || upd[0].Payload["cause"] != "write" || upd[0].Payload["key"] != ts.Fx.activeKey {
		t.Errorf("ticket:updated = %+v", upd)
	}
	foundView := false
	for _, e := range ts.Em.named("data:changed") {
		foundView = foundView || (e.Payload["what"] == "view" && int64(e.Payload["standupId"].(float64)) == ts.standup)
	}
	if !foundView {
		t.Errorf("no data:changed view for the standup: %+v", ts.Em.named("data:changed"))
	}
	// the transitions cache was dropped by the status change
	var n int
	_ = ts.DB.R.QueryRow(`SELECT COUNT(*) FROM transition_cache WHERE issue_id = ?`, issue).Scan(&n)
	if n != 0 {
		t.Errorf("transition_cache rows after a status change = %d", n)
	}
	// the change is historised from the provider's changelog, not invented locally
	var changes int
	_ = ts.DB.R.QueryRow(`SELECT COUNT(*) FROM status_change WHERE issue_id = ?`, issue).Scan(&changes)
	if changes == 0 {
		t.Errorf("no status_change rows")
	}
}

// optionID is the transition id for an option (the view carries names; ids come from the list).
func optionID(t *testing.T, ts *ticketApp, issue int64, o view.TransitionOption) string {
	t.Helper()
	return o.ID
}

func TestTransition_NeedsScreenIsNeverPosted(t *testing.T) {
	ts := newTicketApp(t)
	issue := ts.idOf(ts.Fx.activeKey)
	list, err := ts.Svc.Ticket.Transitions(ctx, issue)
	noErr(t, err, "Transitions")
	var screen *view.TransitionOption
	for i := range list.Items {
		if list.Items[i].NeedsScreen {
			screen = &list.Items[i]
		}
	}
	if screen == nil {
		t.Skip("the fake offers no screen transition for this issue")
	}
	ts.Em.reset()
	ts.Fake.ResetRequests()
	_, err = ts.Svc.Ticket.Transition(ctx, ts.standup, issue, screen.ID)
	wantCode(t, err, apperr.TransitionNeedsScreen, "Transition(needs screen)")
	if n := countPrefix(ts.requests(), "POST "); n != 0 {
		t.Errorf("a needs-screen transition was posted: %v", ts.requests())
	}
	if evs := ts.Em.named("ticket:write_failed"); len(evs) != 1 || evs[0].Payload["action"] != "transition" {
		t.Errorf("ticket:write_failed = %+v", evs)
	}
}

func TestWrites_FailureStoresNothingEmitsAndNeverRetries(t *testing.T) {
	ts := newTicketApp(t)
	issue := ts.idOf(ts.Fx.activeKey)
	in := view.CommentInput{Text: "will not arrive"}
	if _, err := ts.Svc.Ticket.SaveDraft(ctx, issue, in); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		d := ts.get(issue)
		d.Can.Reason = ""
		b, _ := json.Marshal(d)
		return string(b)
	}
	before, revBefore := snapshot(), ts.view(ts.standup).Rev
	ts.Net.down.Store(true)
	for _, c := range []struct {
		action string
		do     func() error
	}{
		{"assign", func() error { _, err := ts.Svc.Ticket.Assign(ctx, ts.standup, issue, "u-me"); return err }},
		{"flag", func() error { _, err := ts.Svc.Ticket.SetFlag(ctx, ts.standup, issue, true); return err }},
		{"comment", func() error { _, err := ts.Svc.Ticket.AddComment(ctx, ts.standup, issue, in); return err }},
		{"transition", func() error { _, err := ts.Svc.Ticket.Transition(ctx, ts.standup, issue, "11"); return err }},
	} {
		ts.Em.reset()
		attempts := ts.networkCalls()
		wantCode(t, c.do(), apperr.Offline, c.action)
		evs := ts.Em.named("ticket:write_failed")
		if len(evs) != 1 || evs[0].Payload["action"] != c.action || evs[0].Payload["errorCode"] != "offline" {
			t.Errorf("%s: ticket:write_failed = %+v", c.action, evs)
		}
		if n := ts.networkCalls() - attempts; n > 2 { // at most the one attempt (and its lookup); never a retry loop
			t.Errorf("%s: %d attempts", c.action, n)
		}
		if len(ts.Em.named("ticket:updated")) != 0 || len(ts.Em.named("data:changed")) != 0 {
			t.Errorf("%s: a failed write announced a change", c.action)
		}
	}
	ts.Net.down.Store(false)
	if after := snapshot(); after != before {
		t.Errorf("a failed write changed the stored ticket")
	}
	if v := ts.view(ts.standup); v.Rev != revBefore {
		t.Errorf("rev moved %d -> %d on failures", revBefore, v.Rev)
	}
	if dr, _ := ts.Svc.Ticket.Draft(ctx, issue); dr.Text != in.Text {
		t.Errorf("the draft was lost by a failed send: %q", dr.Text)
	}
}

func TestWrites_RefreshFailureAppliesTheDeltaAndWarns(t *testing.T) {
	ts := newTicketApp(t, withFakeOptions(func(o *jiratest.Options) { o.RefreshFails = true }))
	issue := ts.idOf(ts.Fx.activeKey)
	list, err := ts.Svc.Ticket.Transitions(ctx, issue)
	noErr(t, err, "Transitions")
	pick := pickTransition(t, list)
	ts.Em.reset()
	d, err := ts.Svc.Ticket.Transition(ctx, ts.standup, issue, pick.ID)
	noErr(t, err, "Transition with a failing refresh")
	if !contains(d.Warnings, view.WarnRefreshFailed) {
		t.Errorf("warnings = %v, want refresh_failed", d.Warnings)
	}
	if d.Row.StatusName != pick.ToStatusName {
		t.Errorf("status = %q, want the known delta %q", d.Row.StatusName, pick.ToStatusName)
	}
	var changes int
	_ = ts.DB.R.QueryRow(`SELECT COUNT(*) FROM status_change WHERE issue_id = ? AND changed_at >= ?`, issue, ts.Clock.ms()-1000).Scan(&changes)
	if changes != 0 {
		t.Errorf("a status_change row was invented locally")
	}
	if len(ts.Em.named("ticket:updated")) != 1 {
		t.Errorf("events = %+v", ts.Em.all())
	}
	d2, err := ts.Svc.Ticket.Assign(ctx, ts.standup, issue, ts.other())
	noErr(t, err, "Assign with a failing refresh")
	if d2.Row.Assignee != ts.other() || !contains(d2.Warnings, view.WarnRefreshFailed) {
		t.Errorf("assign delta: %q %v", d2.Row.Assignee, d2.Warnings)
	}
}

func TestAssignFlagComment_Success(t *testing.T) {
	ts := newTicketApp(t)
	issue := ts.idOf(ts.Fx.activeKey)
	me, meName := ts.me()
	other := ts.other()

	ts.Em.reset()
	ts.Fake.ResetRequests()
	d, err := ts.Svc.Ticket.Assign(ctx, ts.standup, issue, other)
	noErr(t, err, "Assign")
	if d.Row.Assignee != other {
		t.Errorf("assignee = %q, want %q", d.Row.Assignee, other)
	}
	if n := countPrefix(ts.requests(), "PUT "); n != 1 {
		t.Errorf("assign requests = %v", ts.requests())
	}
	d, err = ts.Svc.Ticket.Assign(ctx, ts.standup, issue, "")
	noErr(t, err, "Unassign")
	if d.Row.Assignee != "" {
		t.Errorf("assignee after unassign = %q", d.Row.Assignee)
	}

	d, err = ts.Svc.Ticket.SetFlag(ctx, ts.standup, issue, true)
	noErr(t, err, "SetFlag")
	if !d.Row.Blocked {
		t.Errorf("not blocked after flagging")
	}
	d, err = ts.Svc.Ticket.SetFlag(ctx, ts.standup, issue, false)
	noErr(t, err, "unflag")
	if d.Row.Blocked {
		t.Errorf("still blocked after unflagging")
	}

	in := view.CommentInput{Text: "Looks good, @" + meName, Mentions: []view.Mention{{Start: 12, End: 13 + len([]rune(meName)), AccountID: me, Name: meName}}}
	_, err = ts.Svc.Ticket.SaveDraft(ctx, issue, in)
	noErr(t, err, "SaveDraft")
	before := len(ts.get(issue).Comments)
	ts.Fake.ResetRequests()
	d, err = ts.Svc.Ticket.AddComment(ctx, ts.standup, issue, in)
	noErr(t, err, "AddComment")
	if len(d.Comments) != before+1 {
		t.Errorf("comments %d -> %d", before, len(d.Comments))
	}
	if last := d.Comments[len(d.Comments)-1]; last.Author.AccountID != me || !last.Author.Self {
		t.Errorf("last comment author = %+v", last.Author)
	}
	if n := countPrefix(ts.requests(), "POST /rest/api/3/issue/"); n != 1 {
		t.Errorf("comment POSTs = %v", ts.requests())
	}
	if d.Draft.Text != "" {
		t.Errorf("the draft survived a successful send: %q", d.Draft.Text)
	}
	if dr, _ := ts.Svc.Ticket.Draft(ctx, issue); dr.Text != "" {
		t.Errorf("stored draft after send = %q", dr.Text)
	}
}

func TestWrites_RefusedBeforeAnyRequestWhenAuthIsNotOK(t *testing.T) {
	ts := newTicketApp(t)
	issue := ts.idOf(ts.Fx.activeKey)
	noErr(t, ts.Svc.Connection.Disconnect(ctx, false), "Disconnect")
	ts.Em.reset()
	ts.Fake.ResetRequests()
	mark := ts.networkCalls()
	_, err := ts.Svc.Ticket.SetFlag(ctx, ts.standup, issue, true)
	wantCode(t, err, apperr.AuthMissing, "SetFlag without a token")
	_, err = ts.Svc.Ticket.AddComment(ctx, ts.standup, issue, view.CommentInput{Text: "x"})
	wantCode(t, err, apperr.AuthMissing, "AddComment without a token")
	_, err = ts.Svc.Ticket.Refresh(ctx, ts.standup, issue)
	wantCode(t, err, apperr.AuthMissing, "Refresh without a token")
	_, err = ts.Svc.Ticket.Transitions(ctx, issue)
	wantCode(t, err, apperr.AuthMissing, "Transitions without a token")
	ts.assertNoNetwork(mark, "refused writes")
	if evs := ts.Em.named("ticket:write_failed"); len(evs) != 2 {
		t.Errorf("ticket:write_failed = %+v", evs)
	}
	if d := ts.get(issue); d.Can.Reason != "auth" || d.Can.Comment {
		t.Errorf("Can = %+v", d.Can)
	}
}

func TestRefresh_OnlyThatIssueCooldownAndOffline(t *testing.T) {
	ts := newTicketApp(t)
	issue := ts.idOf(ts.Fx.activeKey)
	ts.Em.reset()
	ts.Fake.ResetRequests()
	d, err := ts.Svc.Ticket.Refresh(ctx, ts.standup, issue)
	noErr(t, err, "Refresh")
	if d.Row.Key != ts.Fx.activeKey {
		t.Errorf("key = %q", d.Row.Key)
	}
	for _, r := range ts.requests() {
		if !strings.Contains(r, "/issue/") && !strings.Contains(r, "/field") && !strings.Contains(r, "/comment") && !strings.Contains(r, "bulkfetch") && !strings.Contains(r, "/attachment/") && !strings.Contains(r, "/media/") {
			t.Errorf("a Refresh touched %q (not this issue)", r)
		}
	}
	ts.Svc.WaitBackground()
	if evs := ts.Em.named("ticket:updated"); len(evs) != 1 || evs[0].Payload["issueId"] == nil {
		t.Errorf("ticket:updated = %+v", evs)
	}

	// a second Refresh within 10 s: cooldown, zero requests
	ts.Fake.ResetRequests()
	mark := ts.networkCalls()
	_, err = ts.Svc.Ticket.Refresh(ctx, ts.standup, issue)
	ae := wantCode(t, err, apperr.Cooldown, "second Refresh")
	if ae != nil && (ae.RetryInMs <= 0 || ae.RetryInMs > 10_001) {
		t.Errorf("retryInMs = %d", ae.RetryInMs)
	}
	ts.assertNoNetwork(mark, "a Refresh inside the cooldown")

	// after the cooldown, offline: an error and the stored content is untouched
	ts.Clock.Advance(11 * time.Second)
	ts.Net.down.Store(true)
	before, _ := json.Marshal(ts.get(issue))
	_, err = ts.Svc.Ticket.Refresh(ctx, ts.standup, issue)
	wantCode(t, err, apperr.Offline, "offline Refresh")
	ts.Net.down.Store(false)
	after, _ := json.Marshal(func() view.TicketDetail { x := ts.get(issue); x.Can.Reason = ""; return x }())
	b2 := bytes.ReplaceAll(before, []byte(`"reason":"offline"`), []byte(`"reason":""`))
	if !bytes.Equal(b2, after) {
		t.Errorf("an offline Refresh changed the stored content")
	}
}

func TestDrafts_SurviveARestartEmptyDeletesAndValidate(t *testing.T) {
	ts := newTicketApp(t)
	issue := ts.issue
	in := view.CommentInput{Text: "ping @Dev User later", Mentions: []view.Mention{{Start: 5, End: 14, AccountID: "u-me", Name: "Dev User"}}}
	if _, err := ts.Svc.Ticket.SaveDraft(ctx, issue, in); err != nil {
		t.Fatal(err)
	}
	ts.restart()
	got, err := ts.Svc.Ticket.Draft(ctx, issue)
	noErr(t, err, "Draft")
	if got.Text != in.Text || len(got.Mentions) != 1 || got.Mentions[0].AccountID != "u-me" {
		t.Errorf("draft after restart = %+v", got)
	}
	if _, err := ts.Svc.Ticket.SaveDraft(ctx, issue, view.CommentInput{}); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = ts.DB.R.QueryRow(`SELECT COUNT(*) FROM ticket_draft`).Scan(&n)
	if n != 0 {
		t.Errorf("ticket_draft rows = %d, want 0", n)
	}
	got, _ = ts.Svc.Ticket.Draft(ctx, issue)
	if got.Text != "" || got.Mentions == nil {
		t.Errorf("empty draft = %+v", got)
	}
	_, err = ts.Svc.Ticket.SaveDraft(ctx, issue, view.CommentInput{Text: "short", Mentions: []view.Mention{{Start: 0, End: 99, AccountID: "x"}}})
	wantCode(t, err, apperr.Validation, "a mention past the end")
	_, err = ts.Svc.Ticket.SaveDraft(ctx, 424242, view.CommentInput{Text: "x"})
	wantCode(t, err, apperr.NotFound, "a draft for an unknown issue")
}

func TestTicketFieldPrefs_PinMoveHideUnpin(t *testing.T) {
	ts := newTicketApp(t)
	prefs, err := ts.Svc.Connection.TicketFields(ctx)
	noErr(t, err, "TicketFields")
	if len(prefs.Pinned) != 0 || len(prefs.Hidden) != 0 || len(prefs.Available) < 3 {
		t.Fatalf("defaults = %+v", prefs)
	}
	a, b, c := prefs.Available[0].FieldID, prefs.Available[1].FieldID, prefs.Available[2].FieldID
	for _, id := range []string{a, b, c} {
		prefs, err = ts.Svc.Connection.PinTicketField(ctx, id)
		noErr(t, err, "Pin")
	}
	if got := fieldIDs(prefs.Pinned); strings.Join(got, ",") != a+","+b+","+c {
		t.Errorf("pinned = %v", got)
	}
	_, err = ts.Svc.Connection.MoveTicketField(ctx, c, 0)
	noErr(t, err, "Move")
	prefs, _ = ts.Svc.Connection.TicketFields(ctx)
	if got := fieldIDs(prefs.Pinned); strings.Join(got, ",") != c+","+a+","+b {
		t.Errorf("after move = %v", got)
	}
	prefs, err = ts.Svc.Connection.SetTicketFieldHidden(ctx, a, true)
	noErr(t, err, "Hide")
	if len(prefs.Hidden) != 1 || prefs.Hidden[0].FieldID != a || !prefs.Pinned[1].Hidden {
		t.Errorf("hide: %+v", prefs)
	}
	// a hidden field is not a row and not in Not set
	d := ts.get(ts.issue)
	for _, g := range d.Rail {
		for _, r := range g.Rows {
			if r.FieldID == a {
				t.Errorf("hidden field %s still has a row", a)
			}
		}
	}
	prefs, err = ts.Svc.Connection.UnpinTicketField(ctx, a)
	noErr(t, err, "Unpin hidden")
	if len(prefs.Hidden) != 1 || len(prefs.Pinned) != 2 || prefs.Hidden[0].Position != -1 {
		t.Errorf("unpin of a hidden field: %+v", prefs)
	}
	prefs, err = ts.Svc.Connection.SetTicketFieldHidden(ctx, a, false)
	noErr(t, err, "Unhide")
	if len(prefs.Hidden) != 0 {
		t.Errorf("unhide: %+v", prefs)
	}
	_, err = ts.Svc.Connection.PinTicketField(ctx, "nope")
	wantCode(t, err, apperr.NotFound, "Pin(unknown)")
	_, err = ts.Svc.Connection.PinTicketField(ctx, "priority")
	wantCode(t, err, apperr.Validation, "Pin(system field)")
	// pinned order shows in the modal's custom group
	d = ts.get(ts.issue)
	var rows []string
	for _, g := range d.Rail {
		if g.ID == view.RailCustom {
			for _, r := range g.Rows {
				rows = append(rows, r.FieldID)
			}
		}
	}
	if len(rows) > 0 && rows[0] != c && !contains(fieldIDs(prefs.Pinned), rows[0]) {
		t.Logf("custom rows = %v (pinned first: %v)", rows, fieldIDs(prefs.Pinned))
	}
}

func fieldIDs(fs []view.FieldPref) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.FieldID)
	}
	return out
}

func TestDisconnect_DeleteContentKeepsHistoryAndDropsContent(t *testing.T) {
	ts := newTicketApp(t)
	count := func(q string) int {
		var n int
		if err := ts.DB.R.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	issues, statusChanges, comments := count(`SELECT COUNT(*) FROM issue`), count(`SELECT COUNT(*) FROM status_change`), count(`SELECT COUNT(*) FROM comment`)
	if count(`SELECT COUNT(*) FROM issue_content`) == 0 || ts.Media == nil {
		t.Fatal("no content to delete")
	}
	if _, err := ts.Svc.Ticket.SaveDraft(ctx, ts.issue, view.CommentInput{Text: "d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Svc.Ticket.Transitions(ctx, ts.issue); err != nil {
		t.Fatal(err)
	}

	// without deleteContent nothing changes
	noErr(t, ts.Svc.Connection.Disconnect(ctx, false), "Disconnect(false)")
	if count(`SELECT COUNT(*) FROM issue_content`) == 0 || count(`SELECT COUNT(*) FROM comment WHERE body_adf IS NOT NULL`) == 0 {
		t.Fatal("Disconnect(false) removed content")
	}
	noErr(t, ts.Svc.Connection.Disconnect(ctx, true), "Disconnect(true)")
	for _, q := range []string{
		`SELECT COUNT(*) FROM issue_content`, `SELECT COUNT(*) FROM issue_field_value`, `SELECT COUNT(*) FROM issue_link`,
		`SELECT COUNT(*) FROM transition_cache`, `SELECT COUNT(*) FROM ticket_draft`, `SELECT COUNT(*) FROM comment WHERE body_adf IS NOT NULL`,
		`SELECT COUNT(*) FROM attachment WHERE file_path IS NOT NULL OR thumb_path IS NOT NULL`,
	} {
		if n := count(q); n != 0 {
			t.Errorf("%s = %d after deleting content", q, n)
		}
	}
	if got := count(`SELECT COUNT(*) FROM issue`); got != issues {
		t.Errorf("issues %d -> %d", issues, got)
	}
	if got := count(`SELECT COUNT(*) FROM status_change`); got != statusChanges {
		t.Errorf("status_change %d -> %d", statusChanges, got)
	}
	if got := count(`SELECT COUNT(*) FROM comment`); got != comments {
		t.Errorf("comment rows %d -> %d (rows stay, bodies go)", comments, got)
	}
	if st, _ := ts.Media.Stats(ctx); st.Files != 0 {
		t.Errorf("media files after delete = %d", st.Files)
	}
	if b := ts.bootstrap(); b.View == nil || len(b.View.Tickets) == 0 {
		t.Errorf("the board must still show from the kept records")
	}
	if !ts.config(ts.standup).NeedsSync {
		t.Errorf("standups must be flagged for a sync so content returns on reconnect")
	}
}

func TestMediaCacheInfoAndClear(t *testing.T) {
	ts := newTicketApp(t)
	info, err := ts.Svc.App.MediaCache(ctx)
	noErr(t, err, "MediaCache")
	if info.Files == 0 || info.CapMB != 1024 || info.ThumbBytes == 0 {
		t.Fatalf("info = %+v", info)
	}
	_, err = ts.Svc.App.SetAppSetting(ctx, "media.cache_max_mb", "256")
	noErr(t, err, "SetAppSetting(media.cache_max_mb)")
	if info, _ = ts.Svc.App.MediaCache(ctx); info.CapMB != 256 {
		t.Errorf("cap = %d", info.CapMB)
	}
	info, err = ts.Svc.App.ClearMediaCache(ctx)
	noErr(t, err, "ClearMediaCache")
	if info.Files != 0 || info.Bytes != 0 || info.ThumbBytes != 0 {
		t.Errorf("after clear = %+v", info)
	}
}

func TestAppSettings_TicketAndStorageKeysAreValidated(t *testing.T) {
	ts := newTestApp(t)
	for _, c := range []struct {
		k, v string
		ok   bool
	}{
		{"ticket.comment_order", "oldest", true}, {"ticket.comment_order", "random", false},
		{"storage.cleanup_offer_mb", "4096", true}, {"storage.cleanup_offer_mb", "10", false}, {"storage.cleanup_offer_mb", "lots", false},
		{"media.cache_max_mb", "1024", true}, {"media.cache_max_mb", "0", false},
		{"jira.max_rps", "12", true}, {"jira.max_rps", "40", false},
	} {
		_, err := ts.Svc.App.SetAppSetting(ctx, c.k, c.v)
		if (err == nil) != c.ok {
			t.Errorf("SetAppSetting(%s=%s) err=%v, want ok=%v", c.k, c.v, err, c.ok)
		}
	}
}

func TestOpenURL_AllowsOnlyWebAndMail(t *testing.T) {
	ts := newTestApp(t)
	for _, u := range []string{"https://example.com/a?b=c", "http://example.com", "mailto:someone@example.com"} {
		noErr(t, ts.Svc.App.OpenURL(ctx, u), "OpenURL("+u+")")
	}
	for _, u := range []string{"", "javascript:alert(1)", "file:///etc/passwd", "ftp://example.com", "https://", "mailto:", "//example.com", "data:text/html,x", "vscode://x"} {
		wantCode(t, ts.Svc.App.OpenURL(ctx, u), apperr.Validation, "OpenURL("+u+")")
	}
	if got := ts.openedURLs(); len(got) != 3 {
		t.Errorf("opened = %v", got)
	}
}

func TestSearchMentions_LocalFirstThenTheSource(t *testing.T) {
	ts := newTicketApp(t)
	ts.Fake.ResetRequests()
	local, err := ts.Svc.Ticket.SearchMentions(ctx, ts.standup, "")
	noErr(t, err, "SearchMentions(empty)")
	if len(local) == 0 || len(ts.requests()) != 0 {
		t.Fatalf("empty query: %d people, requests %v", len(local), ts.requests())
	}
	name := local[0].DisplayName
	got, err := ts.Svc.Ticket.SearchMentions(ctx, ts.standup, name[:3])
	noErr(t, err, "SearchMentions")
	if len(got) == 0 || got[0].AccountID != local[0].AccountID && !got[0].OnBoard {
		t.Errorf("results = %+v", got)
	}
	if n := countPrefix(ts.requests(), "GET /rest/api/3/user/assignable/search"); n != 1 {
		t.Errorf("a typed query makes one user search: %v", ts.requests())
	}
	seen := map[string]bool{}
	for _, u := range got {
		if seen[u.AccountID] {
			t.Errorf("duplicate %s", u.AccountID)
		}
		seen[u.AccountID] = true
	}
}

func TestContentNeverReachesTheLog(t *testing.T) {
	ts := newTicketApp(t)
	d := ts.get(ts.issue)
	_, _ = ts.Svc.Ticket.SaveDraft(ctx, ts.issue, view.CommentInput{Text: "DRAFTMARKER-7f3a unsent"})
	_, _ = ts.Svc.Ticket.Refresh(ctx, ts.standup, ts.issue)
	_, _ = ts.Svc.Ticket.AddComment(ctx, ts.standup, ts.issue, view.CommentInput{Text: "COMMENTMARKER-91bc posted"})
	_, _ = ts.Svc.Ticket.AddComment(ctx, ts.standup, 999999, view.CommentInput{Text: "COMMENTMARKER-91bc nowhere"})
	log := ts.Log.String()
	for _, m := range []string{"DRAFTMARKER-7f3a", "COMMENTMARKER-91bc"} {
		if strings.Contains(log, m) {
			t.Errorf("log contains %s", m)
		}
	}
	if d.Description != nil {
		for _, n := range d.Description.Nodes {
			if n.Text != "" && len(n.Text) > 12 && strings.Contains(log, n.Text) {
				t.Errorf("log contains description text %q", n.Text)
			}
		}
	}
	for _, c := range d.Comments {
		for _, n := range c.Body.Nodes {
			if n.Text != "" && len(n.Text) > 12 && strings.Contains(log, n.Text) {
				t.Errorf("log contains comment text %q", n.Text)
			}
		}
	}
}

// The service builds the same MOB-1421 payload as the stack that wrote the golden (internal/sync/ticket_golden_test.go);
// only the fake server's address differs, and it is normalised the same way.
func TestTicketGet_MOB1421EqualsTheGolden(t *testing.T) {
	// 16:00 Chicago (22:00 UTC) is the clock the golden was written with: its asOf is 1795471200000.
	ts := newTicketApp(t, withNow(time.Date(2026, 11, 23, 16, 0, 0, 0, testLoc)))
	b, err := json.Marshal(ts.get(ts.issue))
	noErr(t, err, "marshal")
	raw := strings.ReplaceAll(string(b), ts.Fake.URL, "https://example.atlassian.net")
	want, err := os.ReadFile("testdata/ticket_mob1421.golden.json")
	noErr(t, err, "read golden")
	// The golden was built with a placeholder connected account, so "self" (the connected user) is never set there; the
	// service knows the real account. Everything else must match.
	norm := func(in []byte) []byte {
		var v any
		noErr(t, json.Unmarshal(in, &v), "unmarshal")
		var walk func(any)
		walk = func(x any) {
			switch y := x.(type) {
			case map[string]any:
				delete(y, "self")
				for _, c := range y {
					walk(c)
				}
			case []any:
				for _, c := range y {
					walk(c)
				}
			}
		}
		walk(v)
		out, _ := json.MarshalIndent(v, "", "  ")
		return out
	}
	g, w := string(norm([]byte(raw))), string(norm(want))
	if g != w {
		gl, wl := strings.Split(g, "\n"), strings.Split(w, "\n")
		for i := 0; i < len(gl) && i < len(wl); i++ {
			if gl[i] != wl[i] {
				t.Fatalf("TicketService.Get(MOB-1421) differs from the golden at line %d:\n got: %s\nwant: %s", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("TicketService.Get(MOB-1421) differs in length: %d vs %d lines", len(gl), len(wl))
	}
	if !strings.Contains(raw, `"self":true`) {
		t.Errorf("the connected account's mention/comment is not marked self")
	}
}

func TestWrites_ProviderFailuresMapToTypedErrorsAndNeverRetry(t *testing.T) {
	for _, c := range []struct {
		status int
		code   apperr.Code
	}{
		{400, apperr.WriteRejected}, {403, apperr.PermissionDenied}, {409, apperr.Conflict},
		{429, apperr.RateLimited}, {500, ""}, {503, ""},
	} {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			ts := newTicketApp(t)
			issue := ts.idOf(ts.Fx.activeKey)
			in := view.CommentInput{Text: "kept in the draft"}
			if _, err := ts.Svc.Ticket.SaveDraft(ctx, issue, in); err != nil {
				t.Fatal(err)
			}
			before := func() string {
				d := ts.get(issue)
				b, _ := json.Marshal(d)
				return string(b)
			}()
			rev := ts.view(ts.standup).Rev
			ts.Net.writeStatus.Store(int64(c.status))
			if c.status == 429 {
				ts.Net.retryAfter.Store(6)
			}
			for _, w := range []struct {
				action string
				do     func() error
			}{
				{"assign", func() error { _, err := ts.Svc.Ticket.Assign(ctx, ts.standup, issue, ts.other()); return err }},
				{"flag", func() error { _, err := ts.Svc.Ticket.SetFlag(ctx, ts.standup, issue, true); return err }},
				{"comment", func() error { _, err := ts.Svc.Ticket.AddComment(ctx, ts.standup, issue, in); return err }},
			} {
				ts.Em.reset()
				attempts := ts.Net.writeAttempts.Load()
				err := w.do()
				ae := apperr.From(err)
				if err == nil {
					t.Fatalf("%s: a %d write succeeded", w.action, c.status)
				}
				if c.code != "" && ae.Code != c.code {
					t.Errorf("%s: code = %s, want %s (%v)", w.action, ae.Code, c.code, err)
				}
				if c.status == 429 && (ae.RetryInMs < 5000 || ae.RetryInMs > 7000) {
					t.Errorf("%s: retryInMs = %d, want about 6000", w.action, ae.RetryInMs)
				}
				want := int64(1)
				if c.status == 429 && w.action != "assign" {
					want = 0 // the first 429 closed the connection's gate: later writes are refused locally, with no request
				}
				if n := ts.Net.writeAttempts.Load() - attempts; n != want {
					t.Errorf("%s: %d write requests for a %d answer, want %d (never retried)", w.action, n, c.status, want)
				}
				evs := ts.Em.named("ticket:write_failed")
				if len(evs) != 1 || evs[0].Payload["action"] != w.action || evs[0].Payload["errorCode"] != string(ae.Code) {
					t.Errorf("%s: ticket:write_failed = %+v (error %s)", w.action, evs, ae.Code)
				}
				if len(ts.Em.named("ticket:updated")) != 0 {
					t.Errorf("%s: a failed write announced an update", w.action)
				}
				if strings.Contains(ae.Message, "errorMessages") || strings.Contains(ae.Message, "{") {
					t.Errorf("%s: the raw HTTP body leaked into the message: %q", w.action, ae.Message)
				}
			}
			ts.Net.writeStatus.Store(0)
			if got := func() string { b, _ := json.Marshal(ts.get(issue)); return string(b) }(); got != before {
				t.Errorf("a failed write changed the stored ticket")
			}
			if v := ts.view(ts.standup); v.Rev != rev {
				t.Errorf("rev %d -> %d on failed writes", rev, v.Rev)
			}
			if dr, _ := ts.Svc.Ticket.Draft(ctx, issue); dr.Text != in.Text {
				t.Errorf("the draft was lost: %q", dr.Text)
			}
		})
	}
}

func TestWrites_AnOverlappingWriteToTheSameIssueIsAConflict(t *testing.T) {
	ts := newTicketApp(t, withLatency(150*time.Millisecond))
	issue := ts.idOf(ts.Fx.activeKey)
	ts.Fake.ResetRequests()
	first := make(chan error, 1)
	go func() { _, err := ts.Svc.Ticket.SetFlag(ctx, ts.standup, issue, true); first <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for len(ts.Fake.Requests()) == 0 && time.Now().Before(deadline) { // the first write is now in flight
		time.Sleep(2 * time.Millisecond)
	}
	_, err := ts.Svc.Ticket.Assign(ctx, ts.standup, issue, ts.other())
	wantCode(t, err, apperr.Conflict, "an overlapping write")
	if _, err := ts.Svc.Ticket.Refresh(ctx, ts.standup, issue); !apperr.Is(err, apperr.Conflict) {
		t.Errorf("a Refresh during a write = %v, want conflict", err)
	}
	if e := <-first; e != nil {
		t.Errorf("the first write failed: %v", e)
	}
}

func TestOfflineVerdictClearsOnSuccessAndOnASuccessfulSync(t *testing.T) {
	ts := newTicketApp(t)
	issue := ts.idOf(ts.Fx.activeKey)
	reason := func() string { return ts.get(issue).Can.Reason }

	ts.Net.down.Store(true)
	_, err := ts.Svc.Ticket.Refresh(ctx, ts.standup, issue)
	wantCode(t, err, apperr.Offline, "Refresh offline")
	if got := reason(); got != "offline" {
		t.Fatalf("reason after an offline call = %q", got)
	}
	// back online: a Resync (not a ticket call) clears the verdict
	ts.Net.down.Store(false)
	ts.Clock.Advance(time.Minute)
	ts.syncNow(ts.standup)
	if got := reason(); got != "" {
		t.Errorf("reason after back online and a successful Resync = %q, want empty", got)
	}
	// and so does any successful source call, here the mention search
	ts.Net.down.Store(true)
	_, _ = ts.Svc.Ticket.Refresh(ctx, ts.standup, issue)
	ts.Clock.Advance(time.Minute)
	if got := reason(); got != "offline" {
		t.Fatalf("reason = %q, want offline again", got)
	}
	ts.Net.down.Store(false)
	if _, err := ts.Svc.Ticket.SearchMentions(ctx, ts.standup, "a"); err != nil {
		t.Fatal(err)
	}
	if got := reason(); got != "" {
		t.Errorf("reason after a successful search = %q, want empty", got)
	}
}

func TestWrites_AStaleStandupMakesNoRequest(t *testing.T) {
	ts := newTicketApp(t)
	issue := ts.idOf(ts.Fx.activeKey)
	// a standup that never existed, and one that was archived since the modal opened
	_, err := ts.Svc.Config.Delete(ctx, ts.standup)
	noErr(t, err, "Delete")
	for _, id := range []int64{999999, ts.standup} {
		ts.Fake.ResetRequests()
		mark := ts.networkCalls()
		ts.Em.reset()
		_, err := ts.Svc.Ticket.Assign(ctx, id, issue, ts.other())
		wantCode(t, err, apperr.NotFound, "Assign with a stale standup")
		_, err = ts.Svc.Ticket.SetFlag(ctx, id, issue, true)
		wantCode(t, err, apperr.NotFound, "SetFlag with a stale standup")
		_, err = ts.Svc.Ticket.AddComment(ctx, id, issue, view.CommentInput{Text: "x"})
		wantCode(t, err, apperr.NotFound, "AddComment with a stale standup")
		_, err = ts.Svc.Ticket.Transition(ctx, id, issue, "11")
		wantCode(t, err, apperr.NotFound, "Transition with a stale standup")
		_, err = ts.Svc.Ticket.Refresh(ctx, id, issue)
		wantCode(t, err, apperr.NotFound, "Refresh with a stale standup")
		ts.assertNoNetwork(mark, "writes with standup "+strconv.FormatInt(id, 10))
		if len(ts.Em.named("ticket:updated")) != 0 {
			t.Errorf("a refused write announced an update")
		}
	}
}
