package adf

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/crueber/cuisle/internal/model"
)

// PlainText returns the document's text content (aria labels, tests). Each leaf block is one line; malformed raw is
// returned verbatim.
func PlainText(raw model.RichText) string {
	nodes, ok := decodeRoot(raw)
	if !ok {
		return rawText(raw)
	}
	return strings.Join(blockLines(nodes), "\n")
}

// blockLines returns one line per leaf block under ns.
func blockLines(ns []rawNode) []string {
	var lines []string
	for _, n := range ns {
		lines = append(lines, blockLinesOf(n)...)
	}
	return lines
}

func blockLinesOf(n rawNode) []string {
	switch n.Type {
	case "paragraph", "heading", "codeBlock", "taskItem", "decisionItem":
		return []string{inlineOf(n)}
	case "bulletList", "orderedList", "listItem", "taskList", "decisionList", "blockquote", "panel", "table",
		"tableRow", "tableHeader", "tableCell", "expand", "nestedExpand", "mediaGroup":
		return blockLines(n.Content)
	case "rule":
		return nil
	}
	// Unknown and inline-only nodes: their text, as one line.
	if s := inlineOf(n); s != "" {
		return []string{s}
	}
	return nil
}

// inlineOf returns the text of an inline subtree: hardBreak is a newline, mentions and statuses their text, emoji
// their glyph (else shortName), cards their URL, and anything else its children's text.
func inlineOf(n rawNode) string {
	switch n.Type {
	case "text":
		return n.Text
	case "hardBreak":
		return "\n"
	case "mention", "status":
		return attrStr(n.Attrs, "text")
	case "emoji":
		if g := attrStr(n.Attrs, "text"); g != "" {
			return g
		}
		return attrStr(n.Attrs, "shortName")
	case "inlineCard", "blockCard":
		return attrStr(n.Attrs, "url")
	}
	var sb strings.Builder
	for _, c := range n.Content {
		sb.WriteString(inlineOf(c))
	}
	return sb.String()
}

// FromPlainText builds the ADF for a composer comment: blank-line-separated paragraphs, single newlines → hardBreak,
// mention ranges → {"type":"mention","attrs":{"id":"<accountId>","text":"@Name"}} (jira-sync.md §2.15).
func FromPlainText(c model.NewComment) model.RichText {
	runes := []rune(c.Text)
	mentions := validMentions(c.Mentions, len(runes))

	var paras []any
	var group [][]any // the non-blank lines of the paragraph being built
	flush := func() {
		if len(group) == 0 {
			return
		}
		content := []any{}
		for i, line := range group {
			if i > 0 {
				content = append(content, map[string]any{"type": "hardBreak"})
			}
			content = append(content, line...)
		}
		paras = append(paras, map[string]any{"type": "paragraph", "content": content})
		group = nil
	}
	for _, sp := range lineSpans(runes) {
		if sp.start == sp.end {
			flush()
			continue
		}
		group = append(group, lineInlines(runes, sp, mentions))
	}
	flush()

	if paras == nil {
		paras = []any{}
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "type": "doc", "content": paras})
	if err != nil {
		raw = []byte(`{"version":1,"type":"doc","content":[]}`)
	}
	return model.RichText{Format: model.RichTextADF, Raw: raw}
}

// span is a line as rune offsets [start, end) into the original text, without its line ending.
type span struct{ start, end int }

// lineSpans splits on \n, treating \r\n as one line ending. Offsets stay those of the original text.
func lineSpans(runes []rune) []span {
	var spans []span
	start := 0
	for i, r := range runes {
		if r == '\n' {
			spans = append(spans, trimCR(runes, start, i))
			start = i + 1
		}
	}
	return append(spans, trimCR(runes, start, len(runes)))
}

func trimCR(runes []rune, start, end int) span {
	if end > start && runes[end-1] == '\r' {
		end--
	}
	return span{start, end}
}

// validMentions keeps the ranges that are inside the text, non-empty and not overlapping an earlier one (by Start).
func validMentions(ms []model.Mention, n int) []model.Mention {
	sorted := append([]model.Mention(nil), ms...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	var out []model.Mention
	prevEnd := 0
	for _, m := range sorted {
		if m.Start < 0 || m.End <= m.Start || m.End > n || m.Start < prevEnd {
			continue
		}
		out = append(out, m)
		prevEnd = m.End
	}
	return out
}

// lineInlines returns the inline nodes of one line, with the mentions that lie wholly inside it.
func lineInlines(runes []rune, sp span, ms []model.Mention) []any {
	var out []any
	pos := sp.start
	for _, m := range ms {
		if m.Start < pos || m.End > sp.end {
			continue
		}
		if m.Start > pos {
			out = append(out, textNode(runes[pos:m.Start]))
		}
		out = append(out, map[string]any{
			"type":  "mention",
			"attrs": map[string]any{"id": m.AccountID, "text": "@" + m.Name},
		})
		pos = m.End
	}
	if pos < sp.end {
		out = append(out, textNode(runes[pos:sp.end]))
	}
	return out
}

func textNode(rs []rune) map[string]any {
	return map[string]any{"type": "text", "text": string(rs)}
}
