package view

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/crueber/cuisle/internal/model"
)

// Initials returns up to two upper-case initials ("Iris Calloway" → "PR", "Cher" → "C"): the first rune of the first
// word and of the last word, rune-aware. Bracketed parts and words that don't start with a letter are ignored, so
// "Sebastian (Contractor)" → "S" and "Ana Lima - Ops" → "AL". Blank input gives "".
func Initials(name string) string {
	words := nameWords(name)
	if len(words) == 0 {
		return ""
	}
	first := upperFirstRune(words[0])
	if len(words) == 1 {
		return first
	}
	return first + upperFirstRune(words[len(words)-1])
}

// nameWords is name without (…), […] and {…} parts, split into words that start with a letter. A standalone
// separator ("-", "–", "—", "|", "/") ends the name: what follows is a suffix such as a team or company.
func nameWords(name string) []string {
	var b strings.Builder
	depth := 0
	for _, r := range name {
		switch r {
		case '(', '[', '{':
			depth++
			continue
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 {
			b.WriteRune(r)
		}
	}
	var out []string
	for _, w := range strings.Fields(b.String()) {
		if w == "-" || w == "–" || w == "—" || w == "|" || w == "/" {
			break
		}
		if r, _ := utf8.DecodeRuneInString(w); unicode.IsLetter(r) {
			out = append(out, w)
		}
	}
	return out
}

func upperFirstRune(w string) string {
	r, _ := utf8.DecodeRuneInString(w)
	return string(unicode.ToUpper(r))
}

// LaneLabel returns the display label for a lane id: todo "To Do", prog "In Progress", review "In Review", qa "QA",
// done "Done", backlog "Backlog", hidden "Hidden"; unknown ids are returned unchanged.
// A standup's configured labels (renamed and custom lanes) come from model.EffectiveConfig.LaneLabel.
func LaneLabel(lane string) string {
	if d := model.DefaultLaneLabel(lane); d != "" {
		return d
	}
	return lane
}
