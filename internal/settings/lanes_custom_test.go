package settings

// Custom lanes and lane labels (ux-spec §9.4 Status mapping; data-model.md §2.2 standup_lane, migration 0006).

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/model"
)

// customBoard is w17Board with a Design column between To Do and In Progress and a Peer Review column.
var customBoard = []w17Col{
	{"Backlog", []w17St{{"1", "Backlog", "new"}}},
	{"To Do", []w17St{{"2", "To Do", "new"}}},
	{"Design", []w17St{{"7", "Designing", "indeterminate"}}},
	{"In Progress", []w17St{{"3", "In Progress", "indeterminate"}}},
	{"Peer Review", []w17St{{"8", "Peer Review", "indeterminate"}}},
	{"QA", []w17St{{"5", "In QA", "indeterminate"}}},
	{"Done", []w17St{{"6", "Done", "done"}}},
}

// boardStandup is a standup with customBoard chosen (auto-mapped) and needs_sync cleared.
func (e *w17Env) boardStandup() int64 {
	e.t.Helper()
	id := e.seedStandup("")
	cfg, statuses := w17Config("scrum", "scrum", customBoard)
	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil {
		e.t.Fatal(err)
	}
	e.exec(`UPDATE standup_sync_state SET needs_sync = 0 WHERE standup_id = ?`, id)
	return id
}

// assertNoDanglingLanes checks the invariant: every mapped lane is a built-in lane or one of the standup's lanes.
func (e *w17Env) assertNoDanglingLanes(id int64) {
	e.t.Helper()
	q := `SELECT lane FROM standup_column_map WHERE standup_id = ? AND lane GLOB 'c*'
	        AND lane NOT IN (SELECT lane FROM standup_lane WHERE standup_id = ?)
	      UNION ALL
	      SELECT lane FROM standup_status_lane WHERE standup_id = ? AND lane GLOB 'c*'
	        AND lane NOT IN (SELECT lane FROM standup_lane WHERE standup_id = ?)`
	if got := e.dump(q, id, id, id, id); len(got) != 0 {
		e.t.Errorf("dangling lane ids %v (mapped to a lane the standup does not have)", got)
	}
}

func (e *w17Env) create(id int64, label, column string) string {
	e.t.Helper()
	lane, _, err := e.s.CreateLane(e.ctx, id, label, column)
	if err != nil {
		e.t.Fatalf("CreateLane(%q, %q): %v", label, column, err)
	}
	return lane
}

const laneDump = `SELECT lane, walk_position, limit_days, COALESCE(label, '-'), COALESCE(hue, 0) FROM standup_lane
	WHERE standup_id = ? ORDER BY walk_position`

