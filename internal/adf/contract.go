// Package adf turns stored rich text (Atlassian Document Format JSON, model.RichText) into the typed node tree the
// ticket modal renders, and composer text into ADF (architecture.md §2.4, ux-spec §14.4, ADR 0015).
//
// Raw ADF is stored as received and parsed at view-build time, so renderer fixes apply to already-synced data. The
// frontend never sees raw ADF and never uses innerHTML.
//
// FROZEN CONTRACT: the types in this file and the exported signatures in adf.go. Implementation: WP-T1.
// Imports allowed: model, stdlib.
package adf

import (
	"time"

	"github.com/crueber/cuisle/internal/model"
)

// Doc is a parsed document: the top-level block nodes. JSON names are short because a ticket can carry many nodes.
type Doc struct {
	Nodes       []Node `json:"c"`           // never nil (empty document = [])
	Unsupported int    `json:"u,omitempty"` // number of nodes rendered as "unknown"
}

// Node is one normalised node. Only the fields relevant to T are set.
type Node struct {
	T     string `json:"t"`           // one of the Node* constants
	Text  string `json:"x,omitempty"` // text / unknown: the text content; code: the code text
	Marks []Mark `json:"m,omitempty"` // text marks
	A     *Attrs `json:"a,omitempty"`
	C     []Node `json:"c,omitempty"` // children
}

// Normalised node types (Node.T). ux-spec §14.4 maps each to its rendering.
const (
	NodeParagraph   = "p"
	NodeHeading     = "h" // Attrs.Level 1–6
	NodeText        = "text"
	NodeHardBreak   = "br"
	NodeBulletList  = "ul"
	NodeOrderedList = "ol" // Attrs.Order = first number
	NodeListItem    = "li"
	NodeTaskList    = "task"
	NodeTaskItem    = "taskItem" // Attrs.Done
	NodeDecision    = "decision" // decisionList → one node; children are its items' content
	NodeQuote       = "quote"
	NodeRule        = "rule"
	NodeCode        = "code"  // codeBlock; Attrs.Lang; Text = the code
	NodePanel       = "panel" // Attrs.Panel
	NodeTable       = "table"
	NodeRow         = "tr"
	NodeHeaderCell  = "th" // Attrs.Colspan, Rowspan, Colwidth
	NodeCell        = "td"
	NodeExpand      = "expand"  // expand and nestedExpand; Attrs.Title, Collapsed
	NodeStatus      = "status"  // lozenge; Attrs.Label, Lozenge
	NodeMention     = "mention" // Attrs.AccountID, Label (display text without "@"), Self
	NodeDate        = "date"    // Attrs.Date (local YYYY-MM-DD), Past
	NodeEmoji       = "emoji"   // Attrs.Emoji (glyph), ShortName
	NodeCard        = "card"    // inlineCard / blockCard; Attrs.Block; resolved issue fields or URL/Host/Path
	NodeMedia       = "media"   // mediaSingle (Attrs.Block=true) / inline image; Attrs.AttachmentID, Width, Height, Layout, Caption, Cached, Missing
	NodeFile        = "file"    // mediaGroup / mediaInline non-image: file card; Attrs.AttachmentID, Name, Mime, Bytes, Missing
	NodeUnknown     = "unknown" // Text = concatenated text content; counted in Doc.Unsupported
)

// NodeTypes lists every normalised node type (golden tests cover each).
var NodeTypes = []string{NodeParagraph, NodeHeading, NodeText, NodeHardBreak, NodeBulletList, NodeOrderedList,
	NodeListItem, NodeTaskList, NodeTaskItem, NodeDecision, NodeQuote, NodeRule, NodeCode, NodePanel, NodeTable,
	NodeRow, NodeHeaderCell, NodeCell, NodeExpand, NodeStatus, NodeMention, NodeDate, NodeEmoji, NodeCard, NodeMedia,
	NodeFile, NodeUnknown}

// Mark is a text mark. Unknown marks are dropped.
type Mark struct {
	T string `json:"t"`           // one of the Mark* constants
	V string `json:"v,omitempty"` // link: href; color: "#rrggbb" as authored (TS clamps contrast)
}

// Mark types (Mark.T).
const (
	MarkStrong    = "strong"
	MarkEm        = "em"
	MarkStrike    = "strike"
	MarkUnderline = "underline"
	MarkSub       = "sub"
	MarkSup       = "sup"
	MarkCode      = "code"
	MarkLink      = "link"
	MarkColor     = "color"
)

