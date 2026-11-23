package pulse

// WP-05 tests: SpeakerOrder, DailySeed, Shuffle (data-model.md §2.3 "Speaker rules").
//
// SPEC-NOTE: the input Speakers slice is "by Position"; these tests always pass it already sorted with Position equal
// to the slice index, so they hold whether the implementation trusts slice order or sorts by Position.
//
// SPEC-NOTE: slot order is: included speakers (by position, or shuffled), then Unassigned when it has tickets
// (first → before the included speakers, last → after them, skip → after them with Included=false), then excluded
// speakers who have tickets (Included=false). With "skip" the Unassigned slot therefore precedes the excluded speakers.
//
// SPEC-NOTE: included speakers appear whether or not they have tickets (WithTickets only gates EXCLUDED speakers).
//
// SPEC-NOTE: with ReshuffleDaily the included speakers' order is exactly Shuffle(includedIDsByPosition, Seed); the
// Unassigned slot and the excluded tail are never shuffled. With ReshuffleDaily=false Seed is ignored.
//
// SPEC-NOTE: DailySeed = int64(FNV-1a-64 of "<standupID>|<date>") with the id in decimal, e.g. "7|2026-11-23".
// Shuffle's PRNG algorithm is not specified: tests assert determinism, permutation and that varying the seed varies the
// output (over 10 seeds and 10 items), never specific permutations.

import (
	"fmt"
	"hash/fnv"
	"reflect"
	"sort"
	"testing"

	"github.com/crueber/cuisle/internal/model"
)

func spk(id string, included bool) model.SpeakerConfig {
	return model.SpeakerConfig{AccountID: id, Name: "Name " + id, Included: included}
}

// spkList assigns Position = slice index.
func spkList(list ...model.SpeakerConfig) []model.SpeakerConfig {
	for i := range list {
		list[i].Position = i
	}
	return list
}

func tickets(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func slot(id string, included bool) SpeakerSlot { return SpeakerSlot{ID: id, Included: included} }

func unassignedSlot(included bool) SpeakerSlot {
	return SpeakerSlot{ID: model.UnassignedID, Included: included, Unassigned: true}
}

func assertSlots(t *testing.T, got, want []SpeakerSlot) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slots: got %d %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("slot %d: got %+v, want %+v (all: %+v)", i, got[i], want[i], got)
		}
	}
}

func TestSpeakerOrder_ManualOrderByPosition(t *testing.T) {
	got := SpeakerOrder(SpeakerInput{
		Speakers:           spkList(spk("ana", true), spk("arjun", true), spk("cy", true)),
		UnassignedPosition: "last",
		WithTickets:        tickets("ana", "arjun", "cy"),
	})
	assertSlots(t, got, []SpeakerSlot{slot("ana", true), slot("arjun", true), slot("cy", true)})
}

func TestSpeakerOrder_IncludedSpeakersWithoutTicketsStillAppear(t *testing.T) {
	got := SpeakerOrder(SpeakerInput{
		Speakers:    spkList(spk("ana", true), spk("arjun", true)),
		WithTickets: tickets("ana"),
	})
	assertSlots(t, got, []SpeakerSlot{slot("ana", true), slot("arjun", true)})
}

func TestSpeakerOrder_UnassignedFirstLastSkip(t *testing.T) {
	speakers := func() []model.SpeakerConfig { return spkList(spk("ana", true), spk("arjun", true)) }
	cases := []struct {
		name     string
		position string
		has      bool
		want     []SpeakerSlot
	}{
		{"first", "first", true, []SpeakerSlot{unassignedSlot(true), slot("ana", true), slot("arjun", true)}},
		{"last", "last", true, []SpeakerSlot{slot("ana", true), slot("arjun", true), unassignedSlot(true)}},
		{"skip stays in the model with Included=false, appended", "skip", true, []SpeakerSlot{slot("ana", true), slot("arjun", true), unassignedSlot(false)}},
		{"first but no unassigned tickets: omitted", "first", false, []SpeakerSlot{slot("ana", true), slot("arjun", true)}},
		{"last but no unassigned tickets: omitted", "last", false, []SpeakerSlot{slot("ana", true), slot("arjun", true)}},
		{"skip but no unassigned tickets: omitted", "skip", false, []SpeakerSlot{slot("ana", true), slot("arjun", true)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SpeakerOrder(SpeakerInput{
				Speakers: speakers(), UnassignedPosition: tc.position, HasUnassigned: tc.has,
				WithTickets: tickets("ana", "arjun"),
			})
			assertSlots(t, got, tc.want)
		})
	}
}

