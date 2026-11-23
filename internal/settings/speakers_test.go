package settings

// WP-17 tests: speakers (data-model.md §2.3 "Speaker rules"; ux-spec §9.4 Speakers).
//
// SPEC-NOTE: SortSpeakers "az" compares display names case-insensitively (a stable human sort), ties by account id.
// "active" sorts by the active map descending (accounts missing from the map count 0), ties by case-insensitive name.
// Excluded speakers take part in every sort (included and excluded share one list).
// SPEC-NOTE: SortSpeakers "shuffle" with reshuffle_daily=1 stores shuffle_seed = rnd() and shuffle_date = today in the
// Store clock's own location (the viewer's local date, not UTC; the tests use UTC-8 at 22:30 so the dates differ),
// and leaves positions alone. With reshuffle off it permutes positions using rnd (Fisher-Yates) and persists them.
// Both bump rev. The exact permutation for a given rnd is not specified.
// SPEC-NOTE: AddSpeaker with an empty AccountID is validation Field "accountId". Adding an existing speaker re-includes
// it and sets user_touched without moving it or changing its manual flag.
// SPEC-NOTE: MergeSpeakersTx "changed" means the speaker LIST changed (a speaker appended, renamed or pruned);
// refreshing last_seen_at alone is not a change. A row last seen exactly 30 days before now is kept (only strictly
// older rows are pruned). Rows with last_seen_at NULL are not covered by the tests. Positions are dense afterwards.
// Appended speakers keep the order of `seen`, duplicates in `seen` are ignored, and a merge never re-includes an
// excluded speaker.

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

type w17Sp struct {
	Acc, Name string
	Pos       int
	Included  bool
	Manual    bool
	Touched   bool
	LastSeen  int64 // unix ms; 0 = NULL
}

func w17b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (e *w17Env) seedSpeakers(id int64, sp ...w17Sp) {
	e.t.Helper()
	for _, s := range sp {
		var seen any
		if s.LastSeen != 0 {
			seen = s.LastSeen
		}
		e.exec(`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, included, added_manually, user_touched, last_seen_at, created_at)
		        VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1000)`, id, s.Acc, s.Name, s.Pos, w17b2i(s.Included), w17b2i(s.Manual), w17b2i(s.Touched), seen)
	}
}

const w17SpeakerDump = `SELECT account_id, display_name, position, included, added_manually, user_touched FROM standup_speaker WHERE standup_id = ? ORDER BY position, account_id`

func (e *w17Env) accounts(id int64) []string {
	e.t.Helper()
	return e.dump(`SELECT account_id FROM standup_speaker WHERE standup_id = ? ORDER BY position, account_id`, id)
}

func (e *w17Env) assertDense(id int64) {
	e.t.Helper()
	got := e.dump(`SELECT position FROM standup_speaker WHERE standup_id = ? ORDER BY position`, id)
	for i, p := range got {
		if p != fmt.Sprint(i) {
			e.t.Errorf("speaker positions = %v, want dense 0..%d", got, len(got)-1)
			return
		}
	}
}

func w17Accounts(sp []Speaker) []string {
	out := make([]string, len(sp))
	for i, s := range sp {
		out[i] = s.AccountID
	}
	return out
}

func w17Roster() []w17Sp {
	return []w17Sp{
		{Acc: "acc-a", Name: "Ada", Pos: 0, Included: true, LastSeen: 5000},
		{Acc: "acc-b", Name: "Ben", Pos: 1, Included: true, LastSeen: 5000},
		{Acc: "acc-c", Name: "Cy", Pos: 2, Included: false, Touched: true, LastSeen: 5000},
		{Acc: "acc-d", Name: "Dee", Pos: 3, Included: true, Manual: true},
	}
}

func (e *w17Env) speakerSetup() int64 {
	e.t.Helper()
	id := e.seedStandup("scrum")
	e.seedSpeakers(id, w17Roster()...)
	return id
}

func TestSpeakers_ReturnsEveryRowByPositionWithAllFields(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	// inserted out of order
	e.seedSpeakers(id,
		w17Sp{Acc: "acc-c", Name: "Cy", Pos: 2, Included: true, Manual: true},
		w17Sp{Acc: "acc-a", Name: "Ada", Pos: 0, Included: false, Touched: true, LastSeen: 5000},
		w17Sp{Acc: "acc-b", Name: "Ben", Pos: 1, Included: true},
	)
	got, err := e.s.Speakers(e.ctx, id)
	if err != nil {
		t.Fatalf("Speakers: %v", err)
	}
	if want := []string{"acc-a", "acc-b", "acc-c"}; !reflect.DeepEqual(w17Accounts(got), want) {
		t.Fatalf("order = %v, want %v", w17Accounts(got), want)
	}
	a, b, c := got[0], got[1], got[2]
	if a.DisplayName != "Ada" || a.Position != 0 || a.Included || a.Manual || !a.UserTouched || !a.LastSeenAt.Equal(time.UnixMilli(5000)) || !a.CreatedAt.Equal(time.UnixMilli(1000)) {
		t.Errorf("acc-a = %+v", a)
	}
	if b.DisplayName != "Ben" || b.Position != 1 || !b.Included || b.Manual || b.UserTouched || !b.LastSeenAt.IsZero() {
		t.Errorf("acc-b = %+v (NULL last_seen_at must be the zero time)", b)
	}
	if c.DisplayName != "Cy" || c.Position != 2 || !c.Included || !c.Manual || c.UserTouched {
		t.Errorf("acc-c = %+v", c)
	}
}

