package adf

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/model"
)

// rawNode is one ADF node as received. Attrs stay untyped; the helpers below read them leniently.
type rawNode struct {
	Type    string         `json:"type"`
	Attrs   map[string]any `json:"attrs"`
	Text    string         `json:"text"`
	Marks   []rawMark      `json:"marks"`
	Content []rawNode      `json:"content"`
}

type rawMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

// decodeRoot parses a stored document. ok is false when the raw bytes are not a JSON object (malformed input).
// An absent or JSON-null document is empty: ok is true and the node list is empty.
func decodeRoot(raw model.RichText) (nodes []rawNode, ok bool) {
	data := bytes.TrimSpace(raw.Raw)
	if len(data) == 0 || string(data) == "null" {
		return nil, true
	}
	var root rawNode
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, false
	}
	if root.Type == "" || root.Type == "doc" {
		return root.Content, true
	}
	return []rawNode{root}, true
}

// rawText is the raw bytes as a string, used for malformed input.
func rawText(raw model.RichText) string { return string(raw.Raw) }

func attrStr(a map[string]any, k string) string {
	s, _ := a[k].(string)
	return s
}

func attrInt(a map[string]any, k string) int {
	f, ok := a[k].(float64)
	if !ok {
		return 0
	}
	return int(f)
}

func attrBool(a map[string]any, k string) bool {
	b, _ := a[k].(bool)
	return b
}

// attrInts reads a numeric array (colwidth). Absent or empty gives nil so the key is omitted.
func attrInts(a map[string]any, k string) []int {
	arr, _ := a[k].([]any)
	var out []int
	for _, v := range arr {
		if f, ok := v.(float64); ok {
			out = append(out, int(f))
		}
	}
	return out
}

// allText concatenates every descendant text node with no separator.
func allText(n rawNode) string {
	if n.Type == "text" {
		return n.Text
	}
	var sb strings.Builder
	for _, c := range n.Content {
		sb.WriteString(allText(c))
	}
	return sb.String()
}

// timestampDate converts an ADF date timestamp (epoch milliseconds as a string) to its UTC calendar date.
func timestampDate(s string) (string, bool) {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return "", false
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02"), true
}

// urlParts returns the host and path of a URL, or empty strings when it does not parse.
func urlParts(s string) (host, path string) {
	u, err := url.Parse(s)
	if err != nil {
		return "", ""
	}
	return u.Host, u.Path
}

// hrefOK reports whether a link target is http, https or mailto.
func hrefOK(href string) bool {
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return true
	}
	return false
}

// validColor reports whether s is exactly a lowercase #rrggbb colour.
func validColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
