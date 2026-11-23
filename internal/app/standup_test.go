package app_test

// StandupService (read side) and the lifecycle half of ConfigService (Create, Duplicate, CopySettingsFrom,
// DiscardDraft, Delete, Restore, Move, SetSpace, SetBoard).
//
// SPEC-NOTE: (list contents) StandupService.List returns every live standup including drafts (Configured=false);
// the frontend hides drafts from tabs but needs them for the Configure rail. Archived standups are never listed.
//
// SPEC-NOTE: (Restore event) Restore is not "caused by the receiving call" from the point of view of other windows
// and tabs, so it emits data:changed with what="standups" (architecture 3.3 lists restore explicitly).
//
// SPEC-NOTE: (SetBoard errors) an unknown board id (Jira 404) is apperr not_found; an unreachable source is apperr
// offline and leaves the standup untouched (still a draft); SetBoard before a space is chosen is a validation error.
//
// SPEC-NOTE: (SetSpace) resolves the space from the local autocomplete cache (filled by SearchSpaces) and makes no
// network request; a space id the cache has never seen is not_found.

import (
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/store/storetest"
	"github.com/crueber/cuisle/internal/view"
)

func TestStandupList_EmptyIsANonNilSlice(t *testing.T) {
	ta := newTestApp(t)
	tabs, err := ta.Svc.Standup.List(ctx)
	noErr(t, err, "List")
	if tabs == nil || len(tabs) != 0 {
		t.Errorf("List = %#v, want a non-nil empty slice", tabs)
	}
}

func TestStandupList_ReportsDraftsAndConfiguredTabsInOrder(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	kanban := ta.kanbanStandup()
	draft, err := ta.Svc.Config.Create(ctx)
	noErr(t, err, "Create")

	tabs, err := ta.Svc.Standup.List(ctx)
	noErr(t, err, "List")
	if len(tabs) != 3 {
		t.Fatalf("List = %+v, want 3 tabs (scrum, kanban, draft)", tabs)
	}
	for i := 1; i < len(tabs); i++ {
		if tabs[i].Position <= tabs[i-1].Position {
			t.Errorf("tabs are not in increasing Position order: %+v", tabs)
		}
	}
	by := map[int64]view.StandupTab{}
	for _, tb := range tabs {
		by[tb.ID] = tb
	}
	if s := by[id]; !s.Configured || s.NeedsSync || s.LastSyncedAt != ta.Clock.ms() || s.ActiveCount == 0 || s.SpeakerCount == 0 ||
		s.SpaceKey != ta.Fx.scrumProject.Key {
		t.Errorf("synced tab = %+v", s)
	}
	if k := by[kanban]; !k.Configured || !k.NeedsSync || k.LastSyncedAt != 0 || k.SpaceKey != ta.Fx.kanbanProject.Key {
		t.Errorf("never-synced kanban tab = %+v, want configured, needsSync, lastSyncedAt 0", k)
	}
	if d := by[draft.ID]; d.Configured {
		t.Errorf("draft tab = %+v, want Configured=false", d)
	}
}

