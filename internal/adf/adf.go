package adf

import (
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/model"
)

// Parse normalises a stored document into a Doc. It never fails: malformed input becomes one `unknown` node with the
// plain text; unknown nodes become {T:"unknown", Text:<text content>} and increment Doc.Unsupported.
func Parse(raw model.RichText, r Resolver) Doc {
	nodes, ok := decodeRoot(raw)
	if !ok {
		return Doc{Nodes: []Node{{T: NodeUnknown, Text: rawText(raw)}}, Unsupported: 1}
	}
	if r == nil {
		r = noResolver{}
	}
	p := &parser{r: r, nowDay: r.Now().Format("2006-01-02")}
	return Doc{Nodes: p.nodes(nodes), Unsupported: p.unsupported}
}

// noResolver is used when the caller passes no Resolver: nothing resolves, and dates are relative to the wall clock.
type noResolver struct{}

func (noResolver) Attachment(string, string) (model.Attachment, string, bool) {
	return model.Attachment{}, "", false
}
func (noResolver) Issue(string) (ResolvedIssue, bool) { return ResolvedIssue{}, false }
func (noResolver) Person(string) (string, bool)       { return "", false }
func (noResolver) Now() time.Time                     { return time.Now() }

type parser struct {
	r           Resolver
	nowDay      string // YYYY-MM-DD of Now() in its own location
	unsupported int
}

func (p *parser) nodes(ns []rawNode) []Node {
	out := make([]Node, 0, len(ns))
	for _, n := range ns {
		out = append(out, p.node(n)...)
	}
	return out
}

// node normalises one raw node. It returns a slice because a mediaGroup flattens into several file nodes.
func (p *parser) node(n rawNode) []Node {
	a := n.Attrs
	switch n.Type {
	case "paragraph":
		return one(Node{T: NodeParagraph, C: p.nodes(n.Content)})
	case "heading":
		return one(withAttrs(Node{T: NodeHeading, C: p.nodes(n.Content)}, Attrs{Level: attrInt(a, "level")}))
	case "text":
		return one(Node{T: NodeText, Text: n.Text, Marks: p.marks(n.Marks)})
	case "hardBreak":
		return one(Node{T: NodeHardBreak})
	case "bulletList":
		return one(Node{T: NodeBulletList, C: p.nodes(n.Content)})
	case "orderedList":
		return one(withAttrs(Node{T: NodeOrderedList, C: p.nodes(n.Content)}, Attrs{Order: attrInt(a, "order")}))
	case "listItem":
		return one(Node{T: NodeListItem, C: p.nodes(n.Content)})
	case "taskList":
		return one(Node{T: NodeTaskList, C: p.nodes(n.Content)})
	case "taskItem":
		return one(withAttrs(Node{T: NodeTaskItem, C: p.nodes(n.Content)}, Attrs{Done: attrStr(a, "state") == "DONE"}))
	case "decisionList":
		return one(Node{T: NodeDecision, C: p.decisionItems(n.Content)})
	case "blockquote":
		return one(Node{T: NodeQuote, C: p.nodes(n.Content)})
	case "rule":
		return one(Node{T: NodeRule})
	case "codeBlock":
		return one(withAttrs(Node{T: NodeCode, Text: allText(n)}, Attrs{Lang: attrStr(a, "language")}))
	case "panel":
		return one(withAttrs(Node{T: NodePanel, C: p.nodes(n.Content)}, Attrs{Panel: panelKind(attrStr(a, "panelType"))}))
	case "table":
		return one(Node{T: NodeTable, C: p.nodes(n.Content)})
	case "tableRow":
		return one(withAttrs(Node{T: NodeRow, C: p.nodes(n.Content)}, Attrs{Header: isHeaderRow(n.Content)}))
	case "tableHeader":
		return one(withAttrs(Node{T: NodeHeaderCell, C: p.nodes(n.Content)}, cellAttrs(a)))
	case "tableCell":
		return one(withAttrs(Node{T: NodeCell, C: p.nodes(n.Content)}, cellAttrs(a)))
	case "expand", "nestedExpand":
		return one(withAttrs(Node{T: NodeExpand, C: p.nodes(n.Content)}, Attrs{
			Title: attrStr(a, "title"), Collapsed: attrBool(a, "collapsed")}))
	case "status":
		return one(withAttrs(Node{T: NodeStatus}, Attrs{Label: attrStr(a, "text"), Lozenge: lozenge(attrStr(a, "color"))}))
	case "mention":
		return one(p.mention(n))
	case "date":
		return one(p.date(n))
	case "emoji":
		return one(emoji(n))
	case "inlineCard", "blockCard":
		return one(p.card(n))
	case "mediaSingle":
		return one(p.mediaSingle(n))
	case "mediaGroup":
		return p.mediaGroup(n)
	case "mediaInline":
		return one(p.mediaInline(n))
	case "media":
		return one(p.media(n, false, "", ""))
	default:
		return one(p.unknown(n))
	}
}

