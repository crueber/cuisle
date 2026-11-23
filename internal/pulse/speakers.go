package pulse

import (
	"fmt"
	"hash/fnv"
	"sort"

	"github.com/crueber/cuisle/internal/model"
)

// SpeakerOrder returns the effective speaking order (data-model.md §2.3): included speakers by position (Fisher–Yates
// with Seed when ReshuffleDaily), Unassigned first/last when it has tickets (skip → appended with Included=false), then
// excluded speakers who have tickets (Included=false).
func SpeakerOrder(in SpeakerInput) []SpeakerSlot {
	speakers := append([]model.SpeakerConfig(nil), in.Speakers...)
	sort.SliceStable(speakers, func(i, j int) bool { return speakers[i].Position < speakers[j].Position })

	var included, excluded []string
	for _, s := range speakers {
		if s.Included {
			included = append(included, s.AccountID)
		} else if in.WithTickets[s.AccountID] {
			excluded = append(excluded, s.AccountID)
		}
	}
	if in.ReshuffleDaily {
		included = Shuffle(included, in.Seed)
	}

	var out []SpeakerSlot
	pos := in.UnassignedPosition
	if in.HasUnassigned && pos == "first" {
		out = append(out, SpeakerSlot{ID: model.UnassignedID, Included: true, Unassigned: true})
	}
	for _, id := range included {
		out = append(out, SpeakerSlot{ID: id, Included: true})
	}
	if in.HasUnassigned && pos != "first" {
		out = append(out, SpeakerSlot{ID: model.UnassignedID, Included: pos != "skip", Unassigned: true})
	}
	for _, id := range excluded {
		out = append(out, SpeakerSlot{ID: id})
	}
	return out
}

// DailySeed = int64(fnv64a("<standupID>|<date>")).
func DailySeed(standupID int64, date string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(fmt.Sprintf("%d|%s", standupID, date)))
	return int64(h.Sum64())
}

// Shuffle returns a Fisher–Yates permutation of ids driven by a deterministic PRNG seeded with seed.
func Shuffle(ids []string, seed int64) []string {
	out := append([]string(nil), ids...)
	state := uint64(seed)
	next := func() uint64 {
		// splitmix64
		state += 0x9e3779b97f4a7c15
		z := state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		return z ^ (z >> 31)
	}
	for i := len(out) - 1; i > 0; i-- {
		j := int(next() % uint64(i+1))
		out[i], out[j] = out[j], out[i]
	}
	return out
}
