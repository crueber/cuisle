package adf_test

// Tests for adf.PlainText and adf.FromPlainText (WP-T1). Spec: architecture §2.4, jira-sync §2.15.
//
// SPEC-NOTE (PlainText separators): the text of each leaf block (paragraph, heading, code block, task/decision item,
// list item paragraph) is joined to the next with a single "\n", with no trailing newline; hardBreak is "\n"; marks and
// list markers are ignored; mention = its authored attrs.text (e.g. "@Silas M"; PlainText has no Resolver); emoji = its
// glyph (attrs.text), else its shortName; status = its text; inlineCard/blockCard = its url; an unknown node
// contributes its descendant text; malformed raw is returned verbatim; an empty document is "". Tables, media and
// dates are unspecified and not tested. Empty paragraphs are not tested.
// SPEC-NOTE (FromPlainText): output is {"version":1,"type":"doc","content":[...]} with Format "adf". Paragraphs are
// split on blank lines (any run of blank lines is one separator, no empty paragraphs); "\r\n" is treated as "\n";
// single newlines become hardBreak; leading/trailing newlines of the whole text are dropped (no leading/trailing
// hardBreak); empty or blank-lines-only text gives "content":[]. Text nodes carry no marks. A mention range
// [Start,End) of RUNE offsets into NewComment.Text is replaced by {"type":"mention","attrs":{"id":AccountID,"text":"@"+Name}};
// ranges that are out of bounds, empty/inverted or overlap an earlier (by Start) range are ignored and their text stays
// plain; mentions may be given unsorted.

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/crueber/cuisle/internal/adf"
	"github.com/crueber/cuisle/internal/model"
)

func TestPlainText(t *testing.T) {
	doc := func(content string) string {
		return `{"version":1,"type":"doc","content":[` + content + `]}`
	}
	para := func(inline string) string { return `{"type":"paragraph","content":[` + inline + `]}` }
	txt := func(s string) string { return `{"type":"text","text":` + quote(s) + `}` }
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"one paragraph", doc(para(txt("Hello world"))), "Hello world"},
		{"paragraphs are separated by one newline", doc(para(txt("One")) + `,` + para(txt("Two"))), "One\nTwo"},
		{"hardBreak is a newline", doc(para(txt("a") + `,{"type":"hardBreak"},` + txt("b"))), "a\nb"},
		{"heading then paragraph", doc(`{"type":"heading","attrs":{"level":2},"content":[` + txt("Title") + `]},` + para(txt("Body"))), "Title\nBody"},
		{"marks are ignored", doc(para(`{"type":"text","text":"bold","marks":[{"type":"strong"}]},` + txt(" plain"))), "bold plain"},
		{"nested bullet list: one line per item, no markers",
			doc(`{"type":"bulletList","content":[
				{"type":"listItem","content":[` + para(txt("x")) + `,{"type":"bulletList","content":[{"type":"listItem","content":[` + para(txt("y")) + `]}]}]},
				{"type":"listItem","content":[` + para(txt("z")) + `]}]}`), "x\ny\nz"},
		{"task list", doc(`{"type":"taskList","content":[{"type":"taskItem","attrs":{"state":"DONE"},"content":[` + txt("a") + `]},{"type":"taskItem","attrs":{"state":"TODO"},"content":[` + txt("b") + `]}]}`), "a\nb"},
		{"blockquote", doc(`{"type":"blockquote","content":[` + para(txt("q")) + `]}`), "q"},
		{"code block keeps its lines", doc(`{"type":"codeBlock","content":[` + txt("a\nb") + `]}`), "a\nb"},
		{"inline nodes use their text", doc(para(
			txt("Ping ") + `,{"type":"mention","attrs":{"id":"silas","text":"@Silas M"}},` + txt(" ") +
				`,{"type":"emoji","attrs":{"shortName":":cupcake:","text":"🧁"}},` + txt(" ") +
				`,{"type":"status","attrs":{"text":"DONE","color":"green"}},` + txt(" ") +
				`,{"type":"inlineCard","attrs":{"url":"https://example.atlassian.net/browse/MOB-1200"}}`)),
			"Ping @Silas M 🧁 DONE https://example.atlassian.net/browse/MOB-1200"},
		{"emoji without a glyph uses its shortName", doc(para(`{"type":"emoji","attrs":{"shortName":":wave:"}}`)), ":wave:"},
		{"unknown node contributes its text", doc(`{"type":"bodiedExtension","content":[` + para(txt("Loom")) + `]}`), "Loom"},
		{"empty document", doc(``), ""},
		{"malformed raw is returned verbatim", `{oops`, "{oops"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := adf.PlainText(rich(tc.raw)); got != tc.want {
				t.Errorf("PlainText = %q, want %q", got, tc.want)
			}
		})
	}
}

