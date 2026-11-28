package keyfind

// The title half of the palette's ticket finder (ux-spec §15.3): a typed query is split into words and every word must
// match the ticket's summary, in any order. Case-insensitive, per rune (so a position never shifts and the ink indexes
// are indexes into the summary's runes, which is how the frontend slices a string). Stdlib only; no I/O.
//
// A word matches the summary as, best first:
//  1. a whole word    ("card" in "Recipe card crops")             4000
//  2. a word start    ("rec" in "Recipe card crops")              3000
//  3. a substring     ("cip" in "Recipe card crops")              2000
//  4. an in-order subsequence, words of 3+ runes only ("rcd")     1..999, fzf-style: a bonus for contiguous runs and
//     for the start of a word, a penalty for gaps; the matched runes must lie within 3×len+3 runes of each other.
//
// A ticket's score is the sum over the query's words. Words are matched independently, so they may overlap.

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Title is the tier name of a title hit (the same string as view.TierTitle; keyfind imports nothing).
const Title = "title"

const (
	minTitleRunes   = 2  // non-space runes a title query needs
	maxTitleWords   = 8  // words beyond this are ignored
	maxTitleWordLen = 64 // runes of a word beyond this are ignored
	minSubseqLen    = 3  // shorter words must match contiguously

	scoreWhole  = 4000
	scoreStart  = 3000
	scoreSubstr = 2000

	fzfMatch   = 16
	fzfBoundry = 8
	fzfConsec  = 8
	fzfGapOpen = 3
	fzfGapMore = 1
	negInf     = -1 << 30

	// A subsequence's first and last matched rune lie within maxSpanPer*len(word)+maxSpanPlus runes of each other.
	maxSpanPer  = 3
	maxSpanPlus = 3
)

// TitleQuery is a normalised title query: lower-cased words.
type TitleQuery struct {
	words    [][]rune
	hasDigit []bool
}

// Words is the number of words in the query.
func (q TitleQuery) Words() int { return len(q.words) }

// ParseTitle normalises a typed query for title matching. ok is false (no title matching) when the query has fewer
// than two non-space characters or no letter or digit at all ("--"). Numbers search titles too ("404", "v2"); a word with a digit only matches as a whole
// word, a word start or a substring, never as scattered digits (see word).
func ParseTitle(s string) (q TitleQuery, ok bool) {
	count, alnum := 0, false
	for _, f := range strings.Fields(s) {
		if len(q.words) == maxTitleWords {
			break
		}
		w := make([]rune, 0, len(f))
		digit := false
		for _, r := range f {
			if len(w) == maxTitleWordLen {
				break
			}
			if unicode.IsDigit(r) {
				alnum, digit = true, true
			} else if unicode.IsLetter(r) {
				alnum = true
			}
			w = append(w, unicode.ToLower(r))
		}
		count += len(w)
		q.words = append(q.words, w)
		q.hasDigit = append(q.hasDigit, digit)
	}
	if count < minTitleRunes || !alnum {
		return TitleQuery{}, false
	}
	return q, true
}

// TitleMatcher scores summaries against one query. It owns scratch buffers, so it is not safe for concurrent use; make
// one per call.
type TitleMatcher struct {
	q    TitleQuery
	buf  []rune
	m    []int   // DP scores, row j at m[j*n:(j+1)*n]
	from []int32 // DP predecessors, same layout
	path []int   // scratch: the matched indexes of the best subsequence, last first
}

// NewTitleMatcher returns a matcher for q.
func NewTitleMatcher(q TitleQuery) *TitleMatcher { return &TitleMatcher{q: q} }

// Score reports whether summary matches every word of the query, and the score (higher is better). It does not
// allocate once its buffers have grown to the longest summary.
func (t *TitleMatcher) Score(summary []byte) (score int, ok bool) {
	t.buf = foldBytes(t.buf[:0], summary)
	return t.ScoreFolded(t.buf)
}

// ScoreFolded is Score for a summary already folded with Fold (an index keeps them folded).
func (t *TitleMatcher) ScoreFolded(s []rune) (score int, ok bool) {
	for i, w := range t.q.words {
		ws, ok := t.word(w, t.q.hasDigit[i], s, nil)
		if !ok {
			return 0, false
		}
		score += ws
	}
	return score, true
}

// Fold appends the case-folded runes of s to dst (one rune per rune of s, so indexes stay put).
func Fold(dst []rune, s string) []rune {
	for _, r := range s {
		dst = append(dst, unicode.ToLower(r))
	}
	return dst
}

// FoldBytes is Fold for UTF-8 bytes (an invalid byte folds to U+FFFD, as ranging over a string does).
func FoldBytes(dst []rune, b []byte) []rune { return foldBytes(dst, b) }

// Indexes are the rune indexes of summary to ink, ascending and unique ("Recipe card" for "rcd": [0 7 10]). nil when
// the summary does not match.
func (t *TitleMatcher) Indexes(summary string) []int {
	t.buf = Fold(t.buf[:0], summary)
	mark := make([]bool, len(t.buf))
	for i, w := range t.q.words {
		var idx []int
		if _, ok := t.word(w, t.q.hasDigit[i], t.buf, &idx); !ok {
			return nil
		}
		for _, i := range idx {
			mark[i] = true
		}
	}
	out := []int{}
	for i, m := range mark {
		if m {
			out = append(out, i)
		}
	}
	return out
}

