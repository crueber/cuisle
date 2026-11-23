package app_test

// ConfigService autosave mutations (architecture.md section 5.3): SaveAck semantics, validation errors with the patch
// field name, source-vs-view-only edits, column mapping, walk order, limits and speakers.
//
// SPEC-NOTE: (SaveAck) Rev is the standup's rev after the mutation (previous rev + 1 for every accepted call),
// SavedAt is Deps.Now in Unix ms, and NeedsSync is the standup's needs_sync flag after the mutation. It is true after a
// source change (space, board, scope, extra filter, column mapping) until a successful sync clears it, and false for
// view-only edits on a synced standup (pulse days, marks, limits, walk order, speakers, done window, backlog rows,
// rows mode, name). Tests assert the false case only on standups that have been synced (a never-synced standup has no
// sync-state row, which reads as needs_sync=true).
//
// SPEC-NOTE: (events) a mutation does NOT emit data:changed for its own caller (architecture 3.3).
//
// SPEC-NOTE: (rejected patches) a patch with any invalid field is rejected as a whole (nothing applied, rev not
// bumped), and the validation error's Field is the patch field's JSON name.

import (
	"sort"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/view"
)

func TestConfigUpdate_ViewOnlyEditsAckWithNextRevAndNoSyncNeeded(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	tests := []struct {
		name  string
		patch standupPatch
		check func(c view.StandupConfig) bool
	}{
		{"name", standupPatch{Name: ptr("Platform standup")}, func(c view.StandupConfig) bool { return c.Name == "Platform standup" }},
		{"pulseDays", standupPatch{PulseDays: ptr(28)}, func(c view.StandupConfig) bool { return c.PulseDays == 28 }},
		{"rowsMode", standupPatch{RowsMode: ptr("roomy")}, func(c view.StandupConfig) bool { return c.RowsMode == "roomy" }},
		{"unassignedPosition", standupPatch{UnassignedPosition: ptr("first")}, func(c view.StandupConfig) bool { return c.UnassignedPosition == "first" }},
		{"reshuffleDaily", standupPatch{ReshuffleDaily: ptr(true)}, func(c view.StandupConfig) bool { return c.ReshuffleDaily }},
		{"marks", standupPatch{ShowCommits: ptr(false), ShowPRs: ptr(false), ShowComments: ptr(false), ShadeWeekends: ptr(false)},
			func(c view.StandupConfig) bool {
				return !c.ShowCommits && !c.ShowPRs && !c.ShowComments && !c.ShadeWeekends
			}},
		{"doneWindow", standupPatch{DoneWindow: ptr("14d")}, func(c view.StandupConfig) bool { return c.DoneWindow == "14d" }},
		{"backlogRows", standupPatch{BacklogRows: ptr(5)}, func(c view.StandupConfig) bool { return c.BacklogRows == 5 }},
	}
	ta.Em.reset()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prev := ta.config(id).Rev
			ack, err := ta.Svc.Config.Update(ctx, id, tc.patch)
			noErr(t, err, "Update")
			if ack.Rev != prev+1 || ack.SavedAt != ta.Clock.ms() || ack.NeedsSync {
				t.Errorf("ack = %+v, want rev %d, savedAt %d, needsSync=false", ack, prev+1, ta.Clock.ms())
			}
			got := ta.config(id)
			if !tc.check(got) || got.Rev != ack.Rev || got.NeedsSync {
				t.Errorf("Get after Update = %+v (rev should be %d, needsSync false)", got, ack.Rev)
			}
		})
	}
	if evs := ta.Em.named("data:changed"); len(evs) != 0 {
		t.Errorf("Update emitted data:changed for its own caller: %+v", evs)
	}
}