// Panel kinds (Attrs.Panel).
const (
	PanelInfo    = "info"
	PanelNote    = "note"
	PanelSuccess = "success"
	PanelWarning = "warning"
	PanelError   = "error"
)

// Cache states of a media/file node (Attrs.Cached; same values as view.AttachmentView.Cached).
const (
	CachedFull  = "full"
	CachedThumb = "thumb"
	CachedNone  = "none"
)

// Attrs are flat, all optional; only the fields relevant to the node type are set. JSON names are lowerCamel.
type Attrs struct {
	Level     int    `json:"level,omitempty"`     // heading 1–6
	Lang      string `json:"lang,omitempty"`      // code
	Panel     string `json:"panel,omitempty"`     // panel: info|note|success|warning|error
	Title     string `json:"title,omitempty"`     // expand
	Done      bool   `json:"done,omitempty"`      // taskItem
	Collapsed bool   `json:"collapsed,omitempty"` // expand: as authored
	Order     int    `json:"order,omitempty"`     // ol: first number (0 = 1)
	Colspan   int    `json:"colspan,omitempty"`   // th/td (0 = 1)
	Rowspan   int    `json:"rowspan,omitempty"`
	Colwidth  []int  `json:"colwidth,omitempty"` // th/td: authored px widths
	Header    bool   `json:"header,omitempty"`   // tr: header row

	Lozenge string `json:"lozenge,omitempty"` // status: todo|prog|review|qa|done|alert (from the ADF colour)
	Label   string `json:"label,omitempty"`   // status text; mention display name (no "@")

	AccountID string `json:"accountId,omitempty"` // mention
	Self      bool   `json:"self,omitempty"`      // mention of the connected user

	Date string `json:"date,omitempty"` // date node: local YYYY-MM-DD
	Past bool   `json:"past,omitempty"` // date before asOf's local day

	Emoji     string `json:"emoji,omitempty"` // glyph, "" when unknown
	ShortName string `json:"shortName,omitempty"`

	URL  string `json:"url,omitempty"`  // card / external media: target URL
	Host string `json:"host,omitempty"` // card to a non-synced URL
	Path string `json:"path,omitempty"`

	IssueID       int64  `json:"issueId,omitempty"` // card resolved to a synced issue (local issue id)
	Key           string `json:"key,omitempty"`
	Summary       string `json:"summary,omitempty"`
	TypeKind      string `json:"typeKind,omitempty"`
	StatusName    string `json:"statusName,omitempty"`
	StatusLozenge string `json:"statusLozenge,omitempty"` // lane of the status: todo|prog|review|qa|done

	AttachmentID string `json:"attachmentId,omitempty"` // media/file: local attachment row id in decimal (= view.AttachmentView.ID, the /media/{id} id)
	Name         string `json:"name,omitempty"`
	Mime         string `json:"mime,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	Width        int    `json:"width,omitempty"` // media: authored or stored px
	Height       int    `json:"height,omitempty"`
	Layout       string `json:"layout,omitempty"` // mediaSingle layout as authored (center, wide, full-width, …)
	Caption      string `json:"caption,omitempty"`
	Cached       string `json:"cached,omitempty"`  // full|thumb|none
	Missing      bool   `json:"missing,omitempty"` // unresolved media: "attachment unavailable ↗"

	Block bool `json:"block,omitempty"` // blockCard / mediaSingle (vs inline)
}

// ResolvedIssue is a synced issue an inline/block card points at.
type ResolvedIssue struct {
	IssueID       int64 // local issue id
	Key           string
	Summary       string
	TypeKind      string // story|bug|task|spike|subtask|epic|other
	StatusName    string
	StatusLozenge string // lane of the status in the viewing standup: todo|prog|review|qa|done
}

// Resolver looks up what a document references. Implemented in internal/view over the loaded ticket (cache.Ticket);
// it never touches the network.
type Resolver interface {
	// Attachment maps a media node (attrs.id = media file id, attrs.alt) to an attachment of the issue, using the
	// resolution order of jira-sync.md §2.14. cached is full|thumb|none. att.LocalID is the row id.
	Attachment(mediaFileID, alt string) (att model.Attachment, cached string, ok bool)
	// Issue returns the synced issue for an inline/block card URL or issue key.
	Issue(url string) (ref ResolvedIssue, ok bool)
	// Person returns a display name and whether the account is the connected user.
	Person(accountID string) (name string, self bool)
	// Now is asOf (date nodes: Past is relative to it, in its Location).
	Now() time.Time
}