func TestSpeakers_EmptyAndUnknownStandup(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	got, err := e.s.Speakers(e.ctx, id)
	if err != nil || len(got) != 0 {
		t.Errorf("Speakers on an empty standup = %v, %v", got, err)
	}
	_, err = e.s.Speakers(e.ctx, 999)
	w17RequireCode(t, err, apperr.NotFound)
}

// ── SetSpeakerIncluded ──

func TestSetSpeakerIncluded_TogglesAndMarksUserTouched(t *testing.T) {
	e := newW17(t)
	id := e.speakerSetup()
	before := e.rev(id)
	e.clk.Advance(time.Minute)

	ack, err := e.s.SetSpeakerIncluded(e.ctx, id, "acc-b", false)
	if err != nil {
		t.Fatalf("SetSpeakerIncluded: %v", err)
	}
	e.assertAck(id, before, ack)
	if ack.NeedsSync || e.needsSync(id) {
		t.Error("including/excluding a speaker is view-only: no needs_sync")
	}
	want := []string{"acc-a|Ada|0|1|0|0", "acc-b|Ben|1|0|0|1", "acc-c|Cy|2|0|0|1", "acc-d|Dee|3|1|1|0"}
	if got := e.dump(w17SpeakerDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("speakers = %v\nwant %v", got, want)
	}

	// and back in
	before = e.rev(id)
	ack, err = e.s.SetSpeakerIncluded(e.ctx, id, "acc-c", true)
	if err != nil {
		t.Fatal(err)
	}
	e.assertAck(id, before, ack)
	want = []string{"acc-a|Ada|0|1|0|0", "acc-b|Ben|1|0|0|1", "acc-c|Cy|2|1|0|1", "acc-d|Dee|3|1|1|0"}
	if got := e.dump(w17SpeakerDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("speakers = %v\nwant %v", got, want)
	}
}

func TestSetSpeakerIncluded_UnknownSpeakerOrStandupIsNotFound(t *testing.T) {
	e := newW17(t)
	id := e.speakerSetup()
	before := e.rev(id)
	_, err := e.s.SetSpeakerIncluded(e.ctx, id, "nobody", false)
	w17RequireCode(t, err, apperr.NotFound)
	if e.rev(id) != before {
		t.Error("a failed call must not bump rev")
	}
	_, err = e.s.SetSpeakerIncluded(e.ctx, 999, "acc-a", false)
	w17RequireCode(t, err, apperr.NotFound)
}

func TestSetSpeakerIncluded_OnlyAffectsTheGivenStandup(t *testing.T) {
	e := newW17(t)
	a := e.speakerSetup()
	b := e.speakerSetup()
	if _, err := e.s.SetSpeakerIncluded(e.ctx, a, "acc-a", false); err != nil {
		t.Fatal(err)
	}
	if got := e.str(`SELECT included || '|' || user_touched FROM standup_speaker WHERE standup_id = ? AND account_id = 'acc-a'`, b); got != "1|0" {
		t.Errorf("the same account in another standup changed: %s", got)
	}
}

// ── MoveSpeaker ──

func TestMoveSpeaker_RewritesDensePositions(t *testing.T) {
	cases := []struct {
		name string
		acc  string
		to   int
		want []string
	}{
		{"first_to_third", "acc-a", 2, []string{"acc-b", "acc-c", "acc-a", "acc-d"}},
		{"last_to_first", "acc-d", 0, []string{"acc-d", "acc-a", "acc-b", "acc-c"}},
		{"clamped_high", "acc-b", 99, []string{"acc-a", "acc-c", "acc-d", "acc-b"}},
		{"clamped_low", "acc-c", -5, []string{"acc-c", "acc-a", "acc-b", "acc-d"}},
		{"one_step_up", "acc-c", 1, []string{"acc-a", "acc-c", "acc-b", "acc-d"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newW17(t)
			id := e.speakerSetup()
			before := e.rev(id)
			e.clk.Advance(time.Minute)
			ack, err := e.s.MoveSpeaker(e.ctx, id, tc.acc, tc.to)
			if err != nil {
				t.Fatalf("MoveSpeaker: %v", err)
			}
			e.assertAck(id, before, ack)
			if got := e.accounts(id); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("order = %v, want %v", got, tc.want)
			}
			e.assertDense(id)
			// only the moved speaker becomes user_touched; Cy was already touched in the roster
			for _, row := range e.dump(`SELECT account_id, user_touched FROM standup_speaker WHERE standup_id = ?`, id) {
				acc, touched, _ := strings.Cut(row, "|")
				want := "0"
				if acc == tc.acc || acc == "acc-c" {
					want = "1"
				}
				if touched != want {
					t.Errorf("%s user_touched = %s, want %s", acc, touched, want)
				}
			}
			// included / manual flags are untouched
			if got := e.int(`SELECT count(*) FROM standup_speaker WHERE standup_id = ? AND included = 1`, id); got != 3 {
				t.Errorf("included count = %d, want 3", got)
			}
		})
	}
}

