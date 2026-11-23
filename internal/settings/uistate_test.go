package settings

// WP-16 tests: persisted window/navigation state (data-model.md §2.1 ui_state, standup_ui_state).
//
// SPEC-NOTE: LastPerson without StandupID is a validation error with Field "standupId" (the pair is one patch).
// SPEC-NOTE: Validation errors carry the patch JSON field name: "lastView", "lastStandupId", "standupId". A patch with
// any invalid field applies nothing. LastStandupID = pointer to 0 clears the last standup.
// SPEC-NOTE: Removing a standup (DiscardDraft hard delete) resets LastStandupID to 0 and drops its LastPerson entry
// (the foreign keys in 0001_init.sql do this; the test pins the observable behaviour).

import (
	"reflect"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
)

func (e *w16Env) uiState() UIState {
	e.t.Helper()
	st, err := e.s.UIState(e.ctx)
	if err != nil {
		e.t.Fatalf("UIState: %v", err)
	}
	return st
}

func (e *w16Env) setUI(p UIStatePatch) {
	e.t.Helper()
	if err := e.s.SetUIState(e.ctx, p); err != nil {
		e.t.Fatalf("SetUIState(%+v): %v", p, err)
	}
}

func TestUIState_FreshDatabaseDefaults(t *testing.T) {
	e := newW16(t)
	st := e.uiState()
	if st.LastStandupID != 0 || st.LastView != "standup" || len(st.LastPerson) != 0 || st.Window != nil {
		t.Errorf("fresh UIState = %+v, want lastStandupId 0, lastView standup, no persons, no window", st)
	}
}

func TestUIState_PatchesApplyIndependently(t *testing.T) {
	e := newW16(t)
	ids := e.createN(2)

	e.setUI(UIStatePatch{LastStandupID: w16p(ids[1])})
	if st := e.uiState(); st.LastStandupID != ids[1] || st.LastView != "standup" {
		t.Errorf("after LastStandupID patch: %+v", st)
	}
	e.setUI(UIStatePatch{LastView: w16p("configure")})
	if st := e.uiState(); st.LastStandupID != ids[1] || st.LastView != "configure" {
		t.Errorf("a LastView patch must keep LastStandupID: %+v", st)
	}
	e.setUI(UIStatePatch{LastStandupID: w16p(ids[0]), LastView: w16p("standup")})
	if st := e.uiState(); st.LastStandupID != ids[0] || st.LastView != "standup" {
		t.Errorf("after combined patch: %+v", st)
	}
	e.setUI(UIStatePatch{LastStandupID: w16p(int64(0))})
	if st := e.uiState(); st.LastStandupID != 0 {
		t.Errorf("LastStandupID 0 must clear it: %+v", st)
	}
}

func TestUIState_WindowGeometryRoundTrips(t *testing.T) {
	e := newW16(t)
	w := WindowState{X: -1200, Y: 40, W: 1440, H: 900, Maximised: false}
	e.setUI(UIStatePatch{Window: &w})
	st := e.uiState()
	if st.Window == nil || *st.Window != w {
		t.Fatalf("Window = %+v, want %+v", st.Window, w)
	}
	// unrelated patches keep the window
	e.setUI(UIStatePatch{LastView: w16p("configure")})
	if st := e.uiState(); st.Window == nil || *st.Window != w {
		t.Errorf("a patch without Window must keep it: %+v", st.Window)
	}
	// maximised persists and replaces
	w2 := WindowState{X: 0, Y: 0, W: 1920, H: 1080, Maximised: true}
	e.setUI(UIStatePatch{Window: &w2})
	if st := e.uiState(); st.Window == nil || *st.Window != w2 {
		t.Errorf("Window = %+v, want %+v", st.Window, w2)
	}
	e.setUI(UIStatePatch{Window: &w})
	if st := e.uiState(); st.Window == nil || *st.Window != w {
		t.Errorf("Window = %+v, want %+v (maximised cleared)", st.Window, w)
	}
}