func one(n Node) []Node { return []Node{n} }

// withAttrs sets Node.A unless every attribute is zero, so no empty "a" key is serialised.
func withAttrs(n Node, a Attrs) Node {
	if !reflect.ValueOf(a).IsZero() {
		n.A = &a
	}
	return n
}

func (p *parser) unknown(n rawNode) Node {
	p.unsupported++
	return Node{T: NodeUnknown, Text: allText(n)}
}

// decisionItems wraps each decisionItem's inline content in one paragraph.
func (p *parser) decisionItems(ns []rawNode) []Node {
	out := make([]Node, 0, len(ns))
	for _, it := range ns {
		if it.Type != "decisionItem" {
			out = append(out, p.node(it)...)
			continue
		}
		out = append(out, Node{T: NodeParagraph, C: p.nodes(it.Content)})
	}
	return out
}

func isHeaderRow(cells []rawNode) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if c.Type != "tableHeader" {
			return false
		}
	}
	return true
}

func cellAttrs(a map[string]any) Attrs {
	return Attrs{Colspan: attrInt(a, "colspan"), Rowspan: attrInt(a, "rowspan"), Colwidth: attrInts(a, "colwidth")}
}

func panelKind(s string) string {
	switch s {
	case PanelNote, PanelSuccess, PanelWarning, PanelError:
		return s
	}
	return PanelInfo
}

// lozenge maps an ADF status colour to the lane names. Unknown or missing colours are todo.
func lozenge(color string) string {
	switch color {
	case "blue":
		return "prog"
	case "purple":
		return "review"
	case "yellow":
		return "qa"
	case "green":
		return "done"
	case "red":
		return "alert"
	}
	return "todo"
}

func emoji(n rawNode) Node {
	short := attrStr(n.Attrs, "shortName")
	glyph := attrStr(n.Attrs, "text")
	if glyph == short {
		glyph = ""
	}
	return withAttrs(Node{T: NodeEmoji}, Attrs{Emoji: glyph, ShortName: short})
}

func (p *parser) mention(n rawNode) Node {
	id := attrStr(n.Attrs, "id")
	label, self := p.r.Person(id)
	if label == "" {
		label = strings.TrimPrefix(attrStr(n.Attrs, "text"), "@")
	}
	return withAttrs(Node{T: NodeMention}, Attrs{AccountID: id, Label: label, Self: self})
}

func (p *parser) date(n rawNode) Node {
	d, ok := timestampDate(attrStr(n.Attrs, "timestamp"))
	if !ok {
		return p.unknown(n)
	}
	return withAttrs(Node{T: NodeDate}, Attrs{Date: d, Past: d < p.nowDay})
}

// card renders an inline or block card: a synced issue gets its fields, anything else its URL, host and path.
func (p *parser) card(n rawNode) Node {
	u := attrStr(n.Attrs, "url")
	block := n.Type == "blockCard"
	if u != "" {
		if ref, ok := p.r.Issue(u); ok {
			return withAttrs(Node{T: NodeCard}, Attrs{
				IssueID: ref.IssueID, Key: ref.Key, Summary: ref.Summary, TypeKind: ref.TypeKind,
				StatusName: ref.StatusName, StatusLozenge: ref.StatusLozenge, URL: u, Block: block})
		}
	}
	host, path := urlParts(u)
	return withAttrs(Node{T: NodeCard}, Attrs{URL: u, Host: host, Path: path, Block: block})
}