func TestConfigUpdate_SourceEditsNeedASyncUntilOneSucceeds(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	tests := []struct {
		name  string
		patch standupPatch
		check func(c view.StandupConfig) bool
	}{
		{"scope", standupPatch{Scope: ptr("board")}, func(c view.StandupConfig) bool { return c.Scope == "board" }},
		{"extraFilter", standupPatch{ExtraFilter: ptr(`status in ("In Progress")`)}, func(c view.StandupConfig) bool { return c.ExtraFilter == `status in ("In Progress")` }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ack, err := ta.Svc.Config.Update(ctx, id, tc.patch)
			noErr(t, err, "Update")
			if !ack.NeedsSync {
				t.Errorf("ack.NeedsSync = false after a source change (%s)", tc.name)
			}
			got := ta.config(id)
			if !tc.check(got) || !got.NeedsSync {
				t.Errorf("Get = %+v, want the new value and NeedsSync=true", got)
			}
			if tab := ta.bootstrap().Standups[0]; !tab.NeedsSync {
				t.Errorf("tab.NeedsSync = false, want true")
			}
			// a view-only edit while a sync is pending keeps reporting it
			ack, err = ta.Svc.Config.Update(ctx, id, standupPatch{PulseDays: ptr(14)})
			noErr(t, err, "Update(view-only)")
			if !ack.NeedsSync {
				t.Errorf("the pending needs_sync flag must survive later view-only edits")
			}
			// a successful sync clears it
			if _, err := ta.Svc.Config.Update(ctx, id, standupPatch{Scope: ptr("sprint"), ExtraFilter: ptr("")}); err != nil {
				t.Fatalf("restore source settings: %v", err)
			}
			ta.syncNow(id)
			if ta.config(id).NeedsSync {
				t.Errorf("NeedsSync still true after a successful sync")
			}
		})
	}
}

func TestConfigUpdate_RejectsInvalidPatchesWithTheFieldName(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	tests := []struct {
		field string
		patch standupPatch
	}{
		{"pulseDays", standupPatch{PulseDays: ptr(10)}},
		{"pulseDays", standupPatch{PulseDays: ptr(0)}},
		{"backlogRows", standupPatch{BacklogRows: ptr(51)}},
		{"backlogRows", standupPatch{BacklogRows: ptr(-1)}},
		{"rowsMode", standupPatch{RowsMode: ptr("wide")}},
		{"doneWindow", standupPatch{DoneWindow: ptr("week")}},
		{"unassignedPosition", standupPatch{UnassignedPosition: ptr("middle")}},
		{"scope", standupPatch{Scope: ptr("galaxy")}},
		{"name", standupPatch{Name: ptr("")}},
		{"name", standupPatch{Name: ptr("   ")}},
		{"name", standupPatch{Name: ptr(strings.Repeat("x", 81))}},
		// one bad field rejects the whole patch
		{"backlogRows", standupPatch{PulseDays: ptr(14), BacklogRows: ptr(99)}},
	}
	before := ta.config(id)
	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			_, err := ta.Svc.Config.Update(ctx, id, tc.patch)
			ae := wantCode(t, err, apperr.Validation, "Update")
			if ae.Field != tc.field {
				t.Errorf("validation Field = %q, want %q", ae.Field, tc.field)
			}
			if ae.Message == "" {
				t.Errorf("validation errors need a user-presentable message")
			}
		})
	}
	after := ta.config(id)
	if fmtJSON(before) != fmtJSON(after) {
		t.Errorf("rejected patches changed the standup:\nbefore %s\nafter  %s", fmtJSON(before), fmtJSON(after))
	}
}

func TestConfigUpdate_UnknownStandupIsNotFound(t *testing.T) {
	ta := newTestApp(t)
	_, err := ta.Svc.Config.Update(ctx, 4242, standupPatch{PulseDays: ptr(14)})
	wantCode(t, err, apperr.NotFound, "Update(unknown)")
}

func TestConfigSetColumnLane_RemapsAColumnAndNeedsASync(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	cfg := ta.config(id)
	col := cfg.Columns[1].ColumnName // a middle column

	ack, err := ta.Svc.Config.SetColumnLane(ctx, id, col, "hidden")
	noErr(t, err, "SetColumnLane")
	if !ack.NeedsSync || ack.Rev != cfg.Rev+1 {
		t.Errorf("ack = %+v, want needsSync and rev %d (a mapping change is a source change)", ack, cfg.Rev+1)
	}
	got := ta.config(id)
	if got.Columns[1].Lane != "hidden" || got.Columns[1].Auto {
		t.Errorf("column = %+v, want lane hidden with auto=false (a user choice)", got.Columns[1])
	}

	_, err = ta.Svc.Config.SetColumnLane(ctx, id, col, "bogus")
	wantCode(t, err, apperr.Validation, "SetColumnLane(bad lane)")
	_, err = ta.Svc.Config.SetColumnLane(ctx, id, "No such column", "todo")
	wantCode(t, err, apperr.NotFound, "SetColumnLane(unknown column)")
}

func laneOrder(c view.StandupConfig) []string {
	var out []string
	for _, l := range c.Lanes {
		if l.Lane != "done" {
			out = append(out, l.Lane)
		}
	}
	return out
}