func TestSpeakerOrder_ExcludedSpeakers(t *testing.T) {
	speakers := func() []model.SpeakerConfig {
		return spkList(spk("ana", true), spk("dee", false), spk("arjun", true), spk("eli", false))
	}
	t.Run("excluded with tickets follow the included, Included=false; excluded without tickets are omitted", func(t *testing.T) {
		got := SpeakerOrder(SpeakerInput{
			Speakers: speakers(), UnassignedPosition: "last", WithTickets: tickets("ana", "arjun", "dee"),
		})
		assertSlots(t, got, []SpeakerSlot{slot("ana", true), slot("arjun", true), slot("dee", false)})
	})
	t.Run("excluded keep their relative position order", func(t *testing.T) {
		got := SpeakerOrder(SpeakerInput{
			Speakers: speakers(), UnassignedPosition: "last", WithTickets: tickets("eli", "dee"),
		})
		assertSlots(t, got, []SpeakerSlot{slot("ana", true), slot("arjun", true), slot("dee", false), slot("eli", false)})
	})
	t.Run("excluded come after Unassigned-last", func(t *testing.T) {
		got := SpeakerOrder(SpeakerInput{
			Speakers: speakers(), UnassignedPosition: "last", HasUnassigned: true, WithTickets: tickets("dee"),
		})
		assertSlots(t, got, []SpeakerSlot{slot("ana", true), slot("arjun", true), unassignedSlot(true), slot("dee", false)})
	})
	t.Run("excluded come after Unassigned-first and the included", func(t *testing.T) {
		got := SpeakerOrder(SpeakerInput{
			Speakers: speakers(), UnassignedPosition: "first", HasUnassigned: true, WithTickets: tickets("dee"),
		})
		assertSlots(t, got, []SpeakerSlot{unassignedSlot(true), slot("ana", true), slot("arjun", true), slot("dee", false)})
	})
	t.Run("skip: Unassigned (Included=false) precedes the excluded speakers", func(t *testing.T) {
		got := SpeakerOrder(SpeakerInput{
			Speakers: speakers(), UnassignedPosition: "skip", HasUnassigned: true, WithTickets: tickets("dee"),
		})
		assertSlots(t, got, []SpeakerSlot{slot("ana", true), slot("arjun", true), unassignedSlot(false), slot("dee", false)})
	})
	t.Run("no included speakers at all", func(t *testing.T) {
		got := SpeakerOrder(SpeakerInput{
			Speakers: spkList(spk("dee", false)), UnassignedPosition: "first", HasUnassigned: true, WithTickets: tickets("dee"),
		})
		assertSlots(t, got, []SpeakerSlot{unassignedSlot(true), slot("dee", false)})
	})
	t.Run("empty input gives no slots", func(t *testing.T) {
		if got := SpeakerOrder(SpeakerInput{UnassignedPosition: "last"}); len(got) != 0 {
			t.Errorf("got %+v, want none", got)
		}
	})
}

func tenSpeakers() ([]model.SpeakerConfig, []string) {
	ids := []string{"a0", "a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"}
	var list []model.SpeakerConfig
	for _, id := range ids {
		list = append(list, spk(id, true))
	}
	return spkList(list...), ids
}

func slotIDs(slots []SpeakerSlot) []string {
	var out []string
	for _, s := range slots {
		out = append(out, s.ID)
	}
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestSpeakerOrder_ReshuffleDaily_SameSeedSameOrderAndPreservesMembers(t *testing.T) {
	speakers, ids := tenSpeakers()
	in := SpeakerInput{Speakers: speakers, ReshuffleDaily: true, Seed: 42, UnassignedPosition: "last", WithTickets: tickets(ids...)}
	first := SpeakerOrder(in)
	second := SpeakerOrder(in)
	assertSlots(t, second, first) // deterministic

	if got := sortedCopy(slotIDs(first)); !reflect.DeepEqual(got, sortedCopy(ids)) {
		t.Errorf("shuffled order lost or duplicated speakers: %v", slotIDs(first))
	}
	for _, s := range first {
		if !s.Included || s.Unassigned {
			t.Errorf("slot %+v should be an included, assigned speaker", s)
		}
	}
	// exactly Shuffle over the by-position ids
	if want := Shuffle(ids, 42); !reflect.DeepEqual(slotIDs(first), want) {
		t.Errorf("order = %v, want Shuffle(ids, seed) = %v", slotIDs(first), want)
	}
}

func TestSpeakerOrder_ReshuffleDaily_DifferentSeedsGiveDifferentOrders(t *testing.T) {
	speakers, ids := tenSpeakers()
	seen := map[string]bool{}
	for seed := int64(1); seed <= 10; seed++ {
		got := SpeakerOrder(SpeakerInput{Speakers: speakers, ReshuffleDaily: true, Seed: seed, WithTickets: tickets(ids...)})
		seen[fmt.Sprint(slotIDs(got))] = true
	}
	if len(seen) < 2 {
		t.Errorf("10 different seeds produced %d distinct orders, want at least 2", len(seen))
	}
}

func TestSpeakerOrder_ReshuffleOff_IgnoresSeed(t *testing.T) {
	speakers, ids := tenSpeakers()
	for _, seed := range []int64{0, 1, 42, -7} {
		got := SpeakerOrder(SpeakerInput{Speakers: speakers, ReshuffleDaily: false, Seed: seed, WithTickets: tickets(ids...)})
		if !reflect.DeepEqual(slotIDs(got), ids) {
			t.Errorf("seed %d: order = %v, want manual order %v", seed, slotIDs(got), ids)
		}
	}
}

func TestSpeakerOrder_ReshuffleDaily_UnassignedAndExcludedAreNotShuffled(t *testing.T) {
	list := []model.SpeakerConfig{}
	var ids []string
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("a%d", i)
		ids = append(ids, id)
		list = append(list, spk(id, true))
	}
	list = append(list, spk("ex1", false), spk("ex2", false))
	speakers := spkList(list...)
	for _, pos := range []string{"first", "last", "skip"} {
		for seed := int64(1); seed <= 5; seed++ {
			got := SpeakerOrder(SpeakerInput{
				Speakers: speakers, ReshuffleDaily: true, Seed: seed, UnassignedPosition: pos, HasUnassigned: true,
				WithTickets: tickets("ex1", "ex2"),
			})
			shuffled := Shuffle(ids, seed)
			var want []SpeakerSlot
			if pos == "first" {
				want = append(want, unassignedSlot(true))
			}
			for _, id := range shuffled {
				want = append(want, slot(id, true))
			}
			switch pos {
			case "last":
				want = append(want, unassignedSlot(true))
			case "skip":
				want = append(want, unassignedSlot(false))
			}
			want = append(want, slot("ex1", false), slot("ex2", false))
			assertSlots(t, got, want)
		}
	}
}

