// Package keyfind is the pure matching of the palette's ticket finder (ux-spec §15.3, F14): it normalises a typed query
// into a project-prefix part and a digits part and classifies an issue key against it as exact, starts-with or close
// (one digit off, or two adjacent digits swapped). The title half (words matched fuzzily against a summary) is in
// title.go. Stdlib only; no I/O.
package keyfind

import (
	"strconv"
	"strings"
)

// Tiers (the same strings as view.TierExact, view.TierPrefix and view.TierClose; keyfind imports nothing).
const (
	Exact  = "exact"
	Prefix = "prefix"
	Close  = "close"
)

// Query is a normalised finder query.
type Query struct {
	Letters string // lowercase prefix of the project key; "" = any project
	Digits  string // the issue-number part; never empty in a valid query
}

// Parse normalises a typed query: lowercase, spaces and '-' dropped, then split into the letters part and the digits
// part ("mob-1429" → mob + 1429, "m14" → m + 14, "14 29" → "" + 1429). ok is false when there are no digits or the
// text is not letters followed by digits (a query without digits finds no tickets).
func Parse(s string) (q Query, ok bool) {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r == ' ' || r == '-' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	n := b.String()
	i := 0
	for i < len(n) && (n[i] < '0' || n[i] > '9') {
		i++
	}
	q.Letters, q.Digits = n[:i], n[i:]
	if q.Digits == "" {
		return Query{}, false
	}
	for j := 0; j < len(q.Digits); j++ {
		if q.Digits[j] < '0' || q.Digits[j] > '9' {
			return Query{}, false
		}
	}
	return q, true
}

// SplitKey splits "MOB-1429" into "MOB" and "1429". ok is false when the key has no '-' or a non-numeric number.
func SplitKey(key string) (project, number string, ok bool) {
	i := strings.LastIndexByte(key, '-')
	if i <= 0 || i == len(key)-1 {
		return "", "", false
	}
	number = key[i+1:]
	for j := 0; j < len(number); j++ {
		if number[j] < '0' || number[j] > '9' {
			return "", "", false
		}
	}
	return key[:i], number, true
}

// Number is the numeric value of the key's number part (0 when the key is malformed). Used for ordering.
func Number(key string) int {
	_, n, ok := SplitKey(key)
	if !ok {
		return 0
	}
	v, _ := strconv.Atoi(n)
	return v
}

// Tier reports how key matches q without allocating: the tier, or ok=false. Classify adds the ink indexes.
func Tier(q Query, key string) (tier string, ok bool) {
	i := strings.LastIndexByte(key, '-')
	if i <= 0 || i == len(key)-1 || q.Digits == "" {
		return "", false
	}
	project, number := key[:i], key[i+1:]
	for j := 0; j < len(number); j++ {
		if number[j] < '0' || number[j] > '9' {
			return "", false
		}
	}
	if q.Letters != "" && !hasPrefixFold(project, q.Letters) {
		return "", false
	}
	switch {
	case number == q.Digits:
		return Exact, true
	case strings.HasPrefix(number, q.Digits):
		return Prefix, true
	case len(number) == len(q.Digits) && len(q.Digits) >= 2 && oneOff(number, q.Digits):
		return Close, true
	}
	return "", false
}

// hasPrefixFold: s starts with the lowercase prefix p, ignoring ASCII case in s.
func hasPrefixFold(s, p string) bool {
	if len(s) < len(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != p[i] {
			return false
		}
	}
	return true
}

// Classify reports how key matches q. match is the character indexes of key to ink: the letters prefix (when the query
// has one) and the matched digits; for a close match the digits that equal the query's at the same position (the
// differing digit stays plain).
func Classify(q Query, key string) (tier string, match []int, ok bool) {
	tier, ok = Tier(q, key)
	if !ok {
		return "", nil, false
	}
	project, number, _ := SplitKey(key)
	base := len(project) + 1
	match = make([]int, 0, len(q.Letters)+len(q.Digits))
	for i := 0; i < len(q.Letters); i++ {
		match = append(match, i)
	}
	for i := 0; i < len(q.Digits); i++ {
		if tier == Close && number[i] != q.Digits[i] {
			continue
		}
		match = append(match, base+i)
	}
	return tier, match, true
}

// oneOff: a and b (equal length) differ in exactly one position, or in exactly two adjacent positions that are swapped.
func oneOff(a, b string) bool {
	first, diffs := -1, 0
	last := -1
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			if first < 0 {
				first = i
			}
			last = i
			diffs++
		}
	}
	switch diffs {
	case 1:
		return true
	case 2:
		return last == first+1 && a[first] == b[last] && a[last] == b[first]
	}
	return false
}