func TestStandupView_ReflectsTheSyncedBoard(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	cfg := ta.config(id)
	v := ta.view(id)

	if v.StandupID != id || v.Rev != cfg.Rev || !v.Live || v.Name != cfg.Name {
		t.Errorf("view identity = id %d rev %d live %v name %q, want id %d rev %d live name %q", v.StandupID, v.Rev, v.Live, v.Name, id, cfg.Rev, cfg.Name)
	}
	if v.Today != "2026-11-23" || v.AsOf != ta.Clock.ms() || v.GeneratedAt != v.AsOf {
		t.Errorf("view time = today %q asOf %d generatedAt %d, want 2026-11-23 and %d (the fixed clock)", v.Today, v.AsOf, v.GeneratedAt, ta.Clock.ms())
	}
	if v.SpaceKey != ta.Fx.scrumProject.Key || v.BoardName != ta.Fx.scrum.Name || v.BoardType != "scrum" || v.Scope != "sprint" {
		t.Errorf("view source = %q/%q/%q/%q", v.SpaceKey, v.BoardName, v.BoardType, v.Scope)
	}
	if v.PulseDays != 21 || len(v.Axis) != 21 {
		t.Errorf("PulseDays/Axis = %d/%d, want the default 21", v.PulseDays, len(v.Axis))
	}
	if v.Axis == nil || v.Lanes == nil || v.Speakers == nil || v.Tickets == nil || v.Backlog == nil || v.Warnings == nil {
		t.Errorf("view lists must never be nil: %s", fmtJSON(map[string]bool{
			"axis": v.Axis == nil, "lanes": v.Lanes == nil, "speakers": v.Speakers == nil,
			"tickets": v.Tickets == nil, "backlog": v.Backlog == nil, "warnings": v.Warnings == nil}))
	}
	var found *view.TicketView
	for i := range v.Tickets {
		if v.Tickets[i].Key == ta.Fx.activeKey {
			found = &v.Tickets[i]
		}
	}
	if found == nil {
		t.Fatalf("active issue %s is not in the view (%d tickets)", ta.Fx.activeKey, len(v.Tickets))
	}
	if found.URL != ta.Fake.URL+"/browse/"+ta.Fx.activeKey || found.Assignee != ta.Fx.activeAssign || found.Lane == "" || found.Lane == "done" ||
		len(found.Pulse.Segs) == 0 || found.Pulse.Label == "" {
		t.Errorf("ticket = %+v, want a browse URL, assignee %s, an active lane and a pulse with a label", *found, ta.Fx.activeAssign)
	}
	if v.Counts.Active == 0 || v.LastSyncedAt != ta.Clock.ms() {
		t.Errorf("Counts/LastSyncedAt = %+v / %d", v.Counts, v.LastSyncedAt)
	}
}

func TestStandupView_SyncStatusIsFilledByTheAppLayer(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	s := ta.view(id).Sync
	if s.StandupID != id || s.State != "idle" || s.LastStatus != "ok" || s.LastSuccessAt != ta.Clock.ms() || s.NeedsSync || s.RunID == 0 {
		t.Errorf("view.Sync = %+v, want idle/ok at the fixed clock, no pending sync, a run id", s)
	}
}

func TestStandupView_NeverSyncedStandupIsEmptyNotAnError(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()
	cfg := ta.newStandup(ta.Fx.scrumProject, ta.Fx.scrum)
	v := ta.view(cfg.ID)
	if v.LastSyncedAt != 0 || !v.Sync.NeedsSync {
		t.Errorf("LastSyncedAt/Sync.NeedsSync = %d/%v, want 0/true before the first sync", v.LastSyncedAt, v.Sync.NeedsSync)
	}
	if v.Tickets == nil || len(v.Tickets) != 0 || v.Speakers == nil || v.Backlog == nil {
		t.Errorf("an unsynced view needs empty non-nil lists, got tickets=%v speakers=%v backlog=%v", v.Tickets, v.Speakers, v.Backlog)
	}
}

func TestStandupView_UnknownStandupIsNotFound(t *testing.T) {
	ta := newTestApp(t)
	_, err := ta.Svc.Standup.View(ctx, 4242)
	wantCode(t, err, apperr.NotFound, "View(unknown)")
}

func TestStandupView_ViewOnlySettingsApplyToTheNextViewWithoutASync(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	ack, err := ta.Svc.Config.Update(ctx, id, standupPatch{PulseDays: ptr(14), BacklogRows: ptr(3)})
	noErr(t, err, "Update")
	v := ta.view(id)
	if v.PulseDays != 14 || len(v.Axis) != 14 || v.Rev != ack.Rev {
		t.Errorf("view after Update = pulseDays %d axis %d rev %d, want 14/14/%d", v.PulseDays, len(v.Axis), v.Rev, ack.Rev)
	}
	if len(v.Backlog) > 3 {
		t.Errorf("Backlog has %d rows, want at most the new backlogRows 3", len(v.Backlog))
	}
}