// quote JSON-encodes a string.
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ---- FromPlainText ----

// Expected-ADF builders (maps, compared canonically).
type obj = map[string]any

func adfDoc(content ...obj) obj {
	c := make([]any, len(content))
	for i, n := range content {
		c[i] = n
	}
	return obj{"version": 1, "type": "doc", "content": c}
}
func adfPara(inline ...obj) obj {
	c := make([]any, len(inline))
	for i, n := range inline {
		c[i] = n
	}
	return obj{"type": "paragraph", "content": c}
}
func adfText(s string) obj { return obj{"type": "text", "text": s} }

var adfBreak = obj{"type": "hardBreak"}

func adfMention(id, name string) obj {
	return obj{"type": "mention", "attrs": obj{"id": id, "text": "@" + name}}
}

// at builds a mention over the first occurrence of sub in text, using RUNE offsets.
func at(t *testing.T, text, sub, id, name string) model.Mention {
	t.Helper()
	i := strings.Index(text, sub)
	if i < 0 {
		t.Fatalf("%q not found in %q", sub, text)
	}
	start := utf8.RuneCountInString(text[:i])
	return model.Mention{Start: start, End: start + utf8.RuneCountInString(sub), AccountID: id, Name: name}
}

func TestFromPlainText(t *testing.T) {
	const (
		mixed     = "héllo 🧁 日本 @Dev User, 🧁!"
		mid       = "hi @Silas Marchetti ok"
		multiline = "first\n\nline @Ann\nthen @Bob end"
	)
	tests := []struct {
		name     string
		text     string
		mentions func(t *testing.T) []model.Mention
		want     obj
	}{
		{"one line", "hello", nil, adfDoc(adfPara(adfText("hello")))},
		{"blank line makes a new paragraph", "a\n\nb", nil, adfDoc(adfPara(adfText("a")), adfPara(adfText("b")))},
		{"single newline is a hardBreak", "a\nb", nil, adfDoc(adfPara(adfText("a"), adfBreak, adfText("b")))},
		{"a run of blank lines is one separator", "a\n\n\n\nb", nil, adfDoc(adfPara(adfText("a")), adfPara(adfText("b")))},
		{"CRLF is normalised", "a\r\nb\r\n\r\nc", nil, adfDoc(adfPara(adfText("a"), adfBreak, adfText("b")), adfPara(adfText("c")))},
		{"trailing newline adds nothing", "a\n", nil, adfDoc(adfPara(adfText("a")))},
		{"leading newlines add nothing", "\n\na", nil, adfDoc(adfPara(adfText("a")))},
		{"empty text has no paragraphs", "", nil, adfDoc()},
		{"blank lines only has no paragraphs", "\n\n", nil, adfDoc()},
		{"markup characters stay literal", `<b>"x" & y</b>`, nil, adfDoc(adfPara(adfText(`<b>"x" & y</b>`)))},
		{"mention in the middle", mid, func(t *testing.T) []model.Mention {
			return []model.Mention{at(t, mid, "@Silas Marchetti", "silas", "Silas Marchetti")}
		}, adfDoc(adfPara(adfText("hi "), adfMention("silas", "Silas Marchetti"), adfText(" ok")))},
		{"mention at the start", "@Silas Marchetti thanks", func(t *testing.T) []model.Mention {
			return []model.Mention{at(t, "@Silas Marchetti thanks", "@Silas Marchetti", "silas", "Silas Marchetti")}
		}, adfDoc(adfPara(adfMention("silas", "Silas Marchetti"), adfText(" thanks")))},
		{"mention at the end", "thanks @Silas Marchetti", func(t *testing.T) []model.Mention {
			return []model.Mention{at(t, "thanks @Silas Marchetti", "@Silas Marchetti", "silas", "Silas Marchetti")}
		}, adfDoc(adfPara(adfText("thanks "), adfMention("silas", "Silas Marchetti")))},
		{"a mention and nothing else", "@Dev User", func(t *testing.T) []model.Mention {
			return []model.Mention{at(t, "@Dev User", "@Dev User", "me", "Dev User")}
		}, adfDoc(adfPara(adfMention("me", "Dev User")))},
		{"adjacent mentions", "@Ann@Bob", func(t *testing.T) []model.Mention {
			return []model.Mention{at(t, "@Ann@Bob", "@Ann", "a1", "Ann"), at(t, "@Ann@Bob", "@Bob", "b2", "Bob")}
		}, adfDoc(adfPara(adfMention("a1", "Ann"), adfMention("b2", "Bob")))},
		{"offsets are runes, not bytes or UTF-16 units", mixed, func(t *testing.T) []model.Mention {
			return []model.Mention{at(t, mixed, "@Dev User", "me", "Dev User")}
		}, adfDoc(adfPara(adfText("héllo 🧁 日本 "), adfMention("me", "Dev User"), adfText(", 🧁!")))},
		{"offsets count the whole text, across paragraphs and hard breaks", multiline, func(t *testing.T) []model.Mention {
			return []model.Mention{at(t, multiline, "@Ann", "a1", "Ann"), at(t, multiline, "@Bob", "b2", "Bob")}
		}, adfDoc(adfPara(adfText("first")),
			adfPara(adfText("line "), adfMention("a1", "Ann"), adfBreak, adfText("then "), adfMention("b2", "Bob"), adfText(" end")))},
		{"mentions given out of order are applied by position", "@Ann and @Bob", func(t *testing.T) []model.Mention {
			return []model.Mention{at(t, "@Ann and @Bob", "@Bob", "b2", "Bob"), at(t, "@Ann and @Bob", "@Ann", "a1", "Ann")}
		}, adfDoc(adfPara(adfMention("a1", "Ann"), adfText(" and "), adfMention("b2", "Bob")))},
		{"a range past the end of the text is ignored", "hello @Ann world", func(t *testing.T) []model.Mention {
			return []model.Mention{{Start: 6, End: 99, AccountID: "a1", Name: "Ann"}}
		}, adfDoc(adfPara(adfText("hello @Ann world")))},
		{"an empty or inverted range is ignored", "hello @Ann world", func(t *testing.T) []model.Mention {
			return []model.Mention{{Start: 6, End: 6, AccountID: "a1", Name: "Ann"}, {Start: 10, End: 7, AccountID: "a1", Name: "Ann"}}
		}, adfDoc(adfPara(adfText("hello @Ann world")))},
		{"an overlapping range is ignored", "@Ann Lee x", func(t *testing.T) []model.Mention {
			return []model.Mention{{Start: 0, End: 8, AccountID: "a1", Name: "Ann Lee"}, {Start: 4, End: 8, AccountID: "l2", Name: "Lee"}}
		}, adfDoc(adfPara(adfMention("a1", "Ann Lee"), adfText(" x")))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := model.NewComment{Text: tc.text}
			if tc.mentions != nil {
				c.Mentions = tc.mentions(t)
			}
			got := adf.FromPlainText(c)
			if got.Format != model.RichTextADF {
				t.Errorf("Format = %q, want %q", got.Format, model.RichTextADF)
			}
			if g, w := canon(t, got.Raw), canon(t, tc.want); g != w {
				t.Errorf("FromPlainText(%q)\n--- got ---\n%s\n--- want ---\n%s", tc.text, g, w)
			}
		})
	}
}