func TestDailySeed(t *testing.T) {
	fnvOf := func(s string) int64 {
		h := fnv.New64a()
		h.Write([]byte(s))
		return int64(h.Sum64())
	}
	cases := []struct {
		id   int64
		date string
	}{
		{7, "2026-11-23"},
		{1, "2026-01-01"},
		{123456, "2026-12-31"},
	}
	for _, tc := range cases {
		want := fnvOf(fmt.Sprintf("%d|%s", tc.id, tc.date))
		if got := DailySeed(tc.id, tc.date); got != want {
			t.Errorf("DailySeed(%d, %q) = %d, want %d (fnv64a of %q)", tc.id, tc.date, got, want, fmt.Sprintf("%d|%s", tc.id, tc.date))
		}
	}
}

func TestDailySeed_StableForSameInputsDifferentAcrossDatesAndStandups(t *testing.T) {
	a := DailySeed(7, "2026-11-23")
	if b := DailySeed(7, "2026-11-23"); a != b {
		t.Errorf("not stable: %d vs %d", a, b)
	}
	if b := DailySeed(7, "2026-11-24"); a == b {
		t.Errorf("seed identical across dates: %d", a)
	}
	if b := DailySeed(8, "2026-11-23"); a == b {
		t.Errorf("seed identical across standups: %d", a)
	}
	// "1|11" vs "11|1"-style ambiguity is avoided by the separator
	if DailySeed(1, "2026-11-23") == DailySeed(12, "026-11-23") {
		t.Errorf("separator missing: seeds collide")
	}
}

func TestShuffle_IsADeterministicPermutation(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	orig := append([]string(nil), ids...)
	for _, seed := range []int64{0, 1, 42, -1, 1 << 40, -(1 << 40)} {
		got := Shuffle(ids, seed)
		if !reflect.DeepEqual(sortedCopy(got), sortedCopy(orig)) {
			t.Errorf("seed %d: not a permutation: %v", seed, got)
		}
		if again := Shuffle(ids, seed); !reflect.DeepEqual(got, again) {
			t.Errorf("seed %d: not deterministic: %v vs %v", seed, got, again)
		}
	}
	if !reflect.DeepEqual(ids, orig) {
		t.Errorf("Shuffle mutated its input: %v", ids)
	}
}

func TestShuffle_DifferentSeedsGiveDifferentOrders(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	seen := map[string]bool{}
	for seed := int64(1); seed <= 10; seed++ {
		seen[fmt.Sprint(Shuffle(ids, seed))] = true
	}
	if len(seen) < 2 {
		t.Errorf("10 seeds produced %d distinct orders, want at least 2", len(seen))
	}
	// over a few seeds, at least one result differs from the input order
	changed := false
	for seed := int64(1); seed <= 10; seed++ {
		if !reflect.DeepEqual(Shuffle(ids, seed), ids) {
			changed = true
		}
	}
	if !changed {
		t.Errorf("Shuffle returned the input order for every seed")
	}
}

func TestShuffle_SmallInputs(t *testing.T) {
	if got := Shuffle(nil, 5); len(got) != 0 {
		t.Errorf("Shuffle(nil) = %v, want empty", got)
	}
	if got := Shuffle([]string{}, 5); len(got) != 0 {
		t.Errorf("Shuffle(empty) = %v, want empty", got)
	}
	if got := Shuffle([]string{"only"}, 5); !reflect.DeepEqual(got, []string{"only"}) {
		t.Errorf("Shuffle(single) = %v", got)
	}
}