func TestConfigCreate_ReturnsADraftWithDefaults(t *testing.T) {
	ta := newTestApp(t)
	cfg, err := ta.Svc.Config.Create(ctx)
	noErr(t, err, "Create")
	if cfg.ID <= 0 || !cfg.Draft || cfg.Name == "" || cfg.Space != nil || cfg.Board != nil {
		t.Errorf("draft = %+v", cfg)
	}
	if cfg.PulseDays != 21 || cfg.BacklogRows != 12 || cfg.DoneWindow != "sprint" || cfg.RowsMode != "fit" {
		t.Errorf("defaults = pulse %d backlog %d done %q rows %q, want 21/12/sprint/fit", cfg.PulseDays, cfg.BacklogRows, cfg.DoneWindow, cfg.RowsMode)
	}
	if len(cfg.Lanes) < 4 {
		t.Fatalf("Lanes = %+v, want the 4 walk lanes", cfg.Lanes)
	}
	wantLanes, wantLimits := []string{"qa", "review", "prog", "todo"}, []int{3, 2, 5, 10}
	for i := 0; i < 4; i++ {
		if cfg.Lanes[i].Lane != wantLanes[i] || cfg.Lanes[i].LimitDays != wantLimits[i] {
			t.Errorf("Lanes[%d] = %s/%d, want %s/%d (right-to-left walk order and limits)", i, cfg.Lanes[i].Lane, cfg.Lanes[i].LimitDays, wantLanes[i], wantLimits[i])
		}
	}
	if cfg.Speakers == nil || cfg.Columns == nil || cfg.UnmappedStatuses == nil {
		t.Errorf("config lists must be non-nil: speakers=%v columns=%v unmapped=%v", cfg.Speakers, cfg.Columns, cfg.UnmappedStatuses)
	}
	got, err := ta.Svc.Config.Get(ctx, cfg.ID)
	noErr(t, err, "Get")
	if got.ID != cfg.ID || !got.Draft {
		t.Errorf("Get = %+v, want the same draft", got)
	}
}

func TestConfigGet_UnknownStandupIsNotFound(t *testing.T) {
	ta := newTestApp(t)
	_, err := ta.Svc.Config.Get(ctx, 4242)
	wantCode(t, err, apperr.NotFound, "Get(unknown)")
}

func TestConfigDuplicate_CopiesSettingsUnderACopyName(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	_, err := ta.Svc.Config.Update(ctx, id, standupPatch{PulseDays: ptr(14)})
	noErr(t, err, "Update")
	src := ta.config(id)
	dup, err := ta.Svc.Config.Duplicate(ctx, id)
	noErr(t, err, "Duplicate")
	if dup.ID == id || dup.Name != src.Name+" (copy)" || dup.PulseDays != 14 {
		t.Errorf("duplicate = id %d name %q pulse %d, want a new id, %q and pulse 14", dup.ID, dup.Name, dup.PulseDays, src.Name+" (copy)")
	}
}

func TestConfigCopySettingsFrom_CopiesSettingsButNotSourceOrSpeakers(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	_, err := ta.Svc.Config.Update(ctx, id, standupPatch{PulseDays: ptr(14), BacklogRows: ptr(5), RowsMode: ptr("roomy")})
	noErr(t, err, "Update")
	_, err = ta.Svc.Config.SetLaneLimit(ctx, id, "prog", 7)
	noErr(t, err, "SetLaneLimit")
	draft, err := ta.Svc.Config.Create(ctx)
	noErr(t, err, "Create")

	got, err := ta.Svc.Config.CopySettingsFrom(ctx, draft.ID, id)
	noErr(t, err, "CopySettingsFrom")
	if got.PulseDays != 14 || got.BacklogRows != 5 || got.RowsMode != "roomy" {
		t.Errorf("copied settings = pulse %d backlog %d rows %q, want 14/5/roomy", got.PulseDays, got.BacklogRows, got.RowsMode)
	}
	limit := -1
	for _, l := range got.Lanes {
		if l.Lane == "prog" {
			limit = l.LimitDays
		}
	}
	if limit != 7 {
		t.Errorf("prog limit = %d, want the copied 7", limit)
	}
	if got.Space != nil || got.Board != nil || len(got.Speakers) != 0 || !got.Draft {
		t.Errorf("source, board and speakers must not be copied: space=%v board=%v speakers=%d draft=%v", got.Space, got.Board, len(got.Speakers), got.Draft)
	}
}

