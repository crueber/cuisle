package keyfind_test

import (
	"reflect"
	"testing"

	"github.com/crueber/cuisle/internal/keyfind"
)

func TestParseTitle(t *testing.T) {
	for _, c := range []struct {
		q     string
		ok    bool
		words int
	}{
		{"recipe card", true, 2},
		{"  Recipe   CARD ", true, 2},
		{"ab", true, 1},
		{"a", false, 0},      // under two non-space characters
		{"a ", false, 0},     //
		{" ", false, 0},      //
		{"", false, 0},       //
		{"1429", true, 1},    // numbers search titles too (contiguously)
		{"14 29", true, 2},   //
		{"-", false, 0},      //
		{"--", false, 0},     // no letter or digit
		{"mob1429", true, 1}, // key tiers first, then titles
		{"v2", true, 1},
		{"1429 share", true, 2},
		{"寿司", true, 1},
		{"寿", false, 0},
	} {
		q, ok := keyfind.ParseTitle(c.q)
		if ok != c.ok || (ok && q.Words() != c.words) {
			t.Errorf("ParseTitle(%q) = %d words, %v; want %d, %v", c.q, q.Words(), ok, c.words, c.ok)
		}
	}
}

// match runs one query against one summary: the score, the ink indexes, and whether it matched.
func match(t *testing.T, query, summary string) (int, []int, bool) {
	t.Helper()
	q, ok := keyfind.ParseTitle(query)
	if !ok {
		t.Fatalf("ParseTitle(%q) not ok", query)
	}
	m := keyfind.NewTitleMatcher(q)
	sc, ok := m.Score([]byte(summary))
	idx := m.Indexes(summary)
	if ok != (idx != nil) {
		t.Errorf("%q in %q: Score ok=%v but Indexes=%v", query, summary, ok, idx)
	}
	return sc, idx, ok
}

func TestTitleMatchTable(t *testing.T) {
	const s = "Recipe card crops the hero photo"
	for _, c := range []struct {
		name, query, summary string
		ok                   bool
		idx                  []int // nil = not checked
	}{
		{"whole words", "recipe card", s, true, []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 10}},
		{"any order", "card recipe", s, true, []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 10}},
		{"partial words", "rec ca", s, true, []int{0, 1, 2, 7, 8}},
		{"case-insensitive", "RECIPE CARD", s, true, []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 10}},
		{"substring", "cip", s, true, []int{2, 3, 4}},
		{"subsequence picks word starts", "rcd", s, true, []int{0, 7, 10}},
		{"one word missing", "recipe photo cat zzz", s, false, nil},
		{"no match", "zebra", s, false, nil},
		{"a 2-rune word must be contiguous", "ce", "Recipe card", false, nil},
		{"a 3-rune word may be a subsequence", "rpe", "Recipe card", true, nil},
		{"a subsequence must be compact", "mob", "Meal plan templates for busy weeks", false, nil},
		{"a compact subsequence matches", "mpt", "Meal plan templates", true, []int{0, 5, 10}},
	} {
		_, idx, ok := match(t, c.query, c.summary)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v (idx %v)", c.name, ok, c.ok, idx)
			continue
		}
		if c.idx != nil && !reflect.DeepEqual(idx, c.idx) {
			t.Errorf("%s: idx = %v, want %v", c.name, idx, c.idx)
		}
	}
}

func TestTitleScoreOrder(t *testing.T) {
	score := func(query, summary string) int {
		t.Helper()
		sc, _, ok := match(t, query, summary)
		if !ok {
			t.Fatalf("%q does not match %q", query, summary)
		}
		return sc
	}
	// whole word > word start > substring > subsequence, for the same query
	whole := score("card", "Recipe card crops")
	start := score("card", "Cardinal photo crops")
	sub := score("card", "Discard the crops")
	seq := score("crd", "Recipe card crops")
	if !(whole > start && start > sub && sub > seq && seq > 0) {
		t.Errorf("scores whole %d, start %d, substring %d, subsequence %d are not in descending order", whole, start, sub, seq)
	}
	// two whole words beat one whole and one partial
	if a, b := score("recipe card", "Recipe card"), score("recipe card", "Recipes cardinal"); a <= b {
		t.Errorf("two whole words %d <= two word starts %d", a, b)
	}
	// a subsequence prefers word starts and contiguous runs over scattered letters
	if a, b := score("rcp", "Recipe card crops"), score("rcp", "xrxxcxxpx"); a <= b {
		t.Errorf("word-start subsequence %d <= scattered %d", a, b)
	}
}

func TestTitleIndexesAreRuneIndexes(t *testing.T) {
	// An emoji (one rune, two UTF-16 units) and accented letters before the match: the frontend walks [...summary].
	const s = "🍳 Crème brûlée recipe"
	_, idx, ok := match(t, "recipe", s)
	if !ok {
		t.Fatal("no match")
	}
	runes := []rune(s)
	if got := string(runes[idx[0] : idx[len(idx)-1]+1]); got != "recipe" || len(idx) != 6 {
		t.Errorf("idx %v ink %q, want the six runes of recipe", idx, got)
	}
	// case folding keeps positions
	_, idx, ok = match(t, "été", "Été à Paris")
	if !ok || !reflect.DeepEqual(idx, []int{0, 1, 2}) {
		t.Errorf("été: idx %v ok %v", idx, ok)
	}
	// subsequence over multi-byte runes: C(2) b(8)? computed below from the runes themselves
	_, idx, ok = match(t, "cbl", "🍳 Crème brûlée")
	if !ok || len(idx) != 3 {
		t.Fatalf("cbl: idx %v ok %v", idx, ok)
	}
	r := []rune("🍳 Crème brûlée")
	for i, want := range []rune{'c', 'b', 'l'} {
		if got := []rune(string(r[idx[i]]))[0]; got != want && got != want-('a'-'A') {
			t.Errorf("cbl: idx[%d]=%d is %q", i, idx[i], got)
		}
	}
	// CJK
	_, idx, ok = match(t, "寿司", "今日の寿司メニュー")
	if !ok || !reflect.DeepEqual(idx, []int{3, 4}) {
		t.Errorf("寿司: idx %v ok %v", idx, ok)
	}
}

func TestTitleScoreDoesNotAllocate(t *testing.T) {
	q, _ := keyfind.ParseTitle("recipe card")
	m := keyfind.NewTitleMatcher(q)
	summary := []byte("Swipe to check off items on the shopping list")
	m.Score(summary) // grow the buffers
	if n := testing.AllocsPerRun(100, func() { m.Score(summary) }); n != 0 {
		t.Errorf("Score allocates %v times per call", n)
	}
	q2, _ := keyfind.ParseTitle("swp lst")
	m2 := keyfind.NewTitleMatcher(q2)
	m2.Score(summary)
	if n := testing.AllocsPerRun(100, func() { m2.Score(summary) }); n != 0 {
		t.Errorf("Score (subsequence) allocates %v times per call", n)
	}
}

func TestTitleInvalidUTF8DoesNotPanic(t *testing.T) {
	q, _ := keyfind.ParseTitle("abc")
	m := keyfind.NewTitleMatcher(q)
	m.Score([]byte{0xff, 'a', 0xfe, 'b', 'c'})
}