func TestUIState_LastPersonIsPerStandup(t *testing.T) {
	e := newW16(t)
	ids := e.createN(2)
	a, b := ids[0], ids[1]

	e.setUI(UIStatePatch{StandupID: w16p(a), LastPerson: w16p("acc-1")})
	e.setUI(UIStatePatch{StandupID: w16p(b), LastPerson: w16p("unassigned")})
	st := e.uiState()
	if st.LastPerson[a] != "acc-1" || st.LastPerson[b] != "unassigned" {
		t.Errorf("LastPerson = %v", st.LastPerson)
	}

	// "" means the whole board
	e.setUI(UIStatePatch{StandupID: w16p(a), LastPerson: w16p("")})
	st = e.uiState()
	if st.LastPerson[a] != "" || st.LastPerson[b] != "unassigned" {
		t.Errorf("after clearing a: %v", st.LastPerson)
	}
}

func TestUIState_LastPersonDoesNotChangeLastStandup(t *testing.T) {
	e := newW16(t)
	ids := e.createN(2)
	e.setUI(UIStatePatch{LastStandupID: w16p(ids[0])})
	e.setUI(UIStatePatch{StandupID: w16p(ids[1]), LastPerson: w16p("acc-9")})
	if st := e.uiState(); st.LastStandupID != ids[0] {
		t.Errorf("LastStandupID = %d, want %d", st.LastStandupID, ids[0])
	}
}

func TestUIState_Validation(t *testing.T) {
	cases := []struct {
		name  string
		patch UIStatePatch
		field string
	}{
		{"last_view_unknown", UIStatePatch{LastView: w16p("dashboard")}, "lastView"},
		{"last_view_empty", UIStatePatch{LastView: w16p("")}, "lastView"},
		{"last_standup_missing", UIStatePatch{LastStandupID: w16p(int64(999))}, "lastStandupId"},
		{"standup_id_missing", UIStatePatch{StandupID: w16p(int64(999)), LastPerson: w16p("acc")}, "standupId"},
		{"last_person_without_standup", UIStatePatch{LastPerson: w16p("acc")}, "standupId"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newW16(t)
			err := e.s.SetUIState(e.ctx, tc.patch)
			w16RequireValidation(t, err, tc.field)
		})
	}
}

func TestUIState_InvalidFieldAppliesNothing(t *testing.T) {
	e := newW16(t)
	err := e.s.SetUIState(e.ctx, UIStatePatch{
		LastView:      w16p("configure"),
		LastStandupID: w16p(int64(999)),
		Window:        &WindowState{X: 1, Y: 2, W: 3, H: 4},
	})
	if !apperr.Is(err, apperr.Validation) {
		t.Fatalf("want validation, got %v", err)
	}
	st := e.uiState()
	if st.LastView != "standup" || st.Window != nil {
		t.Errorf("a rejected patch applied part of itself: %+v", st)
	}
}

func TestUIState_RemovedStandupDropsOutOfState(t *testing.T) {
	e := newW16(t)
	ids := e.createN(2)
	e.setUI(UIStatePatch{LastStandupID: w16p(ids[1])})
	e.setUI(UIStatePatch{StandupID: w16p(ids[1]), LastPerson: w16p("acc-1")})
	if err := e.s.DiscardDraft(e.ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	st := e.uiState()
	if st.LastStandupID != 0 {
		t.Errorf("LastStandupID = %d, want 0 after the standup was discarded", st.LastStandupID)
	}
	if _, ok := st.LastPerson[ids[1]]; ok {
		t.Errorf("LastPerson still has the discarded standup: %v", st.LastPerson)
	}
	if !reflect.DeepEqual(e.order(false), []int64{ids[0]}) {
		t.Errorf("order = %v", e.order(false))
	}
}

func TestUIState_DoesNotBumpStandupRev(t *testing.T) {
	e := newW16(t)
	id := e.create()
	before := e.rev(id)
	e.setUI(UIStatePatch{LastStandupID: w16p(id), StandupID: w16p(id), LastPerson: w16p("acc")})
	if e.rev(id) != before {
		t.Error("navigation state is not standup config; rev must not change")
	}
}