func foldBytes(dst []rune, b []byte) []rune {
	for i := 0; i < len(b); {
		if c := b[i]; c < utf8.RuneSelf {
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			dst = append(dst, rune(c))
			i++
			continue
		}
		r, w := utf8.DecodeRune(b[i:])
		dst = append(dst, unicode.ToLower(r))
		i += w
	}
	return dst
}

func isWordRune(r rune) bool {
	if r < 128 {
		return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z')
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func boundary(s []rune, i int) bool { return i == 0 || !isWordRune(s[i-1]) }

// word scores one query word against the folded summary s. When idx is not nil the matched rune indexes are appended.
func (t *TitleMatcher) word(w []rune, digit bool, s []rune, idx *[]int) (int, bool) {
	m, n := len(w), len(s)
	if m == 0 || m > n {
		return 0, false
	}
	best, at := 0, -1
	for p := 0; p+m <= n; p++ {
		if s[p] != w[0] {
			continue
		}
		j := 1
		for j < m && s[p+j] == w[j] {
			j++
		}
		if j < m {
			continue
		}
		class := scoreSubstr
		if boundary(s, p) {
			class = scoreStart
			if p+m == n || !isWordRune(s[p+m]) {
				class = scoreWhole
			}
		}
		if class > best {
			best, at = class, p
			if class == scoreWhole {
				break
			}
		}
	}
	if at >= 0 {
		if idx != nil {
			for i := 0; i < m; i++ {
				*idx = append(*idx, at+i)
			}
		}
		return best, true
	}
	// No subsequence for short words or for words with a digit: "1429" must not match digits spread across a title.
	if m < minSubseqLen || digit || !isSubsequence(w, s) {
		return 0, false
	}
	sc := t.subseq(w, s, idx)
	if sc < 0 {
		return 0, false
	}
	if sc > 998 {
		sc = 998
	}
	return 1 + sc, true
}

func isSubsequence(w, s []rune) bool {
	j := 0
	for _, r := range s {
		if r == w[j] {
			j++
			if j == len(w) {
				return true
			}
		}
	}
	return false
}

// subseq is the fzf-style best in-order subsequence of w in s (which contains it): a match scores 16, plus 8 on the
// start of a word; a match right after the previous one adds 8; a gap costs 3 for its first rune and 1 for each more.
// Matched indexes are appended to idx when it is not nil. It returns -1 when the best match is too spread out.
func (t *TitleMatcher) subseq(w, s []rune, idx *[]int) int {
	m, n := len(w), len(s)
	if cap(t.m) < m*n {
		t.m = make([]int, m*n)
		t.from = make([]int32, m*n)
	}
	t.m, t.from = t.m[:m*n], t.from[:m*n]
	bonus := func(i int) int {
		if boundary(s, i) {
			return fzfBoundry
		}
		return 0
	}
	for i := 0; i < n; i++ {
		if s[i] == w[0] {
			t.m[i] = fzfMatch + bonus(i)
		} else {
			t.m[i] = negInf
		}
		t.from[i] = -1
	}
	for j := 1; j < m; j++ {
		row, prev := t.m[j*n:(j+1)*n], t.m[(j-1)*n:j*n]
		from := t.from[j*n : (j+1)*n]
		gap, gapAt := negInf, -1 // best prev[i'] - gap cost over i' <= i-2
		for i := 0; i < n; i++ {
			if i >= 2 {
				if gap > negInf {
					gap -= fzfGapMore
				}
				if v := prev[i-2]; v > negInf && v-fzfGapOpen > gap {
					gap, gapAt = v-fzfGapOpen, i-2
				}
			}
			row[i], from[i] = negInf, -1
			if s[i] != w[j] || i == 0 {
				continue
			}
			pick, at := negInf, -1
			if v := prev[i-1]; v > negInf {
				pick, at = v+fzfConsec, i-1
			}
			if gap > pick {
				pick, at = gap, gapAt
			}
			if at < 0 {
				continue
			}
			row[i], from[i] = pick+fzfMatch+bonus(i), int32(at)
		}
	}
	last := t.m[(m-1)*n : m*n]
	bi, bs := -1, negInf
	for i, v := range last {
		if v > bs {
			bi, bs = i, v
		}
	}
	if bi < 0 {
		return -1
	}
	// Walk the best match back to its first rune: the matched runes must lie close together, or any three letters
	// would "match" any long summary ("mob" in "Meal plan templates for busy weeks").
	t.path = t.path[:0]
	for j, i := m-1, bi; j >= 0; j-- {
		t.path = append(t.path, i)
		i = int(t.from[j*n+i])
	}
	if span := t.path[0] - t.path[m-1] + 1; span > maxSpanPer*m+maxSpanPlus {
		return -1
	}
	if idx != nil {
		for j := m - 1; j >= 0; j-- {
			*idx = append(*idx, t.path[j])
		}
	}
	return bs
}