func TestConfigDiscardDraft_RemovesADraftAndIgnoresConfiguredStandups(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	draft, err := ta.Svc.Config.Create(ctx)
	noErr(t, err, "Create")

	noErr(t, ta.Svc.Config.DiscardDraft(ctx, draft.ID), "DiscardDraft(draft)")
	_, err = ta.Svc.Config.Get(ctx, draft.ID)
	wantCode(t, err, apperr.NotFound, "Get(discarded draft)")
	if n := storetest.Count(t, ta.DB, "standup", "id = ?", draft.ID); n != 0 {
		t.Errorf("a discarded draft must be hard-deleted, standup rows = %d", n)
	}

	noErr(t, ta.Svc.Config.DiscardDraft(ctx, id), "DiscardDraft(configured)")
	if got := ta.config(id); got.Draft {
		t.Errorf("a configured standup must survive DiscardDraft: %+v", got)
	}
}

func TestConfigDelete_ArchivesAndKeepsSyncedHistory(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	tables := []string{"standup_issue", "standup_issue_span", "status_change", "standup_config_history"}
	before := map[string]int{}
	for _, tb := range tables {
		before[tb] = storetest.Count(t, ta.DB, tb, "")
	}
	if before["standup_issue"] == 0 || before["status_change"] == 0 {
		t.Fatalf("the sync recorded no history to protect: %v", before)
	}

	ack, err := ta.Svc.Config.Delete(ctx, id)
	noErr(t, err, "Delete")
	if ack.SavedAt != ta.Clock.ms() {
		t.Errorf("ack = %+v, want SavedAt = the fixed clock", ack)
	}
	tabs, _ := ta.Svc.Standup.List(ctx)
	if len(tabs) != 0 || len(ta.bootstrap().Standups) != 0 {
		t.Errorf("an archived standup must leave List and Bootstrap: %+v", tabs)
	}
	_, err = ta.Svc.Standup.View(ctx, id)
	wantCode(t, err, apperr.NotFound, "View(archived)")
	for _, tb := range tables {
		if after := storetest.Count(t, ta.DB, tb, ""); after != before[tb] {
			t.Errorf("table %s: %d rows after Delete, want %d (history is never deleted)", tb, after, before[tb])
		}
	}
	if n := storetest.Count(t, ta.DB, "standup", "id = ? AND deleted_at IS NOT NULL", id); n != 1 {
		t.Errorf("the standup row must be soft-deleted, got %d archived rows", n)
	}

	ta.Em.reset()
	if _, err := ta.Svc.Config.Restore(ctx, id); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	tabs, _ = ta.Svc.Standup.List(ctx)
	if len(tabs) != 1 || tabs[0].ID != id || !tabs[0].Configured {
		t.Errorf("after Restore List = %+v, want the standup back and configured", tabs)
	}
	if v := ta.view(id); len(v.Tickets) == 0 {
		t.Errorf("a restored standup must show its synced tickets again")
	}
	evs := ta.Em.named("data:changed")
	if len(evs) == 0 || evs[len(evs)-1].Payload["what"] != "standups" {
		t.Errorf("Restore must emit data:changed what=standups, got %+v", evs)
	}
}

func TestConfigDeleteAndRestore_UnknownStandupIsNotFound(t *testing.T) {
	ta := newTestApp(t)
	_, err := ta.Svc.Config.Delete(ctx, 4242)
	wantCode(t, err, apperr.NotFound, "Delete(unknown)")
	_, err = ta.Svc.Config.Restore(ctx, 4242)
	wantCode(t, err, apperr.NotFound, "Restore(unknown)")
}

