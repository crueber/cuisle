package settings

// WP-17 tests: walk order and per-lane limits (data-model.md standup_lane; ux-spec §9.4 Person view / Pulse).
//
// SPEC-NOTE: Lane edits are view-only: they bump rev but never set needs_sync (Ack.NeedsSync reports the existing flag,
// which these tests clear first).
// SPEC-NOTE: Lane ids are case-sensitive lowercase ("QA" is rejected).

import (
	"reflect"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
)

func w17Perms(items []string) [][]string {
	if len(items) <= 1 {
		return [][]string{append([]string{}, items...)}
	}
	var out [][]string
	for i := range items {
		rest := append(append([]string{}, items[:i]...), items[i+1:]...)
		for _, p := range w17Perms(rest) {
			out = append(out, append([]string{items[i]}, p...))
		}
	}
	return out
}

var w17DefaultLimits = map[string]int{"qa": 3, "review": 2, "prog": 5, "todo": 10}

func TestLanes_ReturnsWalkOrderWithLimits(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	got, err := e.s.Lanes(e.ctx, id)
	if err != nil {
		t.Fatalf("Lanes: %v", err)
	}
	want := []model.LaneConfig{
		{Lane: "qa", Position: 0, LimitDays: 3}, {Lane: "review", Position: 1, LimitDays: 2},
		{Lane: "prog", Position: 2, LimitDays: 5}, {Lane: "todo", Position: 3, LimitDays: 10},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Lanes = %+v, want %+v", got, want)
	}

	e.exec(`UPDATE standup_lane SET walk_position = 0, limit_days = 0 WHERE standup_id = ? AND lane = 'todo'`, id)
	e.exec(`UPDATE standup_lane SET walk_position = 3, limit_days = 99 WHERE standup_id = ? AND lane = 'qa'`, id)
	got, err = e.s.Lanes(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	want = []model.LaneConfig{
		{Lane: "todo", Position: 0, LimitDays: 0}, {Lane: "review", Position: 1, LimitDays: 2},
		{Lane: "prog", Position: 2, LimitDays: 5}, {Lane: "qa", Position: 3, LimitDays: 99},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Lanes after edit = %+v, want %+v (sorted by walk position)", got, want)
	}
}

func TestLanes_UnknownStandupIsNotFound(t *testing.T) {
	e := newW17(t)
	_, err := e.s.Lanes(e.ctx, 999)
	w17RequireCode(t, err, apperr.NotFound)
}

func TestSetWalkOrder_AcceptsEveryPermutation(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	perms := w17Perms([]string{"todo", "prog", "review", "qa"})
	if len(perms) != 24 {
		t.Fatalf("test bug: %d permutations", len(perms))
	}
	for _, perm := range perms {
		e.clk.Advance(time.Minute)
		before := e.rev(id)
		ack, err := e.s.SetWalkOrder(e.ctx, id, perm)
		if err != nil {
			t.Fatalf("SetWalkOrder(%v): %v", perm, err)
		}
		e.assertAck(id, before, ack)
		if ack.NeedsSync || e.needsSync(id) {
			t.Fatalf("SetWalkOrder(%v) must not set needs_sync", perm)
		}
		got, err := e.s.Lanes(e.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var want []model.LaneConfig
		for i, lane := range perm {
			want = append(want, model.LaneConfig{Lane: lane, Position: i, LimitDays: w17DefaultLimits[lane]})
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("after SetWalkOrder(%v): Lanes = %+v, want %+v (limits stay with their lane)", perm, got, want)
		}
	}
}

func TestSetWalkOrder_RejectsInvalidOrders(t *testing.T) {
	cases := []struct {
		name  string
		lanes []string
	}{
		{"duplicate", []string{"qa", "qa", "prog", "todo"}},
		{"duplicate_all_same", []string{"todo", "todo", "todo", "todo"}},
		{"missing_one", []string{"qa", "review", "prog"}},
		{"missing_all", []string{}},
		{"nil", nil},
		{"single", []string{"qa"}},
		{"done_instead_of_a_lane", []string{"qa", "review", "prog", "done"}},
		{"done_added_to_all_four", []string{"qa", "review", "prog", "todo", "done"}},
		{"five_with_duplicate", []string{"qa", "review", "prog", "todo", "qa"}},
		{"backlog", []string{"backlog", "review", "prog", "todo"}},
		{"hidden", []string{"hidden", "review", "prog", "todo"}},
		{"unknown_lane", []string{"qa", "review", "prog", "staging"}},
		{"uppercase", []string{"QA", "review", "prog", "todo"}},
		{"empty_string_lane", []string{"", "review", "prog", "todo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newW17(t)
			id := e.seedStandup("scrum")
			before := e.rev(id)
			orderBefore := e.dump(`SELECT lane, walk_position, limit_days FROM standup_lane WHERE standup_id = ? ORDER BY walk_position`, id)
			_, err := e.s.SetWalkOrder(e.ctx, id, tc.lanes)
			w17RequireValidation(t, err, "lanes")
			if e.rev(id) != before {
				t.Error("a rejected order must not bump rev")
			}
			if got := e.dump(`SELECT lane, walk_position, limit_days FROM standup_lane WHERE standup_id = ? ORDER BY walk_position`, id); !reflect.DeepEqual(got, orderBefore) {
				t.Errorf("a rejected order must not change lanes: %v → %v", orderBefore, got)
			}
		})
	}
}

func TestSetWalkOrder_UnknownStandupIsNotFound(t *testing.T) {
	e := newW17(t)
	_, err := e.s.SetWalkOrder(e.ctx, 999, []string{"todo", "prog", "review", "qa"})
	w17RequireCode(t, err, apperr.NotFound)
}

func TestSetWalkOrder_OnlyChangesTheGivenStandup(t *testing.T) {
	e := newW17(t)
	a := e.seedStandup("scrum")
	b := e.seedStandup("scrum")
	if _, err := e.s.SetWalkOrder(e.ctx, a, []string{"todo", "prog", "review", "qa"}); err != nil {
		t.Fatal(err)
	}
	got, err := e.s.Lanes(e.ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Lane != "qa" || got[3].Lane != "todo" {
		t.Errorf("another standup's walk order changed: %+v", got)
	}
}

func TestSetLaneLimit_AcceptsZeroToNinetyNine(t *testing.T) {
	for _, days := range []int{0, 1, 7, 98, 99} {
		e := newW17(t)
		id := e.seedStandup("scrum")
		before := e.rev(id)
		e.clk.Advance(time.Minute)
		ack, err := e.s.SetLaneLimit(e.ctx, id, "prog", days)
		if err != nil {
			t.Fatalf("SetLaneLimit(prog, %d): %v", days, err)
		}
		e.assertAck(id, before, ack)
		if ack.NeedsSync || e.needsSync(id) {
			t.Errorf("a limit is view-only: needs_sync must stay false")
		}
		if got := e.int(`SELECT limit_days FROM standup_lane WHERE standup_id = ? AND lane = 'prog'`, id); got != int64(days) {
			t.Errorf("limit_days = %d, want %d", got, days)
		}
		// the other lanes keep their limits and the walk order is unchanged
		for lane, want := range w17DefaultLimits {
			if lane == "prog" {
				continue
			}
			if got := e.int(`SELECT limit_days FROM standup_lane WHERE standup_id = ? AND lane = ?`, id, lane); got != int64(want) {
				t.Errorf("lane %s limit = %d, want unchanged %d", lane, got, want)
			}
		}
		got, _ := e.s.Lanes(e.ctx, id)
		if got[0].Lane != "qa" || got[1].Lane != "review" || got[2].Lane != "prog" || got[3].Lane != "todo" {
			t.Errorf("walk order changed: %+v", got)
		}
	}
}

func TestSetLaneLimit_EveryWorkingLane(t *testing.T) {
	for _, lane := range []string{"todo", "prog", "review", "qa"} {
		t.Run(lane, func(t *testing.T) {
			e := newW17(t)
			id := e.seedStandup("scrum")
			if _, err := e.s.SetLaneLimit(e.ctx, id, lane, 42); err != nil {
				t.Fatalf("SetLaneLimit(%s): %v", lane, err)
			}
			if got := e.int(`SELECT limit_days FROM standup_lane WHERE standup_id = ? AND lane = ?`, id, lane); got != 42 {
				t.Errorf("limit_days = %d, want 42", got)
			}
		})
	}
}

func TestSetLaneLimit_RejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name  string
		lane  string
		days  int
		field string
	}{
		{"days_negative", "prog", -1, "days"},
		{"days_100", "prog", 100, "days"},
		{"days_huge", "prog", 1000000, "days"},
		{"lane_done", "done", 5, "lane"},
		{"lane_backlog", "backlog", 5, "lane"},
		{"lane_hidden", "hidden", 5, "lane"},
		{"lane_unknown", "staging", 5, "lane"},
		{"lane_empty", "", 5, "lane"},
		{"lane_uppercase", "QA", 5, "lane"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newW17(t)
			id := e.seedStandup("scrum")
			before := e.rev(id)
			limits := e.dump(`SELECT lane, limit_days FROM standup_lane WHERE standup_id = ? ORDER BY lane`, id)
			_, err := e.s.SetLaneLimit(e.ctx, id, tc.lane, tc.days)
			w17RequireValidation(t, err, tc.field)
			if e.rev(id) != before {
				t.Error("a rejected limit must not bump rev")
			}
			if got := e.dump(`SELECT lane, limit_days FROM standup_lane WHERE standup_id = ? ORDER BY lane`, id); !reflect.DeepEqual(got, limits) {
				t.Errorf("limits changed: %v → %v", limits, got)
			}
		})
	}
}

func TestSetLaneLimit_UnknownStandupIsNotFound(t *testing.T) {
	e := newW17(t)
	_, err := e.s.SetLaneLimit(e.ctx, 999, "prog", 3)
	w17RequireCode(t, err, apperr.NotFound)
}