func TestCreateLane_AddsAWorkingLaneAtTheEndAndMapsTheColumn(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	before := e.rev(id)
	lane, ack, err := e.s.CreateLane(e.ctx, id, "  Design  ", "Design")
	if err != nil {
		t.Fatal(err)
	}
	if lane != "c1" {
		t.Errorf("lane id = %q, want c1", lane)
	}
	e.assertAck(id, before, ack)
	if !ack.NeedsSync || !e.needsSync(id) {
		t.Error("mapping a column to the new lane must set needs_sync")
	}
	want := []string{"qa|0|3|-|0", "review|1|2|-|0", "prog|2|5|-|0", "todo|3|10|-|0", "c1|4|5|Design|1"}
	if got := e.dump(laneDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("lanes = %v, want %v", got, want)
	}
	if got := e.str(`SELECT lane || '|' || auto FROM standup_column_map WHERE standup_id = ? AND column_name = 'Design'`, id); got != "c1|0" {
		t.Errorf("Design column = %q, want c1|0", got)
	}
	if got := e.str(`SELECT lane FROM standup_status_lane WHERE standup_id = ? AND status_id = '7'`, id); got != "c1" {
		t.Errorf("status 7 lane = %q, want c1", got)
	}
	cfg, err := e.s.Effective(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.LaneLabel("c1"); got != "Design" {
		t.Errorf("LaneLabel(c1) = %q, want Design", got)
	}
	e.assertNoDanglingLanes(id)
}

func TestCreateLane_WithoutAColumnOnlyAddsTheLane(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	if lane := e.create(id, "Blocked", ""); lane != "c1" {
		t.Fatalf("lane = %q", lane)
	}
	if e.needsSync(id) {
		t.Error("a lane with no column does not change the sync plan")
	}
	if n := e.int(`SELECT count(*) FROM standup_column_map WHERE standup_id = ? AND lane = 'c1'`, id); n != 0 {
		t.Errorf("%d columns mapped to the new lane, want 0", n)
	}
}

func TestCreateLane_SameNameAgainReusesTheLane(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	e.create(id, "Design", "")
	if lane := e.create(id, "design", "Design"); lane != "c1" {
		t.Errorf("retried create = %q, want c1 (idempotent by name, ignoring case)", lane)
	}
	if n := e.int(`SELECT count(*) FROM standup_lane WHERE standup_id = ? AND lane GLOB 'c*'`, id); n != 1 {
		t.Errorf("%d custom lanes, want 1", n)
	}
	if got := e.int(`SELECT lane_seq FROM standup_settings WHERE standup_id = ?`, id); got != 1 {
		t.Errorf("lane_seq = %d, want 1", got)
	}
}

func TestCreateLane_RejectsBlankLongAndClashingNames(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	if _, err := e.s.RenameLane(e.ctx, id, "review", "Code Review"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"", "   ", strings.Repeat("x", 41), "QA", "to do", "Code review", "BACKLOG", "Hidden", "Done"} {
		before := e.rev(id)
		_, _, err := e.s.CreateLane(e.ctx, id, label, "")
		w17RequireValidation(t, err, "label")
		if e.rev(id) != before {
			t.Errorf("%q: rev changed on a rejected create", label)
		}
	}
	// "In Review" is free once review is renamed.
	if lane := e.create(id, "In Review", ""); lane != "c1" {
		t.Errorf("lane = %q", lane)
	}
	if lane := e.create(id, strings.Repeat("é", 40), ""); lane != "c2" {
		t.Errorf("a 40-character name is allowed, got %q", lane)
	}
}

func TestCreateLane_IsCappedPerStandup(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	for i := 1; i <= model.MaxCustomLanes; i++ {
		e.create(id, fmt.Sprintf("Lane %d", i), "")
	}
	_, _, err := e.s.CreateLane(e.ctx, id, "One too many", "")
	w17RequireValidation(t, err, "label")
}

func TestCreateLane_IdsAreNeverReusedAndColoursFillTheLowestFreeHue(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	hue := func(lane string) int64 {
		return e.int(`SELECT hue FROM standup_lane WHERE standup_id = ? AND lane = ?`, id, lane)
	}
	e.create(id, "A", "")
	e.create(id, "B", "")
	e.create(id, "C", "")
	if _, err := e.s.DeleteLane(e.ctx, id, "c2", ""); err != nil {
		t.Fatal(err)
	}
	// The id is new, the colour is the one c2 freed (the lowest unused), so live lanes keep distinct colours.
	if lane := e.create(id, "D", ""); lane != "c4" || hue(lane) != 2 {
		t.Errorf("after deleting c2: lane %q hue %d, want c4 hue 2", lane, hue(lane))
	}
	for i := 5; i <= 9; i++ { // c5..c9 take hues 4..8: all eight in use
		e.create(id, fmt.Sprintf("L%d", i), "")
	}
	if hue("c9") != 8 {
		t.Errorf("c9 hue = %d, want 8", hue("c9"))
	}
	// Every colour taken: rotate by sequence number (c10 → (10−1) mod 8 + 1 = 2).
	if lane := e.create(id, "L10", ""); hue(lane) != 2 {
		t.Errorf("%s hue = %d, want 2 (rotation once all eight are in use)", lane, hue(lane))
	}
}

func TestRenameLane_BuiltInCustomAndDone(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	e.create(id, "Design", "Design")
	e.exec(`UPDATE standup_sync_state SET needs_sync = 0 WHERE standup_id = ?`, id)
	for _, c := range []struct{ lane, label string }{{"review", " Code Review "}, {"c1", "Discovery"}, {"done", "Shipped"}} {
		before := e.rev(id)
		ack, err := e.s.RenameLane(e.ctx, id, c.lane, c.label)
		if err != nil {
			t.Fatalf("RenameLane(%s): %v", c.lane, err)
		}
		e.assertAck(id, before, ack)
	}
	if e.needsSync(id) {
		t.Error("a rename never needs a sync")
	}
	cfg, err := e.s.Effective(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for lane, want := range map[string]string{"review": "Code Review", "c1": "Discovery", "done": "Shipped", "qa": "QA"} {
		if got := cfg.LaneLabel(lane); got != want {
			t.Errorf("LaneLabel(%s) = %q, want %q", lane, got, want)
		}
	}
	// "" restores a built-in label; a custom lane must keep a name.
	if _, err := e.s.RenameLane(e.ctx, id, "review", ""); err != nil {
		t.Fatal(err)
	}
	if !e.isNull(`SELECT label FROM standup_lane WHERE standup_id = ? AND lane = 'review'`, id) {
		t.Error("review label not reset to NULL")
	}
	if _, err := e.s.RenameLane(e.ctx, id, "done", ""); err != nil {
		t.Fatal(err)
	}
	if !e.isNull(`SELECT done_label FROM standup_settings WHERE standup_id = ?`, id) {
		t.Error("done label not reset to NULL")
	}
	_, err = e.s.RenameLane(e.ctx, id, "c1", " ")
	w17RequireValidation(t, err, "label")
	_, err = e.s.RenameLane(e.ctx, id, "qa", "discovery")
	w17RequireValidation(t, err, "label")
	for _, lane := range []string{"backlog", "hidden", "c9", "QA"} {
		_, err := e.s.RenameLane(e.ctx, id, lane, "X")
		w17RequireValidation(t, err, "lane")
	}
}

func TestRenameLane_ResetIsRefusedWhenTheDefaultIsTaken(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	if _, err := e.s.RenameLane(e.ctx, id, "review", "Code Review"); err != nil {
		t.Fatal(err)
	}
	e.create(id, "In Review", "")
	_, err := e.s.RenameLane(e.ctx, id, "review", "")
	w17RequireValidation(t, err, "label")
}

func TestDeleteLane_MovesItsColumnsAndClosesTheWalk(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	e.create(id, "Design", "Design")
	e.create(id, "Blocked", "")
	if _, err := e.s.SetWalkOrder(e.ctx, id, []string{"c1", "qa", "review", "c2", "prog", "todo"}); err != nil {
		t.Fatal(err)
	}
	e.exec(`UPDATE standup_sync_state SET needs_sync = 0 WHERE standup_id = ?`, id)
	before := e.rev(id)
	ack, err := e.s.DeleteLane(e.ctx, id, "c1", "prog")
	if err != nil {
		t.Fatal(err)
	}
	e.assertAck(id, before, ack)
	if !e.needsSync(id) {
		t.Error("moving columns changes the plan: needs_sync")
	}
	want := []string{"qa|0|3|-|0", "review|1|2|-|0", "c2|2|5|Blocked|2", "prog|3|5|-|0", "todo|4|10|-|0"}
	if got := e.dump(laneDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("lanes = %v, want %v", got, want)
	}
	if got := e.str(`SELECT lane || '|' || auto FROM standup_column_map WHERE standup_id = ? AND column_name = 'Design'`, id); got != "prog|0" {
		t.Errorf("Design column = %q, want prog|0", got)
	}
	if got := e.str(`SELECT lane FROM standup_status_lane WHERE standup_id = ? AND status_id = '7'`, id); got != "prog" {
		t.Errorf("status 7 = %q, want prog", got)
	}
	e.assertNoDanglingLanes(id)

	// A lane with no columns needs no target; deleting it again is a no-op.
	if _, err := e.s.DeleteLane(e.ctx, id, "c2", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.DeleteLane(e.ctx, id, "c2", ""); err != nil {
		t.Errorf("repeated delete: %v", err)
	}
}

func TestDeleteLane_Validation(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	e.create(id, "Design", "Design")
	for _, c := range []struct{ lane, moveTo, field string }{
		{"review", "prog", "lane"},
		{"done", "prog", "lane"},
		{"c1", "", "moveTo"},
		{"c1", "c1", "moveTo"},
		{"c1", "c7", "moveTo"},
		{"c1", "nowhere", "moveTo"},
	} {
		_, err := e.s.DeleteLane(e.ctx, id, c.lane, c.moveTo)
		w17RequireValidation(t, err, c.field)
	}
	// Moving to another custom lane, to done or to hidden is fine.
	e.create(id, "Blocked", "")
	if _, err := e.s.DeleteLane(e.ctx, id, "c1", "c2"); err != nil {
		t.Fatal(err)
	}
	if got := e.str(`SELECT lane FROM standup_column_map WHERE standup_id = ? AND column_name = 'Design'`, id); got != "c2" {
		t.Errorf("Design = %q, want c2", got)
	}
	e.assertNoDanglingLanes(id)
}

func TestSetColumnLane_AcceptsTheStandupsCustomLanesOnly(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	e.create(id, "Design", "")
	if _, err := e.s.SetColumnLane(e.ctx, id, "In Progress", "c1"); err != nil {
		t.Fatal(err)
	}
	other := e.boardStandup()
	_, err := e.s.SetColumnLane(e.ctx, other, "In Progress", "c1")
	w17RequireValidation(t, err, "lane")
	for _, bad := range []string{"c0", "custom", "QA", ""} {
		_, err := e.s.SetColumnLane(e.ctx, id, "In Progress", bad)
		w17RequireValidation(t, err, "lane")
	}
	e.assertNoDanglingLanes(id)
}

func TestWalkOrderAndLimits_IncludeCustomLanes(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	e.create(id, "Design", "")
	if _, err := e.s.SetWalkOrder(e.ctx, id, []string{"todo", "prog", "review", "qa"}); err == nil {
		t.Error("a walk order missing the custom lane was accepted")
	}
	_, err := e.s.SetWalkOrder(e.ctx, id, []string{"todo", "c1", "prog", "review", "qa", "c2"})
	w17RequireValidation(t, err, "lanes")
	if _, err := e.s.SetWalkOrder(e.ctx, id, []string{"todo", "c1", "prog", "review", "qa"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SetLaneLimit(e.ctx, id, "c1", 7); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.SetLaneLimit(e.ctx, id, "c4", 7)
	w17RequireValidation(t, err, "lane")
	want := []string{"todo|0|10|-|0", "c1|1|7|Design|1", "prog|2|5|-|0", "review|3|2|-|0", "qa|4|3|-|0"}
	if got := e.dump(laneDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("lanes = %v, want %v", got, want)
	}
}

func TestDuplicate_CopiesCustomLanesLabelsAndTheIdCounter(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	e.create(id, "Design", "Design")
	e.create(id, "Gone", "")
	if _, err := e.s.DeleteLane(e.ctx, id, "c2", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.RenameLane(e.ctx, id, "done", "Shipped"); err != nil {
		t.Fatal(err)
	}
	copyID, err := e.s.Duplicate(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := e.dump(laneDump, copyID), e.dump(laneDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("copy lanes = %v, want %v", got, want)
	}
	if got := e.str(`SELECT lane_seq || '|' || done_label FROM standup_settings WHERE standup_id = ?`, copyID); got != "2|Shipped" {
		t.Errorf("copy lane_seq|done_label = %q, want 2|Shipped", got)
	}
	if lane := e.create(copyID, "Next", ""); lane != "c3" {
		t.Errorf("next lane on the copy = %q, want c3", lane)
	}
	e.assertNoDanglingLanes(copyID)
}

func TestCopySettingsFrom_KeepsTheTargetsCustomLanes(t *testing.T) {
	e := newW17(t)
	from := e.boardStandup()
	e.create(from, "Source only", "")
	if _, err := e.s.SetWalkOrder(e.ctx, from, []string{"todo", "prog", "c1", "review", "qa"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SetLaneLimit(e.ctx, from, "todo", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.RenameLane(e.ctx, from, "review", "Design"); err != nil { // clashes with the target's custom lane
		t.Fatal(err)
	}
	if _, err := e.s.RenameLane(e.ctx, from, "qa", "Testing"); err != nil {
		t.Fatal(err)
	}
	to := e.boardStandup()
	e.create(to, "Design", "Design")
	if _, err := e.s.SetWalkOrder(e.ctx, to, []string{"qa", "c1", "review", "prog", "todo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.CopySettingsFrom(e.ctx, to, from); err != nil {
		t.Fatal(err)
	}
	// The built-in slots (0, 2, 3, 4) take the source's built-in order; c1 keeps slot 1.
	want := []string{"todo|0|1|-|0", "c1|1|5|Design|1", "prog|2|5|-|0", "review|3|2|-|0", "qa|4|3|Testing|0"}
	if got := e.dump(laneDump, to); !reflect.DeepEqual(got, want) {
		t.Errorf("lanes = %v, want %v", got, want)
	}
	e.assertNoDanglingLanes(to)
}

func TestSetBoard_KeepsCustomLanesAndMatchesThemByColumnName(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("")
	// Lanes named before the board is chosen: a custom "Peer Review" and a renamed built-in.
	e.create(id, "Peer Review", "")
	if _, err := e.s.RenameLane(e.ctx, id, "prog", "Design"); err != nil {
		t.Fatal(err)
	}
	cfg, statuses := w17Config("scrum", "scrum", customBoard)
	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Backlog|0|backlog|1", "To Do|1|todo|1", "Design|2|prog|1", "In Progress|3|prog|1",
		"Peer Review|4|c1|1", "QA|5|qa|1", "Done|6|done|1",
	}
	if got := e.dump(w17MapDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("map = %v, want %v (names matched before the review/progress rules)", got, want)
	}
	// Another board: the custom lane stays, columns are auto-matched again.
	cfg.Board.ID = "43"
	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil {
		t.Fatal(err)
	}
	if n := e.int(`SELECT count(*) FROM standup_lane WHERE standup_id = ? AND lane = 'c1'`, id); n != 1 {
		t.Error("the custom lane did not survive a board change")
	}
	e.assertNoDanglingLanes(id)
}

func TestAutoLaneNamed_NameMatchFirstThenRules(t *testing.T) {
	lanes := []model.LaneConfig{{Lane: "c3", Label: "Code Review", Hue: 3}, {Lane: "qa", Label: "Verify"}}
	cols, byID, _ := w17Build([]w17Col{
		{"code review ", []w17St{{"1", "CR", "indeterminate"}}},
		{"In Review", []w17St{{"2", "IR", "indeterminate"}}},
		{"VERIFY", []w17St{{"3", "V", "indeterminate"}}},
		{"Done", []w17St{{"4", "D", "done"}}},
	})
	var got []string
	for i, c := range cols {
		got = append(got, AutoLaneNamed(c, i, len(cols), byID, lanes))
	}
	if want := []string{"c3", "review", "qa", "done"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AutoLaneNamed = %v, want %v", got, want)
	}
}

func TestMergeColumns_DropsAManualChoiceOfALaneThatNoLongerExists(t *testing.T) {
	e := newW17(t)
	id := e.boardStandup()
	e.create(id, "Design", "Design")
	// Simulate an old mapping to a lane that is gone (e.g. a row written before a delete): the merge re-matches it.
	e.exec(`DELETE FROM standup_lane WHERE standup_id = ? AND lane = 'c1'`, id)
	cols, byID, _ := w17Build(customBoard)
	e.merge(id, cols, byID)
	if got := e.str(`SELECT lane || '|' || auto FROM standup_column_map WHERE standup_id = ? AND column_name = 'Design'`, id); got != "prog|1" {
		t.Errorf("Design = %q, want prog|1 (auto-mapped again)", got)
	}
	e.assertNoDanglingLanes(id)
}