func TestConfigMove_ReordersTabsAndAcknowledges(t *testing.T) {
	ta := newTestApp(t)
	a, _ := ta.Svc.Config.Create(ctx)
	b, _ := ta.Svc.Config.Create(ctx)
	c, _ := ta.Svc.Config.Create(ctx)
	ack, err := ta.Svc.Config.Move(ctx, c.ID, 0)
	noErr(t, err, "Move")
	if ack.SavedAt != ta.Clock.ms() {
		t.Errorf("ack = %+v", ack)
	}
	tabs, _ := ta.Svc.Standup.List(ctx)
	var got []int64
	for _, tb := range tabs {
		got = append(got, tb.ID)
	}
	if len(got) != 3 || got[0] != c.ID || got[1] != a.ID || got[2] != b.ID {
		t.Errorf("order after Move(c, 0) = %v, want [%d %d %d]", got, c.ID, a.ID, b.ID)
	}
}

func TestConfigSetSpace_StoresSpaceClearsBoardAndNeedsNoNetwork(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup() // also warms the space/board caches
	spaces, err := ta.Svc.Connection.SearchSpaces(ctx, ta.Fx.kanbanProject.Key)
	noErr(t, err, "SearchSpaces")
	if len(spaces) != 1 {
		t.Fatalf("SearchSpaces = %+v", spaces)
	}
	mark := ta.networkCalls()
	ta.Fake.ResetRequests()

	cfg, err := ta.Svc.Config.SetSpace(ctx, id, spaces[0].ID)
	noErr(t, err, "SetSpace")
	if cfg.Space == nil || cfg.Space.Key != ta.Fx.kanbanProject.Key || cfg.Space.Name != ta.Fx.kanbanProject.Name {
		t.Errorf("Space = %+v, want the kanban project", cfg.Space)
	}
	if cfg.Board != nil || !cfg.Draft || len(cfg.Columns) != 0 {
		t.Errorf("changing space must clear board and mapping: board=%v draft=%v columns=%d", cfg.Board, cfg.Draft, len(cfg.Columns))
	}
	ta.assertNoNetwork(mark, "SetSpace")

	_, err = ta.Svc.Config.SetSpace(ctx, id, "never-seen-space")
	wantCode(t, err, apperr.NotFound, "SetSpace(unknown space)")
	_, err = ta.Svc.Config.SetSpace(ctx, 4242, spaces[0].ID)
	wantCode(t, err, apperr.NotFound, "SetSpace(unknown standup)")
}

// expectedLane are jira-sync.md section 2.5 auto-mapping results for column names the sample board is known to use.
var expectedLane = map[string]string{
	"Selected for Development": "todo", "To Do": "todo", "In Progress": "prog", "Code Review": "review",
	"Review": "review", "QA": "qa", "Done": "done", "Ops Queue": "todo", "Ops Active": "prog", "Ops Done": "done",
}

func TestConfigSetBoard_Scrum_FetchesColumnsAndAutoMapsThem(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()
	cfg := ta.newStandup(ta.Fx.scrumProject, ta.Fx.scrum)

	if cfg.Board == nil || cfg.Board.ID != ta.Fx.scrumID() || cfg.Board.Name != ta.Fx.scrum.Name || cfg.Board.Type != "scrum" {
		t.Fatalf("Board = %+v, want the scrum board", cfg.Board)
	}
	if cfg.Draft || !cfg.NeedsSync || cfg.Scope != "sprint" || cfg.Space == nil || cfg.Space.Key != ta.Fx.scrumProject.Key {
		t.Errorf("cfg = draft %v needsSync %v scope %q space %+v, want configured, needsSync, sprint scope", cfg.Draft, cfg.NeedsSync, cfg.Scope, cfg.Space)
	}
	if len(cfg.Columns) != len(ta.Fx.scrum.Columns) {
		t.Fatalf("Columns = %d, want the board's %d", len(cfg.Columns), len(ta.Fx.scrum.Columns))
	}
	valid := []string{"backlog", "todo", "prog", "review", "qa", "done", "hidden"}
	for i, col := range cfg.Columns {
		if col.ColumnName != ta.Fx.scrum.Columns[i].Name || !col.Auto || !contains(valid, col.Lane) {
			t.Errorf("Columns[%d] = %+v, want name %q, auto=true and a valid lane", i, col, ta.Fx.scrum.Columns[i].Name)
		}
		if want, ok := expectedLane[col.ColumnName]; ok && col.Lane != want {
			t.Errorf("column %q auto-mapped to %q, want %q (jira-sync 2.5)", col.ColumnName, col.Lane, want)
		}
	}
	if last := cfg.Columns[len(cfg.Columns)-1]; last.Lane != "done" {
		t.Errorf("the last column %q maps to %q, want done", last.ColumnName, last.Lane)
	}
	if cfg.UnmappedStatuses == nil {
		t.Errorf("UnmappedStatuses must be non-nil")
	}
	if got := ta.config(cfg.ID); got.Board == nil || got.Board.ID != cfg.Board.ID || len(got.Columns) != len(cfg.Columns) {
		t.Errorf("Get after SetBoard = %+v, want the persisted board and columns", got)
	}
}

