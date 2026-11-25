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
		{"board_group_unknown_value", UIStatePatch{StandupID: w16p(int64(1)), BoardGroup: w16p("columns")}, "boardGroup"},
		{"board_group_empty", UIStatePatch{StandupID: w16p(int64(1)), BoardGroup: w16p("")}, "boardGroup"},
		{"board_group_without_standup", UIStatePatch{BoardGroup: w16p("status")}, "standupId"},
		{"zoom_not_a_step", UIStatePatch{StandupID: w16p(int64(1)), Zoom: w16p(105)}, "zoom"},
		{"zoom_zero", UIStatePatch{StandupID: w16p(int64(1)), Zoom: w16p(0)}, "zoom"},
		{"zoom_without_standup", UIStatePatch{Zoom: w16p(150)}, "standupId"},
		{"zoom_standup_missing", UIStatePatch{StandupID: w16p(int64(999)), Zoom: w16p(150)}, "standupId"},
		{"board_group_standup_missing", UIStatePatch{StandupID: w16p(int64(999)), BoardGroup: w16p("status")}, "standupId"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newW16(t)
			err := e.s.SetUIState(e.ctx, tc.patch)
			w16RequireValidation(t, err, tc.field)
		})
	}
}

// F15: one zoom per standup, default 100, shared by its person view and Whole board; other UI patches never disturb it.
func TestUIState_ZoomDefaultsTo100AndIsPerStandup(t *testing.T) {
	e := newW16(t)
	ids := e.createN(3)
	a, b, c := ids[0], ids[1], ids[2]

	if st := e.uiState(); len(st.Zoom) != 0 {
		t.Errorf("fresh Zoom = %v, want empty (absent = 100)", st.Zoom)
	}
	e.setUI(UIStatePatch{StandupID: w16p(a), Zoom: w16p(150)})
	e.setUI(UIStatePatch{StandupID: w16p(b), LastPerson: w16p("acc-1")})
	st := e.uiState()
	if st.Zoom[a] != 150 {
		t.Errorf("Zoom[a] = %d, want 150", st.Zoom[a])
	}
	if st.Zoom[b] != 100 {
		t.Errorf("Zoom[b] = %d, want 100 (a row made by a LastPerson patch)", st.Zoom[b])
	}
	if z, ok := st.Zoom[c]; ok && z != 100 {
		t.Errorf("Zoom[c] = %d, want absent or 100", z)
	}

	// Grouping and last-person patches keep the zoom; a zoom patch keeps them.
	e.setUI(UIStatePatch{StandupID: w16p(a), BoardGroup: w16p("status")})
	e.setUI(UIStatePatch{StandupID: w16p(a), LastPerson: w16p("acc-2")})
	e.setUI(UIStatePatch{StandupID: w16p(a), Zoom: w16p(80)})
	st = e.uiState()
	if st.Zoom[a] != 80 || st.BoardGroup[a] != "status" || st.LastPerson[a] != "acc-2" {
		t.Errorf("a = zoom %d group %q person %q, want 80 status acc-2", st.Zoom[a], st.BoardGroup[a], st.LastPerson[a])
	}

	for _, z := range ZoomLevels {
		e.setUI(UIStatePatch{StandupID: w16p(c), Zoom: w16p(z)})
		if got := e.uiState().Zoom[c]; got != z {
			t.Errorf("Zoom[c] = %d after setting %d", got, z)
		}
	}
}

func TestUIState_BoardGroupDefaultsToAssigneeAndIsPerStandup(t *testing.T) {
	e := newW16(t)
	ids := e.createN(3)
	a, b, c := ids[0], ids[1], ids[2]

	if st := e.uiState(); len(st.BoardGroup) != 0 {
		t.Errorf("fresh BoardGroup = %v, want empty (absent = assignee)", st.BoardGroup)
	}
	e.setUI(UIStatePatch{StandupID: w16p(a), BoardGroup: w16p("status")})
	st := e.uiState()
	if st.BoardGroup[a] != "status" {
		t.Errorf("BoardGroup[a] = %q, want status", st.BoardGroup[a])
	}
	if g, ok := st.BoardGroup[b]; ok && g != "assignee" {
		t.Errorf("BoardGroup[b] = %q, want absent or assignee (per-standup isolation)", g)
	}

	// A last-person patch creates b's row with the default grouping and never disturbs a's.
	e.setUI(UIStatePatch{StandupID: w16p(b), LastPerson: w16p("acc-1")})
	e.setUI(UIStatePatch{StandupID: w16p(a), LastPerson: w16p("acc-2")})
	st = e.uiState()
	if st.BoardGroup[a] != "status" || st.BoardGroup[b] != "assignee" {
		t.Errorf("BoardGroup = %v, want a=status b=assignee after LastPerson patches", st.BoardGroup)
	}
	if st.LastPerson[a] != "acc-2" || st.LastPerson[b] != "acc-1" {
		t.Errorf("LastPerson = %v", st.LastPerson)
	}

	// A grouping patch on a standup with no row yet inserts it as the whole board.
	e.setUI(UIStatePatch{StandupID: w16p(c), BoardGroup: w16p("status")})
	st = e.uiState()
	if st.BoardGroup[c] != "status" || st.LastPerson[c] != "" {
		t.Errorf("c: group %q person %q, want status and whole board", st.BoardGroup[c], st.LastPerson[c])
	}

	// It is stored in standup_ui_state.board_group itself (what survives a restart).
	if got := e.str("SELECT board_group FROM standup_ui_state WHERE standup_id = ?", a); got != "status" {
		t.Errorf("stored board_group = %q, want status", got)
	}

	// Back to assignee; the grouping patch keeps LastPerson.
	e.setUI(UIStatePatch{StandupID: w16p(a), BoardGroup: w16p("assignee")})
	st = e.uiState()
	if st.BoardGroup[a] != "assignee" || st.LastPerson[a] != "acc-2" {
		t.Errorf("a: group %q person %q, want assignee and acc-2", st.BoardGroup[a], st.LastPerson[a])
	}
}

func TestUIState_BoardGroupRejectedPatchWritesNothing(t *testing.T) {
	e := newW16(t)
	id := e.create()
	err := e.s.SetUIState(e.ctx, UIStatePatch{LastView: w16p("configure"), StandupID: w16p(id), BoardGroup: w16p("columns")})
	if !apperr.Is(err, apperr.Validation) {
		t.Fatalf("want validation, got %v", err)
	}
	if st := e.uiState(); st.LastView != "standup" || len(st.BoardGroup) != 0 {
		t.Errorf("a rejected patch applied part of itself: %+v", st)
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
	e.setUI(UIStatePatch{StandupID: w16p(ids[1]), LastPerson: w16p("acc-1"), BoardGroup: w16p("status")})
	if err := e.s.DiscardDraft(e.ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	st := e.uiState()
	if _, ok := st.BoardGroup[ids[1]]; ok {
		t.Errorf("BoardGroup still has the discarded standup: %v", st.BoardGroup)
	}
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