// ---- round trip: Parse(FromPlainText(x)) ----

func TestFromPlainText_RoundTripsThroughParse(t *testing.T) {
	txt := func(s string) adf.Node { return adf.Node{T: adf.NodeText, Text: s} }
	para := func(c ...adf.Node) adf.Node { return adf.Node{T: adf.NodeParagraph, C: c} }
	br := adf.Node{T: adf.NodeHardBreak}
	mention := func(id, label string, self bool) adf.Node {
		return adf.Node{T: adf.NodeMention, A: &adf.Attrs{AccountID: id, Label: label, Self: self}}
	}
	const withMentions = "hi @Silas Marchetti,\nping @Dev User 🧁"
	tests := []struct {
		name     string
		text     string
		mentions func(t *testing.T) []model.Mention
		want     []adf.Node
	}{
		{"paragraphs", "a\n\nb", nil, []adf.Node{para(txt("a")), para(txt("b"))}},
		{"hard break", "a\nb", nil, []adf.Node{para(txt("a"), br, txt("b"))}},
		{"mentions resolve their names, self flagged", withMentions, func(t *testing.T) []model.Mention {
			return []model.Mention{at(t, withMentions, "@Silas Marchetti", "silas", "Silas Marchetti"), at(t, withMentions, "@Dev User", "me", "Dev User")}
		}, []adf.Node{para(txt("hi "), mention("silas", "Silas Marchetti", false), txt(","), br, txt("ping "), mention("me", "Dev User", true), txt(" 🧁"))}},
		{"empty text is an empty document", "", nil, []adf.Node{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := model.NewComment{Text: tc.text}
			if tc.mentions != nil {
				c.Mentions = tc.mentions(t)
			}
			got := adf.Parse(adf.FromPlainText(c), newResolver())
			want := adf.Doc{Nodes: tc.want}
			if g, w := canon(t, got), canon(t, want); g != w {
				t.Errorf("Parse(FromPlainText(%q))\n--- got ---\n%s\n--- want ---\n%s", tc.text, g, w)
			}
		})
	}
}
