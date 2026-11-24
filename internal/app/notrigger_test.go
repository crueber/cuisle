package app_test

// NO TRIGGER, NO NETWORK (AGENTS.md section 3 invariant 2, testing.md section 3, MANDATORY).
//
// The app is started with sync-on-open "never" over a database that already holds synced data and an enabled
// after_sync file sink. Everything below must leave the fake Jira's request log EMPTY, attempt no HTTP round trip at
// all (counted at the transport, so even failed attempts show), never touch the sink dispatcher, and write nothing
// into the sink folder or the outbox.
//
// Allowed triggers (and only these) are Resync, the sync-on-open policy, leaving Configure after source changes,
// Jira-backed combobox typing, board pick and filter validation, Connect, and explicit sink sends or after-sync sends.
// So the tests also pin that source-changing edits only FLAG needsSync: the frontend starts the sync on leaving
// Configure, never the service call itself.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/store/storetest"
)

type quietApp struct {
	*testApp
	scrum, kanban int64
	mark          int64
	outbox        int
	files         []string
	runCount      int
}

// newQuietApp builds the described state and then "restarts" the app with the given sync-on-open policy.
func newQuietApp(t *testing.T, policy string, opts ...appOpt) *quietApp {
	t.Helper()
	ta := newTestApp(t, opts...)
	ta.connect()
	// configured standups: one synced, one never synced
	scrum := ta.newStandup(ta.Fx.scrumProject, ta.Fx.scrum)
	kanban := ta.newStandup(ta.Fx.kanbanProject, ta.Fx.kanban)
	// an enabled after_sync sink for every standup, created BEFORE the sync so the setup sync exercises it
	ta.saveFileSink(0, "after_sync")
	ta.syncNow(scrum.ID)
	ta.Orch.WaitMedia() // the media phase runs after sync:finished; it must not land in the quiet window
	// warm the autocomplete caches, as a user's earlier Configure session would have
	if _, err := ta.Svc.Connection.SearchUsers(ctx, scrum.ID, "Zed"); err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if _, err := ta.Svc.App.SetAppSetting(ctx, "sync_on_open", policy); err != nil {
		t.Fatalf("SetAppSetting: %v", err)
	}

	ta.restart() // a new process over the same data
	q := &quietApp{testApp: ta, scrum: scrum.ID, kanban: kanban.ID}
	ta.Fake.ResetRequests()
	q.mark = ta.networkCalls()
	q.outbox = storetest.Count(t, ta.DB, "sink_outbox", "")
	q.files = ta.dirEntries()
	q.runCount = ta.runRows()
	return q
}

// assertQuiet fails when anything network- or sink-related happened since newQuietApp.
func (q *quietApp) assertQuiet(what string) {
	q.t.Helper()
	q.assertNoNetwork(q.mark, what)
	if n := q.Disp.callCount(); n != 0 {
		q.t.Errorf("%s: the sink dispatcher was called %d times (%v), want 0", what, n, q.Disp.calls)
	}
	if n := storetest.Count(q.t, q.DB, "sink_outbox", ""); n != q.outbox {
		q.t.Errorf("%s: sink_outbox rows %d -> %d", what, q.outbox, n)
	}
	if got := q.dirEntries(); len(got) != len(q.files) {
		q.t.Errorf("%s: the sink folder changed: %v -> %v", what, q.files, got)
	}
	if got := q.runRows(); got != q.runCount {
		q.t.Errorf("%s: %d sync runs were created", what, got-q.runCount)
	}
	if evs := q.Em.named("sync:started"); len(evs) != 0 {
		q.t.Errorf("%s: sync:started was emitted: %+v", what, evs)
	}
}

func serveBootQuiet(t *testing.T, ta *testApp) {
	t.Helper()
	rec := httptest.NewRecorder()
	h := app.BootMiddleware(ta.Svc)(http.NotFoundHandler())
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boot.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /boot.js = %d", rec.Code)
	}
}