func TestMoveSpeaker_NonDensePositionsBecomeDense(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	e.seedSpeakers(id,
		w17Sp{Acc: "a", Name: "A", Pos: 0, Included: true},
		w17Sp{Acc: "b", Name: "B", Pos: 5, Included: true},
		w17Sp{Acc: "c", Name: "C", Pos: 9, Included: true},
		w17Sp{Acc: "d", Name: "D", Pos: 20, Included: true},
	)
	if _, err := e.s.MoveSpeaker(e.ctx, id, "d", 1); err != nil {
		t.Fatal(err)
	}
	if got, want := e.accounts(id), []string{"a", "d", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	e.assertDense(id)
}

func TestMoveSpeaker_UnknownSpeakerOrStandupIsNotFound(t *testing.T) {
	e := newW17(t)
	id := e.speakerSetup()
	_, err := e.s.MoveSpeaker(e.ctx, id, "nobody", 0)
	w17RequireCode(t, err, apperr.NotFound)
	_, err = e.s.MoveSpeaker(e.ctx, 999, "acc-a", 0)
	w17RequireCode(t, err, apperr.NotFound)
}

// ── SortSpeakers ──

func TestSortSpeakers_AZIsCaseInsensitiveAndIncludesExcluded(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	e.seedSpeakers(id,
		w17Sp{Acc: "1", Name: "bob", Pos: 0, Included: true},
		w17Sp{Acc: "2", Name: "Alice", Pos: 1, Included: false, Touched: true},
		w17Sp{Acc: "3", Name: "carol", Pos: 2, Included: true},
		w17Sp{Acc: "4", Name: "Dave", Pos: 3, Included: true, Manual: true},
	)
	before := e.rev(id)
	e.clk.Advance(time.Minute)
	got, ack, err := e.s.SortSpeakers(e.ctx, id, "az", nil, nil)
	if err != nil {
		t.Fatalf("SortSpeakers: %v", err)
	}
	e.assertAck(id, before, ack)
	if want := []string{"2", "1", "3", "4"}; !reflect.DeepEqual(w17Accounts(got), want) {
		t.Errorf("returned order = %v, want %v (Alice, bob, carol, Dave)", w17Accounts(got), want)
	}
	for i, s := range got {
		if s.Position != i {
			t.Errorf("returned %s position = %d, want %d", s.AccountID, s.Position, i)
		}
	}
	if want := []string{"2", "1", "3", "4"}; !reflect.DeepEqual(e.accounts(id), want) {
		t.Errorf("persisted order = %v, want %v", e.accounts(id), want)
	}
	e.assertDense(id)
	// flags survive
	if got := e.str(`SELECT included || '|' || added_manually FROM standup_speaker WHERE standup_id = ? AND account_id = '2'`, id); got != "0|0" {
		t.Errorf("Alice flags = %s, want 0|0", got)
	}
	if got := e.str(`SELECT included || '|' || added_manually FROM standup_speaker WHERE standup_id = ? AND account_id = '4'`, id); got != "1|1" {
		t.Errorf("Dave flags = %s, want 1|1", got)
	}
}

func TestSortSpeakers_ActiveByCountDescendingThenName(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	e.seedSpeakers(id,
		w17Sp{Acc: "a", Name: "Ada", Pos: 0, Included: true},
		w17Sp{Acc: "b", Name: "Ben", Pos: 1, Included: true},
		w17Sp{Acc: "c", Name: "Cy", Pos: 2, Included: true},
		w17Sp{Acc: "d", Name: "Dee", Pos: 3, Included: true},
		w17Sp{Acc: "e", Name: "Eve", Pos: 4, Included: false, Touched: true},
	)
	active := map[string]int{"a": 1, "b": 5, "d": 5, "e": 3} // c is absent: 0
	before := e.rev(id)
	got, ack, err := e.s.SortSpeakers(e.ctx, id, "active", active, nil)
	if err != nil {
		t.Fatalf("SortSpeakers: %v", err)
	}
	e.assertAck(id, before, ack)
	want := []string{"b", "d", "e", "a", "c"}
	if !reflect.DeepEqual(w17Accounts(got), want) {
		t.Errorf("returned order = %v, want %v", w17Accounts(got), want)
	}
	if !reflect.DeepEqual(e.accounts(id), want) {
		t.Errorf("persisted order = %v, want %v", e.accounts(id), want)
	}
	e.assertDense(id)
}

func TestSortSpeakers_ShuffleWithReshuffleOffRewritesPositionsUsingRnd(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	roster := []string{"a", "b", "c", "d", "e", "f"}
	for i, acc := range roster {
		e.seedSpeakers(id, w17Sp{Acc: acc, Name: strings.ToUpper(acc), Pos: i, Included: i != 3, Touched: i == 3})
	}
	seedBefore := e.int(`SELECT shuffle_seed FROM standup_settings WHERE standup_id = ?`, id)

	orders := map[string]bool{}
	for start := int64(0); start < 12; start++ {
		counter := start * 7919
		calls := 0
		rnd := func() int64 { calls++; counter += 104729; return counter }
		before := e.rev(id)
		e.clk.Advance(time.Minute)
		got, ack, err := e.s.SortSpeakers(e.ctx, id, "shuffle", nil, rnd)
		if err != nil {
			t.Fatalf("SortSpeakers shuffle: %v", err)
		}
		e.assertAck(id, before, ack)
		if calls == 0 {
			t.Fatal("shuffle must draw from the rnd it was given")
		}
		persisted := e.accounts(id)
		if !reflect.DeepEqual(w17Accounts(got), persisted) {
			t.Fatalf("returned %v but persisted %v", w17Accounts(got), persisted)
		}
		seen := map[string]bool{}
		for _, acc := range persisted {
			seen[acc] = true
		}
		if len(persisted) != len(roster) || len(seen) != len(roster) {
			t.Fatalf("shuffle must keep exactly the same speakers: %v", persisted)
		}
		e.assertDense(id)
		orders[strings.Join(persisted, ",")] = true
	}
	if len(orders) < 2 {
		t.Errorf("12 different rnd streams produced the same order %v: shuffle ignores rnd", orders)
	}
	// flags are preserved
	if got := e.str(`SELECT included || '|' || user_touched FROM standup_speaker WHERE standup_id = ? AND account_id = 'd'`, id); got != "0|1" {
		t.Errorf("speaker d flags = %s, want 0|1", got)
	}
	// reshuffle_daily off: the stored seed is not used
	if got := e.int(`SELECT shuffle_seed FROM standup_settings WHERE standup_id = ?`, id); got != seedBefore {
		t.Errorf("shuffle_seed changed %d → %d with reshuffle_daily off", seedBefore, got)
	}
}

func TestSortSpeakers_ShuffleWithReshuffleOnStoresNewSeedForTodayAndKeepsPositions(t *testing.T) {
	e := newW17(t)
	id := e.speakerSetup()
	e.exec(`UPDATE standup_settings SET reshuffle_daily = 1, shuffle_seed = 7, shuffle_date = '2026-11-20' WHERE standup_id = ?`, id)
	orderBefore := e.accounts(id)
	before := e.rev(id)
	e.clk.Advance(time.Minute) // 22:31 local on 2026-11-23, already 2026-11-24 in UTC

	got, ack, err := e.s.SortSpeakers(e.ctx, id, "shuffle", nil, func() int64 { return 4242 })
	if err != nil {
		t.Fatalf("SortSpeakers shuffle: %v", err)
	}
	e.assertAck(id, before, ack)
	if got := e.int(`SELECT shuffle_seed FROM standup_settings WHERE standup_id = ?`, id); got != 4242 {
		t.Errorf("shuffle_seed = %d, want 4242 (the value drawn from rnd)", got)
	}
	if got := e.str(`SELECT shuffle_date FROM standup_settings WHERE standup_id = ?`, id); got != "2026-11-23" {
		t.Errorf("shuffle_date = %q, want the LOCAL date 2026-11-23", got)
	}
	if !reflect.DeepEqual(e.accounts(id), orderBefore) {
		t.Errorf("positions changed with reshuffle_daily on: %v → %v", orderBefore, e.accounts(id))
	}
	if !reflect.DeepEqual(w17Accounts(got), orderBefore) {
		t.Errorf("returned order = %v, want the unchanged stored order %v", w17Accounts(got), orderBefore)
	}
	if got := e.int(`SELECT count(*) FROM standup_speaker WHERE standup_id = ? AND user_touched = 1`, id); got != 1 {
		t.Errorf("user_touched count = %d, want only the roster's 1", got)
	}
}

func TestSortSpeakers_InvalidModeIsValidation(t *testing.T) {
	for _, mode := range []string{"", "random", "AZ", "alphabetical"} {
		t.Run("mode_"+mode, func(t *testing.T) {
			e := newW17(t)
			id := e.speakerSetup()
			before := e.rev(id)
			orderBefore := e.accounts(id)
			_, _, err := e.s.SortSpeakers(e.ctx, id, mode, nil, nil)
			w17RequireValidation(t, err, "mode")
			if e.rev(id) != before || !reflect.DeepEqual(e.accounts(id), orderBefore) {
				t.Error("a rejected sort must change nothing")
			}
		})
	}
}

func TestSortSpeakers_UnknownStandupIsNotFound(t *testing.T) {
	e := newW17(t)
	_, _, err := e.s.SortSpeakers(e.ctx, 999, "az", nil, nil)
	w17RequireCode(t, err, apperr.NotFound)
}

// ── AddSpeaker / RemoveSpeaker ──

func TestAddSpeaker_AppendsManualSpeakerIncluded(t *testing.T) {
	e := newW17(t)
	id := e.speakerSetup()
	before := e.rev(id)
	e.clk.Advance(time.Minute)

	got, ack, err := e.s.AddSpeaker(e.ctx, id, model.Person{AccountID: "new-1", DisplayName: "Nia", Active: true, Kind: "user"})
	if err != nil {
		t.Fatalf("AddSpeaker: %v", err)
	}
	e.assertAck(id, before, ack)
	if want := []string{"acc-a", "acc-b", "acc-c", "acc-d", "new-1"}; !reflect.DeepEqual(w17Accounts(got), want) {
		t.Fatalf("returned = %v, want %v", w17Accounts(got), want)
	}
	last := got[4]
	if last.DisplayName != "Nia" || last.Position != 4 || !last.Included || !last.Manual {
		t.Errorf("new speaker = %+v, want name Nia, position 4, included, manual", last)
	}
	if !last.CreatedAt.Equal(e.clk.Now()) {
		t.Errorf("CreatedAt = %v, want now %v", last.CreatedAt, e.clk.Now())
	}
	want := []string{"acc-a|Ada|0|1|0|0", "acc-b|Ben|1|1|0|0", "acc-c|Cy|2|0|0|1", "acc-d|Dee|3|1|1|0"}
	if got := e.dump(w17SpeakerDump, id); !reflect.DeepEqual(got[:4], want) {
		t.Errorf("existing speakers changed: %v", got[:4])
	}
	if got := e.str(`SELECT included || '|' || added_manually FROM standup_speaker WHERE standup_id = ? AND account_id = 'new-1'`, id); got != "1|1" {
		t.Errorf("new row flags = %s, want 1|1", got)
	}
}

func TestAddSpeaker_ToAnEmptyListStartsAtZero(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	got, _, err := e.s.AddSpeaker(e.ctx, id, model.Person{AccountID: "x", DisplayName: "Xan"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Position != 0 {
		t.Errorf("got %+v, want one speaker at position 0", got)
	}
}

func TestAddSpeaker_ExistingExcludedSpeakerIsReincluded(t *testing.T) {
	e := newW17(t)
	id := e.speakerSetup()
	e.exec(`UPDATE standup_speaker SET user_touched = 0 WHERE standup_id = ? AND account_id = 'acc-c'`, id)
	before := e.rev(id)
	got, ack, err := e.s.AddSpeaker(e.ctx, id, model.Person{AccountID: "acc-c", DisplayName: "Cy"})
	if err != nil {
		t.Fatalf("AddSpeaker: %v", err)
	}
	e.assertAck(id, before, ack)
	if len(got) != 4 {
		t.Fatalf("an existing speaker must not be duplicated: %v", w17Accounts(got))
	}
	if row := e.str(`SELECT position || '|' || included || '|' || added_manually || '|' || user_touched FROM standup_speaker WHERE standup_id = ? AND account_id = 'acc-c'`, id); row != "2|1|0|1" {
		t.Errorf("acc-c = %s, want position 2, included 1, manual 0 (unchanged), user_touched 1", row)
	}
}

func TestAddSpeaker_RejectsEmptyAccountID(t *testing.T) {
	e := newW17(t)
	id := e.speakerSetup()
	before := e.rev(id)
	_, _, err := e.s.AddSpeaker(e.ctx, id, model.Person{DisplayName: "Nobody"})
	w17RequireValidation(t, err, "accountId")
	if e.rev(id) != before || len(e.accounts(id)) != 4 {
		t.Error("a rejected add must change nothing")
	}
}

func TestAddSpeaker_UnknownStandupIsNotFound(t *testing.T) {
	e := newW17(t)
	_, _, err := e.s.AddSpeaker(e.ctx, 999, model.Person{AccountID: "x", DisplayName: "X"})
	w17RequireCode(t, err, apperr.NotFound)
}

func TestRemoveSpeaker_DeletesManualSpeakerAndKeepsPositionsDense(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	e.seedSpeakers(id,
		w17Sp{Acc: "a", Name: "A", Pos: 0, Included: true},
		w17Sp{Acc: "m", Name: "M", Pos: 1, Included: true, Manual: true},
		w17Sp{Acc: "c", Name: "C", Pos: 2, Included: true},
	)
	before := e.rev(id)
	e.clk.Advance(time.Minute)
	ack, err := e.s.RemoveSpeaker(e.ctx, id, "m")
	if err != nil {
		t.Fatalf("RemoveSpeaker: %v", err)
	}
	e.assertAck(id, before, ack)
	if got, want := e.accounts(id), []string{"a", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("speakers = %v, want %v", got, want)
	}
	e.assertDense(id)
}

func TestRemoveSpeaker_AutoSpeakerIsRemovedAndSyncDoesNotAddThemBack(t *testing.T) {
	e := newW17(t)
	id := e.speakerSetup()
	before := e.rev(id)
	if _, err := e.s.RemoveSpeaker(e.ctx, id, "acc-a"); err != nil {
		t.Fatal(err)
	}
	if got := e.int(`SELECT count(*) FROM standup_speaker WHERE standup_id = ? AND account_id = 'acc-a'`, id); got != 0 {
		t.Error("the speaker is still listed")
	}
	if e.rev(id) == before {
		t.Error("a remove must bump rev")
	}
	e.mergeSpeakers(id, []model.Person{{AccountID: "acc-a", DisplayName: "Ada"}})
	if got := e.int(`SELECT count(*) FROM standup_speaker WHERE standup_id = ? AND account_id = 'acc-a'`, id); got != 0 {
		t.Error("sync added a removed speaker back")
	}
	// Adding them again undoes the removal.
	if _, _, err := e.s.AddSpeaker(e.ctx, id, model.Person{AccountID: "acc-a", DisplayName: "Ada"}); err != nil {
		t.Fatal(err)
	}
	if got := e.int(`SELECT count(*) FROM standup_speaker_removed WHERE standup_id = ?`, id); got != 0 {
		t.Errorf("removed rows = %d after AddSpeaker, want 0", got)
	}
	if got := e.int(`SELECT included FROM standup_speaker WHERE standup_id = ? AND account_id = 'acc-a'`, id); got != 1 {
		t.Error("re-added speaker is not included")
	}
}

func TestRemoveSpeaker_UnknownSpeakerOrStandupIsNotFound(t *testing.T) {
	e := newW17(t)
	id := e.speakerSetup()
	_, err := e.s.RemoveSpeaker(e.ctx, id, "nobody")
	w17RequireCode(t, err, apperr.NotFound)
	_, err = e.s.RemoveSpeaker(e.ctx, 999, "acc-d")
	w17RequireCode(t, err, apperr.NotFound)
}

// ── EnsureDailySeed ──

func TestEnsureDailySeed_WritesOnlyWhenReshuffleOnAndDateDiffers(t *testing.T) {
	cases := []struct {
		name      string
		reshuffle int
		date      any // stored shuffle_date (nil = NULL)
		wantWrote bool
		wantSeed  int64
		wantDate  string // "" = NULL
	}{
		{"off_never_written", 0, nil, false, 9, ""},
		{"off_with_old_date", 0, "2026-11-22", false, 9, "2026-11-22"},
		{"on_no_date_yet", 1, nil, true, 5, "2026-11-23"},
		{"on_same_date_keeps_seed", 1, "2026-11-23", false, 9, "2026-11-23"},
		{"on_new_day_reseeds", 1, "2026-11-22", true, 5, "2026-11-23"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newW17(t)
			id := e.seedStandup("scrum")
			e.exec(`UPDATE standup_settings SET reshuffle_daily = ?, shuffle_seed = 9, shuffle_date = ? WHERE standup_id = ?`, tc.reshuffle, tc.date, id)
			revBefore := e.rev(id)

			wrote, err := e.s.EnsureDailySeed(e.ctx, id, "2026-11-23", 5)
			if err != nil {
				t.Fatalf("EnsureDailySeed: %v", err)
			}
			if wrote != tc.wantWrote {
				t.Errorf("wrote = %v, want %v", wrote, tc.wantWrote)
			}
			if got := e.int(`SELECT shuffle_seed FROM standup_settings WHERE standup_id = ?`, id); got != tc.wantSeed {
				t.Errorf("shuffle_seed = %d, want %d", got, tc.wantSeed)
			}
			if got := e.str(`SELECT shuffle_date FROM standup_settings WHERE standup_id = ?`, id); got != tc.wantDate {
				t.Errorf("shuffle_date = %q, want %q", got, tc.wantDate)
			}
			if e.rev(id) != revBefore {
				t.Errorf("EnsureDailySeed must not bump rev (%d → %d)", revBefore, e.rev(id))
			}
		})
	}
}

func TestEnsureDailySeed_SecondCallSameDayIsANoOp(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	e.exec(`UPDATE standup_settings SET reshuffle_daily = 1 WHERE standup_id = ?`, id)
	if wrote, err := e.s.EnsureDailySeed(e.ctx, id, "2026-11-23", 5); err != nil || !wrote {
		t.Fatalf("first call: wrote=%v err=%v", wrote, err)
	}
	if wrote, err := e.s.EnsureDailySeed(e.ctx, id, "2026-11-23", 6); err != nil || wrote {
		t.Fatalf("second call same day: wrote=%v err=%v", wrote, err)
	}
	if got := e.int(`SELECT shuffle_seed FROM standup_settings WHERE standup_id = ?`, id); got != 5 {
		t.Errorf("shuffle_seed = %d, want the first call's 5", got)
	}
}

func TestEnsureDailySeed_UnknownStandupIsNotFound(t *testing.T) {
	e := newW17(t)
	_, err := e.s.EnsureDailySeed(e.ctx, 999, "2026-11-23", 1)
	w17RequireCode(t, err, apperr.NotFound)
}

// ── MergeSpeakersTx (sync apply step 6) ──

func (e *w17Env) mergeSpeakers(id int64, seen []model.Person) bool {
	e.t.Helper()
	var changed bool
	err := e.db.WriteTx(e.ctx, func(tx *sql.Tx) error {
		var err error
		changed, err = MergeSpeakersTx(e.ctx, tx, id, seen, e.clk.Now())
		return err
	})
	if err != nil {
		e.t.Fatalf("MergeSpeakersTx: %v", err)
	}
	return changed
}

func (e *w17Env) lastSeen(id int64, acc string) int64 {
	e.t.Helper()
	return e.int(`SELECT last_seen_at FROM standup_speaker WHERE standup_id = ? AND account_id = ?`, id, acc)
}

func w17ago(now time.Time, d int) int64 { return store.Ms(now.Add(-time.Duration(d) * 24 * time.Hour)) }

func TestMergeSpeakers_AppendsRefreshesAndPrunesPerTheSpeakerRules(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	other := e.seedStandup("scrum")
	now := e.clk.Now()
	e.seedSpeakers(id,
		w17Sp{Acc: "acc-a", Name: "Ada", Pos: 0, Included: true, LastSeen: w17ago(now, 10)},                 // seen, renamed
		w17Sp{Acc: "acc-b", Name: "Ben", Pos: 1, Included: true, LastSeen: w17ago(now, 31)},                 // unseen 31d auto → pruned
		w17Sp{Acc: "acc-c", Name: "Cy", Pos: 2, Included: true, LastSeen: w17ago(now, 29)},                  // unseen 29d → kept
		w17Sp{Acc: "acc-d", Name: "Dee", Pos: 3, Included: false, Touched: true, LastSeen: w17ago(now, 90)}, // seen, excluded stays excluded
		w17Sp{Acc: "acc-e", Name: "Eve", Pos: 4, Included: true, Manual: true, LastSeen: w17ago(now, 90)},   // manual → kept
		w17Sp{Acc: "acc-f", Name: "Fay", Pos: 5, Included: true, LastSeen: w17ago(now, 30)},                 // exactly 30d → kept
		w17Sp{Acc: "acc-g", Name: "Gus", Pos: 6, Included: true, LastSeen: w17ago(now, 40)},                 // old but seen now → kept
		w17Sp{Acc: "acc-h", Name: "Hal", Pos: 7, Included: true, Touched: true, LastSeen: w17ago(now, 200)}, // user_touched → kept
	)
	e.seedSpeakers(other, w17Sp{Acc: "acc-b", Name: "Ben", Pos: 0, Included: true, LastSeen: w17ago(now, 400)})
	otherBefore := e.dump(w17SpeakerDump, other)

	seen := []model.Person{
		{AccountID: "acc-a", DisplayName: "Ada L."},
		{AccountID: "acc-d", DisplayName: "Dee"},
		{AccountID: "acc-g", DisplayName: "Gus"},
		{AccountID: "new-1", DisplayName: "Nia"},
		{AccountID: "new-2", DisplayName: "Kenji"},
		{AccountID: "new-1", DisplayName: "Nia"}, // duplicate
	}
	if changed := e.mergeSpeakers(id, seen); !changed {
		t.Error("appending, renaming and pruning must report changed")
	}
	want := []string{
		"acc-a|Ada L.|0|1|0|0",
		"acc-c|Cy|1|1|0|0",
		"acc-d|Dee|2|0|0|1",
		"acc-e|Eve|3|1|1|0",
		"acc-f|Fay|4|1|0|0",
		"acc-g|Gus|5|1|0|0",
		"acc-h|Hal|6|1|0|1",
		"new-1|Nia|7|1|0|0",
		"new-2|Kenji|8|1|0|0",
	}
	if got := e.dump(w17SpeakerDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("speakers after merge:\n got %v\nwant %v", got, want)
	}
	nowMs := store.Ms(now)
	for _, acc := range []string{"acc-a", "acc-d", "acc-g", "new-1", "new-2"} {
		if got := e.lastSeen(id, acc); got != nowMs {
			t.Errorf("%s last_seen_at = %d, want now %d", acc, got, nowMs)
		}
	}
	for acc, want := range map[string]int64{"acc-c": w17ago(now, 29), "acc-e": w17ago(now, 90), "acc-f": w17ago(now, 30), "acc-h": w17ago(now, 200)} {
		if got := e.lastSeen(id, acc); got != want {
			t.Errorf("%s last_seen_at = %d, want unchanged %d (not seen)", acc, got, want)
		}
	}
	if got := e.dump(w17SpeakerDump, other); !reflect.DeepEqual(got, otherBefore) {
		t.Errorf("another standup's speakers changed: %v → %v", otherBefore, got)
	}
}

func TestMergeSpeakers_NoListChangeReportsFalseButRefreshesLastSeen(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	now := e.clk.Now()
	e.seedSpeakers(id,
		w17Sp{Acc: "a", Name: "Ada", Pos: 0, Included: true, LastSeen: w17ago(now, 1)},
		w17Sp{Acc: "b", Name: "Ben", Pos: 1, Included: true, LastSeen: w17ago(now, 1)},
		w17Sp{Acc: "m", Name: "Mia", Pos: 2, Included: true, Manual: true, LastSeen: w17ago(now, 1)},
	)
	before := e.dump(w17SpeakerDump, id)
	changed := e.mergeSpeakers(id, []model.Person{{AccountID: "a", DisplayName: "Ada"}, {AccountID: "b", DisplayName: "Ben"}})
	if changed {
		t.Error("the same speakers with the same names must report changed=false")
	}
	if got := e.dump(w17SpeakerDump, id); !reflect.DeepEqual(got, before) {
		t.Errorf("speakers changed: %v → %v", before, got)
	}
	nowMs := store.Ms(now)
	if e.lastSeen(id, "a") != nowMs || e.lastSeen(id, "b") != nowMs {
		t.Error("last_seen_at must be refreshed for seen speakers")
	}
	if e.lastSeen(id, "m") != w17ago(now, 1) {
		t.Error("last_seen_at of an unseen speaker must not change")
	}
}

func TestMergeSpeakers_EachKindOfChangeReportsChanged(t *testing.T) {
	cases := []struct {
		name string
		seen []model.Person
	}{
		{"append_only", []model.Person{{AccountID: "a", DisplayName: "Ada"}, {AccountID: "z", DisplayName: "Zed"}}},
		{"rename_only", []model.Person{{AccountID: "a", DisplayName: "Ada Lovelace"}}},
		{"prune_only", []model.Person{{AccountID: "a", DisplayName: "Ada"}}}, // "stale" unseen 40d
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newW17(t)
			id := e.seedStandup("scrum")
			now := e.clk.Now()
			e.seedSpeakers(id, w17Sp{Acc: "a", Name: "Ada", Pos: 0, Included: true, LastSeen: w17ago(now, 1)})
			if tc.name == "prune_only" {
				e.seedSpeakers(id, w17Sp{Acc: "stale", Name: "Stale", Pos: 1, Included: true, LastSeen: w17ago(now, 40)})
			}
			if changed := e.mergeSpeakers(id, tc.seen); !changed {
				t.Error("changed must be true")
			}
		})
	}
}