func (p *parser) mediaSingle(n rawNode) Node {
	var m *rawNode
	caption := ""
	for i := range n.Content {
		c := &n.Content[i]
		switch c.Type {
		case "media":
			if m == nil {
				m = c
			}
		case "caption":
			caption = allText(*c)
		}
	}
	if m == nil {
		return p.unknown(n)
	}
	return p.media(*m, true, attrStr(n.Attrs, "layout"), caption)
}

// mediaGroup flattens into one file node per media child, in place.
func (p *parser) mediaGroup(n rawNode) []Node {
	out := make([]Node, 0, len(n.Content))
	for _, c := range n.Content {
		if c.Type == "media" {
			out = append(out, p.file(c))
			continue
		}
		out = append(out, p.node(c)...)
	}
	return out
}

// mediaInline becomes a media node when it resolves to an image, otherwise a file node.
func (p *parser) mediaInline(n rawNode) Node {
	att, cached, ok := p.attachment(n)
	if ok && strings.HasPrefix(att.Mime, "image/") {
		a := Attrs{Width: att.Width, Height: att.Height}
		a.setAttachment(att, cached)
		return withAttrs(Node{T: NodeMedia}, a)
	}
	return fileFrom(att, cached, ok)
}

// media is a media node of a mediaSingle (block) or a bare media. External media is a link card and is never resolved.
func (p *parser) media(n rawNode, block bool, layout, caption string) Node {
	if isExternal(n) {
		return externalCard(n, block)
	}
	att, cached, ok := p.attachment(n)
	a := Attrs{Layout: layout, Caption: caption, Block: block,
		Width: attrInt(n.Attrs, "width"), Height: attrInt(n.Attrs, "height")}
	if !ok {
		a.Missing = true
	} else {
		a.setAttachment(att, cached)
		if a.Width == 0 {
			a.Width = att.Width
		}
		if a.Height == 0 {
			a.Height = att.Height
		}
	}
	return withAttrs(Node{T: NodeMedia}, a)
}

// file is a file card for a media child of a mediaGroup.
func (p *parser) file(n rawNode) Node {
	if isExternal(n) {
		return externalCard(n, false)
	}
	att, cached, ok := p.attachment(n)
	return fileFrom(att, cached, ok)
}

func fileFrom(att model.Attachment, cached string, ok bool) Node {
	if !ok {
		return withAttrs(Node{T: NodeFile}, Attrs{Missing: true})
	}
	var a Attrs
	a.setAttachment(att, cached)
	return withAttrs(Node{T: NodeFile}, a)
}

func (p *parser) attachment(n rawNode) (model.Attachment, string, bool) {
	return p.r.Attachment(attrStr(n.Attrs, "id"), attrStr(n.Attrs, "alt"))
}

func (a *Attrs) setAttachment(att model.Attachment, cached string) {
	a.AttachmentID = strconv.FormatInt(att.LocalID, 10)
	a.Name = att.Name
	a.Mime = att.Mime
	a.Bytes = att.Bytes
	a.Cached = cached
}

func isExternal(n rawNode) bool { return attrStr(n.Attrs, "type") == "external" }

func externalCard(n rawNode, block bool) Node {
	u := attrStr(n.Attrs, "url")
	host, path := urlParts(u)
	return withAttrs(Node{T: NodeCard}, Attrs{URL: u, Host: host, Path: path, Block: block})
}

// marks keeps the authored order and drops unknown marks, unsafe links and invalid colours.
func (p *parser) marks(ms []rawMark) []Mark {
	var out []Mark
	for _, m := range ms {
		switch m.Type {
		case "strong":
			out = append(out, Mark{T: MarkStrong})
		case "em":
			out = append(out, Mark{T: MarkEm})
		case "strike":
			out = append(out, Mark{T: MarkStrike})
		case "underline":
			out = append(out, Mark{T: MarkUnderline})
		case "code":
			out = append(out, Mark{T: MarkCode})
		case "subsup":
			switch attrStr(m.Attrs, "type") {
			case "sub":
				out = append(out, Mark{T: MarkSub})
			case "sup":
				out = append(out, Mark{T: MarkSup})
			}
		case "link":
			if href := attrStr(m.Attrs, "href"); hrefOK(href) {
				out = append(out, Mark{T: MarkLink, V: href})
			}
		case "textColor":
			if c := attrStr(m.Attrs, "color"); validColor(c) {
				out = append(out, Mark{T: MarkColor, V: c})
			}
		}
	}
	return out
}