func TestNoTrigger_BootstrapBootJSListViewsAndSwitchingMakeNoRequests(t *testing.T) {
	q := newQuietApp(t, "never")

	b := q.bootstrap()
	if b.View == nil || len(b.View.Tickets) == 0 {
		t.Fatalf("the cached view must be in Bootstrap: %+v", b.View)
	}
	serveBootQuiet(t, q.testApp)

	tabs, err := q.Svc.Standup.List(ctx)
	noErr(t, err, "List")
	if len(tabs) != 2 {
		t.Fatalf("List = %+v, want both standups", tabs)
	}
	for round := 0; round < 3; round++ { // switching back and forth
		for _, tab := range tabs {
			v := q.view(tab.ID)
			if v.StandupID != tab.ID {
				t.Errorf("View(%d).StandupID = %d", tab.ID, v.StandupID)
			}
			noErr(t, q.Svc.App.SetUIState(ctx, app.UIStatePatch{LastStandupID: ptr(tab.ID)}), "SetUIState(switch)")
			if got := q.bootstrap(); got.View == nil || got.View.StandupID != tab.ID {
				t.Errorf("Bootstrap after switching to %d = %+v", tab.ID, got.View)
			}
			q.config(tab.ID)
		}
	}
	for _, s := range b.View.Speakers { // person switching is a UI-state write only
		noErr(t, q.Svc.App.SetUIState(ctx, app.UIStatePatch{StandupID: ptr(q.scrum), LastPerson: ptr(s.ID)}), "SetUIState(person)")
	}
	if _, err := q.Svc.Sync.Status(ctx); err != nil {
		t.Fatalf("Status: %v", err)
	}
	q.assertQuiet("bootstrap, boot.js, list, views and switching")
}

func TestNoTrigger_ViewOnlyConfigureEditsMakeNoRequestsAndNoDeliveries(t *testing.T) {
	q := newQuietApp(t, "never")
	id := q.scrum
	cfg := q.config(id)
	if len(cfg.Speakers) < 2 {
		t.Fatalf("need at least 2 speakers, have %d", len(cfg.Speakers))
	}
	first := speakerIDs(cfg)[0]

	steps := []struct {
		name string
		do   func() (app.SaveAck, error)
	}{
		{"pulse days", func() (app.SaveAck, error) { return q.Svc.Config.Update(ctx, id, standupPatch{PulseDays: ptr(14)}) }},
		{"marks", func() (app.SaveAck, error) {
			return q.Svc.Config.Update(ctx, id, standupPatch{ShowCommits: ptr(false), ShowPRs: ptr(false), ShowComments: ptr(false), ShadeWeekends: ptr(false)})
		}},
		{"done window", func() (app.SaveAck, error) {
			return q.Svc.Config.Update(ctx, id, standupPatch{DoneWindow: ptr("yesterday")})
		}},
		{"backlog rows", func() (app.SaveAck, error) { return q.Svc.Config.Update(ctx, id, standupPatch{BacklogRows: ptr(4)}) }},
		{"rows mode", func() (app.SaveAck, error) { return q.Svc.Config.Update(ctx, id, standupPatch{RowsMode: ptr("roomy")}) }},
		{"unassigned position", func() (app.SaveAck, error) {
			return q.Svc.Config.Update(ctx, id, standupPatch{UnassignedPosition: ptr("skip"), ReshuffleDaily: ptr(true)})
		}},
		{"name", func() (app.SaveAck, error) { return q.Svc.Config.Update(ctx, id, standupPatch{Name: ptr("Platform")}) }},
		{"walk order", func() (app.SaveAck, error) {
			return q.Svc.Config.SetWalkOrder(ctx, id, []string{"todo", "prog", "review", "qa"})
		}},
		{"lane limit", func() (app.SaveAck, error) { return q.Svc.Config.SetLaneLimit(ctx, id, "review", 4) }},
		{"speaker included", func() (app.SaveAck, error) { return q.Svc.Config.SetSpeakerIncluded(ctx, id, first, false) }},
		{"move speaker", func() (app.SaveAck, error) { return q.Svc.Config.MoveSpeaker(ctx, id, first, 1) }},
		{"move standup", func() (app.SaveAck, error) { return q.Svc.Config.Move(ctx, id, 1) }},
	}
	for _, s := range steps {
		ack, err := s.do()
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if ack.NeedsSync {
			t.Errorf("%s: ack.NeedsSync = true, a view-only edit must not need a sync", s.name)
		}
		q.view(id) // the frontend re-requests the view after every ack
	}
	// list-returning edits
	if _, err := q.Svc.Config.SortSpeakers(ctx, id, "az"); err != nil {
		t.Fatalf("SortSpeakers: %v", err)
	}
	if _, err := q.Svc.Config.SortSpeakers(ctx, id, "shuffle"); err != nil {
		t.Fatalf("SortSpeakers(shuffle): %v", err)
	}
	if _, err := q.Svc.Config.AddSpeaker(ctx, id, extraUserID); err != nil {
		t.Fatalf("AddSpeaker: %v", err)
	}
	if _, err := q.Svc.Config.RemoveSpeaker(ctx, id, extraUserID); err != nil {
		t.Fatalf("RemoveSpeaker: %v", err)
	}
	if _, err := q.Svc.Config.Duplicate(ctx, id); err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	draft, err := q.Svc.Config.Create(ctx)
	noErr(t, err, "Create")
	if _, err := q.Svc.Config.CopySettingsFrom(ctx, draft.ID, id); err != nil {
		t.Fatalf("CopySettingsFrom: %v", err)
	}
	noErr(t, q.Svc.Config.DiscardDraft(ctx, draft.ID), "DiscardDraft")
	if _, err := q.Svc.Config.Delete(ctx, q.kanban); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := q.Svc.Config.Restore(ctx, q.kanban); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := q.Svc.App.SetAppSetting(ctx, "theme", "dark"); err != nil {
		t.Fatalf("SetAppSetting: %v", err)
	}

	// the edits really happened
	if v := q.view(id); v.PulseDays != 14 || v.Options.DoneWindow != "yesterday" || v.Options.RowsMode != "roomy" || v.Options.ShowCommits {
		t.Errorf("view after the edits = pulseDays %d options %+v", v.PulseDays, v.Options)
	}
	q.assertQuiet("view-only Configure edits")
}