func TestMergeSpeakers_IntoAnEmptyListAppendsInSeenOrder(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	changed := e.mergeSpeakers(id, []model.Person{{AccountID: "x", DisplayName: "Xan"}, {AccountID: "y", DisplayName: "Yui"}})
	if !changed {
		t.Error("appending must report changed")
	}
	want := []string{"x|Xan|0|1|0|0", "y|Yui|1|1|0|0"}
	if got := e.dump(w17SpeakerDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("speakers = %v, want %v", got, want)
	}
	if e.lastSeen(id, "x") != store.Ms(e.clk.Now()) {
		t.Error("last_seen_at must be now for appended speakers")
	}
}

func TestMergeSpeakers_NothingSeenAndNothingStaleIsNoChange(t *testing.T) {
	e := newW17(t)
	id := e.speakerSetup()
	// the roster's last_seen_at values are 1970-era: Ada/Ben are old auto rows. Make them recent first.
	e.exec(`UPDATE standup_speaker SET last_seen_at = ? WHERE standup_id = ?`, w17ago(e.clk.Now(), 2), id)
	before := e.dump(w17SpeakerDump, id)
	if changed := e.mergeSpeakers(id, nil); changed {
		t.Error("nothing seen and nothing stale must report changed=false")
	}
	if got := e.dump(w17SpeakerDump, id); !reflect.DeepEqual(got, before) {
		t.Errorf("speakers changed: %v → %v", before, got)
	}
}