func TestConfigSetWalkOrder_AcceptsPermutationsOfTheFourLanes(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	ack, err := ta.Svc.Config.SetWalkOrder(ctx, id, []string{"todo", "prog", "review", "qa"})
	noErr(t, err, "SetWalkOrder")
	if ack.NeedsSync {
		t.Errorf("a walk order change is view-only, ack = %+v", ack)
	}
	if got := laneOrder(ta.config(id)); strings.Join(got, ",") != "todo,prog,review,qa" {
		t.Errorf("lane order = %v, want [todo prog review qa]", got)
	}
	v := ta.view(id)
	var viewOrder []string
	for _, l := range v.Lanes {
		viewOrder = append(viewOrder, l.Lane)
	}
	if strings.Join(viewOrder, ",") != "todo,prog,review,qa,done" {
		t.Errorf("view lane order = %v, want walk order then done", viewOrder)
	}
}

func TestConfigSetWalkOrder_RejectsEverythingButAPermutation(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	for name, lanes := range map[string][]string{
		"duplicate": {"qa", "qa", "prog", "todo"}, "missing": {"qa", "review", "prog"},
		"done": {"qa", "review", "prog", "done"}, "unknown": {"qa", "review", "prog", "bogus"},
		"five": {"qa", "review", "prog", "todo", "done"}, "empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ta.Svc.Config.SetWalkOrder(ctx, id, lanes)
			wantCode(t, err, apperr.Validation, "SetWalkOrder")
		})
	}
	if got := laneOrder(ta.config(id)); strings.Join(got, ",") != "qa,review,prog,todo" {
		t.Errorf("rejected walk orders changed the order to %v", got)
	}
}

func TestConfigSetLaneLimit_StoresZeroToNinetyNine(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	limitOf := func(lane string) int {
		for _, l := range ta.config(id).Lanes {
			if l.Lane == lane {
				return l.LimitDays
			}
		}
		return -1
	}
	for _, n := range []int{7, 0, 99} {
		ack, err := ta.Svc.Config.SetLaneLimit(ctx, id, "prog", n)
		noErr(t, err, "SetLaneLimit")
		if ack.NeedsSync || limitOf("prog") != n {
			t.Errorf("after SetLaneLimit(prog, %d): ack %+v limit %d", n, ack, limitOf("prog"))
		}
	}
	for name, tc := range map[string]struct {
		lane string
		days int
	}{"negative": {"prog", -1}, "too large": {"prog", 100}, "done lane": {"done", 3}, "unknown lane": {"bogus", 3}} {
		t.Run(name, func(t *testing.T) {
			_, err := ta.Svc.Config.SetLaneLimit(ctx, id, tc.lane, tc.days)
			wantCode(t, err, apperr.Validation, "SetLaneLimit")
		})
	}
}

func speakerIDs(c view.StandupConfig) []string {
	sp := append([]view.SpeakerConfig(nil), c.Speakers...)
	sort.SliceStable(sp, func(i, j int) bool { return sp[i].Position < sp[j].Position })
	var out []string
	for _, s := range sp {
		out = append(out, s.AccountID)
	}
	return out
}