func TestNoTrigger_SourceEditsOnlyFlagNeedsSyncAndNeverSyncThemselves(t *testing.T) {
	q := newQuietApp(t, "never")
	id := q.scrum
	col := q.config(id).Columns[1].ColumnName

	ack, err := q.Svc.Config.Update(ctx, id, standupPatch{Scope: ptr("board")})
	noErr(t, err, "Update(scope)")
	if !ack.NeedsSync {
		t.Errorf("a scope change must flag needsSync")
	}
	ack, err = q.Svc.Config.Update(ctx, id, standupPatch{ExtraFilter: ptr(`status in ("In Progress")`)})
	noErr(t, err, "Update(extraFilter)")
	if !ack.NeedsSync {
		t.Errorf("an extra filter change must flag needsSync")
	}
	ack, err = q.Svc.Config.SetColumnLane(ctx, id, col, "hidden")
	noErr(t, err, "SetColumnLane")
	if !ack.NeedsSync {
		t.Errorf("a column mapping change must flag needsSync")
	}
	// choosing a (cached) space on a draft is a local lookup; the space id comes from the warm setup state
	space := q.config(q.kanban).Space
	if space == nil {
		t.Fatalf("the kanban standup has no space")
	}
	draft, err := q.Svc.Config.Create(ctx)
	noErr(t, err, "Create")
	if _, err := q.Svc.Config.SetSpace(ctx, draft.ID, space.ID); err != nil {
		t.Fatalf("SetSpace: %v", err)
	}
	if !q.config(id).NeedsSync || !q.bootstrap().Standups[0].NeedsSync {
		t.Errorf("the standup must read needsSync until a sync succeeds")
	}
	q.assertQuiet("source-changing edits")
}

func TestNoTrigger_BootstrapNeverSyncsUnderAnyPolicyNorAfterTimePasses(t *testing.T) {
	for _, policy := range []string{"never", "stale", "always"} {
		t.Run(policy, func(t *testing.T) {
			q := newQuietApp(t, policy)
			q.Clock.Advance(72 * time.Hour) // far past any stale threshold; no ticker may fire a sync
			q.bootstrap()
			serveBootQuiet(t, q.testApp)
			if _, err := q.Svc.Standup.List(ctx); err != nil {
				t.Fatalf("List: %v", err)
			}
			q.view(q.scrum)
			q.view(q.kanban)
			q.assertQuiet("Bootstrap under sync_on_open=" + policy + " (the policy is acted on by the startup hook only)")
		})
	}
}

func TestNoTrigger_BootstrapWorksWithTheNetworkDown(t *testing.T) {
	q := newQuietApp(t, "never")
	q.Net.down.Store(true)
	b := q.bootstrap()
	if b.View == nil || len(b.View.Tickets) == 0 || b.Connection.State != "ok" {
		t.Errorf("offline Bootstrap = connection %q view %v, want the cached board and the stored connection state", b.Connection.State, b.View != nil)
	}
	serveBootQuiet(t, q.testApp)
	q.assertQuiet("offline start (no attempt at all, not even a failing one)")
}