func TestConfigSetBoard_Kanban_ForcesBoardScopeAnd14DayDoneWindow(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()
	cfg := ta.newStandup(ta.Fx.kanbanProject, ta.Fx.kanban)
	if cfg.Board == nil || cfg.Board.Type != "kanban" || cfg.Scope != "board" || cfg.DoneWindow != "14d" {
		t.Errorf("kanban cfg = board %+v scope %q doneWindow %q, want kanban/board/14d", cfg.Board, cfg.Scope, cfg.DoneWindow)
	}
	// and the rules stay enforced afterwards
	_, err := ta.Svc.Config.Update(ctx, cfg.ID, standupPatch{Scope: ptr("sprint")})
	ae := wantCode(t, err, apperr.Validation, "Update(scope=sprint on kanban)")
	if ae.Field != "scope" {
		t.Errorf("validation Field = %q, want scope", ae.Field)
	}
	_, err = ta.Svc.Config.Update(ctx, cfg.ID, standupPatch{DoneWindow: ptr("sprint")})
	ae = wantCode(t, err, apperr.Validation, "Update(doneWindow=sprint on kanban)")
	if ae.Field != "doneWindow" {
		t.Errorf("validation Field = %q, want doneWindow", ae.Field)
	}
}

func TestConfigSetBoard_ErrorsLeaveTheStandupUntouched(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()

	t.Run("no space chosen yet", func(t *testing.T) {
		draft, err := ta.Svc.Config.Create(ctx)
		noErr(t, err, "Create")
		_, err = ta.Svc.Config.SetBoard(ctx, draft.ID, ta.Fx.scrumID())
		wantCode(t, err, apperr.Validation, "SetBoard(no space)")
	})

	// choose a space (online), then break things
	draft, err := ta.Svc.Config.Create(ctx)
	noErr(t, err, "Create")
	spaces, err := ta.Svc.Connection.SearchSpaces(ctx, ta.Fx.scrumProject.Key)
	noErr(t, err, "SearchSpaces")
	_, err = ta.Svc.Config.SetSpace(ctx, draft.ID, spaces[0].ID)
	noErr(t, err, "SetSpace")

	t.Run("board that does not exist", func(t *testing.T) {
		_, err := ta.Svc.Config.SetBoard(ctx, draft.ID, "424242")
		wantCode(t, err, apperr.NotFound, "SetBoard(unknown board)")
	})
	t.Run("source unreachable", func(t *testing.T) {
		ta.Net.down.Store(true)
		defer ta.Net.down.Store(false)
		_, err := ta.Svc.Config.SetBoard(ctx, draft.ID, ta.Fx.scrumID())
		wantCode(t, err, apperr.Offline, "SetBoard(offline)")
	})
	if got := ta.config(draft.ID); !got.Draft || got.Board != nil {
		t.Errorf("a failed SetBoard changed the standup: %+v", got)
	}
}