func TestConfigSpeakers_IncludeMoveAndSort(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	cfg := ta.config(id)
	if len(cfg.Speakers) < 2 {
		t.Fatalf("the synced board produced %d speakers, need at least 2 for this test", len(cfg.Speakers))
	}
	ids := speakerIDs(cfg)
	first := ids[0]
	if !contains(ids, ta.Fx.activeAssign) {
		t.Fatalf("speakers %v lack the assignee %s of an active ticket", ids, ta.Fx.activeAssign)
	}

	// include / exclude (the excluded speaker must still own tickets, so use an assignee of an active issue)
	owner := ta.Fx.activeAssign
	ack, err := ta.Svc.Config.SetSpeakerIncluded(ctx, id, owner, false)
	noErr(t, err, "SetSpeakerIncluded(false)")
	if ack.NeedsSync {
		t.Errorf("speaker edits are view-only, ack = %+v", ack)
	}
	for _, s := range ta.config(id).Speakers {
		if s.AccountID == owner && s.Included {
			t.Errorf("speaker %s is still included", owner)
		}
	}
	var viewSpeaker *view.SpeakerView
	v := ta.view(id)
	for i := range v.Speakers {
		if v.Speakers[i].ID == owner {
			viewSpeaker = &v.Speakers[i]
		}
	}
	if viewSpeaker == nil || viewSpeaker.Included {
		t.Errorf("an excluded speaker with tickets stays in the view with Included=false, got %+v", viewSpeaker)
	}
	_, err = ta.Svc.Config.SetSpeakerIncluded(ctx, id, owner, true)
	noErr(t, err, "SetSpeakerIncluded(true)")

	// move the first speaker to the end
	ack, err = ta.Svc.Config.MoveSpeaker(ctx, id, first, len(ids)-1)
	noErr(t, err, "MoveSpeaker")
	if ack.NeedsSync {
		t.Errorf("MoveSpeaker ack = %+v", ack)
	}
	got := speakerIDs(ta.config(id))
	want := append(append([]string(nil), ids[1:]...), first)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("speaker order after MoveSpeaker = %v, want %v", got, want)
	}

	// sort A-Z by display name
	sorted, err := ta.Svc.Config.SortSpeakers(ctx, id, "az")
	noErr(t, err, "SortSpeakers(az)")
	names := make([]string, len(sorted))
	for i, s := range sorted {
		names[i] = strings.ToLower(s.Name)
	}
	if !sort.StringsAreSorted(names) || len(sorted) != len(cfg.Speakers) {
		t.Errorf("SortSpeakers(az) = %v, want all %d speakers sorted by name", names, len(cfg.Speakers))
	}
	if strings.Join(speakerIDs(ta.config(id)), ",") != func() string {
		var o []string
		for _, s := range sorted {
			o = append(o, s.AccountID)
		}
		return strings.Join(o, ",")
	}() {
		t.Errorf("the returned list must be the persisted order")
	}
	if shuffled, err := ta.Svc.Config.SortSpeakers(ctx, id, "shuffle"); err != nil || len(shuffled) != len(cfg.Speakers) {
		t.Errorf("SortSpeakers(shuffle) = %d speakers, err %v, want all %d", len(shuffled), err, len(cfg.Speakers))
	}
	_, err = ta.Svc.Config.SortSpeakers(ctx, id, "alphabetical")
	wantCode(t, err, apperr.Validation, "SortSpeakers(bad mode)")
}

// A board speaker (an assignee) can be removed entirely, and a later sync does not add them back.
func TestConfigSpeakers_RemovedBoardSpeakerStaysRemovedAcrossSync(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	if !contains(speakerIDs(ta.config(id)), ta.Fx.activeAssign) {
		t.Fatalf("fixture: %s is not a speaker", ta.Fx.activeAssign)
	}
	_, err := ta.Svc.Config.RemoveSpeaker(ctx, id, ta.Fx.activeAssign)
	noErr(t, err, "RemoveSpeaker(board speaker)")
	ta.syncNow(id)
	if got := speakerIDs(ta.config(id)); contains(got, ta.Fx.activeAssign) {
		t.Errorf("sync added the removed speaker back: %v", got)
	}
}

func TestConfigSpeakers_AddAndRemoveManualSpeakersFromTheLocalCache(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	users, err := ta.Svc.Connection.SearchUsers(ctx, id, "Zed")
	noErr(t, err, "SearchUsers")
	if len(users) != 1 || users[0].AccountID != extraUserID {
		t.Fatalf("SearchUsers(Zed) = %+v, want only %s", users, extraUserID)
	}
	mark := ta.networkCalls()
	ta.Fake.ResetRequests()

	list, err := ta.Svc.Config.AddSpeaker(ctx, id, extraUserID)
	noErr(t, err, "AddSpeaker")
	var added *view.SpeakerConfig
	for i := range list {
		if list[i].AccountID == extraUserID {
			added = &list[i]
		}
	}
	if added == nil || !added.Manual || !added.Included || added.Name != extraUser || added.Initials != "ZQ" {
		t.Fatalf("AddSpeaker list = %+v, want Zed Quinn added as an included manual speaker with initials ZQ", list)
	}
	if got := speakerIDs(ta.config(id)); !contains(got, extraUserID) {
		t.Errorf("the added speaker was not persisted: %v", got)
	}
	_, err = ta.Svc.Config.AddSpeaker(ctx, id, "u-never-seen")
	wantCode(t, err, apperr.NotFound, "AddSpeaker(unknown person)")

	ack, err := ta.Svc.Config.RemoveSpeaker(ctx, id, extraUserID)
	noErr(t, err, "RemoveSpeaker(manual)")
	if ack.NeedsSync {
		t.Errorf("RemoveSpeaker ack = %+v", ack)
	}
	if got := speakerIDs(ta.config(id)); contains(got, extraUserID) {
		t.Errorf("the manual speaker is still configured: %v", got)
	}
	ta.assertNoNetwork(mark, "AddSpeaker/RemoveSpeaker")
}
